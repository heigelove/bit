package risk

import (
	"testing"
	"time"

	"github.com/work/bit/internal/config"
)

func TestRecordClosedTradeHaltsAfterConsecutiveLosses(t *testing.T) {
	m := NewManager(config.RiskConfig{
		RiskPerTrade:       0.01,
		MaxDailyLossR:      10,
		MaxConsecutiveLoss: 3,
	})
	now := time.Now().UTC()
	equity := 1000.0
	for i := 0; i < 3; i++ {
		m.RecordClosedTrade(now, equity, -5)
	}
	_, cons, halted, reason := m.Snapshot()
	if cons != 3 {
		t.Fatalf("consecutive = %d, want 3", cons)
	}
	if !halted {
		t.Fatalf("expected halt after 3 losses, reason=%q", reason)
	}
	if err := m.AllowOpen(now, equity); err == nil {
		t.Fatal("AllowOpen should refuse after halt")
	}
}

func TestRecordClosedTradeWinResetsStreak(t *testing.T) {
	m := NewManager(config.RiskConfig{
		RiskPerTrade:       0.01,
		MaxDailyLossR:      10,
		MaxConsecutiveLoss: 3,
	})
	now := time.Now().UTC()
	m.RecordClosedTrade(now, 1000, -5)
	m.RecordClosedTrade(now, 1000, 8)
	_, cons, halted, _ := m.Snapshot()
	if cons != 0 || halted {
		t.Fatalf("win should reset streak, cons=%d halted=%v", cons, halted)
	}
}
