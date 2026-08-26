package indicators

import "math"

// EMA computes exponential moving average. Returns NaN until period is warm.
func EMA(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	if period <= 0 || len(closes) == 0 {
		return out
	}
	for i := range out {
		out[i] = math.NaN()
	}
	if len(closes) < period {
		return out
	}
	var sum float64
	for i := 0; i < period; i++ {
		sum += closes[i]
	}
	out[period-1] = sum / float64(period)
	k := 2.0 / (float64(period) + 1.0)
	for i := period; i < len(closes); i++ {
		out[i] = closes[i]*k + out[i-1]*(1-k)
	}
	return out
}

// ATR Wilder's average true range.
func ATR(highs, lows, closes []float64, period int) []float64 {
	n := len(closes)
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	if period <= 0 || n < period+1 {
		return out
	}
	tr := make([]float64, n)
	tr[0] = highs[0] - lows[0]
	for i := 1; i < n; i++ {
		hl := highs[i] - lows[i]
		hc := math.Abs(highs[i] - closes[i-1])
		lc := math.Abs(lows[i] - closes[i-1])
		tr[i] = math.Max(hl, math.Max(hc, lc))
	}
	var sum float64
	for i := 1; i <= period; i++ {
		sum += tr[i]
	}
	out[period] = sum / float64(period)
	for i := period + 1; i < n; i++ {
		out[i] = (out[i-1]*float64(period-1) + tr[i]) / float64(period)
	}
	return out
}

// ADX average directional index (Wilder). Returns ADX series.
func ADX(highs, lows, closes []float64, period int) []float64 {
	_, _, adx := DMI(highs, lows, closes, period)
	return adx
}

// DMI returns Wilder +DI, -DI and ADX series. Used for trend direction
// confirmation (+DI vs -DI) in addition to trend strength (ADX).
func DMI(highs, lows, closes []float64, period int) (plusDI, minusDI, adx []float64) {
	n := len(closes)
	plusDI = make([]float64, n)
	minusDI = make([]float64, n)
	adx = make([]float64, n)
	for i := 0; i < n; i++ {
		plusDI[i] = math.NaN()
		minusDI[i] = math.NaN()
		adx[i] = math.NaN()
	}
	if period <= 0 || n < period*2 {
		return
	}

	plusDM := make([]float64, n)
	minusDM := make([]float64, n)
	tr := make([]float64, n)
	tr[0] = highs[0] - lows[0]

	for i := 1; i < n; i++ {
		up := highs[i] - highs[i-1]
		down := lows[i-1] - lows[i]
		if up > down && up > 0 {
			plusDM[i] = up
		}
		if down > up && down > 0 {
			minusDM[i] = down
		}
		hl := highs[i] - lows[i]
		hc := math.Abs(highs[i] - closes[i-1])
		lc := math.Abs(lows[i] - closes[i-1])
		tr[i] = math.Max(hl, math.Max(hc, lc))
	}

	smoothTR := wilderSmooth(tr, period)
	smoothPlus := wilderSmooth(plusDM, period)
	smoothMinus := wilderSmooth(minusDM, period)

	dx := make([]float64, n)
	for i := range dx {
		dx[i] = math.NaN()
	}
	for i := period; i < n; i++ {
		if smoothTR[i] == 0 || math.IsNaN(smoothTR[i]) {
			continue
		}
		pdi := 100 * smoothPlus[i] / smoothTR[i]
		mdi := 100 * smoothMinus[i] / smoothTR[i]
		plusDI[i] = pdi
		minusDI[i] = mdi
		den := pdi + mdi
		if den == 0 {
			dx[i] = 0
			continue
		}
		dx[i] = 100 * math.Abs(pdi-mdi) / den
	}

	// First ADX = SMA of first `period` DX values starting at index period.
	start := period * 2
	if start >= n {
		return
	}
	var sum float64
	count := 0
	for i := period; i < start && i < n; i++ {
		if !math.IsNaN(dx[i]) {
			sum += dx[i]
			count++
		}
	}
	if count == 0 {
		return
	}
	adx[start-1] = sum / float64(count)
	for i := start; i < n; i++ {
		if math.IsNaN(dx[i]) || math.IsNaN(adx[i-1]) {
			continue
		}
		adx[i] = (adx[i-1]*float64(period-1) + dx[i]) / float64(period)
	}
	return
}

func wilderSmooth(src []float64, period int) []float64 {
	out := make([]float64, len(src))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(src) <= period {
		return out
	}
	var sum float64
	for i := 1; i <= period; i++ {
		sum += src[i]
	}
	out[period] = sum
	for i := period + 1; i < len(src); i++ {
		out[i] = out[i-1] - out[i-1]/float64(period) + src[i]
	}
	return out
}

// SwingLow returns the lowest low over lookback ending at idx (inclusive).
func SwingLow(lows []float64, idx, lookback int) float64 {
	if idx < 0 || idx >= len(lows) {
		return math.NaN()
	}
	start := idx - lookback + 1
	if start < 0 {
		start = 0
	}
	m := lows[start]
	for i := start + 1; i <= idx; i++ {
		if lows[i] < m {
			m = lows[i]
		}
	}
	return m
}

// SwingHigh returns the highest high over lookback ending at idx (inclusive).
func SwingHigh(highs []float64, idx, lookback int) float64 {
	if idx < 0 || idx >= len(highs) {
		return math.NaN()
	}
	start := idx - lookback + 1
	if start < 0 {
		start = 0
	}
	m := highs[start]
	for i := start + 1; i <= idx; i++ {
		if highs[i] > m {
			m = highs[i]
		}
	}
	return m
}

func LastValid(series []float64) (float64, bool) {
	for i := len(series) - 1; i >= 0; i-- {
		if !math.IsNaN(series[i]) {
			return series[i], true
		}
	}
	return math.NaN(), false
}
