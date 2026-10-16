package strategy

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

const testDonchian = 10

func breakoutTestCfg() config.StrategyConfig {
	return config.StrategyConfig{
		Name:      "breakout",
		ATRPeriod: 14,
		MinBars:   80,
		Breakout: config.BreakoutConfig{
			Donchian:       testDonchian,
			EMAFast:        8,
			EMASlow:        21,
			EMAFilter:      40,
			ADXPeriod:      14,
			ADXMin:         12,
			EMASepMinATR:   0,
			EMASlopeMinATR: 0,
			MinRangeATR:    0,
			MinBreakATR:    0.05,
			BreakoutMaxATR: 1.5,
			MinStopATR:     1,
			MaxStopATR:     4,
			ATRStopMult:    0.2,
		}.WithDefaults(),
	}
}

func boBar(i int, o, h, l, c float64) types.Kline {
	if h < math.Max(o, c) {
		h = math.Max(o, c)
	}
	if l > math.Min(o, c) {
		l = math.Min(o, c)
	}
	return types.Kline{
		OpenTime:  time.Unix(int64(i)*3600, 0).UTC(),
		CloseTime: time.Unix(int64(i+1)*3600, 0).UTC(),
		Open:      o,
		High:      h,
		Low:       l,
		Close:     c,
		Volume:    1000,
		Closed:    true,
	}
}

// stackedBreak builds a one-way trend, a short pause inside the last bar's
// range, then an optional close through that pause. extend <= 0 stops at the pause.
func stackedBreak(trendUp, breakUp bool, extend float64) (bars []types.Kline, edge float64) {
	const nTrend = 120
	start := 100.0
	step := 1.0
	if !trendUp {
		start = 100 + float64(nTrend-1)*step
		step = -1
	}
	for i := 0; i < nTrend; i++ {
		px := start + float64(i)*step
		bars = append(bars, boBar(i, px-0.15, px+0.35, px-0.55, px))
	}
	last := bars[len(bars)-1]
	pauseHigh := last.High - 0.05
	pauseLow := last.Low + 0.05
	pauseClose := (pauseHigh + pauseLow) / 2
	for k := 0; k < testDonchian; k++ {
		bars = append(bars, boBar(len(bars), pauseClose, pauseHigh, pauseLow, pauseClose))
	}
	edge = pauseHigh
	if !breakUp {
		edge = pauseLow
	}
	if extend <= 0 {
		return bars, edge
	}
	if breakUp {
		c := edge + extend
		bars = append(bars, boBar(len(bars), edge-0.05, c+0.05, edge-0.35, c))
	} else {
		c := edge - extend
		bars = append(bars, boBar(len(bars), edge+0.05, edge+0.35, c-0.05, c))
	}
	return bars, edge
}

func TestBreakoutOpensLongOnFreshCross(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(true, true, 0.3)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionOpenLong {
		t.Fatalf("action %s (%s), ADX %.1f", sig.Action, sig.Reason, sig.ADX)
	}
	if !strings.Contains(sig.Reason, "donchian breakout") {
		t.Fatalf("reason %q", sig.Reason)
	}
	if sig.StopLoss <= 0 || sig.StopLoss >= sig.Price {
		t.Fatalf("long stop %.4f at price %.4f", sig.StopLoss, sig.Price)
	}
	if sig.DonchianUp <= 0 || sig.Price <= sig.DonchianUp {
		t.Fatalf("price %.4f did not clear upper %.4f", sig.Price, sig.DonchianUp)
	}
}

func TestBreakoutOpensShortOnFreshCross(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(false, false, 0.3)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionOpenShort {
		t.Fatalf("action %s (%s), ADX %.1f", sig.Action, sig.Reason, sig.ADX)
	}
	if !strings.Contains(sig.Reason, "donchian breakdown") {
		t.Fatalf("reason %q", sig.Reason)
	}
	if sig.StopLoss <= sig.Price {
		t.Fatalf("short stop %.4f at price %.4f", sig.StopLoss, sig.Price)
	}
}

