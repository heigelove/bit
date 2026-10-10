package backtest

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/exchange/binance"
	"github.com/work/bit/internal/portfolio"
	"github.com/work/bit/internal/risk"
	"github.com/work/bit/internal/strategy"
	"github.com/work/bit/internal/types"
)

const (
	maxRange      = 731 * 24 * time.Hour
	maxEquityPts  = 1000
	maxPriceBars  = 800
	minWindowBars = 250
)

// CandleSource loads historical OHLCV. The Binance public REST client implements it.
type CandleSource interface {
	KlinesRange(ctx context.Context, symbol, interval string, start, end time.Time) ([]types.Kline, error)
}

// Runner fetches candles and walks the configured strategy bar-by-bar.
type Runner struct {
	cfg    *config.Config
	source CandleSource
}

func NewRunner(cfg *config.Config, source CandleSource) *Runner {
	if source == nil {
		source = binance.NewClient(cfg.Exchange.RestBase, "", "")
	}
	return &Runner{cfg: cfg, source: source}
}

type tradeContext struct {
	EntryTime time.Time
	InitQty   float64
	InitStop  float64
}

type sim struct {
	cfg    *config.Config
	params Params
	strat  strategy.Strategy
	paper  *portfolio.PaperAccount
	risk   *risk.Manager
	trade  tradeContext
	last   strategy.PositionState
	fills  []Fill
	equity []Point
	round  float64
	rounds []float64
}

// Run fetches history and simulates the strategy over [params.Start, params.End].
func (r *Runner) Run(ctx context.Context, params Params) (*Result, error) {
	explicitTF := strings.TrimSpace(params.Primary) != ""
	p, err := r.normalize(params)
	if err != nil {
		return nil, err
	}
	// Vegas is tuned on its own candle size. An explicit primary in the request
	// still wins, so a caller can compare timeframes.
	if !explicitTF {
		if iv := vegasInterval(p.Strategy, r.cfg.Strategy.Vegas.Interval); iv != "" {
			p.Primary = iv
			p.Entry = iv
		}
	}
	pDur, err := ParseInterval(p.Primary)
	if err != nil {
		return nil, fmt.Errorf("primary timeframe: %w", err)
	}

	strat, err := strategy.New(p.Strategy, p.Symbol, r.cfg.Strategy)
	if err != nil {
		return nil, err
	}

	need := r.cfg.Strategy.MinBars
	if need < minWindowBars {
		need = minWindowBars
	}
	if lim := r.cfg.Engine.KlineLimit; lim > need {
		need = lim
	}
	fetchStart := p.Start.Add(-time.Duration(need+20) * pDur)

	primary, err := r.source.KlinesRange(ctx, p.Symbol, p.Primary, fetchStart, p.End)
	if err != nil {
		return nil, fmt.Errorf("fetch %s klines: %w", p.Primary, err)
	}
	primary = closedOnly(primary)
	if len(primary) == 0 {
		return nil, fmt.Errorf("no closed %s klines in range", p.Primary)
	}

	mtf := useEntryTF(p.Strategy, p.Primary, p.Entry)
	var entry []types.Kline
	if mtf {
		entry, err = r.source.KlinesRange(ctx, p.Symbol, p.Entry, fetchStart, p.End)
		if err != nil {
			return nil, fmt.Errorf("fetch %s klines: %w", p.Entry, err)
		}
		entry = closedOnly(entry)
		if len(entry) == 0 {
			return nil, fmt.Errorf("no closed %s klines in range", p.Entry)
		}
	}

	clock := primary
	if mtf {
		clock = entry
	}

	s := &sim{
		cfg:    r.cfg,
		params: p,
		strat:  strat,
		paper:  portfolio.NewPaperAccount(p.Symbol, p.InitialBalance, r.cfg.Paper.FeeRate, r.cfg.Paper.SlippageBPS),
		risk:   risk.NewManager(r.cfg.Risk),
		fills:  make([]Fill, 0, 64),
		equity: make([]Point, 0, 256),
		rounds: make([]float64, 0, 32),
	}
	s.equity = append(s.equity, Point{
		TS:     p.Start.UTC().Format(time.RFC3339Nano),
		Equity: p.InitialBalance,
	})

	window := need
	for i := range clock {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bar := clock[i]
		if bar.CloseTime.Before(p.Start) {
			continue
		}
		primaryWin := windowUpTo(primary, bar.CloseTime, window)
		if len(primaryWin) < r.cfg.Strategy.MinBars {
			continue
		}
		var entryWin []types.Kline
		if mtf {
			entryWin = windowUpTo(entry, bar.CloseTime, window)
		}

		s.checkStop(bar)
		s.sync()

		mkt := strategy.MarketContext{
			Mark:  bar.Close,
			Entry: entryWin,
		}
		sig := s.strat.Evaluate(primaryWin, mkt)
		if err := s.apply(bar, sig); err != nil {
			return nil, err
		}
		s.markToMarket(bar)
	}

	if side, qty, _, _ := s.paper.Position(); qty > 0 {
		last := clock[len(clock)-1]
		action := types.ActionCloseLong
		if side == types.PosShort {
			action = types.ActionCloseShort
		}
		if err := s.closeAt(last.Close, last.CloseTime, action, "backtest end"); err != nil {
			return nil, err
		}
		s.markToMarket(last)
	}

	final := s.paper.WalletBalance()
	stats := computeStats(p.InitialBalance, final, s.equity, s.fills, s.rounds)
	return &Result{
		Strategy:       strat.Name(),
		Symbol:         p.Symbol,
		Primary:        p.Primary,
		Entry:          p.Entry,
		Start:          p.Start.UTC().Format(time.RFC3339),
		End:            p.End.UTC().Format(time.RFC3339),
		PrimaryBars:    countFrom(primary, p.Start),
		EntryBars:      countFrom(entry, p.Start),
		InitialBalance: p.InitialBalance,
		Stats:          stats,
		Equity:         downsamplePoints(s.equity, maxEquityPts),
		Price:          downsamplePrice(primary, p.Start, maxPriceBars),
		Fills:          s.fills,
	}, nil
}

