package binance

import (
	"testing"
	"time"

	"github.com/work/bit/internal/types"
)

func TestParseKlineRows(t *testing.T) {
	body := []byte(`[[1710000000000,"2500.0","2510.0","2490.0","2505.5","12.3",1710003599999]]`)
	got, err := parseKlineRows(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	k := got[0]
	if k.Open != 2500 || k.High != 2510 || k.Low != 2490 || k.Close != 2505.5 || k.Volume != 12.3 {
		t.Fatalf("ohlcv %+v", k)
	}
	if !k.Closed {
		t.Fatal("historical row should be closed")
	}
	if !k.OpenTime.Equal(time.UnixMilli(1710000000000).UTC()) {
		t.Fatalf("open %s", k.OpenTime)
	}
}

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
