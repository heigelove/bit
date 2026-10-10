package backtest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

func TestVegasInterval(t *testing.T) {
	if vegasInterval("vegas", "4h") != "4h" || vegasInterval("vegas_tunnel", " 1h ") != "1h" {
		t.Fatal("vegas should keep its own interval")
	}
	if vegasInterval("trend", "4h") != "" || vegasInterval("squeeze", "4h") != "" {
		t.Fatal("other strategies must ignore the vegas interval")
	}
}

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"15m", 15 * time.Minute},
		{"1h", time.Hour},
		{"4h", 4 * time.Hour},
		{"1d", 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"1M", 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		got, err := ParseInterval(tc.in)
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.in, got, tc.want)
		}
	}
	if _, err := ParseInterval("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWindowUpTo(t *testing.T) {
	bars := make([]types.Kline, 10)
	origin := time.Unix(0, 0).UTC()
	for i := range bars {
		bars[i] = types.Kline{
			OpenTime:  origin.Add(time.Duration(i) * time.Hour),
			CloseTime: origin.Add(time.Duration(i+1) * time.Hour),
			Closed:    true,
		}
	}
	got := windowUpTo(bars, origin.Add(5*time.Hour), 3)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if !got[0].CloseTime.Equal(origin.Add(3*time.Hour)) || !got[2].CloseTime.Equal(origin.Add(5*time.Hour)) {
		t.Fatalf("window %v .. %v", got[0].CloseTime, got[2].CloseTime)
	}
}

func TestComputeStats(t *testing.T) {
	rounds := []float64{10, -4, -4, 8}
	eq := []Point{
		{Equity: 100},
		{Equity: 110},
		{Equity: 90},
		{Equity: 86},
		{Equity: 94},
	}
	st := computeStats(100, 94, eq, nil, rounds)
	if st.Trades != 4 || st.Wins != 2 || st.Losses != 2 {
		t.Fatalf("trades %+v", st)
	}
	if math.Abs(st.WinRate-0.5) > 1e-9 {
		t.Fatalf("win rate %v", st.WinRate)
	}
	if math.Abs(st.MaxDrawdown-24) > 1e-9 {
		t.Fatalf("dd %v want 24", st.MaxDrawdown)
	}
	if st.MaxConsecutiveLoss != 2 {
		t.Fatalf("streak %d", st.MaxConsecutiveLoss)
	}
}

type memSource struct {
	primary []types.Kline
	entry   []types.Kline
	pTF     string
	eTF     string
}

func (m memSource) KlinesRange(_ context.Context, _, interval string, start, end time.Time) ([]types.Kline, error) {
	bars := m.primary
	if interval == m.eTF && len(m.entry) > 0 {
		bars = m.entry
	}
	out := make([]types.Kline, 0, len(bars))
	for _, b := range bars {
		if b.OpenTime.Before(start) {
			continue
		}
		if !end.IsZero() && b.OpenTime.After(end) {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

func testCfg() *config.Config {
	return &config.Config{
		Symbol:     config.SymbolConfig{Name: "ETHUSDT", Leverage: 5},
		Timeframes: config.Timeframes{Primary: "1h", Entry: "15m"},
		Strategy: config.StrategyConfig{
			Name:      "squeeze",
			ATRPeriod: 14,
			MinBars:   220,
			Squeeze:   config.SqueezeConfig{}.WithDefaults(),
		},
		Risk: config.RiskConfig{
			RiskPerTrade:       0.0075,
			MaxDailyLossR:      99,
			MaxConsecutiveLoss: 99,
			MinQty:             0.001,
			QtyPrecision:       3,
			PricePrecision:     2,
			MaxNotionalPct:     0.7,
		},
		Engine: config.EngineConfig{KlineLimit: 500},
		Paper:  config.PaperConfig{InitialBalance: 10000, FeeRate: 0, SlippageBPS: 0},
	}
}

func oscBars(n, switchAt int, base, ampA, ampB float64) []types.Kline {
	bars := make([]types.Kline, n)
	origin := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		amp := ampA
		if i >= switchAt {
			amp = ampB
		}
		c := base + math.Sin(float64(i))*amp
		if i == n-1 {
			c = base
		}
		bars[i] = types.Kline{
			OpenTime:  origin.Add(time.Duration(i) * time.Hour),
			CloseTime: origin.Add(time.Duration(i+1) * time.Hour),
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

func appendBreakout(bars []types.Kline, period int, beyond float64) []types.Kline {
	last := bars[len(bars)-1]
	hi := last.High
	for i := len(bars) - period; i < len(bars); i++ {
		if i >= 0 && bars[i].High > hi {
			hi = bars[i].High
		}
	}
	c := hi + beyond
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

func TestRunSqueezeOpensAndStops(t *testing.T) {
	bars := oscBars(399, 300, 100, 3.0, 0.4)
	bars = appendBreakout(bars, 20, 0.3)
	// A deep red bar after the breakout should tag the ATR stop.
	last := bars[len(bars)-1]
	bars = append(bars, types.Kline{
		OpenTime:  last.CloseTime,
		CloseTime: last.CloseTime.Add(time.Hour),
		Open:      last.Close,
		High:      last.Close,
		Low:       last.Close - 20,
		Close:     last.Close - 5,
		Volume:    1500,
		Closed:    true,
	})

	cfg := testCfg()
	off := false
	cfg.Strategy.Squeeze.UseFundingFilter = &off
	src := memSource{primary: bars, pTF: "1h"}
	r := NewRunner(cfg, src)

	start := bars[0].OpenTime.Add(250 * time.Hour)
	end := bars[len(bars)-1].CloseTime.Add(time.Hour)
	res, err := r.Run(context.Background(), Params{
		Strategy: "squeeze",
		Symbol:   "ETHUSDT",
		Primary:  "1h",
		Start:    start,
		End:      end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Trades < 1 {
		t.Fatalf("expected at least one round trip, fills=%d stats=%+v", len(res.Fills), res.Stats)
	}
	if res.PrimaryBars == 0 {
		t.Fatal("expected primary bars counted")
	}
	if len(res.Price) == 0 {
		t.Fatal("expected price series")
	}
}

func TestNormalizeRejectsBadRange(t *testing.T) {
	r := NewRunner(testCfg(), memSource{})
	_, err := r.normalize(Params{
		Start: time.Now(),
		End:   time.Now().Add(-time.Hour),
	})
	if err == nil {
		t.Fatal("expected start/end error")
	}
}
