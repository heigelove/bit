package strategy

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

func vegasTestCfg() config.StrategyConfig {
	off := false
	on := true
	return config.StrategyConfig{
		Name:      "vegas",
		ATRPeriod: 5,
		MinBars:   40,
		Vegas: config.VegasConfig{
			EMAFast:           3,
			EMATunnelFast:     8,
			EMATunnelSlow:     13,
			SlopeBars:         4,
			SlopeMinATR:       0.01,
			EstablishBars:     12,
			TouchATR:          0.15,
			ReclaimMinATR:     0.01,
			ChaseMaxATR:       3,
			PierceMaxATR:      1.5,
			ATRStopMult:       0.15,
			MinStopATR:        0.6,
			ATRTrailMult:      3,
			TrailBars:         4,
			TrailAfterR:       8,
			TPR:               1.2,
			UseTP:             &on,
			RequireBarConfirm: &on,
			CloseConfirmFrac:  0.45,
			RequireStack:      &on,
			RequireFastSide:   &off,
			RequireFastTurn:   &off,
			ReentryATR:        0.2,
			ReentryCooldown:   2,
			RequireReset:      &on,
		},
	}
}

func vegasBar(i int, o, h, l, c float64) types.Kline {
	return types.Kline{
		OpenTime:  time.Unix(int64(i*3600), 0).UTC(),
		CloseTime: time.Unix(int64((i+1)*3600), 0).UTC(),
		Open:      o,
		High:      math.Max(h, math.Max(o, c)),
		Low:       math.Min(l, math.Min(o, c)),
		Close:     c,
		Volume:    1000,
		Closed:    true,
	}
}

func risingBars(n int, start, step float64) []types.Kline {
	bars := make([]types.Kline, n)
	px := start
	for i := 0; i < n; i++ {
		px += step
		bars[i] = vegasBar(i, px-step*0.3, px+step*0.2, px-step*0.35, px)
	}
	return bars
}

func fallingBars(n int, start, step float64) []types.Kline {
	bars := make([]types.Kline, n)
	px := start
	for i := 0; i < n; i++ {
		px -= step
		bars[i] = vegasBar(i, px+step*0.3, px+step*0.35, px-step*0.2, px)
	}
	return bars
}

// pullbackReclaim dips a finished trend until the low tags the near tunnel
// edge, then appends one confirm bar that closes back beyond it.
func pullbackReclaim(bars []types.Kline, fast, tunF, tunS int, long bool) []types.Kline {
	for n := 0; n < 8; n++ {
		_, _, _, upper, lower, atr := vegasEnds(bars, fast, tunF, tunS)
		last := bars[len(bars)-1]
		i := len(bars)
		if long {
			// Sit the close inside the tunnel and the low on the near edge.
			mid := (upper + lower) / 2
			c := mid
			if c >= upper {
				c = upper - atr*0.05
			}
			bars = append(bars, vegasBar(i, last.Close, math.Max(last.Close, upper), upper-atr*0.05, c))
			_, _, _, upper, lower, atr = vegasEnds(bars, fast, tunF, tunS)
			last = bars[len(bars)-1]
			i = len(bars)
			reclaim := upper + math.Max(atr*0.25, 0.5)
			bars = append(bars, vegasBar(i, last.Close, reclaim+atr*0.1, math.Min(last.Close, upper+atr*0.05), reclaim))
		} else {
			mid := (upper + lower) / 2
			c := mid
			if c <= lower {
				c = lower + atr*0.05
			}
			bars = append(bars, vegasBar(i, last.Close, lower+atr*0.05, math.Min(last.Close, lower), c))
			_, _, _, upper, lower, atr = vegasEnds(bars, fast, tunF, tunS)
			last = bars[len(bars)-1]
			i = len(bars)
			reject := lower - math.Max(atr*0.25, 0.5)
			bars = append(bars, vegasBar(i, last.Close, math.Max(last.Close, lower-atr*0.05), reject-atr*0.1, reject))
		}
		s := NewVegasTunnel("ETHUSDT", vegasTestCfg())
		sig := s.Evaluate(bars, MarketContext{})
		if sig.Action == types.ActionOpenLong && long {
			return bars
		}
		if sig.Action == types.ActionOpenShort && !long {
			return bars
		}
		// The confirm missed. Drop it and dip one bar deeper next loop.
		bars = bars[:len(bars)-1]
	}
	return bars
}

