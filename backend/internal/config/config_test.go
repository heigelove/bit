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
	if cfg.Strategy.Name != "trend" {
		t.Fatalf("strategy.name = %q, want trend", cfg.Strategy.Name)
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
	if cfg.Strategy.Trend.EMASepMinATR != 1.0 {
		t.Fatalf("ema_sep_min_atr = %v, want 1.0", cfg.Strategy.Trend.EMASepMinATR)
	}
	if cfg.Strategy.Trend.ADXMin != 25 {
		t.Fatalf("adx_min = %v, want 25", cfg.Strategy.Trend.ADXMin)
	}
	if cfg.Strategy.Trend.ATRTrailMult != 2.0 {
		t.Fatalf("atr_trail_mult = %v, want 2.0", cfg.Strategy.Trend.ATRTrailMult)
	}
	if cfg.Strategy.Trend.MinStopATR != 1.2 {
		t.Fatalf("min_stop_atr = %v, want 1.2", cfg.Strategy.Trend.MinStopATR)
	}
	if cfg.Strategy.Trend.TrailAfterR != 1.0 {
		t.Fatalf("trail_after_r = %v, want 1.0", cfg.Strategy.Trend.TrailAfterR)
	}
	if cfg.Strategy.Trend.PullbackHTFMaxATR != 0.8 {
		t.Fatalf("pullback_htf_max_atr = %v, want 0.8", cfg.Strategy.Trend.PullbackHTFMaxATR)
	}
	if cfg.Strategy.Trend.ContinuationADX != 28 {
		t.Fatalf("continuation_adx = %v, want 28", cfg.Strategy.Trend.ContinuationADX)
	}
	if cfg.Strategy.Trend.ADXRisingExempt != 30 {
		t.Fatalf("adx_rising_exempt = %v, want 30", cfg.Strategy.Trend.ADXRisingExempt)
	}
	if !cfg.Strategy.Trend.HTFCrossAllowed() {
		t.Fatal("allow_htf_cross should be enabled")
	}
	if !cfg.Strategy.Trend.HTFATREnabled() || !cfg.Strategy.Trend.WidenMinStopEnabled() || !cfg.Strategy.Trend.BarConfirmRequired() {
		t.Fatal("htf atr / widen stop / bar confirm should be enabled")
	}
	if cfg.Strategy.Trend.LTFCrossAllowed() {
		t.Fatal("15m EMA crosses should stay off")
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
	if !cfg.Strategy.Trend.EMA20SideRequired() {
		t.Fatal("require_ema20_side should be enabled")
	}
	if !cfg.Strategy.Trend.ReentryHTFResetRequired() {
		t.Fatal("reentry_htf_reset should be enabled")
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

	tr := TrendConfig{}
	if !tr.EMA20SideRequired() || !tr.ReentryHTFResetRequired() || !tr.ResetRequired() {
		t.Fatal("omitted trend invalidation keys must default to enabled")
	}
	if !tr.HTFATREnabled() || !tr.WidenMinStopEnabled() || !tr.BarConfirmRequired() {
		t.Fatal("omitted trend quality keys must default to enabled")
	}
	if tr.LTFCrossAllowed() {
		t.Fatal("omitted allow_ltf_cross must default to disabled")
	}
	if !tr.HTFCrossAllowed() {
		t.Fatal("omitted allow_htf_cross must default to enabled")
	}
	tr.RequireEMA20Side = &off
	if tr.EMA20SideRequired() {
		t.Fatal("explicit false must disable the EMA20 side filter")
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
