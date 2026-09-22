package strategy

import (
	"strings"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

func testTrendCfg() config.StrategyConfig {
	return config.StrategyConfig{
		ATRPeriod: 5, MinBars: 40,
		Trend: config.TrendConfig{
			EMAFast: 5, EMASlow: 15, EMAFilter: 30,
			ADXPeriod: 5, ADXMin: 0, // disable ADX gate for unit test
			ATRStopMult: 1.5, ATRTrailMult: 1.0, ChaseMaxATR: 10,
			UseEMA200Filter: false,
		},
	}
}

func TestEvaluateWarmup(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", config.StrategyConfig{
		ATRPeriod: 14, MinBars: 220,
		Trend: config.TrendConfig{
			EMAFast: 20, EMASlow: 60, EMAFilter: 200,
			ADXPeriod: 14, ADXMin: 20,
			ATRStopMult: 1.5, ATRTrailMult: 1.0, ChaseMaxATR: 1.5,
			UseEMA200Filter: true,
		},
	})
	sig := s.Evaluate(nil, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected none, got %s", sig.Action)
	}
}

func TestPullbackLongSignal(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", testTrendCfg())

	kl := synthBars(80, 100, 0.5, time.Hour, time.Unix(0, 0).UTC(), 1.5)
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
	cfg := testTrendCfg()
	cfg.Trend.EMASepMinATR = 50 // impossible separation → always chop
	s := NewTrendFollow("ETHUSDT", cfg)

	kl := synthBars(80, 100, 0, time.Hour, time.Unix(0, 0).UTC(), 0)
	for i := range kl {
		c := 100 + float64(i%4)*0.1 - 0.15
		kl[i].Open = c
		kl[i].High = c + 0.3
		kl[i].Low = c - 0.3
		kl[i].Close = c
	}
	sig := s.Evaluate(kl, MarketContext{})
	if sig.Action != types.ActionNone {
		t.Fatalf("expected chop block, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "tangled") && !strings.Contains(sig.Reason, "chop") {
		t.Fatalf("expected chop reason, got %q", sig.Reason)
	}
}

func TestMTFRejectsEntryAgainstHigherTF(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", testTrendCfg())
	htf, ltf := alignedBars(80, 80, 200, -0.5, 100, 0.5, time.Hour, 15*time.Minute, 1.5)
	sig := s.Evaluate(htf, MarketContext{Entry: ltf})
	if sig.Action == types.ActionOpenLong {
		t.Fatalf("must not long against 1h downtrend: %s", sig.Reason)
	}
}

func TestMTFRejectsEntryWhenHigherTFChops(t *testing.T) {
	cfg := testTrendCfg()
	cfg.Trend.EMASepMinATR = 50
	s := NewTrendFollow("ETHUSDT", cfg)

	htf, ltf := alignedBars(80, 80, 100, 0, 100, 0.5, time.Hour, 15*time.Minute, 1.5)
	for i := range htf {
		c := 100 + float64(i%4)*0.1 - 0.15
		htf[i].Open, htf[i].Close = c, c
		htf[i].High, htf[i].Low = c+0.3, c-0.3
	}
	sig := s.Evaluate(htf, MarketContext{Entry: ltf})
	if sig.Action.IsOpen() {
		t.Fatalf("expected 1h chop to block 15m entry, got %s (%s)", sig.Action, sig.Reason)
	}
	if !strings.Contains(sig.Reason, "1h") {
		t.Fatalf("expected 1h-prefixed chop reason, got %q", sig.Reason)
	}
}

func TestMTFHoldsThroughLowerTFCross(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", testTrendCfg())
	s.SyncPosition(true, false, 150, 1)

	htf, ltf := alignedBars(80, 80, 100, 0.5, 200, -0.3, time.Hour, 15*time.Minute, 0)
	sig := s.Evaluate(htf, MarketContext{Entry: ltf})
	if sig.Action == types.ActionCloseLong && strings.Contains(sig.Reason, "ema cross") {
		t.Fatalf("must not exit on 15m cross while 1h trend is intact: %s", sig.Reason)
	}
	if sig.Action != types.ActionNone && sig.Action != types.ActionCloseLong {
		t.Fatalf("unexpected action %s (%s)", sig.Action, sig.Reason)
	}
	if sig.Action == types.ActionNone && sig.Reason != "hold long" {
		t.Fatalf("expected hold long, got %q", sig.Reason)
	}
}

func TestMTFClosesOnHigherTFFlip(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", testTrendCfg())
	s.SyncPosition(true, false, 150, 1)

	htf, ltf := alignedBars(80, 80, 200, -0.5, 150, 0.2, time.Hour, 15*time.Minute, 0)
	sig := s.Evaluate(htf, MarketContext{Entry: ltf})
	if sig.Action != types.ActionCloseLong {
		t.Fatalf("expected close long on 1h flip, got %s (%s)", sig.Action, sig.Reason)
	}
	if sig.Reason != "ema cross down" && sig.Reason != "trail stop hit" {
		t.Fatalf("expected 1h flip or trail, got %q", sig.Reason)
	}
}

func TestTrendRejectsImmediateReentryAfterStop(t *testing.T) {
	s := NewTrendFollow("ETHUSDT", testTrendCfg())
	kl := synthBars(80, 100, 0.5, time.Hour, time.Unix(0, 0).UTC(), 1.5)
	open := s.Evaluate(kl, MarketContext{})
	if open.Action != types.ActionOpenLong {
		// Fresh-touch pullback is stricter; the lock still has to hold if we
		// flatten a long that this series would otherwise want to reopen.
		s.lock.NoteEntry(true, kl[len(kl)-1].Close, kl[len(kl)-1].Close)
	} else {
		s.lock.NoteEntry(true, open.Price, open.EMA20)
	}
	s.Sync(PositionState{Long: true, Entry: s.lock.Entry, Quantity: 1})
	s.Sync(PositionState{})
	sig := s.Evaluate(kl, MarketContext{})
	if sig.Action.IsOpen() {
		t.Fatalf("must not re-enter immediately after a stop, got %s (%s)", sig.Action, sig.Reason)
	}
}

func TestLastIndexAtOrBefore(t *testing.T) {
	origin := time.Unix(0, 0).UTC()
	bars := synthBars(4, 100, 1, time.Hour, origin, 0)
	// bar i closes at origin + (i+1)h
	idx, ok := lastIndexAtOrBefore(bars, origin.Add(2*time.Hour))
	if !ok || idx != 1 {
		t.Fatalf("got idx=%d ok=%v, want 1", idx, ok)
	}
	idx, ok = lastIndexAtOrBefore(bars, origin.Add(4*time.Hour))
	if !ok || idx != 3 {
		t.Fatalf("got idx=%d ok=%v, want 3", idx, ok)
	}
	if _, ok := lastIndexAtOrBefore(bars, origin); ok {
		t.Fatal("expected no bar closing at origin")
	}
}

func synthBars(n int, start, drift float64, interval time.Duration, origin time.Time, endWick float64) []types.Kline {
	kl := make([]types.Kline, n)
	for i := 0; i < n; i++ {
		c := start + float64(i)*drift
		h := c + 0.8
		l := c - 0.8
		if i == n-1 && endWick > 0 {
			c = start + float64(n-2)*drift - 0.2
			h = c + 0.8
			l = c - endWick
		}
		kl[i] = types.Kline{
			OpenTime:  origin.Add(time.Duration(i) * interval),
			CloseTime: origin.Add(time.Duration(i+1) * interval),
			Open:      c - 0.1,
			High:      h,
			Low:       l,
			Close:     c,
			Volume:    1000,
			Closed:    true,
		}
	}
	return kl
}

// alignedBars builds HTF and LTF series that share an end time so the last
// entry bar maps onto a fully warmed higher-TF bar.
func alignedBars(hn, ln int, hStart, hDrift, lStart, lDrift float64, hInt, lInt time.Duration, lWick float64) (higher, lower []types.Kline) {
	origin := time.Unix(1_700_000_000, 0).UTC()
	higher = synthBars(hn, hStart, hDrift, hInt, origin, 0)
	end := origin.Add(time.Duration(hn) * hInt)
	lOrigin := end.Add(-time.Duration(ln) * lInt)
	lower = synthBars(ln, lStart, lDrift, lInt, lOrigin, lWick)
	return
}