func vegasEnds(bars []types.Kline, fastN, tunFN, tunSN int) (fast, tunF, tunS, upper, lower, atr float64) {
	_, _, closes := ohlc(bars)
	highs, lows, _ := ohlc(bars)
	f := indicators.EMA(closes, fastN)
	a := indicators.EMA(closes, tunFN)
	b := indicators.EMA(closes, tunSN)
	tr := indicators.ATR(highs, lows, closes, 5)
	i := len(bars) - 1
	fast, tunF, tunS = f[i], a[i], b[i]
	upper, lower = tunnelBands(tunF, tunS)
	atr = tr[i]
	return
}

func TestVegasLongPullbackReclaim(t *testing.T) {
	bars := pullbackReclaim(risingBars(60, 100, 1.2), 3, 8, 13, true)
	s := NewVegasTunnel("ETHUSDT", vegasTestCfg())
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionOpenLong {
		t.Fatalf("expected open long, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.StopLoss <= 0 || sig.StopLoss >= sig.Price {
		t.Fatalf("long stop %.4f must sit below price %.4f", sig.StopLoss, sig.Price)
	}
	if !strings.Contains(sig.Reason, "reclaim") {
		t.Fatalf("reason %q", sig.Reason)
	}
}

func TestVegasShortPullbackReject(t *testing.T) {
	bars := pullbackReclaim(fallingBars(60, 400, 1.2), 3, 8, 13, false)
	s := NewVegasTunnel("ETHUSDT", vegasTestCfg())
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionOpenShort {
		t.Fatalf("expected open short, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.StopLoss <= sig.Price {
		t.Fatalf("short stop %.4f must sit above price %.4f", sig.StopLoss, sig.Price)
	}
}

func TestVegasIgnoresFreshCross(t *testing.T) {
	// A market that has lived below the tunnel does not get a long just
	// because one bar closes above it.
	bars := fallingBars(50, 200, 0.4)
	last := bars[len(bars)-1]
	jump := last.Close + 30
	bars = append(bars, vegasBar(len(bars), last.Close, jump, last.Close-0.2, jump))
	s := NewVegasTunnel("ETHUSDT", vegasTestCfg())
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action == types.ActionOpenLong {
		t.Fatalf("fresh cross must not open a long: %s", sig.Reason)
	}
}

func TestVegasRejectsChase(t *testing.T) {
	cfg := vegasTestCfg()
	cfg.Vegas.ChaseMaxATR = 0.05
	bars := pullbackReclaim(risingBars(60, 100, 1.2), 3, 8, 13, true)
	s := NewVegasTunnel("ETHUSDT", cfg)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("extended reclaim should be refused, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "chase") && !strings.Contains(sig.Reason, "too far") {
		t.Fatalf("expected chase rejection, got %q", sig.Reason)
	}
}

func TestVegasHoldsTrendPastFixedTarget(t *testing.T) {
	cfg := vegasTestCfg()
	off := false
	cfg.Vegas.UseTP = &off
	cfg.Vegas.TrailAfterR = 20
	s := NewVegasTunnel("ETHUSDT", cfg)
	bars := risingBars(55, 100, 0.5)
	last := bars[len(bars)-1]
	entry := last.Close
	bars[len(bars)-1] = vegasBar(len(bars)-1, last.Open, entry+40, last.Low, entry+30)
	s.Sync(PositionState{
		Long: true, Entry: entry, Quantity: 1, InitQty: 1, InitStop: entry - 10,
		EntryTime: bars[len(bars)-4].CloseTime,
	})
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("a 3R extension must stay open, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "hold") {
		t.Fatalf("reason %q", sig.Reason)
	}
}

func TestVegasExitsWhenStackFlips(t *testing.T) {
	cfg := vegasTestCfg()
	off := false
	cfg.Vegas.UseTP = &off
	cfg.Vegas.TrailAfterR = 50
	cfg.Vegas.ATRTrailMult = 100
	s := NewVegasTunnel("ETHUSDT", cfg)
	bars := risingBars(55, 100, 0.8)
	entry := bars[len(bars)-1].Close
	s.Sync(PositionState{
		Long: true, Entry: entry, Quantity: 1, InitQty: 1, InitStop: entry - 500,
		EntryTime: bars[len(bars)-5].CloseTime,
	})
	var sig types.Signal
	for n := 0; n < 40; n++ {
		last := bars[len(bars)-1]
		px := last.Close - 4
		bars = append(bars, vegasBar(len(bars), last.Close, last.Close, px, px))
		sig = s.Evaluate(bars, MarketContext{})
		if sig.Action != types.ActionNone {
			break
		}
	}
	if sig.Action != types.ActionCloseLong || !strings.Contains(sig.Reason, "stack flipped") {
		t.Fatalf("expected stack flip exit, got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestVegasTakesProfit(t *testing.T) {
	cfg := vegasTestCfg()
	cfg.Vegas.TPR = 1.0
	s := NewVegasTunnel("ETHUSDT", cfg)
	bars := risingBars(50, 100, 0.4)
	entry := bars[len(bars)-1].Close
	// 1R is 10 points with the stop 10 below; push the last close past it.
	last := bars[len(bars)-1]
	bars[len(bars)-1] = vegasBar(len(bars)-1, last.Open, entry+15, last.Low, entry+12)
	s.Sync(PositionState{
		Long: true, Entry: entry, Quantity: 1, InitQty: 1, InitStop: entry - 10,
		EntryTime: bars[len(bars)-3].CloseTime,
	})
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionCloseLong {
		t.Fatalf("expected tp close, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "tp") {
		t.Fatalf("reason %q", sig.Reason)
	}
}

func TestVegasSkipsStaleTunnel(t *testing.T) {
	cfg := vegasTestCfg()
	cfg.Vegas.MaxStackAge = 5
	bars := pullbackReclaim(risingBars(60, 100, 1.2), 3, 8, 13, true)
	s := NewVegasTunnel("ETHUSDT", cfg)
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("old tunnel must not be traded, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "stack age") {
		t.Fatalf("expected stack age rejection, got %q", sig.Reason)
	}
}

func TestVegasReentryLockBlocksSamePullback(t *testing.T) {
	bars := pullbackReclaim(risingBars(60, 100, 1.2), 3, 8, 13, true)
	s := NewVegasTunnel("ETHUSDT", vegasTestCfg())
	open := s.Evaluate(bars, MarketContext{})
	if open.Action != types.ActionOpenLong {
		t.Fatalf("setup: %s (%s)", open.Action, open.Reason)
	}
	s.Sync(PositionState{Long: true, Entry: open.Price, Quantity: 1, InitQty: 1, InitStop: open.StopLoss, EntryTime: open.Time})
	s.Sync(PositionState{})
	sig := s.Evaluate(bars, MarketContext{})
	if sig.Action == types.ActionOpenLong {
		t.Fatalf("same pullback must stay locked, got %s", sig.Reason)
	}
	if !strings.Contains(sig.Reason, "reset") && !strings.Contains(sig.Reason, "cooldown") && !strings.Contains(sig.Reason, "same level") && !strings.Contains(sig.Reason, "already traded") {
		t.Fatalf("expected reentry rejection, got %q", sig.Reason)
	}
}
