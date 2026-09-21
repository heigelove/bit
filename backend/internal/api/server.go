package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/store/redisx"
	"github.com/work/bit/internal/store/sqlite"
)

const ctxUsername = "username"

type Server struct {
	cfg    *config.Config
	db     *sqlite.Store
	redis  *redisx.Store
	engine *gin.Engine
}

func New(cfg *config.Config, db *sqlite.Store, rdb *redisx.Store) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), corsMiddleware())

	s := &Server{cfg: cfg, db: db, redis: rdb, engine: r}
	s.routes()
	return s
}

func (s *Server) Engine() *gin.Engine {
	return s.engine
}

func (s *Server) Handler() http.Handler {
	return s.engine
}

func (s *Server) routes() {
	api := s.engine.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		api.POST("/auth/login", s.handleLogin)

		auth := api.Group("")
		auth.Use(s.authMiddleware())
		{
			auth.GET("/auth/me", s.handleMe)
			auth.GET("/account", s.handleAccount)
			auth.GET("/equity-curve", s.handleEquityCurve)
			auth.GET("/logs", s.handleLogs)
			auth.GET("/trades", s.handleTrades)
			auth.GET("/signals", s.handleSignals)
		}
	}
}

func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	u := s.cfg.API.Username
	p := s.cfg.API.Password
	if u == "" {
		u = "admin"
	}
	if p == "" {
		p = "admin123"
	}
	if req.Username != u || req.Password != p {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	token, err := s.issueToken(req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"username":   req.Username,
		"expires_in": int(s.cfg.API.TokenTTL.Seconds()),
	})
}

func (s *Server) handleMe(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"username": c.GetString(ctxUsername)})
}

func (s *Server) handleAccount(c *gin.Context) {
	mode := c.DefaultQuery("mode", s.cfg.Mode)
	symbol := c.DefaultQuery("symbol", s.cfg.Symbol.Name)

	if s.redis == nil {
		c.JSON(http.StatusOK, gin.H{
			"mode":     mode,
			"symbol":   symbol,
			"account":  nil,
			"position": nil,
			"risk":     nil,
			"warning":  "redis unavailable",
		})
		return
	}

	acc, err := s.redis.GetAccount(c.Request.Context(), mode, symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pos, err := s.redis.GetPosition(c.Request.Context(), mode, symbol)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	risk, err := s.redis.GetRisk(c.Request.Context(), mode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mode":     mode,
		"symbol":   symbol,
		"account":  acc,
		"position": pos,
		"risk":     risk,
	})
}

func (s *Server) handleEquityCurve(c *gin.Context) {
	mode := c.DefaultQuery("mode", s.cfg.Mode)
	symbol := c.DefaultQuery("symbol", s.cfg.Symbol.Name)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))

	initial := 0.0
	if mode == "paper" {
		initial = s.cfg.Paper.InitialBalance
		if initial <= 0 {
			initial = 10000
		}
	}

	curve, err := s.db.ListEquityCurve(c.Request.Context(), symbol, mode, initial, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Live: shift curve so the last realized wallet matches Redis balance.
	if initial == 0 && s.redis != nil {
		if acc, err := s.redis.GetAccount(c.Request.Context(), mode, symbol); err == nil && acc != nil {
			shift := acc.Balance - curve.TotalPNL
			curve.InitialBalance = shift
			for i := range curve.Points {
				curve.Points[i].Equity += shift
			}
		}
	}

	if len(curve.Points) > 0 && curve.Points[0].TS == "" {
		if len(curve.Points) > 1 {
			curve.Points[0].TS = curve.Points[1].TS
		} else {
			curve.Points[0].TS = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}

	if s.redis != nil {
		if acc, err := s.redis.GetAccount(c.Request.Context(), mode, symbol); err == nil && acc != nil {
			last := curve.Points[len(curve.Points)-1]
			if math.Abs(acc.Equity-last.Equity) > 1e-6 {
				curve.Points = append(curve.Points, sqlite.EquityPoint{
					TS:            acc.UpdatedAt,
					Equity:        acc.Equity,
					CumulativePNL: acc.Equity - curve.InitialBalance,
					TradePNL:      0,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"mode":   mode,
		"symbol": symbol,
		"curve":  curve,
	})
}

func (s *Server) handleLogs(c *gin.Context) {
	page, size := pageParams(c)
	res, err := s.db.ListLogs(c.Request.Context(), page, size, c.Query("level"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *Server) handleTrades(c *gin.Context) {
	page, size := pageParams(c)
	res, err := s.db.ListTrades(c.Request.Context(), page, size, c.Query("symbol"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *Server) handleSignals(c *gin.Context) {
	page, size := pageParams(c)
	res, err := s.db.ListSignals(c.Request.Context(), page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		user, err := s.verifyToken(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(ctxUsername, user)
		c.Next()
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) issueToken(username string) (string, error) {
	exp := time.Now().Add(s.cfg.API.TokenTTL).Unix()
	payload := fmt.Sprintf("%s|%d", username, exp)
	mac := hmac.New(sha256.New, []byte(s.cfg.API.JWTSecret))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig, nil
}

func (s *Server) verifyToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("bad token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.API.JWTSecret))
	mac.Write(raw)
	expect := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expect), []byte(parts[1])) {
		return "", fmt.Errorf("bad sig")
	}
	fields := strings.Split(string(raw), "|")
	if len(fields) != 2 {
		return "", fmt.Errorf("bad payload")
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", fmt.Errorf("expired")
	}
	return fields[0], nil
}

func pageParams(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.Query("page"))
	size, _ = strconv.Atoi(c.Query("size"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	return
}