func (r *Runner) normalize(p Params) (Params, error) {
	if p.Symbol == "" {
		p.Symbol = r.cfg.Symbol.Name
	}
	p.Symbol = strings.ToUpper(strings.TrimSpace(p.Symbol))
	if p.Strategy == "" {
		p.Strategy = r.cfg.Strategy.Name
	}
	if p.Primary == "" {
		p.Primary = r.cfg.Timeframes.Primary
	}
	if p.Entry == "" {
		p.Entry = r.cfg.Timeframes.Entry
	}
	if p.InitialBalance <= 0 {
		p.InitialBalance = r.cfg.Paper.InitialBalance
	}
	if p.InitialBalance <= 0 {
		p.InitialBalance = 10000
	}
	if p.End.IsZero() {
		p.End = time.Now().UTC()
	}
	if p.Start.IsZero() {
		p.Start = p.End.Add(-90 * 24 * time.Hour)
	}
	p.Start = p.Start.UTC()
	p.End = p.End.UTC()
	if !p.Start.Before(p.End) {
		return p, fmt.Errorf("start must be before end")
	}
	if p.End.Sub(p.Start) > maxRange {
		return p, fmt.Errorf("range longer than %d days", int(maxRange.Hours()/24))
	}
	if _, err := ParseInterval(p.Primary); err != nil {
		return p, fmt.Errorf("primary timeframe: %w", err)
	}
	if strings.TrimSpace(p.Entry) != "" {
		if _, err := ParseInterval(p.Entry); err != nil {
			return p, fmt.Errorf("entry timeframe: %w", err)
		}
	}
	return p, nil
}

func (s *sim) sync() {
	side, qty, entry, trail := s.paper.Position()
	st := strategy.PositionState{
		Long:     side == types.PosLong && qty > 0,
		Short:    side == types.PosShort && qty > 0,
		Entry:    entry,
		Trail:    trail,
		Quantity: qty,
	}
	if st.Flat() {
		s.trade = tradeContext{}
	} else {
		st.InitQty = s.trade.InitQty
		st.InitStop = s.trade.InitStop
		st.EntryTime = s.trade.EntryTime
		if st.InitQty <= 0 {
			st.InitQty = st.Quantity
		}
	}
	s.last = st
	s.strat.Sync(st)
}