func TestBreakoutRejectsAgainstStack(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(false, true, 0.3)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "against EMA stack") {
		t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestBreakoutStaysInsideWithoutCross(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(true, true, 0)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "inside channel") {
		t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestBreakoutIgnoresUnclosedBreak(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, edge := stackedBreak(true, true, 0)
	live := boBar(len(bars), edge, edge+1, edge-0.2, edge+0.8)
	live.Closed = false
	bars = append(bars, live)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("unclosed bar must not open, got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestBreakoutRejectsStaleOutsideClose(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(true, true, 0.3)
	last := bars[len(bars)-1]
	c := last.Close + 0.15
	bars = append(bars, boBar(len(bars), last.Close, c+0.05, last.Close-0.1, c))
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "no fresh cross") {
		t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestBreakoutRejectsExtensionAndQuality(t *testing.T) {
	cases := []struct {
		name   string
		cfg    func(config.StrategyConfig) config.StrategyConfig
		extend float64
		want   string
	}{
		{
			name:   "shallow",
			extend: 0.02,
			cfg: func(c config.StrategyConfig) config.StrategyConfig {
				c.Breakout.MinBreakATR = 0.5
				return c
			},
			want: "too shallow",
		},
		{
			name:   "extended",
			extend: 6,
			cfg: func(c config.StrategyConfig) config.StrategyConfig {
				c.Breakout.BreakoutMaxATR = 1
				return c
			},
			want: "no chase",
		},
		{
			name:   "adx",
			extend: 0.3,
			cfg: func(c config.StrategyConfig) config.StrategyConfig {
				c.Breakout.ADXMin = 1000
				return c
			},
			want: "ADX",
		},
		{
			name:   "tangled",
			extend: 0.3,
			cfg: func(c config.StrategyConfig) config.StrategyConfig {
				c.Breakout.EMASepMinATR = 1000
				return c
			},
			want: "tangled",
		},
		{
			name:   "flat slope",
			extend: 0.3,
			cfg: func(c config.StrategyConfig) config.StrategyConfig {
				c.Breakout.EMASlopeBars = 5
				c.Breakout.EMASlopeMinATR = 1000
				return c
			},
			want: "slope flat",
		},
		{
			name:   "narrow",
			extend: 0.3,
			cfg: func(c config.StrategyConfig) config.StrategyConfig {
				c.Breakout.MinRangeATR = 100
				return c
			},
			want: "too narrow",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg(breakoutTestCfg())
			s := NewTrendBreakout("ETHUSDT", cfg)
			bars, _ := stackedBreak(true, true, tc.extend)
			sig := s.Evaluate(bars, MarketContext{})
			if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, tc.want) {
				t.Fatalf("got %s (%s), want %q", sig.Action, sig.Reason, tc.want)
			}
		})
	}
}

func TestBreakoutRejectsWeakCloseAndWideBar(t *testing.T) {
	t.Run("weak close", func(t *testing.T) {
		s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
		bars, edge := stackedBreak(true, true, 0.3)
		i := len(bars) - 1
		bars[i] = boBar(i, edge, edge+2.2, edge-0.1, edge+0.2)
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "weak breakout close") {
			t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
		}
	})
	t.Run("wide bar", func(t *testing.T) {
		cfg := breakoutTestCfg()
		cfg.Breakout.MaxStopATR = 0.4
		s := NewTrendBreakout("ETHUSDT", cfg)
		bars, edge := stackedBreak(true, true, 0.3)
		i := len(bars) - 1
		// Keep the low at the prior bar so this is a tall close, not a down wick
		// that would flip DI before the stop-width gate.
		bars[i] = boBar(i, edge, edge+0.35, bars[i-1].Low, edge+0.3)
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "too wide") {
			t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
		}
	})
}

func TestBreakoutHoldsStopUntilOneR(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(true, true, 0.3)
	open := s.Evaluate(bars, MarketContext{})
	if open.Action != types.ActionOpenLong {
		t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
	}
	s.Sync(PositionState{
		Long: true, Entry: open.Price, Quantity: 1, InitQty: 1,
		Trail: open.StopLoss, InitStop: open.StopLoss, EntryTime: open.Time,
	})
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "hold") {
		t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.StopLoss != open.StopLoss {
		t.Fatalf("stop moved from %.4f to %.4f before 1R", open.StopLoss, sig.StopLoss)
	}
}

