package strategy

import (
	"fmt"
	"math"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/indicators"
	"github.com/work/bit/internal/types"
)

// TrendFollow implements EMA20/60 + EMA200 filter + ATR stops + ADX/DMI chop gates.
//
// When the engine supplies a smaller entry timeframe (typically 15m) alongside
// the primary trend bars (typically 1h):
//   - primary: direction, chop gates, EMA200, 1h EMA cross, trail, trend-flip exit
//   - entry:   15m only aligns to 1h closes by default; pullbacks are 1h EMA20 wicks
// A still-bullish EMA20/60 stack does not authorize longs once the 1h close or
// the current price has lost 1h EMA20; that is the early-drop window where
// 15m pullbacks would otherwise keep buying.
type TrendFollow struct {
	cfg config.StrategyConfig
	tr  config.TrendConfig
	sym string

	// runtime trail state (caller may sync from portfolio)
	inLong        bool
	inShort       bool
	trailStop     float64
	entryPrice    float64
	initStop      float64
	swingLookback int
	lock          ReentryLock
}

func NewTrendFollow(sym string, cfg config.StrategyConfig) *TrendFollow {
	if cfg.ATRPeriod <= 0 {
		cfg.ATRPeriod = 14
	}
	return &TrendFollow{
		cfg:           cfg,
		tr:            cfg.Trend.WithDefaults(),
		sym:           sym,
		swingLookback: 10,
	}
}

func (s *TrendFollow) Name() string { return "trend" }

func (s *TrendFollow) Sync(pos PositionState) {
	was := PositionState{Long: s.inLong, Short: s.inShort, Entry: s.entryPrice}
	s.lock.ArmOnFlatten(was, pos)
	s.SyncPosition(pos.Long, pos.Short, pos.Entry, pos.Trail)
	s.initStop = pos.InitStop
}

func (s *TrendFollow) SyncPosition(long, short bool, entry, trail float64) {
	s.inLong = long
	s.inShort = short
	s.entryPrice = entry
	s.trailStop = trail
}

func (s *TrendFollow) ReentryLock() ReentryLock { return s.lock }

func (s *TrendFollow) RestoreReentryLock(l ReentryLock) { s.lock = l }

func (s *TrendFollow) entryMinBars() int {
	need := s.tr.EMASlow + 2
	if s.cfg.ATRPeriod+2 > need {
		need = s.cfg.ATRPeriod + 2
	}
	if s.swingLookback+2 > need {
		need = s.swingLookback + 2
	}
	return need
}

// Evaluate runs on closed bars. klines are the primary (higher) timeframe.
// mkt.Entry, when set, is the smaller timeframe used to time entries and trail.
func (s *TrendFollow) Evaluate(klines []types.Kline, mkt MarketContext) types.Signal {
	sig := types.Signal{Symbol: s.sym, Action: types.ActionNone}
	if len(klines) < s.cfg.MinBars {
		sig.Reason = fmt.Sprintf("warmup %d/%d", len(klines), s.cfg.MinBars)
		return sig
	}

	trend := closedBars(klines)
	if len(trend) < s.cfg.MinBars {
		sig.Reason = "waiting closed bar"
		return sig
	}

	entry := trend
	mtf := len(mkt.Entry) > 0
	if mtf {
		entry = closedBars(mkt.Entry)
		if len(entry) < s.entryMinBars() {
			sig.Reason = fmt.Sprintf("entry warmup %d/%d", len(entry), s.entryMinBars())
			return sig
		}
	}

	return s.evalBars(trend, entry, mtf)
}

