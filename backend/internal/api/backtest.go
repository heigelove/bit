package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/work/bit/internal/backtest"
	"github.com/work/bit/internal/exchange/binance"
)

var backtestMu sync.Mutex

func (s *Server) handleBacktestDefaults(c *gin.Context) {
	bal := s.cfg.Paper.InitialBalance
	if bal <= 0 {
		bal = 10000
	}
	c.JSON(http.StatusOK, gin.H{
		"symbol":          s.cfg.Symbol.Name,
		"strategy":        s.cfg.Strategy.Name,
		"strategies":      []string{"trend", "squeeze"},
		"primary":         s.cfg.Timeframes.Primary,
		"entry":           s.cfg.Timeframes.Entry,
		"initial_balance": bal,
		"fee_rate":        s.cfg.Paper.FeeRate,
		"slippage_bps":    s.cfg.Paper.SlippageBPS,
		"min_bars":        s.cfg.Strategy.MinBars,
		"leverage":        s.cfg.Symbol.Leverage,
	})
}

type backtestBody struct {
	Strategy       string  `json:"strategy"`
	Symbol         string  `json:"symbol"`
	Primary        string  `json:"primary"`
	Entry          string  `json:"entry"`
	Start          string  `json:"start"`
	End            string  `json:"end"`
	InitialBalance float64 `json:"initial_balance"`
}

func (s *Server) handleBacktest(c *gin.Context) {
	if !backtestMu.TryLock() {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "已有回测在运行，请稍后再试"})
		return
	}
	defer backtestMu.Unlock()

	var body backtestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	start, err := parseTime(body.Start)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start time"})
		return
	}
	end, err := parseTime(body.End)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end time"})
		return
	}

	client := binance.NewClient(s.cfg.Exchange.RestBase, "", "")
	runner := backtest.NewRunner(s.cfg, client)
	res, err := runner.Run(c.Request.Context(), backtest.Params{
		Strategy:       body.Strategy,
		Symbol:         body.Symbol,
		Primary:        body.Primary,
		Entry:          body.Entry,
		Start:          start,
		End:            end,
		InitialBalance: body.InitialBalance,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	var last error
	for _, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t.UTC(), nil
		}
		last = err
	}
	return time.Time{}, last
}
