package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/exchange/binance"
	"github.com/work/bit/internal/portfolio"
	"github.com/work/bit/internal/risk"
	"github.com/work/bit/internal/store/redisx"
	"github.com/work/bit/internal/store/sqlite"
	"github.com/work/bit/internal/strategy"
	"github.com/work/bit/internal/types"
)

// tradeContext holds entry-time facts that neither the exchange nor the paper
// account reports back, so the strategy can keep managing a trade across
// restarts. Persisted alongside the position snapshot.
type tradeContext struct {
	EntryTime time.Time
	InitQty   float64
	InitStop  float64
}

// Engine orchestrates market data → strategy → risk → execution.
type Engine struct {
	cfg      *config.Config
	log      *slog.Logger
	client   *binance.Client
	strategy strategy.Strategy
	risk     *risk.Manager
	paper    *portfolio.PaperAccount
	db       *sqlite.Store
	redis    *redisx.Store

	lastSignalBar time.Time
	trade         tradeContext
	lastPos       strategy.PositionState
	// liveStop is the stop price currently resting on the exchange.
	liveStop float64
}

type Deps struct {
	DB    *sqlite.Store
	Redis *redisx.Store
}

func New(cfg *config.Config, log *slog.Logger, deps Deps) (*Engine, error) {
	strat, err := strategy.New(cfg.Strategy.Name, cfg.Symbol.Name, cfg.Strategy)
	if err != nil {
		return nil, err
	}
	client := binance.NewClient(cfg.Exchange.RestBase, cfg.Exchange.APIKey, cfg.Exchange.APISecret)
	e := &Engine{
		cfg:      cfg,
		log:      log,
		client:   client,
		strategy: strat,
		risk:     risk.NewManager(cfg.Risk),
		db:       deps.DB,
		redis:    deps.Redis,
	}
	if cfg.IsPaper() {
		e.paper = portfolio.NewPaperAccount(
			cfg.Symbol.Name,
			cfg.Paper.InitialBalance,
			cfg.Paper.FeeRate,
			cfg.Paper.SlippageBPS,
		)
	}
	return e, nil
}

func (e *Engine) Run(ctx context.Context) error {
	if err := e.client.Ping(ctx); err != nil {
		return fmt.Errorf("binance ping: %w", err)
	}
	e.log.Info("connected",
		"mode", e.cfg.Mode,
		"symbol", e.cfg.Symbol.Name,
		"tf", e.cfg.Timeframes.Primary,
		"strategy", e.strategy.Name(),
	)

	if err := e.restoreState(ctx); err != nil {
		e.log.Warn("restore state", "err", err)
	}

	if !e.cfg.IsPaper() {
		if err := e.client.SetMarginType(ctx, e.cfg.Symbol.Name, e.cfg.Symbol.MarginType); err != nil {
			e.log.Warn("set margin type", "err", err)
		}
		if err := e.client.SetLeverage(ctx, e.cfg.Symbol.Name, e.cfg.Symbol.Leverage); err != nil {
			return fmt.Errorf("set leverage: %w", err)
		}
	}

	ticker := time.NewTicker(e.cfg.Engine.PollInterval)
	defer ticker.Stop()

	if err := e.cycle(ctx); err != nil {
		e.log.Error("cycle", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			e.log.Info("shutting down")
			_ = e.persistAccount(ctx, 0)
			return ctx.Err()
		case <-ticker.C:
			if err := e.cycle(ctx); err != nil {
				e.log.Error("cycle", "err", err)
			}
		}
	}
}

