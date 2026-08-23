package strategy

import (
	"fmt"
	"math"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// TrendFollow implements EMA20/60 + EMA200 filter + ATR stops + ADX gate.
type TrendFollow struct {
	cfg config.StrategyConfig
	sym string

	// runtime trail state (caller may sync from portfolio)
	inLong       bool
	inShort      bool
	trailStop    float64
	entryPrice   float64
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
	adx := indicators.ADX(highs, lows, closes, s.cfg.ADXPeriod)

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

	// Flat: ADX gate for new entries.
	if adx[i] < s.cfg.ADXMin {
		sig.Reason = fmt.Sprintf("ADX %.1f < %.1f chop", adx[i], s.cfg.ADXMin)
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

	swingLow := indicators.SwingLow(lows, i, s.swingLookback)
	swingHigh := indicators.SwingHigh(highs, i, s.swingLookback)

	if bullTrend && (crossedUp || pullbackLong) {
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
