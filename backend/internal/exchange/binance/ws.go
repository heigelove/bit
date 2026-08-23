package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/work/bit/internal/types"
)

// KlineStream streams closed/updated klines via combined stream.
type KlineStream struct {
	wsBase string
	conn   *websocket.Conn
}

func NewKlineStream(wsBase string) *KlineStream {
	return &KlineStream{wsBase: strings.TrimRight(wsBase, "/")}
}

// SubscribeKline connects and emits klines on ch until ctx done.
func (s *KlineStream) SubscribeKline(ctx context.Context, symbol, interval string, ch chan<- types.Kline) error {
	stream := fmt.Sprintf("%s@kline_%s", strings.ToLower(symbol), interval)
	u := s.wsBase + "/ws/" + url.PathEscape(stream)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, u, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	s.conn = conn

	go func() {
		defer close(ch)
		defer conn.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			k, err := parseKlineEvent(msg)
			if err != nil {
				continue
			}
			select {
			case ch <- k:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func parseKlineEvent(msg []byte) (types.Kline, error) {
	var ev struct {
		K struct {
			T  int64  `json:"t"`
			T2 int64  `json:"T"`
			O  string `json:"o"`
			H  string `json:"h"`
			L  string `json:"l"`
			C  string `json:"c"`
			V  string `json:"v"`
			X  bool   `json:"x"`
		} `json:"k"`
	}
	if err := json.Unmarshal(msg, &ev); err != nil {
		return types.Kline{}, err
	}
	o, _ := strconv.ParseFloat(ev.K.O, 64)
	h, _ := strconv.ParseFloat(ev.K.H, 64)
	l, _ := strconv.ParseFloat(ev.K.L, 64)
	c, _ := strconv.ParseFloat(ev.K.C, 64)
	v, _ := strconv.ParseFloat(ev.K.V, 64)
	return types.Kline{
		OpenTime:  time.UnixMilli(ev.K.T).UTC(),
		CloseTime: time.UnixMilli(ev.K.T2).UTC(),
		Open:      o,
		High:      h,
		Low:       l,
		Close:     c,
		Volume:    v,
		Closed:    ev.K.X,
	}, nil
}

func (s *KlineStream) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
