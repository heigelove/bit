package portfolio

import (
	"math"
	"testing"
	"time"
)

func TestClosePartialKeepsRemainder(t *testing.T) {
	p := NewPaperAccount("ETHUSDT", 10000, 0, 0)
	now := time.Now().UTC()

	if _, err := p.OpenLong(100, 2, "test", now); err != nil {
		t.Fatalf("open: %v", err)
	}
	fill, err := p.ClosePartial(110, 1, "tp1", now)
	if err != nil {
		t.Fatalf("close partial: %v", err)
	}
	if fill.Quantity != 1 {
		t.Fatalf("filled %v, want 1", fill.Quantity)
	}
	if math.Abs(fill.PNL-10) > 1e-9 {
		t.Fatalf("pnl %v, want 10", fill.PNL)
	}

	side, qty, entry, _ := p.Position()
	if qty != 1 {
		t.Fatalf("remaining qty %v, want 1", qty)
	}
	if entry != 100 {
		t.Fatalf("entry moved to %v, want 100", entry)
	}
	if side == "" {
		t.Fatal("position side cleared while size remains")
	}

	// The remainder still carries the full unrealized move.
	if got := p.Snapshot(110).Balance; math.Abs(got-10020) > 1e-9 {
		t.Fatalf("equity %v, want 10020", got)
	}
}

func TestClosePartialOversizeClosesAll(t *testing.T) {
	p := NewPaperAccount("ETHUSDT", 10000, 0, 0)
	now := time.Now().UTC()

	if _, err := p.OpenShort(100, 1, "test", now); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.ClosePartial(90, 5, "oversize", now); err != nil {
		t.Fatalf("close partial: %v", err)
	}
	if _, qty, _, _ := p.Position(); qty != 0 {
		t.Fatalf("expected flat, qty %v", qty)
	}
	if math.Abs(p.WalletBalance()-10010) > 1e-9 {
		t.Fatalf("balance %v, want 10010", p.WalletBalance())
	}
}
