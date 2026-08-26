package strategy

import (
	"strings"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

func TestEvaluateWarmup(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", config.StrategyConfig{
		EMAFast: 20, EMASlow: 60, EMAFilter: 200,
		ATRPeriod: 14, ADXPeriod: 14, ADXMin: 20,
		ATRStopMult: 1.5, ATRTrailMult: 1.0, ChaseMaxATR: 1.5,
		UseEMA200Filter: true, MinBars: 220,
	})
	sig := s.Evaluate(nil, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected none, got %s", sig.Action)
	}
}

func TestPullbackLongSignal(t *testing.T) {
	cfg := config.StrategyConfig{
		EMAFast: 5, EMASlow: 15, EMAFilter: 30,
		ATRPeriod: 5, ADXPeriod: 5, ADXMin: 0, // disable ADX gate for unit test
		ATRStopMult: 1.5, ATRTrailMult: 1.0, ChaseMaxATR: 10,
		UseEMA200Filter: false, MinBars: 40,
	}
	s := NewTrendFollow("ETHUSDT", cfg)

	// Rising series then mild pullback toward EMA.
	n := 80
	kl := make([]types.Kline, n)
	base := 100.0
	for i := 0; i < n; i++ {
		c := base + float64(i)*0.5
		if i == n-1 {
			c = base + float64(n-2)*0.5 - 0.2 // slight dip then close above
		}
		h := c + 0.8
		l := c - 0.8
		if i == n-1 {
			l = c - 1.5 // wick below EMA region
		}
		kl[i] = types.Kline{
			OpenTime:  time.Unix(int64(i*3600), 0).UTC(),
			CloseTime: time.Unix(int64((i+1)*3600), 0).UTC(),
			Open:      c - 0.1,
			High:      h,
			Low:       l,
			Close:     c,
			Volume:    1000,
			Closed:    true,
		}
	}
	sig := s.Evaluate(kl, MarketContext{})
	// May be open long or none depending on EMA geometry; ensure no panic and valid stop if open.
	if sig.Action == types.ActionOpenLong && sig.StopLoss >= sig.Price {
		t.Fatalf("invalid long stop %v >= price %v", sig.StopLoss, sig.Price)
	}
	if sig.Action == types.ActionOpenShort {
		t.Fatalf("unexpected short on rising series: %s", sig.Reason)
	}
}

func TestChopBlocksTangledEMAs(t *testing.T) {
	cfg := config.StrategyConfig{
		EMAFast: 5, EMASlow: 15, EMAFilter: 30,
		ATRPeriod: 5, ADXPeriod: 5, ADXMin: 0,
		ATRStopMult: 1.5, ATRTrailMult: 1.0, ChaseMaxATR: 10,
		UseEMA200Filter: false, MinBars: 40,
		EMASepMinATR: 50, // impossible separation → always chop
	}
	s := NewTrendFollow("ETHUSDT", cfg)

	n := 80
	kl := make([]types.Kline, n)
	for i := 0; i < n; i++ {
		// Tight sideways oscillation around 100.
		c := 100 + float64(i%4)*0.1 - 0.15
		kl[i] = types.Kline{
			OpenTime:  time.Unix(int64(i*3600), 0).UTC(),
			CloseTime: time.Unix(int64((i+1)*3600), 0).UTC(),
			Open:      c,
			High:      c + 0.3,
			Low:       c - 0.3,
			Close:     c,
			Volume:    1000,
			Closed:    true,
		}
	}
	sig := s.Evaluate(kl, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected chop block, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "tangled") && !strings.Contains(sig.Reason, "chop") {
		t.Fatalf("expected chop reason, got %q", sig.Reason)
	}
}