func (s *TrendFollow) evalBars(trend, entry []types.Kline, mtf bool) types.Signal {
	sig := types.Signal{Symbol: s.sym, Action: types.ActionNone}

	th, tl, tc := ohlc(trend)
	eh, el, ec := ohlc(entry)

	tFast := indicators.EMA(tc, s.tr.EMAFast)
	tSlow := indicators.EMA(tc, s.tr.EMASlow)
	tFilter := indicators.EMA(tc, s.tr.EMAFilter)
	tATR := indicators.ATR(th, tl, tc, s.cfg.ATRPeriod)
	plusDI, minusDI, tADX := indicators.DMI(th, tl, tc, s.tr.ADXPeriod)

	eFast := indicators.EMA(ec, s.tr.EMAFast)
	eSlow := indicators.EMA(ec, s.tr.EMASlow)
	eATR := indicators.ATR(eh, el, ec, s.cfg.ATRPeriod)

	i := len(entry) - 1
	j, ok := lastIndexAtOrBefore(trend, entry[i].CloseTime)
	if !ok {
		sig.Reason = "waiting higher-tf bar"
		return sig
	}

	price := ec[i]
	sig.Time = entry[i].CloseTime
	sig.Price = price
	sig.EMA20 = tFast[j]
	sig.EMA60 = tSlow[j]
	sig.EMA200 = tFilter[j]
	sig.ATR = eATR[i]
	sig.ADX = tADX[j]

	if anyNaN(tFast[j], tSlow[j], eATR[i], tADX[j]) {
		sig.Reason = "indicator NaN"
		return sig
	}
	if s.tr.UseEMA200Filter && math.IsNaN(tFilter[j]) {
		sig.Reason = "ema200 warmup"
		return sig
	}

	bullTrend := tFast[j] > tSlow[j]
	bearTrend := tFast[j] < tSlow[j]
	if s.tr.UseEMA200Filter {
		bullTrend = bullTrend && tc[j] > tFilter[j] && price > tFilter[j]
		bearTrend = bearTrend && tc[j] < tFilter[j] && price < tFilter[j]
	}

	// Manage open position: trail on HTF ATR (when enabled), invalidate on 1h flip.
	trailATR := eATR[i]
	if mtf && s.tr.HTFATREnabled() && !math.IsNaN(tATR[j]) && tATR[j] > 0 {
		trailATR = tATR[j]
	}
	if s.inLong {
		s.trailStop = s.ratchetTrail(true, price, trailATR)
		sig.StopLoss = s.trailStop
		if price <= s.trailStop {
			sig.Action = types.ActionCloseLong
			sig.Reason = "trail stop hit"
			s.lock.ArmOnFlatten(PositionState{Long: true, Entry: s.entryPrice}, PositionState{})
			return sig
		}
		if tFast[j] < tSlow[j] {
			sig.Action = types.ActionCloseLong
			sig.Reason = "ema cross down"
			s.lock.ArmOnFlatten(PositionState{Long: true, Entry: s.entryPrice}, PositionState{})
			return sig
		}
		if s.tr.ExitOnEMA20LossEnabled() {
			if reason := s.ema20SideBlock(true, tc[j], tc[j], tFast[j], mtf); reason != "" {
				sig.Action = types.ActionCloseLong
				sig.Reason = reason
				s.lock.ArmOnFlatten(PositionState{Long: true, Entry: s.entryPrice}, PositionState{})
				return sig
			}
		}
		sig.Reason = "hold long"
		return sig
	}
	if s.inShort {
		s.trailStop = s.ratchetTrail(false, price, trailATR)
		sig.StopLoss = s.trailStop
		if price >= s.trailStop {
			sig.Action = types.ActionCloseShort
			sig.Reason = "trail stop hit"
			s.lock.ArmOnFlatten(PositionState{Short: true, Entry: s.entryPrice}, PositionState{})
			return sig
		}
		if tFast[j] > tSlow[j] {
			sig.Action = types.ActionCloseShort
			sig.Reason = "ema cross up"
			s.lock.ArmOnFlatten(PositionState{Short: true, Entry: s.entryPrice}, PositionState{})
			return sig
		}
		if s.tr.ExitOnEMA20LossEnabled() {
			if reason := s.ema20SideBlock(false, tc[j], tc[j], tFast[j], mtf); reason != "" {
				sig.Action = types.ActionCloseShort
				sig.Reason = reason
				s.lock.ArmOnFlatten(PositionState{Short: true, Entry: s.entryPrice}, PositionState{})
				return sig
			}
		}
		sig.Reason = "hold short"
		return sig
	}

	if anyNaN(eFast[i], eSlow[i]) {
		sig.Reason = "entry ema warmup"
		return sig
	}

	if s.lock.Armed {
		s.lock.Stamp(sig.Time)
		// A bar that does not even wick EMA20 means the old pullback is over.
		// On MTF also wait for the 1h close to reclaim / lose EMA20, otherwise
		// one 15m bounce resets the lock and the next dip re-enters the drop.
		htfReclaimed := !mtf || !s.tr.ReentryHTFResetRequired() || tc[j] > tFast[j]
		htfRejected := !mtf || !s.tr.ReentryHTFResetRequired() || tc[j] < tFast[j]
		if s.lock.Long && el[i] > eFast[i] && htfReclaimed {
			s.lock.MarkReset()
		}
		if !s.lock.Long && eh[i] < eFast[i] && htfRejected {
			s.lock.MarkReset()
		}
	}

	// Flat: chop / range gates on the higher TF before new entries.
	if reason := s.chopBlock(tADX, tFast, tSlow, tATR, j); reason != "" {
		if mtf {
			sig.Reason = "1h " + reason
		} else {
			sig.Reason = reason
		}
		return sig
	}

	// EMA20/60 lag: a fresh drop still looks like a bull pullback until the
	// slow pair crosses. Refuse new trades once price has lost the fast EMA.
	if tFast[j] > tSlow[j] {
		if reason := s.ema20SideBlock(true, tc[j], price, tFast[j], mtf); reason != "" {
			sig.Reason = reason
			return sig
		}
	}
	if tFast[j] < tSlow[j] {
		if reason := s.ema20SideBlock(false, tc[j], price, tFast[j], mtf); reason != "" {
			sig.Reason = reason
			return sig
		}
	}

	prev := i - 1
	crossedUp := false
	crossedDown := false
	if prev >= 0 && !anyNaN(eFast[prev], eSlow[prev]) {
		crossedUp = eFast[prev] <= eSlow[prev] && eFast[i] > eSlow[i]
		crossedDown = eFast[prev] >= eSlow[prev] && eFast[i] < eSlow[i]
	}
	pullbackLong := false
	pullbackShort := false
	if prev >= 0 {
		pullbackLong = bullTrend && freshEMAPullback(true, el[prev], eh[prev], eFast[prev], el[i], eh[i], price, eFast[i], eSlow[i])
		pullbackShort = bearTrend && freshEMAPullback(false, el[prev], eh[prev], eFast[prev], el[i], eh[i], price, eFast[i], eSlow[i])
	}
	if mtf && !s.tr.LTFPullbackAllowed() {
		pullbackLong = false
		pullbackShort = false
	}

	// Fresh 15m crosses are the noisiest setup; MTF defaults them off.
	if mtf && !s.tr.LTFCrossAllowed() {
		crossedUp = false
		crossedDown = false
	}
	crossADXFloor := s.tr.ADXMin + s.tr.CrossADXBonus
	if !mtf {
		if crossedUp && tADX[j] < crossADXFloor {
			crossedUp = false
		}
		if crossedDown && tADX[j] < crossADXFloor {
			crossedDown = false
		}
	}

	htfJustClosed := !mtf || entry[i].CloseTime.Equal(trend[j].CloseTime)
	htfCrossedUp := false
	htfCrossedDown := false
	if s.tr.HTFCrossAllowed() && htfJustClosed && j > 0 && !anyNaN(tFast[j-1], tSlow[j-1]) {
		htfCrossedUp = tFast[j-1] <= tSlow[j-1] && tFast[j] > tSlow[j] && tADX[j] >= crossADXFloor
		htfCrossedDown = tFast[j-1] >= tSlow[j-1] && tFast[j] < tSlow[j] && tADX[j] >= crossADXFloor
	}

	htfPullbackLong, htfPullbackShort := false, false
	if htfJustClosed {
		htfPullbackLong, htfPullbackShort = s.htfPullbackSetups(bullTrend, bearTrend, tl, th, tc, tFast, tSlow, tATR, j)
	}
	htfFlagLong := false
	htfFlagShort := false
	if htfJustClosed {
		htfFlagLong = bullTrend && s.htfFlagBreak(true, th, tl, tc, tFast, tATR, tADX[j], j)
		htfFlagShort = bearTrend && s.htfFlagBreak(false, th, tl, tc, tFast, tATR, tADX[j], j)
	}

	// Chase-distance only applies to 15m EMA crosses.
	distEMA := math.Abs(price - eFast[i])
	chasing := distEMA > s.tr.ChaseMaxATR*eATR[i]
	htfSetup := htfCrossedUp || htfCrossedDown || htfPullbackLong || htfPullbackShort || htfFlagLong || htfFlagShort
	if chasing && (crossedUp || crossedDown) && !pullbackLong && !pullbackShort && !htfSetup {
		sig.Reason = "too far from EMA20, no chase"
		return sig
	}
	if chasing {
		crossedUp = false
		crossedDown = false
	}

	swingLow := indicators.SwingLow(el, i, s.swingLookback)
	swingHigh := indicators.SwingHigh(eh, i, s.swingLookback)
	stopATRSrc := eATR[i]
	if mtf && s.tr.HTFATREnabled() && !math.IsNaN(tATR[j]) && tATR[j] > 0 {
		stopATRSrc = tATR[j]
		swingLow = indicators.SwingLow(tl, j, s.swingLookback)
		swingHigh = indicators.SwingHigh(th, j, s.swingLookback)
	}

	if bullTrend && (crossedUp || pullbackLong || htfCrossedUp || htfPullbackLong || htfFlagLong) {
		if reason := s.directionBlock(true, plusDI[j], minusDI[j], tSlow, tATR, j); reason != "" {
			if mtf {
				sig.Reason = "1h " + reason
			} else {
				sig.Reason = reason
			}
			return sig
		}
		if reason := s.htfPullbackBlock(price, tFast[j], tATR[j], tADX[j], mtf, htfCrossedUp || htfPullbackLong || htfFlagLong); reason != "" {
			sig.Reason = reason
			return sig
		}
		if !htfCrossedUp {
			confirm := entry[i]
			if htfPullbackLong || htfFlagLong {
				confirm = trend[j]
			}
			if reason := s.barConfirmBlock(confirm, true); reason != "" {
				sig.Reason = reason
				return sig
			}
		}
		stopATR := price - s.tr.ATRStopMult*stopATRSrc
		stop := stopATR
		if !math.IsNaN(swingLow) && swingLow < stop {
			stop = swingLow
		}
		stop, reason := s.applyMinStop(price, stop, tATR[j], true, mtf)
		if reason != "" {
			sig.Reason = reason
			return sig
		}
		if reason := s.reentryBlock(true, price, eFast[i], eATR[i], entry); reason != "" {
			sig.Reason = reason
			return sig
		}
		sig.Action = types.ActionOpenLong
		sig.StopLoss = stop
		s.trailStop = stop
		s.lock.NoteEntry(true, price, eFast[i])
		sig.Reason = s.entryReason(mtf, true, crossedUp, htfCrossedUp, htfPullbackLong, htfFlagLong)
		return sig
	}

	if bearTrend && (crossedDown || pullbackShort || htfCrossedDown || htfPullbackShort || htfFlagShort) {
		if reason := s.directionBlock(false, plusDI[j], minusDI[j], tSlow, tATR, j); reason != "" {
			if mtf {
				sig.Reason = "1h " + reason
			} else {
				sig.Reason = reason
			}
			return sig
		}
		if reason := s.htfPullbackBlock(price, tFast[j], tATR[j], tADX[j], mtf, htfCrossedDown || htfPullbackShort || htfFlagShort); reason != "" {
			sig.Reason = reason
			return sig
		}
		if !htfCrossedDown {
			confirm := entry[i]
			if htfPullbackShort || htfFlagShort {
				confirm = trend[j]
			}
			if reason := s.barConfirmBlock(confirm, false); reason != "" {
				sig.Reason = reason
				return sig
			}
		}
		stopATR := price + s.tr.ATRStopMult*stopATRSrc
		stop := stopATR
		if !math.IsNaN(swingHigh) && swingHigh > stop {
			stop = swingHigh
		}
		stop, reason := s.applyMinStop(price, stop, tATR[j], false, mtf)
		if reason != "" {
			sig.Reason = reason
			return sig
		}
		if reason := s.reentryBlock(false, price, eFast[i], eATR[i], entry); reason != "" {
			sig.Reason = reason
			return sig
		}
		sig.Action = types.ActionOpenShort
		sig.StopLoss = stop
		s.trailStop = stop
		s.lock.NoteEntry(false, price, eFast[i])
		sig.Reason = s.entryReason(mtf, false, crossedDown, htfCrossedDown, htfPullbackShort, htfFlagShort)
		return sig
	}

	sig.Reason = "no setup"
	return sig
}