func (e *Engine) restoreState(ctx context.Context) error {
	if e.redis == nil {
		return nil
	}
	if riskSnap, err := e.redis.GetRisk(ctx, e.cfg.Mode); err == nil && riskSnap != nil {
		e.risk.Restore(riskSnap.DayRealizedR, riskSnap.ConsecutiveLoss, riskSnap.Halted, riskSnap.HaltReason)
		e.log.Info("restored risk from redis",
			"day_r", riskSnap.DayRealizedR,
			"consecutive_loss", riskSnap.ConsecutiveLoss,
			"halted", riskSnap.Halted,
		)
	}

	pos, err := e.redis.GetPosition(ctx, e.cfg.Mode, e.cfg.Symbol.Name)
	if err != nil {
		return err
	}
	if pos != nil && pos.Quantity > 0 {
		e.trade = tradeContext{
			EntryTime: pos.EntryTimeValue(),
			InitQty:   pos.InitQuantity,
			InitStop:  pos.InitStop,
		}
		if e.trade.InitQty <= 0 {
			e.trade.InitQty = pos.Quantity
		}
		e.liveStop = pos.TrailStop
		e.log.Info("restored trade context",
			"entry_time", e.trade.EntryTime,
			"init_qty", e.trade.InitQty,
			"init_stop", e.trade.InitStop,
		)
	}

	if !e.cfg.IsPaper() || e.paper == nil {
		return nil
	}
	acc, err := e.redis.GetAccount(ctx, e.cfg.Mode, e.cfg.Symbol.Name)
	if err != nil {
		return err
	}
	if acc == nil && pos == nil {
		return nil
	}
	wallet := e.cfg.Paper.InitialBalance
	if acc != nil {
		wallet = acc.Balance
	}
	var side types.PositionSide
	var qty, entry, trail float64
	if pos != nil && pos.Quantity > 0 {
		side = types.PositionSide(pos.Side)
		qty = pos.Quantity
		entry = pos.EntryPrice
		trail = pos.TrailStop
	}
	e.paper.Restore(wallet, side, qty, entry, trail)
	e.log.Info("restored paper from redis", "wallet", wallet, "side", side, "qty", qty)
	return nil
}

func (e *Engine) cycle(ctx context.Context) error {
	klines, err := e.client.Klines(ctx, e.cfg.Symbol.Name, e.cfg.Timeframes.Primary, e.cfg.Engine.KlineLimit)
	if err != nil {
		return err
	}
	if len(klines) == 0 {
		return fmt.Errorf("no klines")
	}

	mkt := strategy.MarketContext{Mark: klines[len(klines)-1].Close}
	if pi, err := e.client.PremiumIndex(ctx, e.cfg.Symbol.Name); err == nil {
		mkt.Mark = pi.MarkPrice
		mkt.FundingRate = pi.LastFundingRate
		mkt.NextFundingTime = pi.NextFundingTime
		mkt.HasFunding = true
	} else {
		e.log.Warn("premium index", "err", err)
	}
	mark := mkt.Mark

	pos := e.syncStrategyPos(ctx)

	sig := e.strategy.Evaluate(klines, mkt)
	e.log.Info("signal", e.signalAttrs(sig)...)
	e.saveSignal(ctx, sig)

	// Keep the protective stop aligned with whatever the strategy last decided,
	// including on hold signals where the trail simply ratcheted. Skipped when
	// we are about to exit: the trail has crossed price by then, so the exchange
	// would reject a stop that triggers immediately.
	holding := sig.Action == types.ActionNone || sig.Action.IsReduce()
	if sig.StopLoss > 0 && !pos.Flat() && holding {
		if e.cfg.IsPaper() && e.paper != nil {
			e.paper.SetTrail(sig.StopLoss)
		} else {
			e.syncLiveStop(ctx, pos, sig.StopLoss, mark)
		}
	}
	_ = e.persistAccount(ctx, mark)

	if sig.Action == types.ActionNone {
		return nil
	}

	if !sig.Time.IsZero() && sig.Time.Equal(e.lastSignalBar) && sig.Action.IsOpen() {
		return nil
	}

	if err := risk.ValidateSignal(sig); err != nil {
		return err
	}

	equity := e.equity(ctx, mark)
	switch {
	case sig.Action.IsClose():
		if err := e.executeClose(ctx, mark, sig); err != nil {
			return err
		}
		e.lastSignalBar = sig.Time
	case sig.Action.IsReduce():
		if err := e.executeReduce(ctx, mark, sig); err != nil {
			return err
		}
	case sig.Action.IsOpen():
		if err := e.risk.AllowOpen(time.Now(), equity); err != nil {
			e.log.Warn("skip open", "err", err)
			return nil
		}
		if err := e.executeOpen(ctx, mark, equity, sig); err != nil {
			return err
		}
		e.lastSignalBar = sig.Time
	}
	return nil
}

