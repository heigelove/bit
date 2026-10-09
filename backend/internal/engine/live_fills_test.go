package engine

import (
	"testing"

	"github.com/work/bit/internal/exchange/binance"
	"github.com/work/bit/internal/types"
)

func TestClosingUserTradesKeepsStopFills(t *testing.T) {
	fills := []binance.UserTrade{
		{ID: 1, Side: types.SideBuy, Quantity: 1.288, Price: 2702, RealizedPNL: 0},
		{ID: 2, Side: types.SideSell, Quantity: 1.288, Price: 2697, RealizedPNL: -6.44},
		{ID: 3, Side: types.SideBuy, Quantity: 0.5, Price: 2560, RealizedPNL: 0},
		{ID: 4, Side: types.SideSell, Quantity: 0.5, Price: 2560, RealizedPNL: 0}, // BE close
	}
	got := closingUserTrades(fills)
	if len(got) != 2 {
		t.Fatalf("closes=%d want 2", len(got))
	}
	if got[0].ID != 2 || got[1].ID != 4 {
		t.Fatalf("ids %d %d", got[0].ID, got[1].ID)
	}
}

func TestClosingUserTradesSkipsOpens(t *testing.T) {
	fills := []binance.UserTrade{
		{ID: 1, Side: types.SideBuy, Quantity: 1, RealizedPNL: 0},
		{ID: 2, Side: types.SideBuy, Quantity: 0.2, RealizedPNL: 0},
	}
	if got := closingUserTrades(fills); len(got) != 0 {
		t.Fatalf("expected no closes, got %d", len(got))
	}
}

func TestBinanceTradeID(t *testing.T) {
	if got := binanceTradeID(99); got != "bt-99" {
		t.Fatalf("got %s", got)
	}
}