func TestBreakoutTrailStop(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	bars, _ := stackedBreak(true, true, 0.3)
	open := s.Evaluate(bars, MarketContext{})
	if open.Action != types.ActionOpenLong {
		t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
	}
	s.Sync(PositionState{
		Long: true, Entry: open.Price, Quantity: 1, InitQty: 1,
		Trail: open.StopLoss, InitStop: open.StopLoss, EntryTime: open.Time,
	})
	c := open.StopLoss - 0.5
	bars = append(bars, boBar(len(bars), c+0.2, c+0.3, c-0.2, c))
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionCloseLong || !strings.Contains(sig.Reason, "trail stop") {
		t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestBreakoutExitsWhenBreakFailsOrStackFlips(t *testing.T) {
	t.Run("mid", func(t *testing.T) {
		cfg := breakoutTestCfg()
		cfg.Breakout.MinStopATR = 8
		cfg.Breakout.MaxStopATR = 12
		s := NewTrendBreakout("ETHUSDT", cfg)
		bars, _ := stackedBreak(true, true, 0.3)
		open := s.Evaluate(bars, MarketContext{})
		if open.Action != types.ActionOpenLong {
			t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
		}
		s.Sync(PositionState{
			Long: true, Entry: open.Price, Quantity: 1, InitQty: 1,
			Trail: open.StopLoss, InitStop: open.StopLoss, EntryTime: open.Time,
		})
		c := open.Price - 1.5
		bars = append(bars, boBar(len(bars), open.Price, open.Price, c, c))
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionCloseLong || !strings.Contains(sig.Reason, "channel mid") {
			t.Fatalf("got %s (%s) stop %.2f mid-ish price %.2f", sig.Action, sig.Reason, open.StopLoss, c)
		}
	})
	t.Run("stack flip", func(t *testing.T) {
		cfg := breakoutTestCfg()
		cfg.Breakout.MinStopATR = 40
		cfg.Breakout.MaxStopATR = 50
		s := NewTrendBreakout("ETHUSDT", cfg)
		off := false
		s.bo.ExitOnMidLoss = &off
		s.bo.TimeStopBars = 1000
		bars, _ := stackedBreak(true, true, 0.3)
		open := s.Evaluate(bars, MarketContext{})
		if open.Action != types.ActionOpenLong {
			t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
		}
		s.Sync(PositionState{
			Long: true, Entry: open.Price, Quantity: 1, InitQty: 1,
			Trail: open.StopLoss, InitStop: open.StopLoss, EntryTime: open.Time,
		})
		px := open.Price
		for k := 0; k < 40; k++ {
			px -= 0.8
			bars = append(bars, boBar(len(bars), px+0.2, px+0.3, px-0.3, px))
		}
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionCloseLong || !strings.Contains(sig.Reason, "ema stack flipped") {
			t.Fatalf("got %s (%s) price %.2f stop %.2f", sig.Action, sig.Reason, px, open.StopLoss)
		}
	})
}

func TestBreakoutTimeStopAndTakeProfit(t *testing.T) {
	bars, _ := stackedBreak(true, true, 0.3)
	open := NewTrendBreakout("ETHUSDT", breakoutTestCfg()).Evaluate(bars, MarketContext{})
	if open.Action != types.ActionOpenLong {
		t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
	}

	t.Run("time stop", func(t *testing.T) {
		s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
		s.bo.TimeStopBars = 4
		s.Sync(PositionState{
			Long: true, Entry: open.Price, Quantity: 1, InitQty: 1,
			Trail: open.Price - 100, InitStop: open.Price - 100,
			EntryTime: bars[0].CloseTime,
		})
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionCloseLong || !strings.Contains(sig.Reason, "time stop") {
			t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
		}
	})
	t.Run("partial tp", func(t *testing.T) {
		s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
		on := true
		s.bo.UseTP = &on
		s.bo.TPR = 1
		s.bo.TPPortion = 0.4
		s.Sync(PositionState{
			Long: true, Entry: open.Price - 10, Quantity: 1, InitQty: 1,
			Trail: open.Price - 100, InitStop: open.Price - 20,
			EntryTime: open.Time,
		})
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionReduceLong || sig.Portion != 0.4 {
			t.Fatalf("got %s portion %.2f (%s)", sig.Action, sig.Portion, sig.Reason)
		}
	})
	t.Run("full tp", func(t *testing.T) {
		s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
		on := true
		s.bo.UseTP = &on
		s.bo.TPR = 1
		s.bo.TPPortion = 0
		s.Sync(PositionState{
			Long: true, Entry: open.Price - 10, Quantity: 1, InitQty: 1,
			Trail: open.Price - 100, InitStop: open.Price - 20,
			EntryTime: open.Time,
		})
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionCloseLong || !strings.Contains(sig.Reason, "tp ") {
			t.Fatalf("got %s (%s)", sig.Action, sig.Reason)
		}
	})
}

func TestBreakoutReentryLock(t *testing.T) {
	s := NewTrendBreakout("ETHUSDT", breakoutTestCfg())
	s.bo.ReentryCooldown = 0
	bars, _ := stackedBreak(true, true, 0.3)
	open := s.Evaluate(bars, MarketContext{})
	if open.Action != types.ActionOpenLong {
		t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
	}
	s.Sync(PositionState{Long: true, Entry: open.Price, Quantity: 1, InitQty: 1, InitStop: open.StopLoss})
	s.Sync(PositionState{})

	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "reset") {
		t.Fatalf("expected reset block, got %s (%s)", sig.Action, sig.Reason)
	}

	s.lock.Reset = true
	sig = s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone || !strings.Contains(sig.Reason, "same level") {
		t.Fatalf("expected same-level block, got %s (%s)", sig.Action, sig.Reason)
	}

	s.lock.Reset = true
	s.lock.Entry = open.Price - 80
	s.lock.Edge = open.DonchianUp - 80
	sig = s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionOpenLong {
		t.Fatalf("new level should trade, got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestBreakoutName(t *testing.T) {
	for _, name := range []string{"breakout", "trend_breakout"} {
		s, err := New(name, "ETHUSDT", breakoutTestCfg())
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		if s.Name() != "breakout" {
			t.Fatalf("New(%q) name %q", name, s.Name())
		}
	}
}
