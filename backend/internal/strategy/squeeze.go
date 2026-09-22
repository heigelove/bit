package strategy

import (
	"fmt"
	"math"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// SqueezeBreakout trades Donchian channel breakouts that fire out of a
// volatility squeeze, with a funding-rate crowding filter.
//
// The edge it targets is specific to perpetuals: quiet ranges build up stop and
// liquidation clusters just outside the channel, and once price takes them out
// the resulting cascade produces the expansion leg. The funding filter keeps us
// from buying a breakout that the whole market is already long, which is the
// setup most likely to be a liquidity grab rather than a real move.
//
// Position management is deliberately asymmetric: take part of the trade off at
// TP1 to bank the frequent small wins, then let a Chandelier trail run the rest,
// and cut anything that stalls (a real breakout does not go sideways).
type SqueezeBreakout struct {
	cfg config.StrategyConfig
	sq  config.SqueezeConfig
	sym string

	minBars int
	pos     PositionState
	lock    ReentryLock
}

func NewSqueezeBreakout(sym string, cfg config.StrategyConfig) *SqueezeBreakout {
	sq := cfg.Squeeze.WithDefaults()
	if cfg.ATRPeriod <= 0 {
		cfg.ATRPeriod = 14
	}

	// Every indicator must be warm before the first signal, otherwise the ATR
	// percentile is computed over a handful of samples and lets everything through.
	need := max(
		cfg.ATRPeriod+1+sq.ATRLookback,
		sq.Donchian+1,
		sq.ChandelierPeriod+1,
	)
	if sq.TrendFilterEnabled() {
		need = max(need, sq.TrendEMA+1)
	}

	return &SqueezeBreakout{
		cfg:     cfg,
		sq:      sq,
		sym:     sym,
		minBars: max(cfg.MinBars, need+5),
	}
}

func (s *SqueezeBreakout) Name() string { return "squeeze" }

func (s *SqueezeBreakout) Sync(pos PositionState) {
	s.lock.ArmOnFlatten(s.pos, pos)
	s.pos = pos
}

func (s *SqueezeBreakout) ReentryLock() ReentryLock { return s.lock }

func (s *SqueezeBreakout) RestoreReentryLock(l ReentryLock) { s.lock = l }

func (s *SqueezeBreakout) Evaluate(klines []types.Kline, mkt MarketContext) types.Signal {
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
	up, mid, dn := indicators.Donchian(highs, lows, s.sq.Donchian)
	trend := indicators.EMA(closes, s.sq.TrendEMA)

	i := len(bars) - 1
	price := closes[i]

	sig.Time = bars[i].CloseTime
	sig.Price = price
	sig.ATR = atr[i]
	sig.EMA200 = trend[i]
	sig.DonchianUp = up[i]
	sig.DonchianDn = dn[i]
	sig.Funding = mkt.FundingRate

	if anyNaN(atr[i], up[i], dn[i]) || atr[i] <= 0 {
		sig.Reason = "indicator warmup"
		return sig
	}

	atrPct := relativeATR(atr, closes)
	sig.ATRPct = atrPct[i]

	if !s.pos.Flat() {
		return s.manage(sig, bars, highs, lows, atr, mid, i)
	}
	return s.entry(sig, bars, closes, atrPct, atr, up, dn, trend, mkt, i)
}

// manage handles an open position: trail, structural failure, time stop, TP1.
func (s *SqueezeBreakout) manage(sig types.Signal, bars []types.Kline, highs, lows, atr, mid []float64, i int) types.Signal {
	long := s.pos.Long
	price := sig.Price
	entry := s.pos.Entry

	riskPerUnit := math.Abs(entry - s.pos.InitStop)
	if riskPerUnit <= 0 {
		// No persisted entry stop (e.g. position adopted from a previous run):
		// fall back to the distance the strategy would have used at entry.
		riskPerUnit = s.sq.ATRStopMult * atr[i]
	}
	progressR := 0.0
	if riskPerUnit > 0 {
		if long {
			progressR = (price - entry) / riskPerUnit
		} else {
			progressR = (entry - price) / riskPerUnit
		}
	}

	closeAction := types.ActionCloseShort
	reduceAction := types.ActionReduceShort
	if long {
		closeAction = types.ActionCloseLong
		reduceAction = types.ActionReduceLong
	}

	trail := s.ratchetTrail(highs, lows, atr, i, long)
	sig.StopLoss = trail

	if trail > 0 {
		if (long && price <= trail) || (!long && price >= trail) {
			sig.Action = closeAction
			sig.Reason = fmt.Sprintf("trail stop %.2f hit", trail)
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	// A breakout that closes back inside the channel has failed by definition.
	if !math.IsNaN(mid[i]) {
		if (long && price < mid[i]) || (!long && price > mid[i]) {
			sig.Action = closeAction
			sig.Reason = "closed back inside channel"
			s.lock.ArmOnFlatten(s.pos, PositionState{})
			return sig
		}
	}

	if held, ok := s.barsHeld(bars, i); ok && held >= s.sq.TimeStopBars && progressR < s.sq.TimeStopMinR {
		sig.Action = closeAction
		sig.Reason = fmt.Sprintf("time stop %d bars at %.2fR", held, progressR)
		s.lock.ArmOnFlatten(s.pos, PositionState{})
		return sig
	}

	if !s.pos.Reduced() && progressR >= s.sq.TP1R {
		sig.Action = reduceAction
		sig.Portion = s.sq.TP1Portion
		sig.Reason = fmt.Sprintf("TP1 %.2fR, take %.0f%%", progressR, s.sq.TP1Portion*100)
		return sig
	}

	sig.Reason = fmt.Sprintf("hold %.2fR", progressR)
	return sig
}

// ratchetTrail returns the Chandelier stop, never allowed to move against the
// position, and floored at breakeven once TP1 has been taken.
func (s *SqueezeBreakout) ratchetTrail(highs, lows, atr []float64, i int, long bool) float64 {
	var stop float64
	if long {
		stop = indicators.ChandelierLong(highs, atr[i], i, s.sq.ChandelierPeriod, s.sq.ChandelierMult)
	} else {
		stop = indicators.ChandelierShort(lows, atr[i], i, s.sq.ChandelierPeriod, s.sq.ChandelierMult)
	}
	if math.IsNaN(stop) {
		return s.pos.Trail
	}
	if s.sq.BreakevenAfterTP1 && s.pos.Reduced() && s.pos.Entry > 0 {
		if long {
			stop = math.Max(stop, s.pos.Entry)
		} else {
			stop = math.Min(stop, s.pos.Entry)
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

// entry applies the squeeze, breakout, trend and funding gates in order.
func (s *SqueezeBreakout) entry(sig types.Signal, bars []types.Kline, closes, atrPct, atr, up, dn, trend []float64, mkt MarketContext, i int) types.Signal {
	price := sig.Price

	inside := price <= up[i] && price >= dn[i]
	if s.lock.Armed {
		s.lock.Stamp(sig.Time)
		if inside {
			s.lock.MarkReset()
		}
	}

	// Measure compression on the bar before the breakout. A decisive breakout
	// bar has a large true range by construction, so including it would let the
	// signal veto itself.
	rank := indicators.PercentileRank(atrPct, i-1, s.sq.ATRLookback)
	if math.IsNaN(rank) {
		sig.Reason = "atr percentile warmup"
		return sig
	}
	if rank > s.sq.ATRPctMax {
		sig.Reason = fmt.Sprintf("no squeeze: ATR%% rank %.2f > %.2f", rank, s.sq.ATRPctMax)
		return sig
	}

	// A breakout is a cross, not a state: staying outside the channel after a
	// stop must not keep re-firing. Require the previous close to have been
	// on or inside the prior edge.
	breakUp := price > up[i]
	breakDown := price < dn[i]
	if i > 0 && !math.IsNaN(up[i-1]) && !math.IsNaN(dn[i-1]) {
		breakUp = breakUp && closes[i-1] <= up[i-1]
		breakDown = breakDown && closes[i-1] >= dn[i-1]
	}
	if !breakUp && !breakDown {
		if inside {
			sig.Reason = fmt.Sprintf("inside channel %.2f-%.2f", dn[i], up[i])
		} else {
			sig.Reason = "outside channel, no fresh cross"
		}
		return sig
	}

	long := breakUp
	if s.sq.TrendFilterEnabled() && !math.IsNaN(trend[i]) {
		if (long && price < trend[i]) || (!long && price > trend[i]) {
			sig.Reason = fmt.Sprintf("against EMA%d trend", s.sq.TrendEMA)
			return sig
		}
	}

	edge := up[i]
	if !long {
		edge = dn[i]
	}
	if math.Abs(price-edge) > s.sq.BreakoutMaxATR*atr[i] {
		sig.Reason = "breakout already extended, no chase"
		return sig
	}

	// Funding is the crowding tell: paying up to hold the side that just broke
	// out means the move is likely already consensus.
	if s.sq.FundingFilterEnabled() && mkt.HasFunding {
		if long && mkt.FundingRate > s.sq.FundingAbsMax {
			sig.Reason = fmt.Sprintf("funding %.4f%% too crowded long", mkt.FundingRate*100)
			return sig
		}
		if !long && mkt.FundingRate < -s.sq.FundingAbsMax {
			sig.Reason = fmt.Sprintf("funding %.4f%% too crowded short", mkt.FundingRate*100)
			return sig
		}
	}

	held := barsOnOrAfter(bars, s.lock.BarTime)
	if reason := s.lock.Block(long, price, edge, atr[i], s.sq.ReentryATR, s.sq.ReentryCooldown, held, s.sq.ResetRequired()); reason != "" {
		sig.Reason = reason
		return sig
	}

	dist := s.sq.ATRStopMult * atr[i]
	if long {
		sig.Action = types.ActionOpenLong
		sig.StopLoss = price - dist
	} else {
		sig.Action = types.ActionOpenShort
		sig.StopLoss = price + dist
	}
	s.lock.NoteEntry(long, price, edge)
	sig.Reason = fmt.Sprintf("squeeze breakout, ATR%% rank %.2f", rank)
	return sig
}

// barsHeld counts bars that closed after entry. Reports false when the entry
// time is unknown, so the time stop stays disabled rather than firing at once.
func (s *SqueezeBreakout) barsHeld(bars []types.Kline, i int) (int, bool) {
	if s.pos.EntryTime.IsZero() {
		return 0, false
	}
	held := 0
	for j := i; j >= 0 && bars[j].CloseTime.After(s.pos.EntryTime); j-- {
		held++
	}
	return held, true
}

func relativeATR(atr, closes []float64) []float64 {
	out := make([]float64, len(atr))
	for i := range out {
		if math.IsNaN(atr[i]) || closes[i] <= 0 {
			out[i] = math.NaN()
			continue
		}
		out[i] = atr[i] / closes[i]
	}
	return out
}
