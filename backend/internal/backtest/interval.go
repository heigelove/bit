package backtest

import (
	"fmt"
	"strings"
	"time"
)

// ParseInterval converts a Binance interval (15m, 1h, 1d, 1w, 1M) to a duration.
// 1M is approximated as 30 days and is only used for warmup lookback.
func ParseInterval(interval string) (time.Duration, error) {
	s := strings.TrimSpace(interval)
	if s == "" {
		return 0, fmt.Errorf("empty interval")
	}
	if s == "1M" {
		return 30 * 24 * time.Hour, nil
	}
	n := 0
	unit := ""
	for i, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
			continue
		}
		unit = s[i:]
		break
	}
	if n <= 0 || unit == "" {
		return 0, fmt.Errorf("invalid interval %q", interval)
	}
	switch unit {
	case "m":
		return time.Duration(n) * time.Minute, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid interval %q", interval)
	}
}
