package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/work/bit/internal/types"
)

// Client is a thin Binance USD-M Futures REST client.
type Client struct {
	restBase  string
	apiKey    string
	apiSecret string
	http      *http.Client
}

func NewClient(restBase, apiKey, apiSecret string) *Client {
	return &Client{
		restBase:  strings.TrimRight(restBase, "/"),
		apiKey:    apiKey,
		apiSecret: apiSecret,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.doPublic(ctx, http.MethodGet, "/fapi/v1/ping", nil)
	return err
}

func (c *Client) ServerTime(ctx context.Context) (time.Time, error) {
	body, err := c.doPublic(ctx, http.MethodGet, "/fapi/v1/time", nil)
	if err != nil {
		return time.Time{}, err
	}
	var resp struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(resp.ServerTime), nil
}

// Klines fetches OHLCV candles. interval e.g. 1h, 15m.
func (c *Client) Klines(ctx context.Context, symbol, interval string, limit int) ([]types.Kline, error) {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(symbol))
	q.Set("interval", interval)
	q.Set("limit", strconv.Itoa(limit))
	body, err := c.doPublic(ctx, http.MethodGet, "/fapi/v1/klines", q)
	if err != nil {
		return nil, err
	}
	var raw [][]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode klines: %w", err)
	}
	out := make([]types.Kline, 0, len(raw))
	for _, row := range raw {
		if len(row) < 7 {
			continue
		}
		ot, _ := toInt64(row[0])
		ct, _ := toInt64(row[6])
		o, _ := toFloat(row[1])
		h, _ := toFloat(row[2])
		l, _ := toFloat(row[3])
		cl, _ := toFloat(row[4])
		v, _ := toFloat(row[5])
		out = append(out, types.Kline{
			OpenTime:  time.UnixMilli(ot).UTC(),
			CloseTime: time.UnixMilli(ct).UTC(),
			Open:      o,
			High:      h,
			Low:       l,
			Close:     cl,
			Volume:    v,
			Closed:    true,
		})
	}
	// Last candle may still be forming; mark last as open if close time in future.
	if n := len(out); n > 0 && out[n-1].CloseTime.After(time.Now().UTC()) {
		out[n-1].Closed = false
	}
	return out, nil
}

// PremiumIndex is the mark price plus the perpetual funding state.
type PremiumIndex struct {
	MarkPrice       float64
	IndexPrice      float64
	LastFundingRate float64
	NextFundingTime time.Time
}

func (c *Client) PremiumIndex(ctx context.Context, symbol string) (*PremiumIndex, error) {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(symbol))
	body, err := c.doPublic(ctx, http.MethodGet, "/fapi/v1/premiumIndex", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	mark, err := strconv.ParseFloat(resp.MarkPrice, 64)
	if err != nil {
		return nil, fmt.Errorf("parse mark price: %w", err)
	}
	idx, _ := strconv.ParseFloat(resp.IndexPrice, 64)
	funding, _ := strconv.ParseFloat(resp.LastFundingRate, 64)
	out := &PremiumIndex{MarkPrice: mark, IndexPrice: idx, LastFundingRate: funding}
	if resp.NextFundingTime > 0 {
		out.NextFundingTime = time.UnixMilli(resp.NextFundingTime).UTC()
	}
	return out, nil
}

func (c *Client) MarkPrice(ctx context.Context, symbol string) (float64, error) {
	pi, err := c.PremiumIndex(ctx, symbol)
	if err != nil {
		return 0, err
	}
	return pi.MarkPrice, nil
}

func (c *Client) SetLeverage(ctx context.Context, symbol string, leverage int) error {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(symbol))
	q.Set("leverage", strconv.Itoa(leverage))
	_, err := c.doSigned(ctx, http.MethodPost, "/fapi/v1/leverage", q)
	return err
}

func (c *Client) SetMarginType(ctx context.Context, symbol, marginType string) error {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(symbol))
	q.Set("marginType", strings.ToUpper(marginType))
	_, err := c.doSigned(ctx, http.MethodPost, "/fapi/v1/marginType", q)
	// -4046 already set — ignore
	if err != nil && strings.Contains(err.Error(), "-4046") {
		return nil
	}
	return err
}

