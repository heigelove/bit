package engine

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/risk"
	"github.com/work/bit/internal/strategy"
)

func testLiveEngine() *Engine {
	cfg := &config.Config{
		Mode:   "live",
		Symbol: config.SymbolConfig{Name: "ETHUSDT", Leverage: 5},
		Risk: config.RiskConfig{
			RiskPerTrade:       0.0075,
			MaxDailyLossR:      2,
			MaxConsecutiveLoss: 3,
			QtyPrecision:       3,
			PricePrecision:     2,
		},
	}
	return &Engine{
		cfg:  cfg,
		log:  slog.Default(),
		risk: risk.NewManager(cfg.Risk),
	}
}

func TestRealizedPNL(t *testing.T) {
	if got := realizedPNL(true, 2700, 2690, 1.2); got != -12 {
		t.Fatalf("long pnl = %v, want -12", got)
	}
	if got := realizedPNL(false, 2560, 2570, 0.5); got != -5 {
		t.Fatalf("short pnl = %v, want -5", got)
	}
}

func TestReconcileLiveFlatBooksLossAndCountsStreak(t *testing.T) {
	e := testLiveEngine()
	ctx := context.Background()
	prev := strategy.PositionState{Long: true, Entry: 2702, Quantity: 1.288, Trail: 2697}
	e.lastPos = prev
	e.liveStop = 2697
	e.trade = tradeContext{InitQty: 1.288, InitStop: 2697}

	e.reconcileLiveFlat(ctx, prev, 2697, strategy.PositionState{}, 2696)

	if !e.lastPos.Flat() {
		t.Fatal("lastPos should be cleared after flatten")
	}
	_, cons, halted, _ := e.risk.Snapshot()
	if cons != 1 {
		t.Fatalf("consecutive = %d, want 1", cons)
	}
	if halted {
		t.Fatal("one loss should not halt")
	}
}

func TestReconcileLiveFlatDoesNotDoubleCount(t *testing.T) {
	e := testLiveEngine()
	ctx := context.Background()
	prev := strategy.PositionState{Long: true, Entry: 2700, Quantity: 1}
	e.reconcileLiveFlat(ctx, prev, 2690, strategy.PositionState{}, 2690)
	e.reconcileLiveFlat(ctx, e.lastPos, 0, strategy.PositionState{}, 2690)
	_, cons, _, _ := e.risk.Snapshot()
	if cons != 1 {
		t.Fatalf("consecutive = %d, want 1 (no double count)", cons)
	}
}

func TestThreeExchangeStopsHaltOpens(t *testing.T) {
	e := testLiveEngine()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		prev := strategy.PositionState{Long: true, Entry: 2700, Quantity: 1}
		e.lastPos = prev
		e.reconcileLiveFlat(ctx, prev, 2690, strategy.PositionState{}, 2690)
	}
	_, cons, halted, _ := e.risk.Snapshot()
	if cons != 3 || !halted {
		t.Fatalf("cons=%d halted=%v, want 3 / true", cons, halted)
	}
	if err := e.risk.AllowOpen(time.Now().UTC(), 1000); err == nil {
		t.Fatal("AllowOpen must refuse after 3 live stop-outs")
	}
}

func TestBookClosedPositionSkipsFlat(t *testing.T) {
	e := testLiveEngine()
	e.bookClosedPosition(context.Background(), strategy.PositionState{}, 2700, "noop", "", false)
	_, cons, _, _ := e.risk.Snapshot()
	if cons != 0 {
		t.Fatalf("flat close must not count, cons=%d", cons)
	}
}
