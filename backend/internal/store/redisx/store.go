package redisx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/work/bit/internal/types"
)

// AccountSnapshot is cached account state in Redis.
type AccountSnapshot struct {
	Mode          string  `json:"mode"`
	Symbol        string  `json:"symbol"`
	Balance       float64 `json:"balance"`
	Available     float64 `json:"available"`
	UnrealizedPNL float64 `json:"unrealized_pnl"`
	Equity        float64 `json:"equity"`
	UpdatedAt     string  `json:"updated_at"`
}

// PositionSnapshot is cached position in Redis.
type PositionSnapshot struct {
	Mode       string  `json:"mode"`
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"`
	Quantity   float64 `json:"quantity"`
	EntryPrice float64 `json:"entry_price"`
	MarkPrice  float64 `json:"mark_price"`
	TrailStop  float64 `json:"trail_stop"`
	// Entry-time facts the strategy needs to resume managing the trade.
	InitQuantity float64 `json:"init_quantity"`
	InitStop     float64 `json:"init_stop"`
	EntryTime    string  `json:"entry_time"`
	UpdatedAt    string  `json:"updated_at"`
}

// PositionMeta is the entry-time context stored alongside a position.
type PositionMeta struct {
	TrailStop    float64
	InitQuantity float64
	InitStop     float64
	EntryTime    time.Time
}

// RiskSnapshot caches risk circuit-breaker state.
type RiskSnapshot struct {
	DayRealizedR    float64 `json:"day_realized_r"`
	ConsecutiveLoss int     `json:"consecutive_loss"`
	Halted          bool    `json:"halted"`
	HaltReason      string  `json:"halt_reason"`
	UpdatedAt       string  `json:"updated_at"`
}

// Store wraps go-redis for account / position / risk cache.
type Store struct {
	rdb    *redis.Client
	prefix string
	ttl    time.Duration
}

type Options struct {
	Addr     string
	Password string
	DB       int
	Prefix   string
	TTL      time.Duration
}

