package strategy

import (
	"fmt"
	"math"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// TrendFollow implements EMA20/60 + EMA200 filter + ATR stops + ADX/DMI chop gates.
//
// When the engine supplies a smaller entry timeframe (typically 15m) alongside
// the primary trend bars (typically 1h):
//   - primary: direction, chop gates, EMA200, trend-flip exit
//   - entry:   pullback / cross timing, chase distance, ATR trail
type TrendFollow struct {
	cfg config.StrategyConfig
	tr  config.TrendConfig
	sym string

	// runtime trail state (caller may sync from portfolio)
	inLong        bool
	inShort       bool
	trailStop     float64
	entryPrice    float64
	swingLookback int
}

func NewTrendFollow(sym string, cfg config.StrategyConfig) *TrendFollow {
	if cfg.ATRPeriod <= 0 {
		cfg.ATRPeriod = 14
	}
	return &TrendFollow{
		cfg:           cfg,
		tr:            cfg.Trend.WithDefaults(),
		sym:           sym,
		swingLookback: 10,
	}
}

func (s *TrendFollow) Name() string { return "trend" }

func (s *TrendFollow) Sync(pos PositionState) {
	s.SyncPosition(pos.Long, pos.Short, pos.Entry, pos.Trail)
}

func (s *TrendFollow) SyncPosition(long, short bool, entry, trail float64) {
	s.inLong = long
	s.inShort = short
	s.entryPrice = entry
	s.trailStop = trail
}

func (s *TrendFollow) entryMinBars() int {
	need := s.tr.EMASlow + 2
	if s.cfg.ATRPeriod+2 > need {
		need = s.cfg.ATRPeriod + 2
	}
	if s.swingLookback+2 > need {
		need = s.swingLookback + 2
	}
	return need
}

// Evaluate runs on closed bars. klines are the primary (higher) timeframe.
// mkt.Entry, when set, is the smaller timeframe used to time entries and trail.
func (s *TrendFollow) Evaluate(klines []types.Kline, mkt MarketContext) types.Signal {
	sig := types.Signal{Symbol: s.sym, Action: types.ActionNone}
	if len(klines) < s.cfg.MinBars {
		sig.Reason = fmt.Sprintf("warmup %d/%d", len(klines), s.cfg.MinBars)
		return sig
	}

	trend := closedBars(klines)
	if len(trend) < s.cfg.MinBars {
		sig.Reason = "waiting closed bar"
		return sig
	}

	entry := trend
	mtf := len(mkt.Entry) > 0
	if mtf {
		entry = closedBars(mkt.Entry)
		if len(entry) < s.entryMinBars() {
			sig.Reason = fmt.Sprintf("entry warmup %d/%d", len(entry), s.entryMinBars())
			return sig
		}
	}

	return s.evalBars(trend, entry, mtf)
}

func (s *TrendFollow) evalBars(trend, entry []types.Kline, mtf bool) types.Signal {
	sig := types.Signal{Symbol: s.sym, Action: types.ActionNone}

	th, tl, tc := ohlc(trend)
	eh, el, ec := ohlc(entry)

	tFast := indicators.EMA(tc, s.tr.EMAFast)
	tSlow := indicators.EMA(tc, s.tr.EMASlow)
	tFilter := indicators.EMA(tc, s.tr.EMAFilter)
	tATR := indicators.ATR(th, tl, tc, s.cfg.ATRPeriod)
	plusDI, minusDI, tADX := indicators.DMI(th, tl, tc, s.tr.ADXPeriod)

	eFast := indicators.EMA(ec, s.tr.EMAFast)
	eSlow := indicators.EMA(ec, s.tr.EMASlow)
	eATR := indicators.ATR(eh, el, ec, s.cfg.ATRPeriod)

	i := len(entry) - 1
	j, ok := lastIndexAtOrBefore(trend, entry[i].CloseTime)
	if !ok {
		sig.Reason = "waiting higher-tf bar"
		return sig
	}

	price := ec[i]
	sig.Time = entry[i].CloseTime
	sig.Price = price
	sig.EMA20 = tFast[j]
	sig.EMA60 = tSlow[j]
	sig.EMA200 = tFilter[j]
	sig.ATR = eATR[i]
	sig.ADX = tADX[j]

	if anyNaN(tFast[j], tSlow[j], eATR[i], tADX[j]) {
		sig.Reason = "indicator NaN"
		return sig
	}
	if s.tr.UseEMA200Filter && math.IsNaN(tFilter[j]) {
		sig.Reason = "ema200 warmup"
		return sig
	}

	bullTrend := tFast[j] > tSlow[j]
	bearTrend := tFast[j] < tSlow[j]
	if s.tr.UseEMA200Filter {
		bullTrend = bullTrend && tc[j] > tFilter[j] && price > tFilter[j]
		bearTrend = bearTrend && tc[j] < tFilter[j] && price < tFilter[j]
	}

	// Manage open position: trail on the entry TF, invalidate on higher-TF flip.
	if s.inLong {
		trail := price - s.tr.ATRTrailMult*eATR[i]
		if s.trailStop == 0 || trail > s.trailStop {
			s.trailStop = trail
		}
		sig.StopLoss = s.trailStop
		if price <= s.trailStop {
			sig.Action = types.ActionCloseLong
			sig.Reason = "trail stop hit"
			return sig
		}
		if tFast[j] < tSlow[j] {
			sig.Action = types.ActionCloseLong
			sig.Reason = "ema cross down"
			return sig
		}
		sig.Reason = "hold long"
		return sig
	}
	if s.inShort {
		trail := price + s.tr.ATRTrailMult*eATR[i]
		if s.trailStop == 0 || trail < s.trailStop {
			s.trailStop = trail
		}
		sig.StopLoss = s.trailStop
		if price >= s.trailStop {
			sig.Action = types.ActionCloseShort
			sig.Reason = "trail stop hit"
			return sig
		}
		if tFast[j] > tSlow[j] {
			sig.Action = types.ActionCloseShort
			sig.Reason = "ema cross up"
			return sig
		}
		sig.Reason = "hold short"
		return sig
	}

	if anyNaN(eFast[i], eSlow[i]) {
		sig.Reason = "entry ema warmup"
		return sig
	}

	// Flat: chop / range gates on the higher TF before new entries.
	if reason := s.chopBlock(tADX, tFast, tSlow, tATR, j); reason != "" {
		if mtf {
			sig.Reason = "1h " + reason
		} else {
			sig.Reason = reason
		}
		return sig
	}

	distEMA := math.Abs(price - eFast[i])
	if distEMA > s.tr.ChaseMaxATR*eATR[i] {
		sig.Reason = "too far from EMA20, no chase"
		return sig
	}

	prev := i - 1
	crossedUp := false
	crossedDown := false
	if prev >= 0 && !anyNaN(eFast[prev], eSlow[prev]) {
		crossedUp = eFast[prev] <= eSlow[prev] && eFast[i] > eSlow[i]
		crossedDown = eFast[prev] >= eSlow[prev] && eFast[i] < eSlow[i]
	}
	// Lower-TF pullback still has to be aligned with that TF's EMA20/60.
	pullbackLong := bullTrend && eFast[i] > eSlow[i] && el[i] <= eFast[i] && price > eFast[i]
	pullbackShort := bearTrend && eFast[i] < eSlow[i] && eh[i] >= eFast[i] && price < eFast[i]

	// Fresh crosses are noisy in mild trends: demand a stronger ADX reading.
	// On MTF the higher TF already passed the chop gates, so skip the extra hurdle.
	if !mtf {
		crossADXFloor := s.tr.ADXMin + s.tr.CrossADXBonus
		if crossedUp && tADX[j] < crossADXFloor {
			crossedUp = false
		}
		if crossedDown && tADX[j] < crossADXFloor {
			crossedDown = false
		}
	}

	swingLow := indicators.SwingLow(el, i, s.swingLookback)
	swingHigh := indicators.SwingHigh(eh, i, s.swingLookback)

	if bullTrend && (crossedUp || pullbackLong) {
		if reason := s.directionBlock(true, plusDI[j], minusDI[j], tSlow, tATR, j); reason != "" {
			if mtf {
				sig.Reason = "1h " + reason
			} else {
				sig.Reason = reason
			}
			return sig
		}
		stopATR := price - s.tr.ATRStopMult*eATR[i]
		stop := stopATR
		if !math.IsNaN(swingLow) && swingLow < stop {
			stop = swingLow
		}
		sig.Action = types.ActionOpenLong
		sig.StopLoss = stop
		s.trailStop = stop
		sig.Reason = s.entryReason(mtf, true, crossedUp)
		return sig
	}

	if bearTrend && (crossedDown || pullbackShort) {
		if reason := s.directionBlock(false, plusDI[j], minusDI[j], tSlow, tATR, j); reason != "" {
			if mtf {
				sig.Reason = "1h " + reason
			} else {
				sig.Reason = reason
			}
			return sig
		}
		stopATR := price + s.tr.ATRStopMult*eATR[i]
		stop := stopATR
		if !math.IsNaN(swingHigh) && swingHigh > stop {
			stop = swingHigh
		}
		sig.Action = types.ActionOpenShort
		sig.StopLoss = stop
		s.trailStop = stop
		sig.Reason = s.entryReason(mtf, false, crossedDown)
		return sig
	}

	sig.Reason = "no setup"
	return sig
}

func (s *TrendFollow) entryReason(mtf, long, crossed bool) string {
	if mtf {
		if long {
			if crossed {
				return "15m EMA cross up + 1h trend"
			}
			return "15m pullback to EMA20 reclaim"
		}
		if crossed {
			return "15m EMA cross down + 1h trend"
		}
		return "15m pullback to EMA20 reject"
	}
	if long {
		if crossed {
			return "EMA cross up + trend filter"
		}
		return "pullback to EMA20 reclaim"
	}
	if crossed {
		return "EMA cross down + trend filter"
	}
	return "pullback to EMA20 reject"
}

// chopBlock rejects entries when the market is ranging: weak/falling ADX or tangled EMAs.
func (s *TrendFollow) chopBlock(adx, emaFast, emaSlow, atr []float64, i int) string {
	if adx[i] < s.tr.ADXMin {
		return fmt.Sprintf("ADX %.1f < %.1f chop", adx[i], s.tr.ADXMin)
	}
	if n := s.tr.ADXRisingBars; n > 0 {
		j := i - n
		if j < 0 || math.IsNaN(adx[j]) {
			return "ADX rising warmup"
		}
		if adx[i] <= adx[j] {
			return fmt.Sprintf("ADX falling %.1f≤%.1f chop", adx[i], adx[j])
		}
	}
	if s.tr.EMASepMinATR > 0 {
		sep := math.Abs(emaFast[i] - emaSlow[i])
		minSep := s.tr.EMASepMinATR * atr[i]
		if sep < minSep {
			return fmt.Sprintf("EMA tangled sep=%.3f < %.3f ATR", sep, minSep)
		}
	}
	return ""
}

// directionBlock confirms the trade side via +DI/−DI and slow-EMA slope.
func (s *TrendFollow) directionBlock(long bool, plusDI, minusDI float64, emaSlow, atr []float64, i int) string {
	if s.tr.UseDIFilter {
		if math.IsNaN(plusDI) || math.IsNaN(minusDI) {
			return "DI warmup"
		}
		if long && plusDI <= minusDI {
			return fmt.Sprintf("+DI %.1f ≤ −DI %.1f chop", plusDI, minusDI)
		}
		if !long && minusDI <= plusDI {
			return fmt.Sprintf("−DI %.1f ≤ +DI %.1f chop", minusDI, plusDI)
		}
	}
	if bars := s.tr.EMASlopeBars; bars > 0 && s.tr.EMASlopeMinATR > 0 {
		j := i - bars
		if j < 0 || math.IsNaN(emaSlow[j]) {
			return "EMA slope warmup"
		}
		slope := emaSlow[i] - emaSlow[j]
		minMove := s.tr.EMASlopeMinATR * atr[i]
		if long && slope < minMove {
			return fmt.Sprintf("EMA%d flat/down slope=%.3f chop", s.tr.EMASlow, slope)
		}
		if !long && slope > -minMove {
			return fmt.Sprintf("EMA%d flat/up slope=%.3f chop", s.tr.EMASlow, slope)
		}
	}
	return ""
}