// applyMinStop floors a stop at MinStopATR × higher-TF ATR. Default is to widen
// a tight 15m stop; explicit widen_min_stop: false still rejects the entry.
func (s *TrendFollow) applyMinStop(price, stop, trendATR float64, long, mtf bool) (float64, string) {
	if s.tr.MinStopATR <= 0 || trendATR <= 0 || math.IsNaN(trendATR) {
		return stop, ""
	}
	dist := math.Abs(price - stop)
	minDist := s.tr.MinStopATR * trendATR
	if dist+1e-9 >= minDist {
		return stop, ""
	}
	if s.tr.WidenMinStopEnabled() {
		if long {
			return price - minDist, ""
		}
		return price + minDist, ""
	}
	label := "ATR"
	if mtf {
		label = "1h ATR"
	}
	return stop, fmt.Sprintf("stop too tight %.2f < %.2f (%.2f×%s)", dist, minDist, s.tr.MinStopATR, label)
}

func (s *TrendFollow) htfPullbackBlock(price, htfEMA, htfATR, adx float64, mtf, skip bool) string {
	if skip || !mtf || htfATR <= 0 || math.IsNaN(htfATR) || math.IsNaN(htfEMA) {
		return ""
	}
	maxMult := s.tr.PullbackHTFMaxATR
	continuation := s.tr.ContinuationADX > 0 && adx >= s.tr.ContinuationADX
	if continuation {
		maxMult = s.tr.ContinuationHTFMaxATR
		if maxMult <= 0 {
			return ""
		}
	}
	if maxMult <= 0 {
		return ""
	}
	dist := math.Abs(price - htfEMA)
	max := maxMult * htfATR
	if dist > max {
		if continuation {
			return fmt.Sprintf("trend extended dist=%.2f > %.2f", dist, max)
		}
		return fmt.Sprintf("not a 1h pullback dist=%.2f > %.2f", dist, max)
	}
	return ""
}