// syncStrategyPos pushes current exposure into the strategy and returns it.
func (e *Engine) syncStrategyPos(ctx context.Context) strategy.PositionState {
	var st strategy.PositionState
	if e.cfg.IsPaper() {
		side, qty, entry, trail := e.paper.Position()
		st = strategy.PositionState{
			Long:     side == types.PosLong && qty > 0,
			Short:    side == types.PosShort && qty > 0,
			Entry:    entry,
			Trail:    trail,
			Quantity: qty,
		}
	} else {
		acc, err := e.client.Account(ctx)
		if err != nil {
			// Don't tell the strategy we are flat on a transient API error.
			e.log.Warn("account", "err", err)
			return e.lastPos
		}
		for _, p := range acc.Positions {
			if p.Symbol != e.cfg.Symbol.Name || p.Quantity == 0 {
				continue
			}
			st.Long = p.Side == types.PosLong
			st.Short = p.Side == types.PosShort
			st.Entry = p.EntryPrice
			st.Quantity = p.Quantity
		}
		st.Trail = e.liveStop
	}

	if st.Flat() {
		e.trade = tradeContext{}
		e.liveStop = 0
	} else {
		st.InitQty = e.trade.InitQty
		st.InitStop = e.trade.InitStop
		st.EntryTime = e.trade.EntryTime
		if st.InitQty <= 0 {
			st.InitQty = st.Quantity
		}
	}

	e.lastPos = st
	e.strategy.Sync(st)
	return st
}

// syncLiveStop moves the resting STOP_MARKET order to follow the strategy trail.
// Only ever tightens, never loosens. PlaceStopMarket arms a conditional close —
// it does not flatten immediately. If mark has already crossed the stop, we
// market-close instead of leaving a resting order that may never fire.
func (e *Engine) syncLiveStop(ctx context.Context, pos strategy.PositionState, stop, mark float64) {
	if e.cfg.IsPaper() || pos.Flat() || stop <= 0 {
		return
	}
	want := e.risk.RoundPrice(stop)
	if want <= 0 {
		return
	}

	breached := mark > 0 && ((pos.Long && mark <= want) || (pos.Short && mark >= want))
	if breached {
		e.log.Warn("stop breached by mark, market close", "stop", want, "mark", mark)
		sig := types.Signal{
			Action: types.ActionCloseLong,
			Reason: fmt.Sprintf("live stop %.2f breached (mark %.2f)", want, mark),
		}
		if pos.Short {
			sig.Action = types.ActionCloseShort
		}
		if err := e.executeClose(ctx, mark, sig); err != nil {
			e.log.Error("breach close", "err", err)
		}
		return
	}

	if e.liveStop > 0 {
		if pos.Long && want <= e.liveStop {
			return
		}
		if pos.Short && want >= e.liveStop {
			return
		}
	}

	side := types.SideSell
	if pos.Short {
		side = types.SideBuy
	}
	if err := e.client.CancelAll(ctx, e.cfg.Symbol.Name); err != nil {
		e.log.Warn("cancel stops before move", "err", err)
		return
	}
	res, err := e.client.PlaceStopMarket(ctx, types.OrderRequest{
		Symbol:    e.cfg.Symbol.Name,
		Side:      side,
		StopPrice: want,
		ClientID:  fmt.Sprintf("bit-sl-%d", time.Now().UnixMilli()),
	})
	if err != nil {
		// The old stop is already cancelled, so flag it for retry next cycle.
		e.liveStop = 0
		e.log.Error("re-place stop, position unprotected", "err", err, "stop", want)
		return
	}
	e.liveStop = want
	e.log.Info("stop moved", "stop", want, "algoId", res.OrderID, "status", res.Status)
}

