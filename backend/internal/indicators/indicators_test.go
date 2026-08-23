package indicators

import (
	"math"
	"testing"
)

func TestEMA(t *testing.T) {
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = 100 + float64(i)
	}
	ema := EMA(closes, 10)
	if math.IsNaN(ema[9]) {
		t.Fatal("expected warm EMA at index 9")
	}
	if math.IsNaN(ema[8]) {
		// ok - before warm
	} else {
		t.Fatal("expected NaN before warm-up")
	}
	if ema[len(ema)-1] <= ema[9] {
		t.Fatalf("EMA should rise on rising series: last=%v firstWarm=%v", ema[len(ema)-1], ema[9])
	}
}

func TestATR(t *testing.T) {
	n := 40
	h := make([]float64, n)
	l := make([]float64, n)
	c := make([]float64, n)
	for i := 0; i < n; i++ {
		c[i] = 100 + float64(i)*0.1
		h[i] = c[i] + 1
		l[i] = c[i] - 1
	}
	atr := ATR(h, l, c, 14)
	v, ok := LastValid(atr)
	if !ok || v <= 0 {
		t.Fatalf("expected positive ATR, got %v", v)
	}
}
