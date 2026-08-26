package strategy

import (
	"fmt"
	"math"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// TrendFollow implements EMA20/60 + EMA200 filter + ATR stops + ADX/DMI chop gates.
type TrendFollow struct {
	cfg config.StrategyConfig
	sym string

	// runtime trail state (caller may sync from portfolio)
	inLong        bool
	inShort       bool
	trailStop     float64
	entryPrice    float64
	swingLookback int
}

func NewTrendFollow(sym string, cfg config.StrategyConfig) *TrendFollow {
	return &TrendFollow{
		cfg:           cfg,
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

// Evaluate runs on closed primary-timeframe bars only.
func (s *TrendFollow) Evaluate(klines []types.Kline, _ MarketContext) types.Signal {
	sig := types.Signal{Symbol: s.sym, Action: types.ActionNone}
	if len(klines) < s.cfg.MinBars {
		sig.Reason = fmt.Sprintf("warmup %d/%d", len(klines), s.cfg.MinBars)
		return sig
	}

	bars := closedBars(klines)
	if len(bars) < s.cfg.MinBars {
		sig.Reason = "waiting closed bar"
		return sig
	}

	highs, lows, closes := ohlc(bars)

	emaFast := indicators.EMA(closes, s.cfg.EMAFast)
	emaSlow := indicators.EMA(closes, s.cfg.EMASlow)
	emaFilter := indicators.EMA(closes, s.cfg.EMAFilter)
	atr := indicators.ATR(highs, lows, closes, s.cfg.ATRPeriod)
	plusDI, minusDI, adx := indicators.DMI(highs, lows, closes, s.cfg.ADXPeriod)

	i := len(bars) - 1
	prev := i - 1
	price := closes[i]
	sig.Time = bars[i].CloseTime
	sig.Price = price
	sig.EMA20 = emaFast[i]
	sig.EMA60 = emaSlow[i]
	sig.EMA200 = emaFilter[i]
	sig.ATR = atr[i]
	sig.ADX = adx[i]

	if anyNaN(emaFast[i], emaSlow[i], atr[i], adx[i]) {
		sig.Reason = "indicator NaN"
		return sig
	}
	if s.cfg.UseEMA200Filter && math.IsNaN(emaFilter[i]) {
		sig.Reason = "ema200 warmup"
		return sig
	}

	bullTrend := emaFast[i] > emaSlow[i]
	bearTrend := emaFast[i] < emaSlow[i]
	if s.cfg.UseEMA200Filter {
		bullTrend = bullTrend && price > emaFilter[i]
		bearTrend = bearTrend && price < emaFilter[i]
	}

	// Manage open position: trail + trend flip exit.
	if s.inLong {
		trail := price - s.cfg.ATRTrailMult*atr[i]
		if s.trailStop == 0 || trail > s.trailStop {
			s.trailStop = trail
		}
		sig.StopLoss = s.trailStop
		if price <= s.trailStop {
			sig.Action = types.ActionCloseLong
			sig.Reason = "trail stop hit"
			return sig
		}
		if emaFast[i] < emaSlow[i] {
			sig.Action = types.ActionCloseLong
			sig.Reason = "ema cross down"
			return sig
		}
		sig.Reason = "hold long"
		return sig
	}
	if s.inShort {
		trail := price + s.cfg.ATRTrailMult*atr[i]
		if s.trailStop == 0 || trail < s.trailStop {
			s.trailStop = trail
		}
		sig.StopLoss = s.trailStop
		if price >= s.trailStop {
			sig.Action = types.ActionCloseShort
			sig.Reason = "trail stop hit"
			return sig
		}
		if emaFast[i] > emaSlow[i] {
			sig.Action = types.ActionCloseShort
			sig.Reason = "ema cross up"
			return sig
		}
		sig.Reason = "hold short"
		return sig
	}

	// Flat: chop / range gates before new entries.
	if reason := s.chopBlock(adx, emaFast, emaSlow, atr, i); reason != "" {
		sig.Reason = reason
		return sig
	}

	distEMA := math.Abs(price - emaFast[i])
	if distEMA > s.cfg.ChaseMaxATR*atr[i] {
		sig.Reason = "too far from EMA20, no chase"
		return sig
	}

	// Pullback / reclaim: previous bar near/below EMA20, current closes back above (long).
	crossedUp := emaFast[prev] <= emaSlow[prev] && emaFast[i] > emaSlow[i]
	crossedDown := emaFast[prev] >= emaSlow[prev] && emaFast[i] < emaSlow[i]
	pullbackLong := bullTrend && lows[i] <= emaFast[i] && price > emaFast[i]
	pullbackShort := bearTrend && highs[i] >= emaFast[i] && price < emaFast[i]

	// Fresh crosses are noisy in mild trends: demand a stronger ADX reading.
	crossADXFloor := s.cfg.ADXMin + s.cfg.CrossADXBonus
	if crossedUp && adx[i] < crossADXFloor {
		crossedUp = false
	}
	if crossedDown && adx[i] < crossADXFloor {
		crossedDown = false
	}

	swingLow := indicators.SwingLow(lows, i, s.swingLookback)
	swingHigh := indicators.SwingHigh(highs, i, s.swingLookback)

	if bullTrend && (crossedUp || pullbackLong) {
		if reason := s.directionBlock(true, plusDI[i], minusDI[i], emaSlow, atr, i); reason != "" {
			sig.Reason = reason
			return sig
		}
		stopATR := price - s.cfg.ATRStopMult*atr[i]
		stop := stopATR
		if !math.IsNaN(swingLow) && swingLow < stop {
			stop = swingLow
		}
		sig.Action = types.ActionOpenLong
		sig.StopLoss = stop
		s.trailStop = stop
		if crossedUp {
			sig.Reason = "EMA cross up + trend filter"
		} else {
			sig.Reason = "pullback to EMA20 reclaim"
		}
		return sig
	}

	if bearTrend && (crossedDown || pullbackShort) {
		if reason := s.directionBlock(false, plusDI[i], minusDI[i], emaSlow, atr, i); reason != "" {
			sig.Reason = reason
			return sig
		}
		stopATR := price + s.cfg.ATRStopMult*atr[i]
		stop := stopATR
		if !math.IsNaN(swingHigh) && swingHigh > stop {
			stop = swingHigh
		}
		sig.Action = types.ActionOpenShort
		sig.StopLoss = stop
		s.trailStop = stop
		if crossedDown {
			sig.Reason = "EMA cross down + trend filter"
		} else {
			sig.Reason = "pullback to EMA20 reject"
		}
		return sig
	}

	sig.Reason = "no setup"
	return sig
}

// chopBlock rejects entries when the market is ranging: weak/falling ADX or tangled EMAs.
func (s *TrendFollow) chopBlock(adx, emaFast, emaSlow, atr []float64, i int) string {
	if adx[i] < s.cfg.ADXMin {
		return fmt.Sprintf("ADX %.1f < %.1f chop", adx[i], s.cfg.ADXMin)
	}
	if n := s.cfg.ADXRisingBars; n > 0 {
		j := i - n
		if j < 0 || math.IsNaN(adx[j]) {
			return "ADX rising warmup"
		}
		if adx[i] <= adx[j] {
			return fmt.Sprintf("ADX falling %.1f≤%.1f chop", adx[i], adx[j])
		}
	}
	if s.cfg.EMASepMinATR > 0 {
		sep := math.Abs(emaFast[i] - emaSlow[i])
		minSep := s.cfg.EMASepMinATR * atr[i]
		if sep < minSep {
			return fmt.Sprintf("EMA tangled sep=%.3f < %.3f ATR", sep, minSep)
		}
	}
	return ""
}

// directionBlock confirms the trade side via +DI/−DI and slow-EMA slope.
func (s *TrendFollow) directionBlock(long bool, plusDI, minusDI float64, emaSlow, atr []float64, i int) string {
	if s.cfg.UseDIFilter {
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
	if bars := s.cfg.EMASlopeBars; bars > 0 && s.cfg.EMASlopeMinATR > 0 {
		j := i - bars
		if j < 0 || math.IsNaN(emaSlow[j]) {
			return "EMA slope warmup"
		}
		slope := emaSlow[i] - emaSlow[j]
		minMove := s.cfg.EMASlopeMinATR * atr[i]
		if long && slope < minMove {
			return fmt.Sprintf("EMA%d flat/down slope=%.3f chop", s.cfg.EMASlow, slope)
		}
		if !long && slope > -minMove {
			return fmt.Sprintf("EMA%d flat/up slope=%.3f chop", s.cfg.EMASlow, slope)
		}
	}
	return ""
}