func (s *TrendFollow) barConfirmBlock(bar types.Kline, long bool) string {
	if !s.tr.BarConfirmRequired() {
		return ""
	}
	rng := bar.High - bar.Low
	if rng <= 0 {
		return ""
	}
	frac := s.tr.CloseConfirmFrac
	if frac <= 0 {
		frac = 0.5
	}
	if long {
		if bar.Close < bar.Open {
			return "weak reclaim bar"
		}
		if (bar.Close-bar.Low)/rng < frac {
			return "weak reclaim close"
		}
		return ""
	}
	if bar.Close > bar.Open {
		return "weak reject bar"
	}
	if (bar.High-bar.Close)/rng < frac {
		return "weak reject close"
	}
	return ""
}

// ratchetTrail only tightens. Until TrailAfterR of progress, the initial stop
// stays put so 15m noise cannot walk a brand-new trade out.
func (s *TrendFollow) ratchetTrail(long bool, price, atr float64) float64 {
	current := s.trailStop
	init := s.initStop
	if init <= 0 {
		init = current
	}
	if s.tr.TrailAfterR > 0 && s.entryPrice > 0 {
		risk := math.Abs(s.entryPrice - init)
		if risk > 0 {
			progress := (price - s.entryPrice) / risk
			if !long {
				progress = (s.entryPrice - price) / risk
			}
			if progress < s.tr.TrailAfterR {
				if current > 0 {
					return current
				}
				return init
			}
		}
	}
	var candidate float64
	if long {
		candidate = price - s.tr.ATRTrailMult*atr
		if current == 0 || candidate > current {
			return candidate
		}
		return current
	}
	candidate = price + s.tr.ATRTrailMult*atr
	if current == 0 || candidate < current {
		return candidate
	}
	return current
}

