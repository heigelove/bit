package strategy

import (
	"fmt"
	"math"
	"time"

	"github.com/work/bit/internal/types"
)

// ReentryPersistent is implemented by strategies that remember the last exit
// so a stop-hunt back to the entry price cannot immediately re-open.
type ReentryPersistent interface {
	ReentryLock() ReentryLock
	RestoreReentryLock(ReentryLock)
}

// ReentryLock blocks repeating the same setup after a position is flattened.
//
// Armed is set when exposure goes from open → flat. A new same-side entry is
// then refused until the structure has reset, a cooldown of bars has elapsed,
// and price / the breakout level have moved away from the old entry.
type ReentryLock struct {
	Armed   bool
	Long    bool
	Entry   float64
	Edge    float64
	BarTime time.Time
	Reset   bool
}

// NoteEntry records the level of a trade we just opened, so a later stop can
// arm the lock against that same price.
func (l *ReentryLock) NoteEntry(long bool, price, edge float64) {
	l.Long = long
	l.Entry = price
	l.Edge = edge
	l.Armed = false
	l.Reset = false
	l.BarTime = time.Time{}
}

// ArmOnFlatten arms the lock when a live position disappears (strategy close
// or exchange stop). wasOpen / nowFlat are the Sync before/after states.
func (l *ReentryLock) ArmOnFlatten(was PositionState, now PositionState) {
	if was.Flat() || !now.Flat() {
		return
	}
	if l.Armed {
		return
	}
	l.Armed = true
	l.Reset = false
	l.BarTime = time.Time{}
	if l.Entry <= 0 {
		l.Entry = was.Entry
		l.Long = was.Long
	}
}

func (l *ReentryLock) Stamp(t time.Time) {
	if l.Armed && l.BarTime.IsZero() && !t.IsZero() {
		l.BarTime = t
	}
}

func (l *ReentryLock) MarkReset() {
	if l.Armed {
		l.Reset = true
	}
}

// Block returns a reason to skip a same-side entry, or empty if the setup is new.
func (l ReentryLock) Block(long bool, price, edge, atr, atrMult float64, cooldown, held int, requireReset bool) string {
	if !l.Armed || l.Long != long {
		return ""
	}
	if requireReset && !l.Reset {
		return "waiting setup reset after exit"
	}
	if cooldown > 0 && held < cooldown {
		return fmt.Sprintf("reentry cooldown %d/%d bars", held, cooldown)
	}
	if nearLevel(l, price, edge, atr, atrMult) {
		return "same level as last exit"
	}
	return ""
}

func nearLevel(l ReentryLock, price, edge, atr, atrMult float64) bool {
	if atr <= 0 || atrMult <= 0 {
		return false
	}
	lim := atrMult * atr
	if l.Entry > 0 && math.Abs(price-l.Entry) < lim {
		return true
	}
	if l.Edge > 0 && edge > 0 && math.Abs(edge-l.Edge) < lim {
		return true
	}
	return false
}

func barsOnOrAfter(bars []types.Kline, t time.Time) int {
	if t.IsZero() {
		return 0
	}
	n := 0
	for i := len(bars) - 1; i >= 0; i-- {
		if bars[i].CloseTime.Before(t) {
			break
		}
		n++
	}
	return n
}
