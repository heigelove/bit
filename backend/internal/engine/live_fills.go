package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/work/bit/internal/exchange/binance"
	"github.com/work/bit/internal/types"
)

const (
	binanceTradePrefix = "bt-"
	userTradePage      = 1000
	userTradeWindow    = 7 * 24 * time.Hour
)

func binanceTradeID(id int64) string {
	return fmt.Sprintf("%s%d", binanceTradePrefix, id)
}

// closingUserTrades keeps fills that realize PnL or reduce an open position.
// Opens have realizedPnl=0; stop / TP / manual closes carry the exchange PnL.
func closingUserTrades(fills []binance.UserTrade) []binance.UserTrade {
	out := make([]binance.UserTrade, 0, len(fills))
	pos := 0.0
	seen := make(map[int64]struct{}, len(fills))
	for _, f := range fills {
		if f.ID == 0 || f.Quantity <= 0 {
			continue
		}
		signed := f.Quantity
		if f.Side == types.SideSell {
			signed = -f.Quantity
		}
		reducing := pos != 0 && pos*signed < 0
		if math.Abs(f.RealizedPNL) > 1e-8 || reducing {
			if _, ok := seen[f.ID]; !ok {
				out = append(out, f)
				seen[f.ID] = struct{}{}
			}
		}
		pos += signed
	}
	return out
}

func (e *Engine) syncLiveFills(ctx context.Context) {
	if e.cfg.IsPaper() || e.client == nil || e.db == nil {
		return
	}
	n, err := e.importUserTrades(ctx)
	if err != nil {
		e.log.Warn("sync exchange fills", "err", err)
		return
	}
	if n > 0 {
		e.log.Info("synced exchange closes", "count", n)
	}
}

func (e *Engine) importUserTrades(ctx context.Context) (int, error) {
	symbol := e.cfg.Symbol.Name
	mode := e.cfg.Mode
	lastID, err := e.db.LastBinanceTradeID(ctx, mode, symbol)
	if err != nil {
		return 0, err
	}

	var fills []binance.UserTrade
	if lastID > 0 {
		fills, err = e.fetchUserTradesFrom(ctx, symbol, lastID)
	} else {
		start := time.Now().UTC().Add(-90 * 24 * time.Hour)
		if t, ok, err := e.db.FirstTradeTime(ctx, mode, symbol); err != nil {
			return 0, err
		} else if ok {
			start = t.Add(-time.Hour)
		}
		fills, err = e.fetchUserTradesRange(ctx, symbol, start, time.Now().UTC())
	}
	if err != nil {
		return 0, err
	}

	inserted := 0
	for _, f := range closingUserTrades(fills) {
		oid := binanceTradeID(f.ID)
		exists, err := e.db.TradeExists(ctx, mode, oid)
		if err != nil {
			return inserted, err
		}
		if exists {
			continue
		}
		// Market close we already booked with the exchange order id.
		if f.OrderID > 0 {
			if exists, err := e.db.TradeExists(ctx, mode, fmt.Sprintf("%d", f.OrderID)); err != nil {
				return inserted, err
			} else if exists {
				continue
			}
		}
		reason := "exchange close"
		if f.RealizedPNL < 0 {
			reason = "exchange stop fill"
		}
		fill := types.TradeFill{
			Time:     f.Time,
			Symbol:   f.Symbol,
			Side:     f.Side,
			Quantity: f.Quantity,
			Price:    f.Price,
			Fee:      f.Commission,
			PNL:      f.RealizedPNL,
			Reason:   reason,
		}
		if err := e.db.InsertTrade(ctx, mode, oid, fill); err != nil {
			return inserted, err
		}
		inserted++
		e.log.Info("fill",
			"side", fill.Side,
			"qty", fill.Quantity,
			"px", fmtF(fill.Price, 2),
			"fee", fmtF(fill.Fee, 4),
			"pnl", fmtF(fill.PNL, 2),
			"reason", fill.Reason,
			"order_id", oid,
		)
	}
	return inserted, nil
}

func (e *Engine) fetchUserTradesFrom(ctx context.Context, symbol string, fromID int64) ([]binance.UserTrade, error) {
	var all []binance.UserTrade
	id := fromID
	for {
		page, err := e.client.UserTrades(ctx, symbol, time.Time{}, time.Time{}, id, userTradePage)
		if err != nil {
			return all, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
		next := page[len(page)-1].ID
		if next <= id || len(page) < userTradePage {
			return all, nil
		}
		id = next
	}
}

func (e *Engine) fetchUserTradesRange(ctx context.Context, symbol string, start, end time.Time) ([]binance.UserTrade, error) {
	if start.IsZero() {
		start = end.Add(-90 * 24 * time.Hour)
	}
	var all []binance.UserTrade
	for w0 := start; w0.Before(end); w0 = w0.Add(userTradeWindow) {
		w1 := w0.Add(userTradeWindow)
		if w1.After(end) {
			w1 = end
		}
		fromID := int64(0)
		for {
			page, err := e.client.UserTrades(ctx, symbol, w0, w1, fromID, userTradePage)
			if err != nil {
				return all, err
			}
			if len(page) == 0 {
				break
			}
			all = append(all, page...)
			last := page[len(page)-1].ID
			if last <= fromID || len(page) < userTradePage {
				break
			}
			fromID = last
		}
	}
	return all, nil
}
