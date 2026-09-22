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
	if cfg.Timeframes.Primary != "1h" {
		t.Fatalf("timeframes.primary = %q, want 1h", cfg.Timeframes.Primary)
	}
	if cfg.Timeframes.Entry != "15m" {
		t.Fatalf("timeframes.entry = %q, want 15m", cfg.Timeframes.Entry)
	}
	if cfg.Strategy.Trend.ADXRisingBars != 2 {
		t.Fatalf("adx_rising_bars = %d, want 2", cfg.Strategy.Trend.ADXRisingBars)
	}
	if cfg.Strategy.Trend.EMASepMinATR != 0.4 {
		t.Fatalf("ema_sep_min_atr = %v, want 0.4", cfg.Strategy.Trend.EMASepMinATR)
	}
	if !cfg.Strategy.Trend.UseDIFilter {
		t.Fatal("use_di_filter should be enabled")
	}
	if cfg.Strategy.Trend.CrossADXBonus != 5 {
		t.Fatalf("cross_adx_bonus = %v, want 5", cfg.Strategy.Trend.CrossADXBonus)
	}
	if cfg.Strategy.ATRPeriod != 14 {
		t.Fatalf("atr_period = %d, want 14", cfg.Strategy.ATRPeriod)
	}
	if cfg.Strategy.MinBars != 220 {
		t.Fatalf("min_bars = %d, want 220", cfg.Strategy.MinBars)
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
	if sq.ReentryATR != 0.25 {
		t.Fatalf("squeeze.reentry_atr = %v, want 0.25", sq.ReentryATR)
	}
	if sq.ReentryCooldown != 2 {
		t.Fatalf("squeeze.reentry_cooldown = %d, want 2", sq.ReentryCooldown)
	}
	if !sq.ResetRequired() {
		t.Fatal("squeeze require_reset should be enabled")
	}
	if cfg.Strategy.Trend.ReentryCooldown != 4 {
		t.Fatalf("trend.reentry_cooldown = %d, want 4", cfg.Strategy.Trend.ReentryCooldown)
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
