package backtest

import "math"

func computeStats(initial, final float64, equity []Point, fills []Fill, rounds []float64) Stats {
	st := Stats{
		FinalEquity: final,
		TotalPNL:    final - initial,
	}
	if initial > 0 {
		st.ReturnPct = (st.TotalPNL / initial) * 100
	}
	for _, f := range fills {
		st.Fees += f.Fee
	}

	var winSum, lossSum float64
	var streak, maxStreak int
	for _, pnl := range rounds {
		st.Trades++
		if pnl > 0 {
			st.Wins++
			winSum += pnl
			streak = 0
		} else if pnl < 0 {
			st.Losses++
			lossSum += pnl
			streak++
			if streak > maxStreak {
				maxStreak = streak
			}
		}
	}
	st.MaxConsecutiveLoss = maxStreak
	if st.Trades > 0 {
		st.WinRate = float64(st.Wins) / float64(st.Trades)
	}
	if st.Wins > 0 {
		st.AvgWin = winSum / float64(st.Wins)
	}
	if st.Losses > 0 {
		st.AvgLoss = lossSum / float64(st.Losses)
	}
	if lossSum < 0 {
		st.ProfitFactor = winSum / math.Abs(lossSum)
	}

	peak := initial
	maxDD := 0.0
	maxDDPct := 0.0
	for _, p := range equity {
		if p.Equity > peak {
			peak = p.Equity
		}
		dd := peak - p.Equity
		if dd > maxDD {
			maxDD = dd
			if peak > 0 {
				maxDDPct = dd / peak * 100
			}
		}
	}
	st.MaxDrawdown = maxDD
	st.MaxDrawdownPct = maxDDPct
	return st
}