func (s *sim) checkStop(bar types.Kline) {
	side, qty, _, trail := s.paper.Position()
	if qty <= 0 || trail <= 0 {
		return
	}
	hit := false
	px := trail
	var action types.SignalAction
	if side == types.PosLong && bar.Low <= trail {
		hit = true
		action = types.ActionCloseLong
		if bar.Open < trail {
			px = bar.Open
		}
	}
	if side == types.PosShort && bar.High >= trail {
		hit = true
		action = types.ActionCloseShort
		if bar.Open > trail {
			px = bar.Open
		}
	}
	if !hit {
		return
	}
	_ = s.closeAt(px, bar.CloseTime, action, fmt.Sprintf("stop %.2f hit", trail))
}

func (s *sim) apply(bar types.Kline, sig types.Signal) error {
	if sig.StopLoss > 0 {
		side, qty, _, _ := s.paper.Position()
		if qty > 0 && (sig.Action == types.ActionNone || sig.Action.IsReduce()) {
			if side == types.PosLong || side == types.PosShort {
				s.paper.SetTrail(sig.StopLoss)
			}
		}
	}
	if sig.Action == types.ActionNone {
		return nil
	}
	if err := risk.ValidateSignal(sig); err != nil {
		return nil
	}
	mark := bar.Close
	if sig.Price > 0 {
		mark = sig.Price
	}
	now := bar.CloseTime
	switch {
	case sig.Action.IsClose():
		return s.closeAt(mark, now, sig.Action, sig.Reason)
	case sig.Action.IsReduce():
		return s.reduceAt(mark, now, sig)
	case sig.Action.IsOpen():
		return s.openAt(mark, now, sig)
	}
	return nil
}

func (s *sim) openAt(mark float64, now time.Time, sig types.Signal) error {
	if _, qty, _, _ := s.paper.Position(); qty > 0 {
		return nil
	}
	equity := s.paper.Snapshot(mark).Balance
	qty, _, err := s.risk.SizePosition(now, equity, sig.Price, sig.StopLoss, s.cfg.Symbol.Leverage)
	if err != nil {
		return nil
	}
	var fill *types.TradeFill
	if sig.Action == types.ActionOpenLong {
		fill, err = s.paper.OpenLong(mark, qty, sig.Reason, now)
	} else {
		fill, err = s.paper.OpenShort(mark, qty, sig.Reason, now)
	}
	if err != nil {
		return err
	}
	s.paper.SetTrail(sig.StopLoss)
	s.trade = tradeContext{EntryTime: now, InitQty: qty, InitStop: sig.StopLoss}
	s.round = fill.PNL
	s.record(fill, sig.Action)
	s.sync()
	return nil
}

func (s *sim) reduceAt(mark float64, now time.Time, sig types.Signal) error {
	_, qty, _, _ := s.paper.Position()
	if qty <= 0 {
		return nil
	}
	part := s.risk.RoundQty(qty * sig.Portion)
	if part <= 0 || part >= qty {
		return nil
	}
	fill, err := s.paper.ClosePartial(mark, part, sig.Reason, now)
	if err != nil {
		return err
	}
	s.round += fill.PNL
	s.record(fill, sig.Action)
	s.sync()
	return nil
}

func (s *sim) closeAt(mark float64, now time.Time, action types.SignalAction, reason string) error {
	if _, qty, _, _ := s.paper.Position(); qty <= 0 {
		return nil
	}
	fill, err := s.paper.Close(mark, reason, now)
	if err != nil {
		return err
	}
	s.round += fill.PNL
	s.rounds = append(s.rounds, s.round)
	s.round = 0
	s.trade = tradeContext{}
	s.last = strategy.PositionState{}
	s.record(fill, action)
	s.sync()
	return nil
}

