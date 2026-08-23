package strategy

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

func squeezeCfg() config.StrategyConfig {
	return config.StrategyConfig{
		Name:      "squeeze",
		ATRPeriod: 14,
		Squeeze:   config.SqueezeConfig{}.WithDefaults(),
	}
}

// oscBars builds a deterministic oscillation around `base`. Amplitude switches
// from ampA to ampB at `switchAt`, which is how the tests create (or defeat) a
// volatility squeeze.
func oscBars(n, switchAt int, base, ampA, ampB float64) []types.Kline {
	bars := make([]types.Kline, n)
	for i := 0; i < n; i++ {
		amp := ampA
		if i >= switchAt {
			amp = ampB
		}
		c := base + math.Sin(float64(i))*amp
		if i == n-1 {
			// Neutral final bar so up and down breakouts travel the same
			// distance, and therefore disturb ATR by the same amount.
			c = base
		}
		bars[i] = types.Kline{
			OpenTime:  time.Unix(int64(i*3600), 0).UTC(),
			CloseTime: time.Unix(int64((i+1)*3600), 0).UTC(),
			Open:      c,
			High:      c + amp*0.5,
			Low:       c - amp*0.5,
			Close:     c,
			Volume:    1000,
			Closed:    true,
		}
	}
	return bars
}

func boolPtr(v bool) *bool { return &v }

// appendBreakout adds a bar closing `beyond` past the Donchian edge of the
// preceding `period` bars.
func appendBreakout(bars []types.Kline, period int, beyond float64, up bool) []types.Kline {
	last := bars[len(bars)-1]
	hi, lo := last.High, last.Low
	for i := len(bars) - period; i < len(bars); i++ {
		if i < 0 {
			continue
		}
		hi = math.Max(hi, bars[i].High)
		lo = math.Min(lo, bars[i].Low)
	}
	c := hi + beyond
	if !up {
		c = lo - beyond
	}
	return append(bars, types.Kline{
		OpenTime:  last.CloseTime,
		CloseTime: last.CloseTime.Add(time.Hour),
		Open:      last.Close,
		High:      math.Max(c, last.Close),
		Low:       math.Min(c, last.Close),
		Close:     c,
		Volume:    2000,
		Closed:    true,
	})
}

// squeezedBreakout is the canonical setup: 300 volatile bars, 99 quiet bars,
// then an upside breakout.
func squeezedBreakout() []types.Kline {
	bars := oscBars(399, 300, 100, 3.0, 0.4)
	return appendBreakout(bars, 20, 0.3, true)
}

func TestSqueezeWarmup(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	sig := s.Evaluate(nil, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected none during warmup, got %s", sig.Action)
	}
	if !strings.HasPrefix(sig.Reason, "warmup") {
		t.Fatalf("expected warmup reason, got %q", sig.Reason)
	}
}

func TestSqueezeOpensLongOnBreakout(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()

	sig := s.Evaluate(bars, MarketContext{HasFunding: true, FundingRate: 0.00001})
	if sig.Action != types.ActionOpenLong {
		t.Fatalf("expected open long, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.StopLoss >= sig.Price {
		t.Fatalf("long stop %.2f must be below price %.2f", sig.StopLoss, sig.Price)
	}
	wantStop := sig.Price - 1.2*sig.ATR
	if math.Abs(sig.StopLoss-wantStop) > 1e-6 {
		t.Fatalf("stop %.4f, want %.4f (1.2 ATR)", sig.StopLoss, wantStop)
	}
	if sig.DonchianUp >= sig.Price {
		t.Fatalf("close %.2f should be above channel top %.2f", sig.Price, sig.DonchianUp)
	}
}

func TestSqueezeRejectsExpandedVolatility(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	// Volatility rises into the breakout instead of compressing.
	bars := appendBreakout(oscBars(399, 300, 100, 0.4, 3.0), 20, 0.3, true)

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected no entry without a squeeze, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "no squeeze") {
		t.Fatalf("expected squeeze rejection, got %q", sig.Reason)
	}
}