func (e *Engine) equity(ctx context.Context, mark float64) float64 {
	if e.cfg.IsPaper() {
		return e.paper.Snapshot(mark).Balance
	}
	st, err := e.client.Account(ctx)
	if err != nil {
		return 0
	}
	return st.Balance
}

func (e *Engine) executeOpen(ctx context.Context, mark, equity float64, sig types.Signal) error {
	qty, riskAmt, err := e.risk.SizePosition(time.Now(), equity, sig.Price, sig.StopLoss, e.cfg.Symbol.Leverage)
	if err != nil {
		return err
	}
	e.log.Info("open", "action", sig.Action, "qty", qty, "risk", fmt.Sprintf("%.2f", riskAmt), "reason", sig.Reason)

	entryTime := sig.Time
	if entryTime.IsZero() {
		entryTime = time.Now().UTC()
	}

	if e.cfg.IsPaper() {
		var fill *types.TradeFill
		if sig.Action == types.ActionOpenLong {
			fill, err = e.paper.OpenLong(mark, qty, sig.Reason, time.Now().UTC())
		} else {
			fill, err = e.paper.OpenShort(mark, qty, sig.Reason, time.Now().UTC())
		}
		if err != nil {
			return err
		}
		e.paper.SetTrail(sig.StopLoss)
		e.trade = tradeContext{EntryTime: entryTime, InitQty: qty, InitStop: sig.StopLoss}
		e.recordFill(ctx, fill, "")
		_ = e.persistAccount(ctx, mark)
		e.persistRisk(ctx)
		return nil
	}

	side := types.SideBuy
	if sig.Action == types.ActionOpenShort {
		side = types.SideSell
	}
	res, err := e.client.PlaceMarketOrder(ctx, types.OrderRequest{
		Symbol:   e.cfg.Symbol.Name,
		Side:     side,
		Quantity: qty,
		ClientID: fmt.Sprintf("bit-o-%d", time.Now().UnixMilli()),
	})
	if err != nil {
		return err
	}
	e.log.Info("live order", "id", res.OrderID, "status", res.Status, "qty", res.Quantity, "px", res.Price)
	fillPx := res.Price
	if fillPx == 0 {
		fillPx = mark
	}
	fillQty := res.Quantity
	if fillQty == 0 {
		fillQty = qty
	}
	e.trade = tradeContext{EntryTime: entryTime, InitQty: fillQty, InitStop: sig.StopLoss}
	e.recordFill(ctx, &types.TradeFill{
		Time:     time.Now().UTC(),
		Symbol:   e.cfg.Symbol.Name,
		Side:     side,
		Quantity: fillQty,
		Price:    fillPx,
		Reason:   sig.Reason,
	}, fmt.Sprintf("%d", res.OrderID))

	stopSide := types.SideSell
	if sig.Action == types.ActionOpenShort {
		stopSide = types.SideBuy
	}
	stopPx := e.risk.RoundPrice(sig.StopLoss)
	if _, err := e.client.PlaceStopMarket(ctx, types.OrderRequest{
		Symbol:    e.cfg.Symbol.Name,
		Side:      stopSide,
		StopPrice: stopPx,
		ClientID:  fmt.Sprintf("bit-sl-%d", time.Now().UnixMilli()),
	}); err != nil {
		e.log.Error("place stop", "err", err)
	} else {
		e.liveStop = stopPx
	}
	_ = e.persistAccount(ctx, mark)
	e.persistRisk(ctx)
	return nil
}

