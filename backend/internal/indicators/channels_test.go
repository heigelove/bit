package indicators

import (
	"math"
	"testing"
)

func TestDonchianExcludesCurrentBar(t *testing.T) {
	highs := []float64{10, 11, 12, 11, 20}
	lows := []float64{5, 6, 7, 6, 4}

	upper, mid, lower := Donchian(highs, lows, 3)

	for i := 0; i < 3; i++ {
		if !math.IsNaN(upper[i]) {
			t.Fatalf("index %d should be NaN before warm-up, got %v", i, upper[i])
		}
	}
	// index 3 looks at bars 0..2
	if upper[3] != 12 || lower[3] != 5 {
		t.Fatalf("index 3 channel = [%v, %v], want [5, 12]", lower[3], upper[3])
	}
	// index 4 looks at bars 1..3 and must ignore its own 20/4 extremes
	if upper[4] != 12 || lower[4] != 6 {
		t.Fatalf("index 4 channel = [%v, %v], want [6, 12]", lower[4], upper[4])
	}
	if mid[4] != 9 {
		t.Fatalf("mid = %v, want 9", mid[4])
	}
}

func TestPercentileRank(t *testing.T) {
	series := []float64{1, 2, 3, 4, 5}

	if got := PercentileRank(series, 4, 4); got != 1 {
		t.Fatalf("highest value should rank 1, got %v", got)
	}
	// A value below every entry in the window ranks 0.
	low := []float64{5, 4, 3, 2, 1}
	if got := PercentileRank(low, 4, 4); got != 0 {
		t.Fatalf("lowest value should rank 0, got %v", got)
	}
	mixed := []float64{1, 5, 2, 6, 3}
	if got := PercentileRank(mixed, 4, 4); got != 0.5 {
		t.Fatalf("rank = %v, want 0.5", got)
	}
	if !math.IsNaN(PercentileRank(series, 0, 10)) {
		t.Fatal("expected NaN with no history")
	}
}

func TestChandelier(t *testing.T) {
	highs := []float64{10, 12, 14, 13}
	lows := []float64{8, 9, 10, 9}

	if got := ChandelierLong(highs, 2, 3, 4, 3); got != 14-6 {
		t.Fatalf("long chandelier = %v, want 8", got)
	}
	if got := ChandelierShort(lows, 2, 3, 4, 3); got != 8+6 {
		t.Fatalf("short chandelier = %v, want 14", got)
	}
	if !math.IsNaN(ChandelierLong(highs, math.NaN(), 3, 4, 3)) {
		t.Fatal("expected NaN when ATR is NaN")
	}
}