func Connect(ctx context.Context, opt Options) (*Store, error) {
	if opt.Addr == "" {
		opt.Addr = "127.0.0.1:6379"
	}
	if opt.Prefix == "" {
		opt.Prefix = "bit"
	}
	if opt.TTL == 0 {
		opt.TTL = 24 * time.Hour
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     opt.Addr,
		Password: opt.Password,
		DB:       opt.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Store{rdb: rdb, prefix: opt.Prefix, ttl: opt.TTL}, nil
}

func (s *Store) Close() error {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Close()
}

func (s *Store) accountKey(mode, symbol string) string {
	return fmt.Sprintf("%s:account:%s:%s", s.prefix, mode, symbol)
}

func (s *Store) positionKey(mode, symbol string) string {
	return fmt.Sprintf("%s:position:%s:%s", s.prefix, mode, symbol)
}

func (s *Store) riskKey(mode string) string {
	return fmt.Sprintf("%s:risk:%s", s.prefix, mode)
}

func (s *Store) reentryKey(mode, symbol string) string {
	return fmt.Sprintf("%s:reentry:%s:%s", s.prefix, mode, symbol)
}

func (s *Store) SaveAccount(ctx context.Context, mode, symbol string, st types.AccountState) error {
	upnl := 0.0
	for _, p := range st.Positions {
		upnl += p.UnrealizedPNL
	}
	equity := st.Balance
	wallet := equity - upnl
	snap := AccountSnapshot{
		Mode:          mode,
		Symbol:        symbol,
		Balance:       wallet,
		Available:     st.Available,
		UnrealizedPNL: upnl,
		Equity:        equity,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	key := s.accountKey(mode, symbol)
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, key, b, s.ttl)
	pipe.HSet(ctx, key+":h", map[string]any{
		"balance":        fmt.Sprintf("%.8f", snap.Balance),
		"available":      fmt.Sprintf("%.8f", snap.Available),
		"unrealized_pnl": fmt.Sprintf("%.8f", snap.UnrealizedPNL),
		"equity":         fmt.Sprintf("%.8f", snap.Equity),
		"updated_at":     snap.UpdatedAt,
		"mode":           mode,
		"symbol":         symbol,
	})
	pipe.Expire(ctx, key+":h", s.ttl)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Store) GetAccount(ctx context.Context, mode, symbol string) (*AccountSnapshot, error) {
	b, err := s.rdb.Get(ctx, s.accountKey(mode, symbol)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap AccountSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *Store) SavePosition(ctx context.Context, mode string, pos types.Position, meta PositionMeta) error {
	snap := PositionSnapshot{
		Mode:         mode,
		Symbol:       pos.Symbol,
		Side:         string(pos.Side),
		Quantity:     pos.Quantity,
		EntryPrice:   pos.EntryPrice,
		MarkPrice:    pos.MarkPrice,
		TrailStop:    meta.TrailStop,
		InitQuantity: meta.InitQuantity,
		InitStop:     meta.InitStop,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if !meta.EntryTime.IsZero() {
		snap.EntryTime = meta.EntryTime.UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	key := s.positionKey(mode, pos.Symbol)
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, key, b, s.ttl)
	pipe.HSet(ctx, key+":h", map[string]any{
		"side":          snap.Side,
		"quantity":      fmt.Sprintf("%.8f", snap.Quantity),
		"entry_price":   fmt.Sprintf("%.8f", snap.EntryPrice),
		"mark_price":    fmt.Sprintf("%.8f", snap.MarkPrice),
		"trail_stop":    fmt.Sprintf("%.8f", snap.TrailStop),
		"init_quantity": fmt.Sprintf("%.8f", snap.InitQuantity),
		"init_stop":     fmt.Sprintf("%.8f", snap.InitStop),
		"entry_time":    snap.EntryTime,
		"updated_at":    snap.UpdatedAt,
	})
	pipe.Expire(ctx, key+":h", s.ttl)
	_, err = pipe.Exec(ctx)
	return err
}

// EntryTimeValue parses the stored entry timestamp, zero when absent/invalid.
func (p *PositionSnapshot) EntryTimeValue() time.Time {
	if p == nil || p.EntryTime == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, p.EntryTime)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func (s *Store) ClearPosition(ctx context.Context, mode, symbol string) error {
	key := s.positionKey(mode, symbol)
	return s.rdb.Del(ctx, key, key+":h").Err()
}

func (s *Store) GetPosition(ctx context.Context, mode, symbol string) (*PositionSnapshot, error) {
	b, err := s.rdb.Get(ctx, s.positionKey(mode, symbol)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap PositionSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *Store) SaveRisk(ctx context.Context, mode string, dayR float64, consecutive int, halted bool, reason string) error {
	snap := RiskSnapshot{
		DayRealizedR:    dayR,
		ConsecutiveLoss: consecutive,
		Halted:          halted,
		HaltReason:      reason,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.riskKey(mode), b, s.ttl).Err()
}

func (s *Store) GetRisk(ctx context.Context, mode string) (*RiskSnapshot, error) {
	b, err := s.rdb.Get(ctx, s.riskKey(mode)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap RiskSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *Store) GetEquityHint(ctx context.Context, mode, symbol string) (float64, bool) {
	v, err := s.rdb.HGet(ctx, s.accountKey(mode, symbol)+":h", "equity").Result()
	if err != nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func (s *Store) Client() *redis.Client { return s.rdb }

// ReentrySnapshot caches the last-exit lock so a restart cannot re-open the
// same stop-hunted level.
type ReentrySnapshot struct {
	Armed   bool    `json:"armed"`
	Long    bool    `json:"long"`
	Entry   float64 `json:"entry"`
	Edge    float64 `json:"edge"`
	BarTime string  `json:"bar_time"`
	Reset   bool    `json:"reset"`
}

func (s *Store) SaveReentry(ctx context.Context, mode, symbol string, snap ReentrySnapshot) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, s.reentryKey(mode, symbol), b, s.ttl).Err()
}

func (s *Store) GetReentry(ctx context.Context, mode, symbol string) (*ReentrySnapshot, error) {
	b, err := s.rdb.Get(ctx, s.reentryKey(mode, symbol)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap ReentrySnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *Store) ClearReentry(ctx context.Context, mode, symbol string) error {
	return s.rdb.Del(ctx, s.reentryKey(mode, symbol)).Err()
}