// executeReduce takes part of the position off at a profit target. The stop
// order is left alone: it closes whatever remains when triggered.
func (e *Engine) executeReduce(ctx context.Context, mark float64, sig types.Signal) error {
	side, qty := e.openQty(ctx)
	if qty <= 0 {
		return nil
	}
	part := e.risk.RoundQty(qty * sig.Portion)
	if part <= 0 || part >= qty {
		e.log.Warn("skip reduce", "reason", "remainder would be untradeable", "qty", qty, "part", part)
		return nil
	}
	e.log.Info("reduce", "action", sig.Action, "qty", part, "of", qty, "reason", sig.Reason)

	if e.cfg.IsPaper() {
		fill, err := e.paper.ClosePartial(mark, part, sig.Reason, time.Now().UTC())
		if err != nil {
			return err
		}
		e.risk.RecordPartial(time.Now(), e.paper.Snapshot(mark).Balance, fill.PNL)
		e.recordFill(ctx, fill, "")
		_ = e.persistAccount(ctx, mark)
		e.persistRisk(ctx)
		return nil
	}

	closeSide := types.SideSell
	if side == types.PosShort {
		closeSide = types.SideBuy
	}
	res, err := e.client.PlaceMarketOrder(ctx, types.OrderRequest{
		Symbol:     e.cfg.Symbol.Name,
		Side:       closeSide,
		Quantity:   part,
		ReduceOnly: true,
		ClientID:   fmt.Sprintf("bit-r-%d", time.Now().UnixMilli()),
	})
	if err != nil {
		return err
	}
	fillPx := res.Price
	if fillPx == 0 {
		fillPx = mark
	}
	e.recordFill(ctx, &types.TradeFill{
		Time:     time.Now().UTC(),
		Symbol:   e.cfg.Symbol.Name,
		Side:     closeSide,
		Quantity: part,
		Price:    fillPx,
		Reason:   sig.Reason,
	}, fmt.Sprintf("%d", res.OrderID))
	_ = e.persistAccount(ctx, mark)
	e.persistRisk(ctx)
	return nil
}

func (e *Engine) executeClose(ctx context.Context, mark float64, sig types.Signal) error {
	e.log.Info("close", "action", sig.Action, "reason", sig.Reason)
	if e.cfg.IsPaper() {
		fill, err := e.paper.Close(mark, sig.Reason, time.Now().UTC())
		if err != nil {
			return err
		}
		e.trade = tradeContext{}
		e.risk.RecordClosedTrade(time.Now(), e.paper.Snapshot(mark).Balance, fill.PNL)
		e.recordFill(ctx, fill, "")
		_ = e.persistAccount(ctx, mark)
		e.persistRisk(ctx)
		return nil
	}

	side, qty := e.openQty(ctx)
	if qty <= 0 {
		e.log.Warn("close skipped, no position qty")
		return nil
	}
	closeSide := types.SideSell
	if side == types.PosShort {
		closeSide = types.SideBuy
	}
	_ = e.client.CancelAll(ctx, e.cfg.Symbol.Name)
	e.liveStop = 0
	res, err := e.client.PlaceMarketOrder(ctx, types.OrderRequest{
		Symbol:     e.cfg.Symbol.Name,
		Side:       closeSide,
		Quantity:   qty,
		ReduceOnly: true,
		ClientID:   fmt.Sprintf("bit-c-%d", time.Now().UnixMilli()),
	})
	if err != nil {
		return err
	}
	e.trade = tradeContext{}
	e.log.Info("live close", "id", res.OrderID, "status", res.Status, "qty", res.Quantity, "px", res.Price)
	fillPx := res.Price
	if fillPx == 0 {
		fillPx = mark
	}
	e.recordFill(ctx, &types.TradeFill{
		Time:     time.Now().UTC(),
		Symbol:   e.cfg.Symbol.Name,
		Side:     closeSide,
		Quantity: qty,
		Price:    fillPx,
		Reason:   sig.Reason,
	}, fmt.Sprintf("%d", res.OrderID))
	_ = e.persistAccount(ctx, mark)
	e.persistRisk(ctx)
	return nil
}

// openQty returns the side and size of the current position, 0 when flat.
func (e *Engine) openQty(ctx context.Context) (types.PositionSide, float64) {
	if e.cfg.IsPaper() {
		side, qty, _, _ := e.paper.Position()
		return side, qty
	}
	st, err := e.client.Account(ctx)
	if err != nil {
		e.log.Warn("account", "err", err)
		return "", 0
	}
	for _, p := range st.Positions {
		if p.Symbol == e.cfg.Symbol.Name && p.Quantity > 0 {
			return p.Side, p.Quantity
		}
	}
	return "", 0
}

