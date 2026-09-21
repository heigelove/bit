package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/work/bit/internal/types"
)

func TestStoreInserts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.InsertLog(ctx, time.Now(), "INFO", "hello", map[string]any{"k": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSignal(ctx, "paper", types.Signal{
		Time: time.Now(), Symbol: "ETHUSDT", Action: types.ActionNone, Reason: "chop",
		Price: 2000, ATR: 10, ADX: 15,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTrade(ctx, "paper", "oid-1", types.TradeFill{
		Time: time.Now(), Symbol: "ETHUSDT", Side: types.SideBuy,
		Quantity: 0.1, Price: 2000, Fee: 0.08, Reason: "test",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTrade(ctx, "paper", "oid-2", types.TradeFill{
		Time: time.Now().Add(time.Minute), Symbol: "ETHUSDT", Side: types.SideSell,
		Quantity: 0.1, Price: 2100, Fee: 0.084, PNL: 9.916, Reason: "close",
	}); err != nil {
		t.Fatal(err)
	}

	curve, err := s.ListEquityCurve(ctx, "ETHUSDT", "paper", 10000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(curve.Points) != 3 { // seed + 2 trades
		t.Fatalf("points=%d want 3", len(curve.Points))
	}
	if mathAbs(curve.TotalPNL-9.916) > 1e-9 {
		t.Fatalf("total pnl %v", curve.TotalPNL)
	}
	if mathAbs(curve.Points[2].Equity-10009.916) > 1e-9 {
		t.Fatalf("equity %v", curve.Points[2].Equity)
	}

	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM app_logs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("app_logs count=%d err=%v", n, err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM signals`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("signals count=%d err=%v", n, err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM trades`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("trades count=%d err=%v", n, err)
	}
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