func (c *Client) Account(ctx context.Context) (*types.AccountState, error) {
	body, err := c.doSigned(ctx, http.MethodGet, "/fapi/v2/account", url.Values{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		TotalWalletBalance string `json:"totalWalletBalance"`
		AvailableBalance   string `json:"availableBalance"`
		Positions          []struct {
			Symbol           string `json:"symbol"`
			PositionAmt      string `json:"positionAmt"`
			EntryPrice       string `json:"entryPrice"`
			MarkPrice        string `json:"markPrice"`
			UnRealizedProfit string `json:"unRealizedProfit"`
			Leverage         string `json:"leverage"`
			PositionSide     string `json:"positionSide"`
		} `json:"positions"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	bal, _ := strconv.ParseFloat(resp.TotalWalletBalance, 64)
	avail, _ := strconv.ParseFloat(resp.AvailableBalance, 64)
	st := &types.AccountState{Balance: bal, Available: avail}
	for _, p := range resp.Positions {
		qty, _ := strconv.ParseFloat(p.PositionAmt, 64)
		if qty == 0 {
			continue
		}
		entry, _ := strconv.ParseFloat(p.EntryPrice, 64)
		mark, _ := strconv.ParseFloat(p.MarkPrice, 64)
		upnl, _ := strconv.ParseFloat(p.UnRealizedProfit, 64)
		lev, _ := strconv.Atoi(p.Leverage)
		side := types.PosBoth
		switch p.PositionSide {
		case "LONG":
			side = types.PosLong
		case "SHORT":
			side = types.PosShort
		default:
			if qty > 0 {
				side = types.PosLong
			} else {
				side = types.PosShort
				qty = -qty
			}
		}
		if qty < 0 {
			qty = -qty
		}
		st.Positions = append(st.Positions, types.Position{
			Symbol:        p.Symbol,
			Side:          side,
			Quantity:      qty,
			EntryPrice:    entry,
			MarkPrice:     mark,
			UnrealizedPNL: upnl,
			Leverage:      lev,
		})
	}
	return st, nil
}

func (c *Client) PlaceMarketOrder(ctx context.Context, req types.OrderRequest) (*types.OrderResult, error) {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(req.Symbol))
	q.Set("side", string(req.Side))
	q.Set("type", "MARKET")
	q.Set("quantity", trimFloat(req.Quantity))
	if req.ReduceOnly {
		q.Set("reduceOnly", "true")
	}
	if req.ClientID != "" {
		q.Set("newClientOrderId", req.ClientID)
	}
	// One-way mode: omit positionSide or BOTH
	body, err := c.doSigned(ctx, http.MethodPost, "/fapi/v1/order", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Status        string `json:"status"`
		AvgPrice      string `json:"avgPrice"`
		ExecutedQty   string `json:"executedQty"`
		UpdateTime    int64  `json:"updateTime"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	px, _ := strconv.ParseFloat(resp.AvgPrice, 64)
	qty, _ := strconv.ParseFloat(resp.ExecutedQty, 64)
	return &types.OrderResult{
		OrderID:   resp.OrderID,
		ClientID:  resp.ClientOrderID,
		Symbol:    resp.Symbol,
		Side:      types.Side(resp.Side),
		Quantity:  qty,
		Price:     px,
		Status:    resp.Status,
		Timestamp: time.UnixMilli(resp.UpdateTime),
	}, nil
}

func (c *Client) PlaceStopMarket(ctx context.Context, req types.OrderRequest) (*types.OrderResult, error) {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(req.Symbol))
	q.Set("side", string(req.Side))
	q.Set("type", "STOP_MARKET")
	q.Set("stopPrice", trimFloat(req.StopPrice))
	q.Set("closePosition", "true")
	if req.ClientID != "" {
		q.Set("newClientOrderId", req.ClientID)
	}
	body, err := c.doSigned(ctx, http.MethodPost, "/fapi/v1/order", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Status        string `json:"status"`
		UpdateTime    int64  `json:"updateTime"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &types.OrderResult{
		OrderID:   resp.OrderID,
		ClientID:  resp.ClientOrderID,
		Symbol:    resp.Symbol,
		Side:      types.Side(resp.Side),
		Status:    resp.Status,
		Timestamp: time.UnixMilli(resp.UpdateTime),
	}, nil
}

func (c *Client) CancelAll(ctx context.Context, symbol string) error {
	q := url.Values{}
	q.Set("symbol", strings.ToUpper(symbol))
	_, err := c.doSigned(ctx, http.MethodDelete, "/fapi/v1/allOpenOrders", q)
	return err
}

func (c *Client) doPublic(ctx context.Context, method, path string, q url.Values) ([]byte, error) {
	u := c.restBase + path
	if q != nil && len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	return c.exec(req)
}

func (c *Client) doSigned(ctx context.Context, method, path string, q url.Values) ([]byte, error) {
	if c.apiKey == "" || c.apiSecret == "" {
		return nil, fmt.Errorf("api key/secret required for signed request")
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	q.Set("recvWindow", "5000")
	payload := q.Encode()
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	u := c.restBase + path + "?" + payload + "&signature=" + sig
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)
	return c.exec(req)
}

func (c *Client) exec(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("binance http %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func toInt64(v any) (int64, error) {
	switch t := v.(type) {
	case float64:
		return int64(t), nil
	case json.Number:
		return t.Int64()
	case string:
		return strconv.ParseInt(t, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case string:
		return strconv.ParseFloat(t, 64)
	case json.Number:
		return t.Float64()
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