func (e *Engine) signalAttrs(sig types.Signal) []any {
	attrs := []any{
		"strategy", e.strategy.Name(),
		"action", sig.Action,
		"reason", sig.Reason,
		"price", fmtF(sig.Price, 2),
		"atr", fmtF(sig.ATR, 2),
		"sl", fmtF(sig.StopLoss, 2),
	}
	if !math.IsNaN(sig.EMA20) && sig.EMA20 != 0 {
		attrs = append(attrs, "ema20", fmtF(sig.EMA20, 2), "ema60", fmtF(sig.EMA60, 2), "adx", fmtF(sig.ADX, 1))
	}
	if !math.IsNaN(sig.DonchianUp) && sig.DonchianUp != 0 {
		attrs = append(attrs,
			"dch_up", fmtF(sig.DonchianUp, 2),
			"dch_dn", fmtF(sig.DonchianDn, 2),
			"atr_pct", fmtF(sig.ATRPct*100, 3),
			"funding", fmtF(sig.Funding*100, 4),
		)
	}
	return attrs
}

func fmtF(v float64, prec int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "n/a"
	}
	return fmt.Sprintf("%.*f", prec, v)
}

func (e *Engine) saveSignal(ctx context.Context, sig types.Signal) {
	if e.db == nil {
		return
	}
	if err := e.db.InsertSignal(ctx, e.cfg.Mode, sig); err != nil {
		e.log.Warn("sqlite signal", "err", err)
	}
}

func (e *Engine) recordFill(ctx context.Context, fill *types.TradeFill, orderID string) {
	if fill == nil {
		return
	}
	e.log.Info("fill",
		"side", fill.Side,
		"qty", fill.Quantity,
		"px", fmtF(fill.Price, 2),
		"fee", fmtF(fill.Fee, 4),
		"pnl", fmtF(fill.PNL, 2),
		"reason", fill.Reason,
	)
	if e.db == nil {
		return
	}
	if err := e.db.InsertTrade(ctx, e.cfg.Mode, orderID, *fill); err != nil {
		e.log.Warn("sqlite trade", "err", err)
	}
}

func (e *Engine) persistAccount(ctx context.Context, mark float64) error {
	if e.redis == nil {
		return nil
	}
	var st types.AccountState
	trail := e.liveStop
	if e.cfg.IsPaper() && e.paper != nil {
		if mark <= 0 {
			if mp, err := e.client.MarkPrice(ctx, e.cfg.Symbol.Name); err == nil {
				mark = mp
			}
		}
		st = e.paper.Snapshot(mark)
		_, _, _, trail = e.paper.Position()
	} else {
		acc, err := e.client.Account(ctx)
		if err != nil {
			return err
		}
		st = *acc
	}
	if err := e.redis.SaveAccount(ctx, e.cfg.Mode, e.cfg.Symbol.Name, st); err != nil {
		e.log.Warn("redis account", "err", err)
		return err
	}
	if len(st.Positions) == 0 {
		_ = e.redis.ClearPosition(ctx, e.cfg.Mode, e.cfg.Symbol.Name)
		return nil
	}
	meta := redisx.PositionMeta{
		TrailStop:    trail,
		InitQuantity: e.trade.InitQty,
		InitStop:     e.trade.InitStop,
		EntryTime:    e.trade.EntryTime,
	}
	if err := e.redis.SavePosition(ctx, e.cfg.Mode, st.Positions[0], meta); err != nil {
		e.log.Warn("redis position", "err", err)
		return err
	}
	return nil
}

func (e *Engine) persistRisk(ctx context.Context) {
	if e.redis == nil {
		return
	}
	dayR, consecutive, halted, reason := e.risk.Snapshot()
	if err := e.redis.SaveRisk(ctx, e.cfg.Mode, dayR, consecutive, halted, reason); err != nil {
		e.log.Warn("redis risk", "err", err)
	}
}
