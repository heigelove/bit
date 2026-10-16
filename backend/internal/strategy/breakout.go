package strategy

import (
	"fmt"
	"math"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// TrendBreakout buys a fresh Donchian close only after EMA, ADX and DI already
// agree on direction.
//
// It is the continuation breakout:
//   - trend buys the pullback into EMA20
//   - squeeze buys the first expansion out of a low-ATR coil
//   - vegas trades the 144/169 tunnel
//
// A close that is already outside the channel does not re-arm. The stop sits
// beyond the signal bar and is widened to MinStopATR. Until TrailAfterR the
// stop stays put; after that a Chandelier trail only tightens. A close back
// through the channel midline, or an EMA20/60 flip, cuts the trade.
//
// Signals use the primary timeframe only. The 15m entry stream is ignored.
type TrendBreakout struct {
	cfg     config.StrategyConfig
	bo      config.BreakoutConfig
	sym     string
	minBars int
	pos     PositionState
	lock    ReentryLock
}

func NewTrendBreakout(sym string, cfg config.StrategyConfig) *TrendBreakout {
	bo := cfg.Breakout.WithDefaults()
	if cfg.ATRPeriod <= 0 {
		cfg.ATRPeriod = 14
	}
	need := max(
		cfg.ATRPeriod+2,
		bo.Donchian+2,
		bo.EMASlow+2,
		bo.ADXPeriod*2+2,
		bo.ChandelierPeriod+2,
	)
	if bo.EMAFilterEnabled() {
		need = max(need, bo.EMAFilter+2)
	}
	if bo.EMASlopeMinATR > 0 {
		need = max(need, bo.EMASlow+bo.EMASlopeBars+2)
	}
	return &TrendBreakout{
		cfg:     cfg,
		bo:      bo,
		sym:     sym,
		minBars: max(cfg.MinBars, need),
	}
}

func (s *TrendBreakout) Name() string { return "breakout" }

func (s *TrendBreakout) Sync(pos PositionState) {
	s.lock.ArmOnFlatten(s.pos, pos)
	s.pos = pos
}

func (s *TrendBreakout) ReentryLock() ReentryLock { return s.lock }

func (s *TrendBreakout) RestoreReentryLock(l ReentryLock) { s.lock = l }

type breakoutView struct {
	bars                    []types.Kline
	highs, lows, closes     []float64
	atr, fast, slow, filter []float64
	plusDI, minusDI, adx    []float64
	up, mid, dn             []float64
}

func (s *TrendBreakout) Evaluate(klines []types.Kline, mkt MarketContext) types.Signal {
	sig := types.Signal{Symbol: s.sym, Action: types.ActionNone}
	if len(klines) < s.minBars {
		sig.Reason = fmt.Sprintf("warmup %d/%d", len(klines), s.minBars)
		return sig
	}
	bars := closedBars(klines)
	if len(bars) < s.minBars {
		sig.Reason = "waiting closed bar"
		return sig
	}

	highs, lows, closes := ohlc(bars)
	up, mid, dn := indicators.Donchian(highs, lows, s.bo.Donchian)
	plusDI, minusDI, adx := indicators.DMI(highs, lows, closes, s.bo.ADXPeriod)
	v := breakoutView{
		bars:    bars,
		highs:   highs,
		lows:    lows,
		closes:  closes,
		atr:     indicators.ATR(highs, lows, closes, s.cfg.ATRPeriod),
		fast:    indicators.EMA(closes, s.bo.EMAFast),
		slow:    indicators.EMA(closes, s.bo.EMASlow),
		filter:  indicators.EMA(closes, s.bo.EMAFilter),
		plusDI:  plusDI,
		minusDI: minusDI,
		adx:     adx,
		up:      up,
		mid:     mid,
		dn:      dn,
	}

	i := len(bars) - 1
	sig.Time = bars[i].CloseTime
	sig.Price = closes[i]
	sig.EMA20 = v.fast[i]
	sig.EMA60 = v.slow[i]
	sig.EMA200 = v.filter[i]
	sig.ATR = v.atr[i]
	sig.ADX = v.adx[i]
	sig.DonchianUp = v.up[i]
	sig.DonchianDn = v.dn[i]
	sig.Funding = mkt.FundingRate

	if anyNaN(v.atr[i], v.up[i], v.dn[i], v.fast[i], v.slow[i]) || v.atr[i] <= 0 {
		sig.Reason = "indicator warmup"
		return sig
	}
	if s.bo.EMAFilterEnabled() && math.IsNaN(v.filter[i]) {
		sig.Reason = "ema filter warmup"
		return sig
	}
	if (s.bo.ADXMin > 0 && math.IsNaN(v.adx[i])) || (s.bo.DIFilterEnabled() && anyNaN(v.plusDI[i], v.minusDI[i])) {
		sig.Reason = "adx warmup"
		return sig
	}

	if !s.pos.Flat() {
		return s.manage(sig, v, i)
	}
	return s.entry(sig, v, i)
}

func (s *TrendBreakout) manage(sig types.Signal, v breakoutView, i int) types.Signal {
	long := s.pos.Long
	price := sig.Price
	entry := s.pos.Entry

	risk := math.Abs(entry - s.pos.InitStop)
	if risk <= 0 {
		risk = s.bo.MinStopATR * v.atr[i]
	}
	progress := 0.0
	if risk > 0 {
		if long {
			progress = (price - entry) / risk
		} else {
			progress = (entry - price) / risk
		}
	}

	closeAction := types.ActionCloseShort
	reduceAction := types.ActionReduceShort
	if long {
		closeAction = types.ActionCloseLong
		reduceAction = types.ActionReduceLong
	}

	trail := s.ratchet(v, i, long, progress)
	sig.StopLoss = trail
	if trail > 0 && ((long && price <= trail) || (!long && price >= trail)) {
		sig.Action = closeAction
		sig.Reason = fmt.Sprintf("trail stop %.2f hit", trail)
		s.lock.ArmOnFlatten(s.pos, PositionState{})
		return sig
	}

	if s.bo.StackFlipExitEnabled() {
		flipped := (long && v.fast[i] < v.slow[i]) || (!long && v.fast[i] > v.slow[i])
		if flipped {
			sig.Action = closeAction
			sig.Reason = "ema stack flipped"
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	if s.bo.MidLossExitEnabled() && !math.IsNaN(v.mid[i]) {
		failed := (long && price < v.mid[i]) || (!long && price > v.mid[i])
		if failed {
			sig.Action = closeAction
			sig.Reason = "closed through channel mid"
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	if held, ok := s.barsHeld(v.bars, i); ok && s.bo.TimeStopBars > 0 && held >= s.bo.TimeStopBars && progress < s.bo.TimeStopMinR {
		sig.Action = closeAction
		sig.Reason = fmt.Sprintf("time stop %d bars at %.2fR", held, progress)
		s.lock.ArmOnFlatten(s.pos, PositionState{})
		return sig
	}

	if s.bo.TPEnabled() && s.bo.TPR > 0 && progress >= s.bo.TPR {
		if s.bo.TPPortion > 0 && s.bo.TPPortion < 1 && !s.pos.Reduced() {
			sig.Action = reduceAction
			sig.Portion = s.bo.TPPortion
			sig.Reason = fmt.Sprintf("TP1 %.2fR, take %.0f%%", progress, s.bo.TPPortion*100)
			return sig
		}
		if s.bo.TPPortion <= 0 || s.bo.TPPortion >= 1 {
			sig.Action = closeAction
			sig.Reason = fmt.Sprintf("tp %.2fR", progress)
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	sig.Reason = fmt.Sprintf("hold %.2fR", progress)
	return sig
}

func (s *TrendBreakout) ratchet(v breakoutView, i int, long bool, progress float64) float64 {
	if s.bo.TrailAfterR > 0 && progress < s.bo.TrailAfterR {
		if s.pos.Trail > 0 {
			return s.pos.Trail
		}
		if s.pos.InitStop > 0 {
			return s.pos.InitStop
		}
	}
	var stop float64
	if long {
		stop = indicators.ChandelierLong(v.highs, v.atr[i], i, s.bo.ChandelierPeriod, s.bo.ChandelierMult)
	} else {
		stop = indicators.ChandelierShort(v.lows, v.atr[i], i, s.bo.ChandelierPeriod, s.bo.ChandelierMult)
	}
	if math.IsNaN(stop) {
		return s.pos.Trail
	}
	if s.pos.Trail > 0 {
		if long {
			stop = math.Max(stop, s.pos.Trail)
		} else {
			stop = math.Min(stop, s.pos.Trail)
		}
	}
	return stop
}

func (s *TrendBreakout) entry(sig types.Signal, v breakoutView, i int) types.Signal {
	price := sig.Price
	inside := price <= v.up[i] && price >= v.dn[i]
	if s.lock.Armed {
		s.lock.Stamp(sig.Time)
		if inside {
			s.lock.MarkReset()
		}
	}

	breakUp := price > v.up[i]
	breakDown := price < v.dn[i]
	if i > 0 && !math.IsNaN(v.up[i-1]) && !math.IsNaN(v.dn[i-1]) {
		breakUp = breakUp && v.closes[i-1] <= v.up[i-1]
		breakDown = breakDown && v.closes[i-1] >= v.dn[i-1]
	}
	if !breakUp && !breakDown {
		if inside {
			sig.Reason = fmt.Sprintf("inside channel %.2f-%.2f", v.dn[i], v.up[i])
		} else {
			sig.Reason = "outside channel, no fresh cross"
		}
		return sig
	}

	long := breakUp
	if reason := s.trendBlock(long, v, i); reason != "" {
		sig.Reason = reason
		return sig
	}

	ref := priorATR(v.atr, i)
	edge := v.up[i]
	if !long {
		edge = v.dn[i]
	}
	ext := math.Abs(price - edge)
	if ext < s.bo.MinBreakATR*ref {
		sig.Reason = "breakout too shallow"
		return sig
	}
	if ext > s.bo.BreakoutMaxATR*ref {
		sig.Reason = "breakout already extended, no chase"
		return sig
	}

	bar := v.bars[i]
	rng := bar.High - bar.Low
	if s.bo.MinRangeATR > 0 && rng < s.bo.MinRangeATR*ref {
		sig.Reason = "breakout bar too narrow"
		return sig
	}
	if s.bo.BarConfirmRequired() && !closeInFavor(long, bar, s.bo.CloseConfirmFrac) {
		sig.Reason = "weak breakout close"
		return sig
	}

	held := barsOnOrAfter(v.bars, s.lock.BarTime)
	if reason := s.lock.Block(long, price, edge, ref, s.bo.ReentryATR, s.bo.ReentryCooldown, held, s.bo.ResetRequired()); reason != "" {
		sig.Reason = reason
		return sig
	}

	stop, reason := s.stopFor(long, price, ref, bar)
	if reason != "" {
		sig.Reason = reason
		return sig
	}

	if long {
		sig.Action = types.ActionOpenLong
		sig.Reason = fmt.Sprintf("donchian breakout ADX %.1f", v.adx[i])
	} else {
		sig.Action = types.ActionOpenShort
		sig.Reason = fmt.Sprintf("donchian breakdown ADX %.1f", v.adx[i])
	}
	sig.StopLoss = stop
	s.lock.NoteEntry(long, price, edge)
	return sig
}

func (s *TrendBreakout) trendBlock(long bool, v breakoutView, i int) string {
	fast, slow := v.fast[i], v.slow[i]
	if long {
		if fast <= slow {
			return "against EMA stack"
		}
	} else if fast >= slow {
		return "against EMA stack"
	}
	if s.bo.EMAFilterEnabled() {
		filter := v.filter[i]
		price := v.closes[i]
		if (long && price <= filter) || (!long && price >= filter) {
			return fmt.Sprintf("against EMA%d filter", s.bo.EMAFilter)
		}
	}
	if s.bo.DIFilterEnabled() {
		if (long && v.plusDI[i] <= v.minusDI[i]) || (!long && v.minusDI[i] <= v.plusDI[i]) {
			return "DI disagrees"
		}
	}
	if s.bo.ADXMin > 0 && v.adx[i] < s.bo.ADXMin {
		return fmt.Sprintf("ADX %.1f < %.1f", v.adx[i], s.bo.ADXMin)
	}
	if s.bo.EMASepMinATR > 0 && math.Abs(fast-slow) < s.bo.EMASepMinATR*v.atr[i] {
		return "EMA tangled"
	}
	if s.bo.EMASlopeMinATR > 0 && s.bo.EMASlopeBars > 0 {
		j := i - s.bo.EMASlopeBars
		if j < 0 || math.IsNaN(v.slow[j]) {
			return "ema slope warmup"
		}
		delta := slow - v.slow[j]
		need := s.bo.EMASlopeMinATR * priorATR(v.atr, i)
		if (long && delta < need) || (!long && -delta < need) {
			return "EMA slope flat"
		}
	}
	return ""
}

func (s *TrendBreakout) stopFor(long bool, price, ref float64, bar types.Kline) (float64, string) {
	extreme := bar.Low
	if !long {
		extreme = bar.High
	}
	dist := math.Abs(price - extreme)
	if s.bo.MaxStopATR > 0 && ref > 0 && dist > s.bo.MaxStopATR*ref {
		return 0, "breakout bar too wide"
	}
	dist += s.bo.ATRStopMult * ref
	if s.bo.MinStopATR > 0 && dist < s.bo.MinStopATR*ref {
		dist = s.bo.MinStopATR * ref
	}
	if long {
		return price - dist, ""
	}
	return price + dist, ""
}

func (s *TrendBreakout) barsHeld(bars []types.Kline, i int) (int, bool) {
	if s.pos.EntryTime.IsZero() {
		return 0, false
	}
	held := 0
	for j := i; j >= 0 && bars[j].CloseTime.After(s.pos.EntryTime); j-- {
		held++
	}
	return held, true
}

func priorATR(atr []float64, i int) float64 {
	if i > 0 && !math.IsNaN(atr[i-1]) && atr[i-1] > 0 {
		return atr[i-1]
	}
	return atr[i]
}

func closeInFavor(long bool, bar types.Kline, frac float64) bool {
	rng := bar.High - bar.Low
	if rng <= 0 {
		return false
	}
	pos := (bar.Close - bar.Low) / rng
	if long {
		return pos >= frac
	}
	return pos <= 1-frac
}