func (s *TrendFollow) reentryBlock(long bool, price, edge, atr float64, bars []types.Kline) string {
	held := barsOnOrAfter(bars, s.lock.BarTime)
	return s.lock.Block(long, price, edge, atr, s.tr.ReentryATR, s.tr.ReentryCooldown, held, s.tr.ResetRequired())
}

func (s *TrendFollow) entryReason(mtf, long, ltfCross, htfCross, htfPullback, htfFlag bool) string {
	if htfCross {
		if long {
			return "1h EMA cross up"
		}
		return "1h EMA cross down"
	}
	if htfPullback {
		if long {
			return "1h pullback to EMA20 reclaim"
		}
		return "1h pullback to EMA20 reject"
	}
	if htfFlag {
		if long {
			return "1h flag breakout"
		}
		return "1h flag breakdown"
	}
	if mtf {
		if long {
			if ltfCross {
				return "15m EMA cross up + 1h trend"
			}
			return "15m pullback to EMA20 reclaim"
		}
		if ltfCross {
			return "15m EMA cross down + 1h trend"
		}
		return "15m pullback to EMA20 reject"
	}
	if long {
		if ltfCross {
			return "EMA cross up + trend filter"
		}
		return "pullback to EMA20 reclaim"
	}
	if ltfCross {
		return "EMA cross down + trend filter"
	}
	return "pullback to EMA20 reject"
}

