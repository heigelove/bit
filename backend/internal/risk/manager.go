package risk

import (
	"fmt"
	"math"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/types"
)

// Manager enforces position sizing and daily circuit breakers.
type Manager struct {
	cfg config.RiskConfig

	day            time.Time
	dayRealizedR   float64
	consecutiveLoss int
	halted         bool
	haltReason     string
	rUnit          float64 // 1R in quote currency for today (equity * risk_per_trade)
}

func NewManager(cfg config.RiskConfig) *Manager {
	return &Manager{cfg: cfg}
}

func (m *Manager) Halted() (bool, string) {
	return m.halted, m.haltReason
}

func (m *Manager) ResetHalt() {
	m.halted = false
	m.haltReason = ""
}

// Snapshot returns risk circuit-breaker fields for caching.
func (m *Manager) Snapshot() (dayR float64, consecutive int, halted bool, reason string) {
	return m.dayRealizedR, m.consecutiveLoss, m.halted, m.haltReason
}

// Restore loads risk state from cache (e.g. Redis).
func (m *Manager) Restore(dayR float64, consecutive int, halted bool, reason string) {
	m.dayRealizedR = dayR
	m.consecutiveLoss = consecutive
	m.halted = halted
	m.haltReason = reason
}

func (m *Manager) onDay(now time.Time, equity float64) {
	d := now.UTC().Truncate(24 * time.Hour)
	if !d.Equal(m.day) {
		m.day = d
		m.dayRealizedR = 0
		m.rUnit = equity * m.cfg.RiskPerTrade
		// consecutive loss persists across days by design
	}
	if m.rUnit <= 0 {
		m.rUnit = equity * m.cfg.RiskPerTrade
	}
}

// SizePosition returns quantity given equity, entry, stop. Leverage caps the
// notional so a very tight stop cannot size into a near-liquidation position.
func (m *Manager) SizePosition(now time.Time, equity, entry, stop float64, leverage int) (qty float64, riskAmt float64, err error) {
	m.onDay(now, equity)
	if m.halted {
		return 0, 0, fmt.Errorf("risk halted: %s", m.haltReason)
	}
	stopDist := math.Abs(entry - stop)
	if stopDist <= 0 || entry <= 0 {
		return 0, 0, fmt.Errorf("invalid stop distance")
	}
	riskAmt = equity * m.cfg.RiskPerTrade
	qty = riskAmt / stopDist
	if capped := m.maxQtyByNotional(equity, entry, leverage); capped > 0 && capped < qty {
		qty = capped
		riskAmt = qty * stopDist
	}
	qty = roundDown(qty, m.cfg.QtyPrecision)
	if qty < m.cfg.MinQty {
		return 0, 0, fmt.Errorf("qty %.6f below min %.6f", qty, m.cfg.MinQty)
	}
	return qty, riskAmt, nil
}

// maxQtyByNotional returns 0 when no cap applies.
func (m *Manager) maxQtyByNotional(equity, price float64, leverage int) float64 {
	if m.cfg.MaxNotionalPct <= 0 || leverage <= 0 || price <= 0 || equity <= 0 {
		return 0
	}
	return equity * float64(leverage) * m.cfg.MaxNotionalPct / price
}

// AllowOpen checks circuit breakers before opening.
func (m *Manager) AllowOpen(now time.Time, equity float64) error {
	m.onDay(now, equity)
	if m.halted {
		return fmt.Errorf("risk halted: %s", m.haltReason)
	}
	if m.consecutiveLoss >= m.cfg.MaxConsecutiveLoss {
		m.halted = true
		m.haltReason = fmt.Sprintf("max consecutive losses %d", m.consecutiveLoss)
		return fmt.Errorf("%s", m.haltReason)
	}
	if m.dayRealizedR <= -m.cfg.MaxDailyLossR {
		m.halted = true
		m.haltReason = fmt.Sprintf("daily loss %.2fR >= limit", -m.dayRealizedR)
		return fmt.Errorf("%s", m.haltReason)
	}
	return nil
}

// RecordPartial books realized PnL from a partial exit into the daily R tally.
// The consecutive-loss streak is left alone: only a full exit decides whether a
// trade was a winner.
func (m *Manager) RecordPartial(now time.Time, equity, pnl float64) {
	m.onDay(now, equity)
	if m.rUnit > 0 {
		m.dayRealizedR += pnl / m.rUnit
	}
	if m.dayRealizedR <= -m.cfg.MaxDailyLossR {
		m.halted = true
		m.haltReason = fmt.Sprintf("daily loss %.2fR", -m.dayRealizedR)
	}
}

// RecordClosedTrade updates R and consecutive loss streak.
func (m *Manager) RecordClosedTrade(now time.Time, equity, pnl float64) {
	m.RecordPartial(now, equity, pnl)
	if pnl < 0 {
		m.consecutiveLoss++
	} else if pnl > 0 {
		m.consecutiveLoss = 0
	}
	if m.consecutiveLoss >= m.cfg.MaxConsecutiveLoss {
		m.halted = true
		m.haltReason = fmt.Sprintf("max consecutive losses %d", m.consecutiveLoss)
	}
}

func (m *Manager) RoundPrice(px float64) float64 {
	return roundHalf(px, m.cfg.PricePrecision)
}

func (m *Manager) RoundQty(qty float64) float64 {
	return roundDown(qty, m.cfg.QtyPrecision)
}

func roundDown(v float64, prec int) float64 {
	p := math.Pow(10, float64(prec))
	return math.Floor(v*p) / p
}

func roundHalf(v float64, prec int) float64 {
	p := math.Pow(10, float64(prec))
	return math.Round(v*p) / p
}

// ValidateSignal ensures open signals have stop.
func ValidateSignal(sig types.Signal) error {
	switch sig.Action {
	case types.ActionOpenLong, types.ActionOpenShort:
		if sig.StopLoss <= 0 {
			return fmt.Errorf("missing stop loss")
		}
		if sig.Action == types.ActionOpenLong && sig.StopLoss >= sig.Price {
			return fmt.Errorf("long stop must be below price")
		}
		if sig.Action == types.ActionOpenShort && sig.StopLoss <= sig.Price {
			return fmt.Errorf("short stop must be above price")
		}
	case types.ActionReduceLong, types.ActionReduceShort:
		if sig.Portion <= 0 || sig.Portion >= 1 {
			return fmt.Errorf("reduce portion %.2f must be in (0,1)", sig.Portion)
		}
	}
	return nil
}
