package types

import "time"

// Side is order / position direction.
type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

// PositionSide for hedge / one-way mode.
type PositionSide string

const (
	PosLong  PositionSide = "LONG"
	PosShort PositionSide = "SHORT"
	PosBoth  PositionSide = "BOTH"
)

// SignalAction is what the strategy wants to do.
type SignalAction string

const (
	ActionNone        SignalAction = "NONE"
	ActionOpenLong    SignalAction = "OPEN_LONG"
	ActionOpenShort   SignalAction = "OPEN_SHORT"
	ActionCloseLong   SignalAction = "CLOSE_LONG"
	ActionCloseShort  SignalAction = "CLOSE_SHORT"
	ActionReduceLong  SignalAction = "REDUCE_LONG"
	ActionReduceShort SignalAction = "REDUCE_SHORT"
)

// IsOpen reports whether the action establishes a new position.
func (a SignalAction) IsOpen() bool {
	return a == ActionOpenLong || a == ActionOpenShort
}

// IsClose reports whether the action fully exits a position.
func (a SignalAction) IsClose() bool {
	return a == ActionCloseLong || a == ActionCloseShort
}

// IsReduce reports whether the action partially exits a position.
func (a SignalAction) IsReduce() bool {
	return a == ActionReduceLong || a == ActionReduceShort
}

// Kline is an OHLCV candle.
type Kline struct {
	OpenTime  time.Time
	CloseTime time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Closed    bool
}

// Signal is a strategy decision at a point in time.
type Signal struct {
	Time     time.Time
	Symbol   string
	Action   SignalAction
	Price    float64
	StopLoss float64
	Reason   string
	// Portion is the fraction of the open position to close, for reduce actions.
	// Ignored by other actions.
	Portion float64
	EMA20   float64
	EMA60   float64
	EMA200  float64
	ATR     float64
	ADX     float64
	// Breakout diagnostics, populated by channel-based strategies.
	DonchianUp float64
	DonchianDn float64
	ATRPct     float64
	Funding    float64
}

// OrderRequest is sent to the exchange.
type OrderRequest struct {
	Symbol       string
	Side         Side
	PositionSide PositionSide
	Quantity     float64
	Price        float64 // 0 = market
	StopPrice    float64
	ReduceOnly   bool
	ClientID     string
}

// OrderResult is exchange ack.
type OrderResult struct {
	OrderID   int64
	ClientID  string
	Symbol    string
	Side      Side
	Quantity  float64
	Price     float64
	Status    string
	Timestamp time.Time
}

// Position is current open exposure.
type Position struct {
	Symbol       string
	Side         PositionSide
	Quantity     float64
	EntryPrice   float64
	MarkPrice    float64
	UnrealizedPNL float64
	Leverage     int
}

// AccountState holds balances and positions.
type AccountState struct {
	Balance   float64
	Available float64
	Positions []Position
}

// TradeFill is a filled execution for logging / PnL.
type TradeFill struct {
	Time     time.Time
	Symbol   string
	Side     Side
	Quantity float64
	Price    float64
	Fee      float64
	PNL      float64
	Reason   string
}