// ema20SideBlock rejects (or, when in a trade, invalidates) a side once the
// last higher-TF close or the current price has lost the fast EMA.
func (s *TrendFollow) ema20SideBlock(long bool, htfClose, price, htfEMA20 float64, mtf bool) string {
	if !s.tr.EMA20SideRequired() {
		return ""
	}
	label := "EMA20"
	if mtf {
		label = "1h EMA20"
	}
	if long {
		if htfClose <= htfEMA20 {
			return fmt.Sprintf("close lost %s", label)
		}
		if price <= htfEMA20 {
			return fmt.Sprintf("price below %s", label)
		}
		return ""
	}
	if htfClose >= htfEMA20 {
		return fmt.Sprintf("close reclaimed %s", label)
	}
	if price >= htfEMA20 {
		return fmt.Sprintf("price above %s", label)
	}
	return ""
}

// chopBlock rejects entries when the market is ranging: weak/falling ADX or tangled EMAs.
func (s *TrendFollow) chopBlock(adx, emaFast, emaSlow, atr []float64, i int) string {
	if adx[i] < s.tr.ADXMin {
		return fmt.Sprintf("ADX %.1f < %.1f chop", adx[i], s.tr.ADXMin)
	}
	if n := s.tr.ADXRisingBars; n > 0 {
		if s.tr.ADXRisingExempt <= 0 || adx[i] < s.tr.ADXRisingExempt {
			j := i - n
			if j < 0 || math.IsNaN(adx[j]) {
				return "ADX rising warmup"
			}
			if adx[i] <= adx[j] {
				return fmt.Sprintf("ADX falling %.1f≤%.1f chop", adx[i], adx[j])
			}
		}
	}
	if s.tr.EMASepMinATR > 0 {
		sep := math.Abs(emaFast[i] - emaSlow[i])
		minSep := s.tr.EMASepMinATR * atr[i]
		if sep < minSep {
			return fmt.Sprintf("EMA tangled sep=%.3f < %.3f ATR", sep, minSep)
		}
	}
	return ""
}

// directionBlock confirms the trade side via +DI/−DI and slow-EMA slope.
func (s *TrendFollow) directionBlock(long bool, plusDI, minusDI float64, emaSlow, atr []float64, i int) string {
	if s.tr.UseDIFilter {
		if math.IsNaN(plusDI) || math.IsNaN(minusDI) {
			return "DI warmup"
		}
		if long && plusDI <= minusDI {
			return fmt.Sprintf("+DI %.1f ≤ −DI %.1f chop", plusDI, minusDI)
		}
		if !long && minusDI <= plusDI {
			return fmt.Sprintf("−DI %.1f ≤ +DI %.1f chop", minusDI, plusDI)
		}
	}
	if bars := s.tr.EMASlopeBars; bars > 0 && s.tr.EMASlopeMinATR > 0 {
		j := i - bars
		if j < 0 || math.IsNaN(emaSlow[j]) {
			return "EMA slope warmup"
		}
		slope := emaSlow[i] - emaSlow[j]
		minMove := s.tr.EMASlopeMinATR * atr[i]
		if long && slope < minMove {
			return fmt.Sprintf("EMA%d flat/down slope=%.3f chop", s.tr.EMASlow, slope)
		}
		if !long && slope > -minMove {
			return fmt.Sprintf("EMA%d flat/up slope=%.3f chop", s.tr.EMASlow, slope)
		}
	}
	return ""
}