func TestSqueezeFundingFilterBlocksCrowdedLong(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()

	sig := s.Evaluate(bars, MarketContext{HasFunding: true, FundingRate: 0.002})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected funding filter to block, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "crowded") {
		t.Fatalf("expected crowding reason, got %q", sig.Reason)
	}

	// Same setup with the filter disabled must still trade.
	cfg := squeezeCfg()
	cfg.Squeeze.UseFundingFilter = boolPtr(false)
	if got := NewSqueezeBreakout("ETHUSDT", cfg).Evaluate(bars, MarketContext{HasFunding: true, FundingRate: 0.002}); got.Action != types.ActionOpenLong {
		t.Fatalf("expected open long with filter off, got %s (%s)", got.Action, got.Reason)
	}
}

func TestSqueezeTakesPartialProfitAtTP1(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()
	price := bars[len(bars)-1].Close

	s.Sync(PositionState{
		Long:      true,
		Entry:     price - 1.5,
		InitStop:  price - 2.5,
		Quantity:  1,
		InitQty:   1,
		EntryTime: bars[len(bars)-3].CloseTime,
	})

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionReduceLong {
		t.Fatalf("expected TP1 reduce at 1.5R, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.Portion != 0.5 {
		t.Fatalf("portion %.2f, want 0.50", sig.Portion)
	}
}

func TestSqueezeDoesNotRepeatTP1(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()
	price := bars[len(bars)-1].Close

	// Half the original size is gone, so TP1 has already fired.
	s.Sync(PositionState{
		Long:      true,
		Entry:     price - 1.5,
		InitStop:  price - 2.5,
		Quantity:  0.5,
		InitQty:   1,
		EntryTime: bars[len(bars)-3].CloseTime,
	})

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected hold after TP1, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.StopLoss < price-2.5 {
		t.Fatalf("stop %.2f should be ratcheted to at least breakeven-ish after TP1", sig.StopLoss)
	}
}

func TestSqueezeTimeStopClosesStalledTrade(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()
	price := bars[len(bars)-1].Close

	s.Sync(PositionState{
		Long:      true,
		Entry:     price,
		InitStop:  price - 1,
		Quantity:  1,
		InitQty:   1,
		EntryTime: bars[len(bars)-20].CloseTime,
	})

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionCloseLong {
		t.Fatalf("expected time stop close, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "time stop") {
		t.Fatalf("expected time stop reason, got %q", sig.Reason)
	}
}

func TestSqueezeTrailStopCloses(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()
	price := bars[len(bars)-1].Close

	s.Sync(PositionState{
		Long:      true,
		Entry:     price - 5,
		InitStop:  price - 6,
		Trail:     price + 0.5, // already ratcheted above the current close
		Quantity:  1,
		InitQty:   1,
		EntryTime: bars[len(bars)-3].CloseTime,
	})

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionCloseLong {
		t.Fatalf("expected trail stop close, got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestSqueezeNoTimeStopWithoutEntryTime(t *testing.T) {
	s := NewSqueezeBreakout("ETHUSDT", squeezeCfg())
	bars := squeezedBreakout()
	price := bars[len(bars)-1].Close

	s.Sync(PositionState{
		Long:     true,
		Entry:    price,
		InitStop: price - 1,
		Quantity: 1,
		InitQty:  1,
	})

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("time stop must stay disabled without an entry time, got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestSqueezeShortBreakout(t *testing.T) {
	cfg := squeezeCfg()
	// The series oscillates around a flat mean, so the EMA filter would veto a
	// short on price alone; this test is about the breakout mechanics.
	cfg.Squeeze.UseTrendFilter = boolPtr(false)
	s := NewSqueezeBreakout("ETHUSDT", cfg)

	bars := appendBreakout(oscBars(399, 300, 100, 3.0, 0.4), 20, 0.3, false)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionOpenShort {
		t.Fatalf("expected open short, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.StopLoss <= sig.Price {
		t.Fatalf("short stop %.2f must be above price %.2f", sig.StopLoss, sig.Price)
	}
}

func TestStrategyFactory(t *testing.T) {
	for _, name := range []string{"", "trend", "squeeze"} {
		s, err := New(name, "ETHUSDT", config.StrategyConfig{
			EMAFast: 20, EMASlow: 60, EMAFilter: 200,
			ATRPeriod: 14, ADXPeriod: 14, MinBars: 220,
		})
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if s.Name() == "" {
			t.Fatalf("New(%q): empty strategy name", name)
		}
	}
	if _, err := New("nope", "ETHUSDT", config.StrategyConfig{}); err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}
