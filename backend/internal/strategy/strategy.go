package strategy

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

// PositionState is what the engine knows about the currently open position.
// The strategy is stateless with respect to fills: everything it needs to
// manage an open trade is pushed in here every cycle, so a restart resumes
// correctly from the persisted snapshot.
type PositionState struct {
	Long     bool
	Short    bool
	Entry    float64
	Trail    float64
	Quantity float64
	// InitQty and InitStop are captured at entry and never change, so R-multiple
	// progress and "have we already taken TP1" survive a restart.
	InitQty   float64
	InitStop  float64
	EntryTime time.Time
}

func (p PositionState) Flat() bool { return !p.Long && !p.Short }

// Reduced reports whether part of the original size has already been taken off,
// which is how the strategy knows the first take-profit already fired.
func (p PositionState) Reduced() bool {
	return p.InitQty > 0 && p.Quantity > 0 && p.Quantity < p.InitQty*0.99
}

// MarketContext carries perpetual-specific data that is not in the candles.
type MarketContext struct {
	Mark            float64
	FundingRate     float64
	NextFundingTime time.Time
	HasFunding      bool
}

// Strategy turns candles plus market context into a single decision.
type Strategy interface {
	Name() string
	Sync(pos PositionState)
	Evaluate(bars []types.Kline, mkt MarketContext) types.Signal
}

// New builds the strategy selected by config.
func New(name, symbol string, cfg config.StrategyConfig) (Strategy, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "trend", "trend_follow":
		return NewTrendFollow(symbol, cfg), nil
	case "squeeze", "squeeze_breakout":
		return NewSqueezeBreakout(symbol, cfg), nil
	default:
		return nil, fmt.Errorf("unknown strategy %q (want \"trend\" or \"squeeze\")", name)
	}
}

// closedBars drops a still-forming final candle so signals never repaint.
func closedBars(klines []types.Kline) []types.Kline {
	if n := len(klines); n > 0 && !klines[n-1].Closed {
		return klines[:n-1]
	}
	return klines
}

func ohlc(bars []types.Kline) (highs, lows, closes []float64) {
	highs = make([]float64, len(bars))
	lows = make([]float64, len(bars))
	closes = make([]float64, len(bars))
	for i, k := range bars {
		highs[i] = k.High
		lows[i] = k.Low
		closes[i] = k.Close
	}
	return
}

func anyNaN(vals ...float64) bool {
	for _, v := range vals {
		if math.IsNaN(v) {
			return true
		}
	}
	return false
}
