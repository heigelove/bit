package strategy

import (
	"testing"
	"time"
)

func TestReentryLockBlocksSameLevelUntilReset(t *testing.T) {
	var l ReentryLock
	l.NoteEntry(true, 100, 99.5)
	l.ArmOnFlatten(PositionState{Long: true, Entry: 100}, PositionState{})
	if !l.Armed {
		t.Fatal("expected lock to arm on flatten")
	}

	if got := l.Block(true, 100.1, 99.5, 2, 0.25, 2, 1, true); got != "waiting setup reset after exit" {
		t.Fatalf("before reset: got %q", got)
	}

	l.MarkReset()
	if got := l.Block(true, 100.1, 99.5, 2, 0.25, 2, 1, true); got != "reentry cooldown 1/2 bars" {
		t.Fatalf("cooldown: got %q", got)
	}
	if got := l.Block(true, 100.1, 99.5, 2, 0.25, 2, 2, true); got != "same level as last exit" {
		t.Fatalf("same level: got %q", got)
	}

	if got := l.Block(false, 100.1, 99.5, 2, 0.25, 2, 2, true); got != "" {
		t.Fatalf("short should be allowed, got %q", got)
	}

	if got := l.Block(true, 108, 107, 2, 0.25, 2, 2, true); got != "" {
		t.Fatalf("new level should be allowed, got %q", got)
	}
}

func TestReentryLockDoesNotReArm(t *testing.T) {
	var l ReentryLock
	l.NoteEntry(true, 100, 99)
	l.ArmOnFlatten(PositionState{Long: true, Entry: 100}, PositionState{})
	l.MarkReset()
	l.Stamp(time.Unix(1, 0).UTC())
	l.ArmOnFlatten(PositionState{Long: true, Entry: 100}, PositionState{})
	if !l.Reset {
		t.Fatal("second flatten must not clear an already-armed reset")
	}
	if l.BarTime.IsZero() {
		t.Fatal("second flatten must not clear the stamped bar time")
	}
}
