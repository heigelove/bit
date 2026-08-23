package indicators

import "math"

// Donchian returns the highest high / lowest low channel over the trailing
// `period` bars, excluding the bar at each index itself. Excluding the current
// bar matters for breakout logic: otherwise a bar that makes a new high is by
// definition at the channel top and every bar would look like a breakout.
func Donchian(highs, lows []float64, period int) (upper, mid, lower []float64) {
	n := len(highs)
	upper = make([]float64, n)
	mid = make([]float64, n)
	lower = make([]float64, n)
	for i := range upper {
		upper[i] = math.NaN()
		mid[i] = math.NaN()
		lower[i] = math.NaN()
	}
	if period <= 0 || n == 0 || len(lows) != n {
		return
	}
	for i := period; i < n; i++ {
		hi := highs[i-period]
		lo := lows[i-period]
		for j := i - period + 1; j < i; j++ {
			if highs[j] > hi {
				hi = highs[j]
			}
			if lows[j] < lo {
				lo = lows[j]
			}
		}
		upper[i] = hi
		lower[i] = lo
		mid[i] = (hi + lo) / 2
	}
	return
}

// PercentileRank returns the fraction of the trailing `lookback` values that are
// strictly below series[idx], in [0,1]. NaN entries are skipped. Used to detect
// volatility compression: a low rank of ATR means the market is coiled.
func PercentileRank(series []float64, idx, lookback int) float64 {
	if idx < 0 || idx >= len(series) || math.IsNaN(series[idx]) || lookback <= 0 {
		return math.NaN()
	}
	start := idx - lookback
	if start < 0 {
		start = 0
	}
	below, total := 0, 0
	for i := start; i < idx; i++ {
		if math.IsNaN(series[i]) {
			continue
		}
		total++
		if series[i] < series[idx] {
			below++
		}
	}
	if total == 0 {
		return math.NaN()
	}
	return float64(below) / float64(total)
}

// ChandelierLong is the trailing exit for a long: highest high of the last
// `period` bars minus `mult` ATRs.
func ChandelierLong(highs []float64, atr float64, idx, period int, mult float64) float64 {
	hh := SwingHigh(highs, idx, period)
	if math.IsNaN(hh) || math.IsNaN(atr) {
		return math.NaN()
	}
	return hh - mult*atr
}

// ChandelierShort is the trailing exit for a short: lowest low of the last
// `period` bars plus `mult` ATRs.
func ChandelierShort(lows []float64, atr float64, idx, period int, mult float64) float64 {
	ll := SwingLow(lows, idx, period)
	if math.IsNaN(ll) || math.IsNaN(atr) {
		return math.NaN()
	}
	return ll + mult*atr
}
