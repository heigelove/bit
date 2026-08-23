package portfolio

import (
	"fmt"
	"sync"
	"time"

	"github.com/work/bit/internal/types"
)

// PaperAccount simulates one-way futures position with fees/slippage.
type PaperAccount struct {
	mu       sync.Mutex
	balance  float64
	feeRate  float64
	slipBPS  float64
	posSide  types.PositionSide // PosLong / PosShort / empty
	qty      float64
	entry    float64
	trail    float64
	symbol   string
	fills    []types.TradeFill
}

func NewPaperAccount(symbol string, balance, feeRate, slipBPS float64) *PaperAccount {
	return &PaperAccount{
		balance: balance,
		feeRate: feeRate,
		slipBPS: slipBPS,
		symbol:  symbol,
	}
}

// Restore wallet and optional open position (e.g. from Redis).
func (p *PaperAccount) Restore(wallet float64, side types.PositionSide, qty, entry, trail float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.balance = wallet
	p.posSide = side
	p.qty = qty
	p.entry = entry
	p.trail = trail
}

// WalletBalance is cash balance without unrealized PnL.
func (p *PaperAccount) WalletBalance() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.balance
}

func (p *PaperAccount) Snapshot(mark float64) types.AccountState {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := types.AccountState{Balance: p.balance, Available: p.balance}
	if p.qty > 0 {
		upnl := p.unrealized(mark)
		st.Balance += upnl
		st.Positions = []types.Position{{
			Symbol:        p.symbol,
			Side:          p.posSide,
			Quantity:      p.qty,
			EntryPrice:    p.entry,
			MarkPrice:     mark,
			UnrealizedPNL: upnl,
		}}
	}
	return st
}

func (p *PaperAccount) Position() (side types.PositionSide, qty, entry, trail float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posSide, p.qty, p.entry, p.trail
}

func (p *PaperAccount) SetTrail(trail float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trail = trail
}

func (p *PaperAccount) Fills() []types.TradeFill {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]types.TradeFill, len(p.fills))
	copy(out, p.fills)
	return out
}

func (p *PaperAccount) OpenLong(price, qty float64, reason string, now time.Time) (*types.TradeFill, error) {
	return p.open(types.PosLong, types.SideBuy, price, qty, reason, now)
}

func (p *PaperAccount) OpenShort(price, qty float64, reason string, now time.Time) (*types.TradeFill, error) {
	return p.open(types.PosShort, types.SideSell, price, qty, reason, now)
}

func (p *PaperAccount) open(pos types.PositionSide, side types.Side, price, qty float64, reason string, now time.Time) (*types.TradeFill, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.qty > 0 {
		return nil, fmt.Errorf("already in position")
	}
	px := p.applySlip(price, side == types.SideBuy)
	fee := px * qty * p.feeRate
	p.balance -= fee
	p.posSide = pos
	p.qty = qty
	p.entry = px
	fill := types.TradeFill{
		Time: now, Symbol: p.symbol, Side: side,
		Quantity: qty, Price: px, Fee: fee, Reason: reason,
	}
	p.fills = append(p.fills, fill)
	return &fill, nil
}

func (p *PaperAccount) Close(price float64, reason string, now time.Time) (*types.TradeFill, error) {
	return p.ClosePartial(price, 0, reason, now)
}

// ClosePartial closes `qty` of the open position, or all of it when qty <= 0 or
// exceeds the remaining size. Entry price is unchanged by a partial close, so
// the remainder keeps its original cost basis.
func (p *PaperAccount) ClosePartial(price, qty float64, reason string, now time.Time) (*types.TradeFill, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.qty == 0 {
		return nil, fmt.Errorf("flat")
	}
	if qty <= 0 || qty > p.qty {
		qty = p.qty
	}
	var side types.Side
	if p.posSide == types.PosLong {
		side = types.SideSell
	} else {
		side = types.SideBuy
	}
	px := p.applySlip(price, side == types.SideBuy)
	pnl := p.realized(px, qty)
	fee := px * qty * p.feeRate
	p.balance += pnl - fee
	fill := types.TradeFill{
		Time: now, Symbol: p.symbol, Side: side,
		Quantity: qty, Price: px, Fee: fee, PNL: pnl - fee, Reason: reason,
	}
	p.fills = append(p.fills, fill)
	p.qty -= qty
	if p.qty <= 0 {
		p.qty = 0
		p.entry = 0
		p.trail = 0
		p.posSide = ""
	}
	return &fill, nil
}

func (p *PaperAccount) unrealized(mark float64) float64 {
	return p.realized(mark, p.qty)
}

func (p *PaperAccount) realized(mark, qty float64) float64 {
	if p.qty == 0 || qty == 0 {
		return 0
	}
	if p.posSide == types.PosLong {
		return (mark - p.entry) * qty
	}
	return (p.entry - mark) * qty
}

func (p *PaperAccount) applySlip(price float64, isBuy bool) float64 {
	slip := p.slipBPS / 10000.0
	if isBuy {
		return price * (1 + slip)
	}
	return price * (1 - slip)
}
