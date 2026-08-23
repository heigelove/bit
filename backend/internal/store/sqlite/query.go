package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
)

type LogRow struct {
	ID        int64          `json:"id"`
	TS        string         `json:"ts"`
	Level     string         `json:"level"`
	Msg       string         `json:"msg"`
	Attrs     map[string]any `json:"attrs"`
	AttrsJSON string         `json:"-"`
}

type TradeRow struct {
	ID       int64   `json:"id"`
	TS       string  `json:"ts"`
	Symbol   string  `json:"symbol"`
	Side     string  `json:"side"`
	Quantity float64 `json:"quantity"`
	Price    float64 `json:"price"`
	Fee      float64 `json:"fee"`
	PNL      float64 `json:"pnl"`
	Reason   string  `json:"reason"`
	Mode     string  `json:"mode"`
	OrderID  string  `json:"order_id"`
}

type SignalRow struct {
	ID         int64   `json:"id"`
	TS         string  `json:"ts"`
	Symbol     string  `json:"symbol"`
	Action     string  `json:"action"`
	Reason     string  `json:"reason"`
	Price      float64 `json:"price"`
	EMA20      float64 `json:"ema20"`
	EMA60      float64 `json:"ema60"`
	EMA200     float64 `json:"ema200"`
	ATR        float64 `json:"atr"`
	ADX        float64 `json:"adx"`
	StopLoss   float64 `json:"stop_loss"`
	DonchianUp float64 `json:"donchian_up"`
	DonchianDn float64 `json:"donchian_dn"`
	ATRPct     float64 `json:"atr_pct"`
	Funding    float64 `json:"funding"`
	Mode       string  `json:"mode"`
}

type PageResult[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Size  int   `json:"size"`
}

func (s *Store) ListLogs(ctx context.Context, page, size int, level string) (*PageResult[LogRow], error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	offset := (page - 1) * size

	where := "1=1"
	args := []any{}
	if level != "" {
		where += " AND level = ?"
		args = append(args, level)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_logs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	q := `SELECT id, ts, level, msg, attrs_json FROM app_logs WHERE ` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, size, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]LogRow, 0, size)
	for rows.Next() {
		var r LogRow
		var attrs string
		if err := rows.Scan(&r.ID, &r.TS, &r.Level, &r.Msg, &attrs); err != nil {
			return nil, err
		}
		r.AttrsJSON = attrs
		_ = json.Unmarshal([]byte(attrs), &r.Attrs)
		if r.Attrs == nil {
			r.Attrs = map[string]any{}
		}
		items = append(items, r)
	}
	return &PageResult[LogRow]{Items: items, Total: total, Page: page, Size: size}, rows.Err()
}

func (s *Store) ListTrades(ctx context.Context, page, size int, symbol string) (*PageResult[TradeRow], error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	offset := (page - 1) * size

	where := "1=1"
	args := []any{}
	if symbol != "" {
		where += " AND symbol = ?"
		args = append(args, symbol)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trades WHERE `+where, args...).Scan(&total); err != nil {
		return nil, err
	}

	q := `SELECT id, ts, symbol, side, quantity, price, fee, pnl, reason, mode, order_id
	      FROM trades WHERE ` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, size, offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]TradeRow, 0, size)
	for rows.Next() {
		var r TradeRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Symbol, &r.Side, &r.Quantity, &r.Price, &r.Fee, &r.PNL, &r.Reason, &r.Mode, &r.OrderID); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return &PageResult[TradeRow]{Items: items, Total: total, Page: page, Size: size}, rows.Err()
}

func (s *Store) ListSignals(ctx context.Context, page, size int) (*PageResult[SignalRow], error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	offset := (page - 1) * size

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM signals`).Scan(&total); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, ts, symbol, action, reason, price, ema20, ema60, ema200, atr, adx, stop_loss, mode,
       donchian_up, donchian_dn, atr_pct, funding
FROM signals ORDER BY id DESC LIMIT ? OFFSET ?`, size, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]SignalRow, 0, size)
	for rows.Next() {
		var r SignalRow
		var ema20, ema60, ema200, atr, adx sql.NullFloat64
		var dchUp, dchDn, atrPct, funding sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.TS, &r.Symbol, &r.Action, &r.Reason, &r.Price,
			&ema20, &ema60, &ema200, &atr, &adx, &r.StopLoss, &r.Mode,
			&dchUp, &dchDn, &atrPct, &funding); err != nil {
			return nil, err
		}
		r.EMA20 = ema20.Float64
		r.EMA60 = ema60.Float64
		r.EMA200 = ema200.Float64
		r.ATR = atr.Float64
		r.ADX = adx.Float64
		r.DonchianUp = dchUp.Float64
		r.DonchianDn = dchDn.Float64
		r.ATRPct = atrPct.Float64
		r.Funding = funding.Float64
		items = append(items, r)
	}
	return &PageResult[SignalRow]{Items: items, Total: total, Page: page, Size: size}, rows.Err()
}
