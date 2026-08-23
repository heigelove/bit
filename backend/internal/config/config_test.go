package config

import (
	"testing"
)

// The shipped config is the one that actually runs, so a typo'd yaml key here
// would silently fall back to defaults rather than fail.
func TestLoadShippedConfig(t *testing.T) {
	cfg, err := Load("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Strategy.Name != "squeeze" {
		t.Fatalf("strategy.name = %q, want squeeze", cfg.Strategy.Name)
	}

	sq := cfg.Strategy.Squeeze
	if sq.Donchian != 20 {
		t.Fatalf("donchian = %d, want 20", sq.Donchian)
	}
	if sq.ATRLookback != 100 {
		t.Fatalf("atr_lookback = %d, want 100", sq.ATRLookback)
	}
	if sq.ATRStopMult != 1.2 {
		t.Fatalf("squeeze.atr_stop_mult = %v, want 1.2", sq.ATRStopMult)
	}
	if sq.ChandelierMult != 3.0 {
		t.Fatalf("chandelier_mult = %v, want 3.0", sq.ChandelierMult)
	}
	if !sq.BreakevenAfterTP1 {
		t.Fatal("breakeven_after_tp1 should be enabled")
	}
	if !sq.FundingFilterEnabled() || !sq.TrendFilterEnabled() {
		t.Fatal("squeeze filters should be enabled")
	}
	if cfg.Risk.MaxNotionalPct != 0.7 {
		t.Fatalf("max_notional_pct = %v, want 0.7", cfg.Risk.MaxNotionalPct)
	}
}

func TestFiltersDefaultOn(t *testing.T) {
	sq := SqueezeConfig{}.WithDefaults()
	if !sq.FundingFilterEnabled() || !sq.TrendFilterEnabled() {
		t.Fatal("omitted filter keys must default to enabled")
	}
	off := false
	sq.UseFundingFilter = &off
	if sq.FundingFilterEnabled() {
		t.Fatal("explicit false must disable the funding filter")
	}
}

func TestValidateRejectsUnknownStrategy(t *testing.T) {
	c := &Config{Mode: "paper"}
	c.Symbol.Name = "ETHUSDT"
	c.Symbol.Leverage = 5
	c.Risk.RiskPerTrade = 0.01
	c.Strategy.Name = "does-not-exist"
	if err := c.Validate(); err == nil {
		t.Fatal("expected validation error for unknown strategy")
	}
}