// freshEMAPullback is a one-bar touch-and-reclaim of the fast EMA, not a state
// of sitting on the average. Previous bar must have stayed on the trend side.
func freshEMAPullback(long bool, prevLow, prevHigh, prevFast, low, high, close, fast, slow float64) bool {
	if anyNaN(prevFast, fast, slow) {
		return false
	}
	if long {
		if fast <= slow {
			return false
		}
		return prevLow > prevFast && low <= fast && close > fast
	}
	if fast >= slow {
		return false
	}
	return prevHigh < prevFast && high >= fast && close < fast
}

func (s *TrendFollow) htfPullbackSetups(bull, bear bool, tl, th, tc, tFast, tSlow, tATR []float64, j int) (long, short bool) {
	if !s.tr.HTFPullbackAllowed() || j < 1 {
		return
	}
	minBeyond := s.tr.HTFPullbackMinATR
	clear := func(isLong bool, close, ema, atr float64) bool {
		if minBeyond <= 0 || atr <= 0 {
			return true
		}
		if isLong {
			return close >= ema+minBeyond*atr
		}
		return close <= ema-minBeyond*atr
	}
	if s.tr.HTFPullbackConfirmEnabled() {
		if j < 2 {
			return
		}
		touchL := freshEMAPullback(true, tl[j-2], th[j-2], tFast[j-2], tl[j-1], th[j-1], tc[j-1], tFast[j-1], tSlow[j-1])
		touchS := freshEMAPullback(false, tl[j-2], th[j-2], tFast[j-2], tl[j-1], th[j-1], tc[j-1], tFast[j-1], tSlow[j-1])
		long = bull && touchL && tc[j] > tFast[j] && clear(true, tc[j], tFast[j], tATR[j])
		short = bear && touchS && tc[j] < tFast[j] && clear(false, tc[j], tFast[j], tATR[j])
		return
	}
	long = bull && freshEMAPullback(true, tl[j-1], th[j-1], tFast[j-1], tl[j], th[j], tc[j], tFast[j], tSlow[j]) && clear(true, tc[j], tFast[j], tATR[j])
	short = bear && freshEMAPullback(false, tl[j-1], th[j-1], tFast[j-1], tl[j], th[j], tc[j], tFast[j], tSlow[j]) && clear(false, tc[j], tFast[j], tATR[j])
	return
}

// htfFlagBreak is a contraction then a close through it. The bars before the
// signal must stay inside the bar that started the pause, and the whole pause
// must be tight, so a one-bar dip-and-rip does not count.
func (s *TrendFollow) htfFlagBreak(long bool, highs, lows, closes, emaFast, atr []float64, adx float64, j int) bool {
	if !s.tr.HTFFlagAllowed() || s.tr.ContinuationADX <= 0 || adx < s.tr.ContinuationADX {
		return false
	}
	pause := s.tr.FlagPauseBars
	if pause < 2 {
		pause = 2
	}
	// pause bars are [j-pause, j-1]; the anchor is the bar before them.
	anchor := j - pause - 1
	if anchor < 0 || atr[j] <= 0 || math.IsNaN(atr[j]) || math.IsNaN(emaFast[j]) || math.IsNaN(closes[j]) {
		return false
	}
	maxH, minL := highs[j-pause], lows[j-pause]
	for k := j - pause; k < j; k++ {
		if math.IsNaN(highs[k]) || math.IsNaN(lows[k]) {
			return false
		}
		if highs[k] > maxH {
			maxH = highs[k]
		}
		if lows[k] < minL {
			minL = lows[k]
		}
	}
	if maxR := s.tr.FlagMaxRangeATR; maxR > 0 && maxH-minL > maxR*atr[j] {
		return false
	}
	minBreak := s.tr.FlagMinBreakATR * atr[j]
	if long {
		if closes[j] <= emaFast[j] {
			return false
		}
		if maxExt := s.tr.FlagMaxExtATR; maxExt > 0 && closes[j]-emaFast[j] > maxExt*atr[j] {
			return false
		}
		// Pause must not have already taken out the anchor high.
		if maxH >= highs[anchor] {
			return false
		}
		return closes[j] >= maxH+minBreak
	}
	if closes[j] >= emaFast[j] {
		return false
	}
	if maxExt := s.tr.FlagMaxExtATR; maxExt > 0 && emaFast[j]-closes[j] > maxExt*atr[j] {
		return false
	}
	if minL <= lows[anchor] {
		return false
	}
	return closes[j] <= minL-minBreak
}
