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

func TestDMIRisingSeries(t *testing.T) {
	n := 80
	h := make([]float64, n)
	l := make([]float64, n)
	c := make([]float64, n)
	for i := 0; i < n; i++ {
		c[i] = 100 + float64(i)
		h[i] = c[i] + 0.5
		l[i] = c[i] - 0.5
	}
	plusDI, minusDI, adx := DMI(h, l, c, 14)
	i := n - 1
	if math.IsNaN(plusDI[i]) || math.IsNaN(minusDI[i]) || math.IsNaN(adx[i]) {
		t.Fatal("expected warm DMI on rising series")
	}
	if plusDI[i] <= minusDI[i] {
		t.Fatalf("+DI should dominate on rising series: +DI=%v −DI=%v", plusDI[i], minusDI[i])
	}
	if adx[i] <= 0 {
		t.Fatalf("ADX should be positive, got %v", adx[i])
	}
	// ADX() must match DMI's ADX series.
	only := ADX(h, l, c, 14)
	if only[i] != adx[i] {
		t.Fatalf("ADX wrapper mismatch: %v vs %v", only[i], adx[i])
	}
}
