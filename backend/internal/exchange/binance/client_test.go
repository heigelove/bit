package binance

import (
	"testing"
	"time"

	"github.com/work/bit/internal/types"
)

func TestParseUserTrades(t *testing.T) {
	body := []byte(`[
	  {"symbol":"ETHUSDT","id":11,"orderId":22,"side":"SELL","price":"2697.10","qty":"1.288",
	   "realizedPnl":"-6.432","commission":"-0.139","time":1710000000000}
	]`)
	got, err := parseUserTrades(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	f := got[0]
	if f.ID != 11 || f.OrderID != 22 || f.Side != types.SideSell {
		t.Fatalf("ids/side %+v", f)
	}
	if f.Quantity != 1.288 || f.RealizedPNL != -6.432 || f.Commission != 0.139 {
		t.Fatalf("qty/pnl/fee %+v", f)
	}
	if !f.Time.Equal(time.UnixMilli(1710000000000).UTC()) {
		t.Fatalf("time %s", f.Time)
	}
}
