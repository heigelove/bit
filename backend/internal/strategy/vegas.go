package strategy

import (
	"fmt"
	"math"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// VegasTunnel trades pullbacks to the Vegas tunnel (EMA 144 / EMA 169).
// EMA 12 is the momentum filter. The tunnel is support in an uptrend and
// resistance in a downtrend; the strategy fades a tag of the near edge only
// after price has already been holding on the trend side of it.
//
// Breakouts through the tunnel are ignored. A position is held for the
// one-sided leg: no fixed target. It ends when EMA144 crosses back through
// EMA169, or a wide trail gives back the extension.
//
// Primary timeframe only. The 15m entry stream is not used.
type VegasTunnel struct {
	cfg config.StrategyConfig
	vg  config.VegasConfig
	sym string

	minBars int
	pos     PositionState
	lock    ReentryLock
}

func NewVegasTunnel(sym string, cfg config.StrategyConfig) *VegasTunnel {
	vg := cfg.Vegas.WithDefaults()
	if cfg.ATRPeriod <= 0 {
		cfg.ATRPeriod = 14
	}
	need := vg.EMATunnelSlow + vg.SlopeBars + vg.EstablishBars + 5
	if vg.ADXMin > 0 {
		need = max(need, vg.ADXPeriod*2+5)
	}
	return &VegasTunnel{
		cfg:     cfg,
		vg:      vg,
		sym:     sym,
		minBars: max(cfg.MinBars, need),
	}
}

func (s *VegasTunnel) Name() string { return "vegas" }

func (s *VegasTunnel) Sync(pos PositionState) {
	s.lock.ArmOnFlatten(s.pos, pos)
	s.pos = pos
}

func (s *VegasTunnel) ReentryLock() ReentryLock { return s.lock }

func (s *VegasTunnel) RestoreReentryLock(l ReentryLock) { s.lock = l }

func (s *VegasTunnel) Evaluate(klines []types.Kline, _ MarketContext) types.Signal {
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
	atr := indicators.ATR(highs, lows, closes, s.cfg.ATRPeriod)
	fast := indicators.EMA(closes, s.vg.EMAFast)
	tunF := indicators.EMA(closes, s.vg.EMATunnelFast)
	tunS := indicators.EMA(closes, s.vg.EMATunnelSlow)
	var adx []float64
	if s.vg.ADXMin > 0 {
		adx = indicators.ADX(highs, lows, closes, s.vg.ADXPeriod)
	}

	i := len(bars) - 1
	upper, lower := tunnelBands(tunF[i], tunS[i])
	sig.Time = bars[i].CloseTime
	sig.Price = closes[i]
	sig.ATR = atr[i]
	sig.EMA20 = fast[i]
	sig.EMA60 = tunF[i]
	sig.EMA200 = tunS[i]
	sig.DonchianUp = upper
	sig.DonchianDn = lower
	if adx != nil {
		sig.ADX = adx[i]
	}

	if anyNaN(atr[i], fast[i], tunF[i], tunS[i]) || atr[i] <= 0 {
		sig.Reason = "indicator warmup"
		return sig
	}

	view := vegasView{
		bars: bars, highs: highs, lows: lows, closes: closes,
		atr: atr, fast: fast, tunF: tunF, tunS: tunS, adx: adx,
	}
	if !s.pos.Flat() {
		return s.manage(sig, view, i)
	}
	return s.entry(sig, view, i)
}

type vegasView struct {
	bars                  []types.Kline
	highs, lows, closes   []float64
	atr, fast, tunF, tunS []float64
	adx                   []float64
}

func (s *VegasTunnel) manage(sig types.Signal, v vegasView, i int) types.Signal {
	long := s.pos.Long
	price := sig.Price
	upper, lower := tunnelBands(v.tunF[i], v.tunS[i])

	risk := math.Abs(s.pos.Entry - s.pos.InitStop)
	if risk <= 0 {
		risk = s.vg.MinStopATR * v.atr[i]
	}
	progress := 0.0
	if risk > 0 {
		if long {
			progress = (price - s.pos.Entry) / risk
		} else {
			progress = (s.pos.Entry - price) / risk
		}
	}

	closeAction := types.ActionCloseShort
	reduceAction := types.ActionReduceShort
	if long {
		closeAction = types.ActionCloseLong
		reduceAction = types.ActionReduceLong
	}

	if s.vg.ExitOnTunnelLossEnabled() {
		lost := (long && price < lower) || (!long && price > upper)
		if lost {
			sig.Action = closeAction
			sig.Reason = "closed through tunnel"
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	// The tunnel reorder is the trend ending. A pullback that only tags the
	// near edge does not cross EMA144 through EMA169, so the trade stays open
	// for the rest of the leg.
	if s.vg.StackFlipExitEnabled() {
		flipped := (long && v.tunF[i] < v.tunS[i]) || (!long && v.tunF[i] > v.tunS[i])
		if flipped {
			sig.Action = closeAction
			sig.Reason = "tunnel stack flipped"
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	if held, ok := vegasBarsHeld(v.bars, s.pos.EntryTime, i); ok && s.vg.TimeStopBars > 0 && held >= s.vg.TimeStopBars && progress < s.vg.TimeStopMinR {
		sig.Action = closeAction
		sig.Reason = fmt.Sprintf("time stop %d bars at %.2fR", held, progress)
		s.lock.ArmOnFlatten(s.pos, PositionState{})
		return sig
	}

	tp := s.vg.TPR
	if s.vg.TPEnabled() && tp > 0 && progress >= tp {
		if s.vg.TPPortion > 0 && s.vg.TPPortion < 1 && !s.pos.Reduced() {
			sig.Action = reduceAction
			sig.Portion = s.vg.TPPortion
			sig.Reason = fmt.Sprintf("TP1 %.2fR, take %.0f%%", progress, s.vg.TPPortion*100)
			return sig
		}
		if s.vg.TPPortion <= 0 || s.vg.TPPortion >= 1 {
			sig.Action = closeAction
			sig.Reason = fmt.Sprintf("tp %.2fR", progress)
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	trail := s.ratchet(v, i, long, progress)
	sig.StopLoss = trail
	if trail > 0 && ((long && price <= trail) || (!long && price >= trail)) {
		sig.Action = closeAction
		sig.Reason = fmt.Sprintf("trail stop %.2f hit", trail)
		s.lock.ArmOnFlatten(s.pos, PositionState{})
		return sig
	}
	sig.Reason = fmt.Sprintf("hold %.2fR", progress)
	return sig
}

func (s *VegasTunnel) ratchet(v vegasView, i int, long bool, progress float64) float64 {
	stop := s.pos.InitStop
	if stop <= 0 {
		stop = s.pos.Trail
	}
	if s.vg.BreakevenAfterR > 0 && progress >= s.vg.BreakevenAfterR && s.pos.Entry > 0 {
		if long {
			stop = math.Max(stop, s.pos.Entry)
		} else if stop <= 0 {
			stop = s.pos.Entry
		} else {
			stop = math.Min(stop, s.pos.Entry)
		}
	}
	if s.vg.ATRTrailMult > 0 && (s.vg.TrailAfterR <= 0 || progress >= s.vg.TrailAfterR) {
		if long {
			hh := indicators.SwingHigh(v.highs, i, s.vg.TrailBars)
			if !math.IsNaN(hh) {
				stop = math.Max(stop, hh-s.vg.ATRTrailMult*v.atr[i])
			}
		} else {
			ll := indicators.SwingLow(v.lows, i, s.vg.TrailBars)
			if !math.IsNaN(ll) {
				ch := ll + s.vg.ATRTrailMult*v.atr[i]
				if stop <= 0 {
					stop = ch
				} else {
					stop = math.Min(stop, ch)
				}
			}
		}
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

func (s *VegasTunnel) entry(sig types.Signal, v vegasView, i int) types.Signal {
	upper, lower := tunnelBands(v.tunF[i], v.tunS[i])
	if s.lock.Armed {
		s.lock.Stamp(sig.Time)
		if s.lock.Long && v.lows[i] > upper {
			s.lock.MarkReset()
		}
		if !s.lock.Long && v.highs[i] < lower {
			s.lock.MarkReset()
		}
	}

	longOK, longWhy := s.qualifies(v, i, true)
	shortOK, shortWhy := s.qualifies(v, i, false)
	// One signal per pullback. The previous bar qualifying means this one is
	// the bar after the entry, not a new setup.
	if longOK {
		if prev, _ := s.qualifies(v, i-1, true); prev {
			longOK = false
			longWhy = "pullback already traded"
		}
	}
	if shortOK {
		if prev, _ := s.qualifies(v, i-1, false); prev {
			shortOK = false
			shortWhy = "pullback already traded"
		}
	}
	if longOK == shortOK {
		if v.tunF[i] > v.tunS[i] {
			sig.Reason = longWhy
		} else if v.tunF[i] < v.tunS[i] {
			sig.Reason = shortWhy
		} else {
			sig.Reason = "tunnel flat"
		}
		if sig.Reason == "" {
			sig.Reason = "no vegas setup"
		}
		return sig
	}

	long := longOK

	edge := upper
	if !long {
		edge = lower
	}
	held := barsOnOrAfter(v.bars, s.lock.BarTime)
	if reason := s.lock.Block(long, sig.Price, edge, v.atr[i], s.vg.ReentryATR, s.vg.ReentryCooldown, held, s.vg.ResetRequired()); reason != "" {
		sig.Reason = reason
		return sig
	}

	stop, stopWhy := s.initialStop(v, i, long)
	if stopWhy != "" {
		sig.Reason = stopWhy
		return sig
	}
	if long {
		sig.Action = types.ActionOpenLong
	} else {
		sig.Action = types.ActionOpenShort
	}
	sig.StopLoss = stop
	sig.Reason = "vegas pullback reclaim"
	if !long {
		sig.Reason = "vegas pullback reject"
	}
	s.lock.NoteEntry(long, sig.Price, edge)
	return sig
}

// qualifies is the setup without the "previous bar was not already a signal" check.
func (s *VegasTunnel) qualifies(v vegasView, i int, long bool) (bool, string) {
	if i < 2 || i >= len(v.closes) {
		return false, "warmup"
	}
	if anyNaN(v.atr[i], v.fast[i], v.tunF[i], v.tunS[i]) || v.atr[i] <= 0 {
		return false, "indicator warmup"
	}
	upper, lower := tunnelBands(v.tunF[i], v.tunS[i])
	if s.vg.StackRequired() {
		if long && v.tunF[i] <= v.tunS[i] {
			return false, "tunnel not stacked long"
		}
		if !long && v.tunF[i] >= v.tunS[i] {
			return false, "tunnel not stacked short"
		}
	}
	// Late pullbacks into a tunnel that has been stacked for weeks are usually
	// the trend ending, not the first dip. max_stack_age keeps only the early ones.
	if s.vg.MinStackAge > 0 || s.vg.MaxStackAge > 0 {
		age := stackAge(v, i, long)
		if s.vg.MinStackAge > 0 && age < s.vg.MinStackAge {
			return false, fmt.Sprintf("tunnel stack age %d < %d", age, s.vg.MinStackAge)
		}
		if s.vg.MaxStackAge > 0 && age > s.vg.MaxStackAge {
			return false, fmt.Sprintf("tunnel stack age %d > %d", age, s.vg.MaxStackAge)
		}
	}
	if s.vg.SlopeMinATR > 0 && s.vg.SlopeBars > 0 && i >= s.vg.SlopeBars {
		delta := v.tunS[i] - v.tunS[i-s.vg.SlopeBars]
		need := s.vg.SlopeMinATR * v.atr[i]
		if long && delta < need {
			return false, fmt.Sprintf("tunnel slope %.2f < %.2f ATR", delta/v.atr[i], s.vg.SlopeMinATR)
		}
		if !long && delta > -need {
			return false, fmt.Sprintf("tunnel slope %.2f > -%.2f ATR", delta/v.atr[i], s.vg.SlopeMinATR)
		}
	}
	if s.vg.ADXMin > 0 && v.adx != nil {
		if math.IsNaN(v.adx[i]) || v.adx[i] < s.vg.ADXMin {
			return false, fmt.Sprintf("ADX %.1f < %.1f", v.adx[i], s.vg.ADXMin)
		}
	}
	if s.vg.FastSideRequired() {
		if long && v.fast[i] <= upper {
			return false, "EMA12 not above tunnel"
		}
		if !long && v.fast[i] >= lower {
			return false, "EMA12 not below tunnel"
		}
	}
	if s.vg.FastTurnRequired() && i > 0 && !math.IsNaN(v.fast[i-1]) {
		if long && v.fast[i] <= v.fast[i-1] {
			return false, "EMA12 not turning up"
		}
		if !long && v.fast[i] >= v.fast[i-1] {
			return false, "EMA12 not turning down"
		}
	}

	price := v.closes[i]
	pad := s.vg.ReclaimMinATR * v.atr[i]
	if long && price <= upper+pad {
		return false, "close not back above tunnel"
	}
	if !long && price >= lower-pad {
		return false, "close not back below tunnel"
	}
	ext := price - upper
	if !long {
		ext = lower - price
	}
	if ext > s.vg.ChaseMaxATR*v.atr[i] {
		return false, fmt.Sprintf("too far from tunnel, no chase (%.2f ATR)", ext/v.atr[i])
	}
	if s.vg.BarConfirmRequired() {
		bar := v.bars[i]
		if long && bar.Close <= bar.Open {
			return false, "weak reclaim bar"
		}
		if !long && bar.Close >= bar.Open {
			return false, "weak reject bar"
		}
		rng := bar.High - bar.Low
		if s.vg.CloseConfirmFrac > 0 && rng > 0 {
			pos := (bar.Close - bar.Low) / rng
			if !long {
				pos = (bar.High - bar.Close) / rng
			}
			if pos < s.vg.CloseConfirmFrac {
				return false, "weak reclaim close"
			}
		}
	}
	if s.vg.SwingRequired() {
		if long && v.lows[i] <= v.lows[i-1] {
			return false, "no higher low"
		}
		if !long && v.highs[i] >= v.highs[i-1] {
			return false, "no lower high"
		}
	}
	if !s.touched(v, i, long) && !s.touched(v, i-1, long) {
		return false, "no pullback to tunnel"
	}
	for j := i; j >= i-3 && j >= 0; j-- {
		if s.pierced(v, j, long) {
			return false, "pullback broke the tunnel"
		}
	}
	if !s.heldBefore(v, i, long) {
		return false, "not a pullback: tunnel was not held"
	}
	return true, ""
}

func (s *VegasTunnel) touched(v vegasView, i int, long bool) bool {
	if i < 0 || i >= len(v.closes) || math.IsNaN(v.atr[i]) {
		return false
	}
	upper, lower := tunnelBands(v.tunF[i], v.tunS[i])
	band := s.vg.TouchATR * v.atr[i]
	if long {
		return v.lows[i] <= upper+band
	}
	return v.highs[i] >= lower-band
}

func (s *VegasTunnel) pierced(v vegasView, i int, long bool) bool {
	if i < 0 || i >= len(v.closes) || math.IsNaN(v.atr[i]) {
		return false
	}
	_, lower := tunnelBands(v.tunF[i], v.tunS[i])
	upper, _ := tunnelBands(v.tunF[i], v.tunS[i])
	room := s.vg.PierceMaxATR * v.atr[i]
	if long {
		return v.closes[i] < lower-room
	}
	return v.closes[i] > upper+room
}

// heldBefore requires the pullback to start from a close already beyond the
// tunnel. A bar that is itself the first cross does not qualify.
func (s *VegasTunnel) heldBefore(v vegasView, i int, long bool) bool {
	if i < 1 {
		return false
	}
	touchAt := i
	if !s.touched(v, i, long) {
		touchAt = i - 1
	}
	limit := i - s.vg.EstablishBars
	if limit < 0 {
		limit = 0
	}
	k := touchAt
	for k > limit {
		prev := k - 1
		if prev < limit {
			break
		}
		if math.IsNaN(v.tunF[prev]) || math.IsNaN(v.atr[prev]) {
			return false
		}
		upper, lower := tunnelBands(v.tunF[prev], v.tunS[prev])
		stillIn := s.touched(v, prev, long)
		if long && v.closes[prev] <= upper {
			stillIn = true
		}
		if !long && v.closes[prev] >= lower {
			stillIn = true
		}
		if !stillIn {
			break
		}
		k = prev
	}
	prev := k - 1
	if prev < 0 || math.IsNaN(v.tunF[prev]) {
		return false
	}
	upper, lower := tunnelBands(v.tunF[prev], v.tunS[prev])
	if long {
		return v.closes[prev] > upper
	}
	return v.closes[prev] < lower
}

func (s *VegasTunnel) initialStop(v vegasView, i int, long bool) (float64, string) {
	price := v.closes[i]
	atr := v.atr[i]
	_, lower := tunnelBands(v.tunF[i], v.tunS[i])
	upper, _ := tunnelBands(v.tunF[i], v.tunS[i])
	anchor := price
	for _, j := range []int{i, i - 1} {
		if j < 0 {
			continue
		}
		if long && v.lows[j] < anchor {
			anchor = v.lows[j]
		}
		if !long && (anchor == price || v.highs[j] > anchor) {
			anchor = v.highs[j]
		}
	}
	pad := s.vg.ATRStopMult * atr
	var stop float64
	if long {
		stop = math.Min(anchor, lower) - pad
		if s.vg.MinStopATR > 0 && price-stop < s.vg.MinStopATR*atr {
			stop = price - s.vg.MinStopATR*atr
		}
		if stop >= price {
			return 0, "stop not below price"
		}
		return stop, ""
	}
	stop = math.Max(anchor, upper) + pad
	if s.vg.MinStopATR > 0 && stop-price < s.vg.MinStopATR*atr {
		stop = price + s.vg.MinStopATR*atr
	}
	if stop <= price {
		return 0, "stop not above price"
	}
	return stop, ""
}

// stackAge counts consecutive bars, including i, that the tunnel has stayed
// ordered for this side. It stops at the 144/169 cross.
func stackAge(v vegasView, i int, long bool) int {
	age := 0
	for j := i; j >= 0; j-- {
		if math.IsNaN(v.tunF[j]) || math.IsNaN(v.tunS[j]) {
			break
		}
		if long && v.tunF[j] <= v.tunS[j] {
			break
		}
		if !long && v.tunF[j] >= v.tunS[j] {
			break
		}
		age++
	}
	return age
}

func tunnelBands(fast, slow float64) (upper, lower float64) {
	if math.IsNaN(fast) || math.IsNaN(slow) {
		return math.NaN(), math.NaN()
	}
	if fast >= slow {
		return fast, slow
	}
	return slow, fast
}

func vegasBarsHeld(bars []types.Kline, entry time.Time, i int) (int, bool) {
	if entry.IsZero() {
		return 0, false
	}
	held := 0
	for j := i; j >= 0 && bars[j].CloseTime.After(entry); j-- {
		held++
	}
	return held, true
}
