package backtest

import "time"

// Params is a single backtest run. Empty strings / zeros fall back to config.
type Params struct {
	Symbol         string
	Strategy       string
	Primary        string
	Entry          string
	Start          time.Time
	End            time.Time
	InitialBalance float64
}

// Result is returned to the API / UI.
type Result struct {
	Strategy       string     `json:"strategy"`
	Symbol         string     `json:"symbol"`
	Primary        string     `json:"primary"`
	Entry          string     `json:"entry"`
	Start          string     `json:"start"`
	End            string     `json:"end"`
	PrimaryBars    int        `json:"primary_bars"`
	EntryBars      int        `json:"entry_bars"`
	InitialBalance float64    `json:"initial_balance"`
	Stats          Stats      `json:"stats"`
	Equity         []Point    `json:"equity"`
	Price          []PriceBar `json:"price"`
	Fills          []Fill     `json:"fills"`
}

// Stats summarizes realized performance. Drawdown is mark-to-market on the clock TF.
type Stats struct {
	FinalEquity        float64 `json:"final_equity"`
	TotalPNL           float64 `json:"total_pnl"`
	ReturnPct          float64 `json:"return_pct"`
	Trades             int     `json:"trades"`
	Wins               int     `json:"wins"`
	Losses             int     `json:"losses"`
	WinRate            float64 `json:"win_rate"`
	ProfitFactor       float64 `json:"profit_factor"`
	AvgWin             float64 `json:"avg_win"`
	AvgLoss            float64 `json:"avg_loss"`
	MaxDrawdown        float64 `json:"max_drawdown"`
	MaxDrawdownPct     float64 `json:"max_drawdown_pct"`
	MaxConsecutiveLoss int     `json:"max_consecutive_loss"`
	Fees               float64 `json:"fees"`
}

// Point is one equity-curve sample.
type Point struct {
	TS            string  `json:"ts"`
	Equity        float64 `json:"equity"`
	CumulativePNL float64 `json:"cumulative_pnl"`
	TradePNL      float64 `json:"trade_pnl"`
}

// PriceBar is a downsampled primary-TF close for the price chart.
type PriceBar struct {
	TS    string  `json:"ts"`
	Open  float64 `json:"open"`
	High  float64 `json:"high"`
	Low   float64 `json:"low"`
	Close float64 `json:"close"`
}

// Fill is a simulated execution.
type Fill struct {
	TS       string  `json:"ts"`
	Side     string  `json:"side"`
	Action   string  `json:"action"`
	Quantity float64 `json:"quantity"`
	Price    float64 `json:"price"`
	Fee      float64 `json:"fee"`
	PNL      float64 `json:"pnl"`
	Reason   string  `json:"reason"`
	Equity   float64 `json:"equity"`
}