func (s *sim) record(fill *types.TradeFill, action types.SignalAction) {
	if fill == nil {
		return
	}
	eq := s.paper.Snapshot(fill.Price).Balance
	s.fills = append(s.fills, Fill{
		TS:       fill.Time.UTC().Format(time.RFC3339Nano),
		Side:     string(fill.Side),
		Action:   string(action),
		Quantity: fill.Quantity,
		Price:    fill.Price,
		Fee:      fill.Fee,
		PNL:      fill.PNL,
		Reason:   fill.Reason,
		Equity:   eq,
	})
	s.equity = append(s.equity, Point{
		TS:            fill.Time.UTC().Format(time.RFC3339Nano),
		Equity:        eq,
		CumulativePNL: eq - s.params.InitialBalance,
		TradePNL:      fill.PNL,
	})
}

func (s *sim) markToMarket(bar types.Kline) {
	eq := s.paper.Snapshot(bar.Close).Balance
	s.equity = append(s.equity, Point{
		TS:            bar.CloseTime.UTC().Format(time.RFC3339Nano),
		Equity:        eq,
		CumulativePNL: eq - s.params.InitialBalance,
	})
}

func vegasInterval(name, interval string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "vegas", "vegas_tunnel":
	default:
		return ""
	}
	return strings.TrimSpace(interval)
}

func useEntryTF(name, primary, entry string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "trend", "trend_follow":
	default:
		return false
	}
	entry = strings.TrimSpace(entry)
	return entry != "" && !strings.EqualFold(entry, primary)
}

func closedOnly(bars []types.Kline) []types.Kline {
	out := bars[:0]
	for _, b := range bars {
		if b.Closed {
			out = append(out, b)
		}
	}
	if out == nil {
		return []types.Kline{}
	}
	return out
}

func windowUpTo(bars []types.Kline, t time.Time, limit int) []types.Kline {
	j := -1
	for i := len(bars) - 1; i >= 0; i-- {
		if !bars[i].CloseTime.After(t) {
			j = i
			break
		}
	}
	if j < 0 {
		return nil
	}
	start := j - limit + 1
	if start < 0 {
		start = 0
	}
	return bars[start : j+1]
}

func countFrom(bars []types.Kline, start time.Time) int {
	n := 0
	for _, b := range bars {
		if !b.CloseTime.Before(start) {
			n++
		}
	}
	return n
}

func downsamplePoints(points []Point, maxN int) []Point {
	if maxN < 3 || len(points) <= maxN {
		return points
	}
	out := make([]Point, 0, maxN)
	out = append(out, points[0])
	step := float64(len(points)-1) / float64(maxN-1)
	for i := 1; i < maxN-1; i++ {
		idx := int(math.Round(float64(i) * step))
		if idx <= 0 {
			idx = 1
		}
		if idx >= len(points)-1 {
			idx = len(points) - 2
		}
		out = append(out, points[idx])
	}
	out = append(out, points[len(points)-1])
	return out
}

func downsamplePrice(bars []types.Kline, start time.Time, maxN int) []PriceBar {
	var src []types.Kline
	for _, b := range bars {
		if !b.CloseTime.Before(start) {
			src = append(src, b)
		}
	}
	if len(src) == 0 {
		return []PriceBar{}
	}
	pick := src
	if maxN >= 3 && len(src) > maxN {
		pick = make([]types.Kline, 0, maxN)
		pick = append(pick, src[0])
		step := float64(len(src)-1) / float64(maxN-1)
		for i := 1; i < maxN-1; i++ {
			idx := int(math.Round(float64(i) * step))
			if idx <= 0 {
				idx = 1
			}
			if idx >= len(src)-1 {
				idx = len(src) - 2
			}
			pick = append(pick, src[idx])
		}
		pick = append(pick, src[len(src)-1])
	}
	out := make([]PriceBar, len(pick))
	for i, b := range pick {
		out[i] = PriceBar{
			TS:    b.CloseTime.UTC().Format(time.RFC3339Nano),
			Open:  b.Open,
			High:  b.High,
			Low:   b.Low,
			Close: b.Close,
		}
	}
	return out
}
