package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode       string         `yaml:"mode"`
	Exchange   ExchangeConfig `yaml:"exchange"`
	Symbol     SymbolConfig   `yaml:"symbol"`
	Timeframes Timeframes     `yaml:"timeframes"`
	Strategy   StrategyConfig `yaml:"strategy"`
	Risk       RiskConfig     `yaml:"risk"`
	Engine     EngineConfig   `yaml:"engine"`
	Paper      PaperConfig    `yaml:"paper"`
	SQLite     SQLiteConfig   `yaml:"sqlite"`
	Redis      RedisConfig    `yaml:"redis"`
	API        APIConfig      `yaml:"api"`
}

type ExchangeConfig struct {
	RestBase string `yaml:"rest_base"`
	WSBase   string `yaml:"ws_base"`
	// APIKeyEnv / APISecretEnv are environment variable *names* (e.g. BINANCE_API_KEY).
	APIKeyEnv    string `yaml:"api_key_env"`
	APISecretEnv string `yaml:"api_secret_env"`
	// APIKey / APISecret may be set directly in yaml for local use; env wins when both are set.
	APIKey    string `yaml:"api_key"`
	APISecret string `yaml:"api_secret"`
}

type SymbolConfig struct {
	Name       string `yaml:"name"`
	Quote      string `yaml:"quote"`
	Leverage   int    `yaml:"leverage"`
	MarginType string `yaml:"margin_type"`
}

type Timeframes struct {
	Primary string `yaml:"primary"`
	Entry   string `yaml:"entry"`
}

type StrategyConfig struct {
	// Name selects the strategy implementation: "trend" or "squeeze".
	Name            string        `yaml:"name"`
	EMAFast         int           `yaml:"ema_fast"`
	EMASlow         int           `yaml:"ema_slow"`
	EMAFilter       int           `yaml:"ema_filter"`
	ATRPeriod       int           `yaml:"atr_period"`
	ADXPeriod       int           `yaml:"adx_period"`
	ADXMin          float64       `yaml:"adx_min"`
	ATRStopMult     float64       `yaml:"atr_stop_mult"`
	ATRTrailMult    float64       `yaml:"atr_trail_mult"`
	ChaseMaxATR     float64       `yaml:"chase_max_atr"`
	UseEMA200Filter bool          `yaml:"use_ema200_filter"`
	MinBars         int           `yaml:"min_bars"`

	// Chop filters for the trend strategy (0 / false = disabled).
	ADXRisingBars  int     `yaml:"adx_rising_bars"`  // require ADX > ADX[N bars ago]
	EMASepMinATR   float64 `yaml:"ema_sep_min_atr"`  // |EMA fast−slow| must exceed this × ATR
	EMASlopeBars   int     `yaml:"ema_slope_bars"`   // slow-EMA slope lookback
	EMASlopeMinATR float64 `yaml:"ema_slope_min_atr"` // |Δ slow EMA| over lookback ≥ this × ATR
	UseDIFilter    bool    `yaml:"use_di_filter"`    // long needs +DI > −DI (and vice versa)
	CrossADXBonus  float64 `yaml:"cross_adx_bonus"`  // fresh EMA cross needs ADX ≥ ADXMin + bonus

	Squeeze SqueezeConfig `yaml:"squeeze"`
}

// SqueezeConfig tunes the volatility-compression breakout strategy.
type SqueezeConfig struct {
	Donchian    int     `yaml:"donchian"`     // breakout channel length
	ATRLookback int     `yaml:"atr_lookback"` // window for the ATR% percentile
	ATRPctMax   float64 `yaml:"atr_pct_max"`  // only enter when ATR% rank <= this

	ATRStopMult    float64 `yaml:"atr_stop_mult"`    // initial stop distance in ATRs
	BreakoutMaxATR float64 `yaml:"breakout_max_atr"` // reject entries this far past the channel

	TrendEMA int `yaml:"trend_ema"` // directional bias filter length
	// Filters are pointers so an omitted key means "on": a zero-valued config
	// must not silently disable the gates the strategy depends on.
	UseTrendFilter *bool `yaml:"use_trend_filter"` // require breakout to align with the EMA

	ChandelierPeriod int     `yaml:"chandelier_period"` // trailing exit lookback
	ChandelierMult   float64 `yaml:"chandelier_mult"`   // trailing exit distance in ATRs

	TP1R              float64 `yaml:"tp1_r"`               // first take-profit, in R
	TP1Portion        float64 `yaml:"tp1_portion"`         // fraction closed at TP1
	BreakevenAfterTP1 bool    `yaml:"breakeven_after_tp1"` // ratchet stop to entry after TP1

	TimeStopBars int     `yaml:"time_stop_bars"`  // bail out if stalled this long
	TimeStopMinR float64 `yaml:"time_stop_min_r"` // progress required to survive the time stop

	UseFundingFilter *bool   `yaml:"use_funding_filter"` // block entries into a crowded side
	FundingAbsMax    float64 `yaml:"funding_abs_max"`    // per-interval funding rate threshold
}

func (s SqueezeConfig) TrendFilterEnabled() bool {
	return s.UseTrendFilter == nil || *s.UseTrendFilter
}

func (s SqueezeConfig) FundingFilterEnabled() bool {
	return s.UseFundingFilter == nil || *s.UseFundingFilter
}

// WithDefaults fills unset fields with sane values. Applied on load, and again
// by the strategy constructor so hand-built configs (tests) behave the same.
func (s SqueezeConfig) WithDefaults() SqueezeConfig {
	if s.Donchian <= 0 {
		s.Donchian = 20
	}
	if s.ATRLookback <= 0 {
		s.ATRLookback = 100
	}
	if s.ATRPctMax <= 0 {
		s.ATRPctMax = 0.2
	}
	if s.ATRStopMult <= 0 {
		s.ATRStopMult = 1.2
	}
	if s.BreakoutMaxATR <= 0 {
		s.BreakoutMaxATR = 1.0
	}
	if s.TrendEMA <= 0 {
		s.TrendEMA = 200
	}
	if s.ChandelierPeriod <= 0 {
		s.ChandelierPeriod = 22
	}
	if s.ChandelierMult <= 0 {
		s.ChandelierMult = 3.0
	}
	if s.TP1R <= 0 {
		s.TP1R = 1.0
	}
	if s.TP1Portion <= 0 {
		s.TP1Portion = 0.5
	}
	if s.TimeStopBars <= 0 {
		s.TimeStopBars = 12
	}
	if s.TimeStopMinR <= 0 {
		s.TimeStopMinR = 0.5
	}
	if s.FundingAbsMax <= 0 {
		s.FundingAbsMax = 0.0005
	}
	return s
}

type RiskConfig struct {
	RiskPerTrade       float64 `yaml:"risk_per_trade"`
	MaxTotalRisk       float64 `yaml:"max_total_risk"`
	MaxDailyLossR      float64 `yaml:"max_daily_loss_r"`
	MaxConsecutiveLoss int     `yaml:"max_consecutive_loss"`
	MinQty             float64 `yaml:"min_qty"`
	QtyPrecision       int     `yaml:"qty_precision"`
	PricePrecision     int     `yaml:"price_precision"`
	// MaxNotionalPct caps position notional at equity * leverage * this, so a
	// very tight stop cannot size into a near-liquidation position.
	MaxNotionalPct float64 `yaml:"max_notional_pct"`
}

type EngineConfig struct {
	PollInterval time.Duration `yaml:"-"`
	PollRaw      string        `yaml:"poll_interval"`
	KlineLimit   int           `yaml:"kline_limit"`
	LogDir       string        `yaml:"log_dir"`
}

type PaperConfig struct {
	InitialBalance float64 `yaml:"initial_balance"`
	FeeRate        float64 `yaml:"fee_rate"`
	SlippageBPS    float64 `yaml:"slippage_bps"`
}

type SQLiteConfig struct {
	Path string `yaml:"path"`
}

type RedisConfig struct {
	Addr     string        `yaml:"addr"`
	Password string        `yaml:"password"`
	DB       int           `yaml:"db"`
	Prefix   string        `yaml:"prefix"`
	TTLRaw   string        `yaml:"ttl"`
	TTL      time.Duration `yaml:"-"`
}

type APIConfig struct {
	Listen      string        `yaml:"listen"`
	Username    string        `yaml:"username"`
	Password    string        `yaml:"password"`
	JWTSecret   string        `yaml:"jwt_secret"`
	TokenTTLRaw string        `yaml:"token_ttl"`
	TokenTTL    time.Duration `yaml:"-"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Mode == "" {
		cfg.Mode = "paper"
	}
	if cfg.Strategy.Name == "" {
		cfg.Strategy.Name = "trend"
	}
	cfg.Strategy.Squeeze = cfg.Strategy.Squeeze.WithDefaults()
	if cfg.Risk.MaxNotionalPct <= 0 {
		cfg.Risk.MaxNotionalPct = 0.7
	}
	if cfg.Engine.PollRaw != "" {
		d, err := time.ParseDuration(cfg.Engine.PollRaw)
		if err != nil {
			return nil, fmt.Errorf("engine.poll_interval: %w", err)
		}
		cfg.Engine.PollInterval = d
	}
	if cfg.Engine.PollInterval == 0 {
		cfg.Engine.PollInterval = 10 * time.Second
	}
	if cfg.Engine.KlineLimit == 0 {
		cfg.Engine.KlineLimit = 500
	}
	if cfg.Engine.LogDir == "" {
		cfg.Engine.LogDir = "logs"
	}
	if cfg.SQLite.Path == "" {
		cfg.SQLite.Path = "data/bit.db"
	}
	if cfg.Redis.Addr == "" {
		cfg.Redis.Addr = "127.0.0.1:6379"
	}
	if cfg.Redis.Prefix == "" {
		cfg.Redis.Prefix = "bit"
	}
	if cfg.Redis.TTLRaw != "" {
		d, err := time.ParseDuration(cfg.Redis.TTLRaw)
		if err != nil {
			return nil, fmt.Errorf("redis.ttl: %w", err)
		}
		cfg.Redis.TTL = d
	}
	if cfg.Redis.TTL == 0 {
		cfg.Redis.TTL = 24 * time.Hour
	}
	if cfg.API.Listen == "" {
		cfg.API.Listen = ":8080"
	}
	if cfg.API.Username == "" {
		cfg.API.Username = "admin"
	}
	if cfg.API.Password == "" {
		cfg.API.Password = "admin123"
	}
	if cfg.API.JWTSecret == "" {
		cfg.API.JWTSecret = "bit-dev-secret-change-me"
	}
	if cfg.API.TokenTTLRaw != "" {
		d, err := time.ParseDuration(cfg.API.TokenTTLRaw)
		if err != nil {
			return nil, fmt.Errorf("api.token_ttl: %w", err)
		}
		cfg.API.TokenTTL = d
	}
	if cfg.API.TokenTTL == 0 {
		cfg.API.TokenTTL = 24 * time.Hour
	}
	if cfg.Exchange.APIKeyEnv == "" {
		cfg.Exchange.APIKeyEnv = "BINANCE_API_KEY"
	}
	if cfg.Exchange.APISecretEnv == "" {
		cfg.Exchange.APISecretEnv = "BINANCE_API_SECRET"
	}
	// Environment variables override yaml literals when present.
	if v := os.Getenv(cfg.Exchange.APIKeyEnv); v != "" {
		cfg.Exchange.APIKey = v
	}
	if v := os.Getenv(cfg.Exchange.APISecretEnv); v != "" {
		cfg.Exchange.APISecret = v
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Symbol.Name == "" {
		return fmt.Errorf("symbol.name is required")
	}
	if c.Mode != "paper" && c.Mode != "live" {
		return fmt.Errorf("mode must be paper or live")
	}
	if c.Mode == "live" && (c.Exchange.APIKey == "" || c.Exchange.APISecret == "") {
		return fmt.Errorf(
			"live mode requires API credentials: set env %s / %s, or put api_key / api_secret in config",
			c.Exchange.APIKeyEnv, c.Exchange.APISecretEnv,
		)
	}
	switch c.Strategy.Name {
	case "trend", "trend_follow":
		if c.Strategy.EMAFast >= c.Strategy.EMASlow {
			return fmt.Errorf("ema_fast must be < ema_slow")
		}
	case "squeeze", "squeeze_breakout":
		if c.Strategy.Squeeze.ATRPctMax > 1 {
			return fmt.Errorf("strategy.squeeze.atr_pct_max must be <= 1")
		}
		if c.Strategy.Squeeze.TP1Portion >= 1 {
			return fmt.Errorf("strategy.squeeze.tp1_portion must be < 1")
		}
	default:
		return fmt.Errorf("strategy.name %q must be trend or squeeze", c.Strategy.Name)
	}
	if c.Risk.MaxNotionalPct > 1 {
		return fmt.Errorf("risk.max_notional_pct must be <= 1")
	}
	if c.Risk.RiskPerTrade <= 0 || c.Risk.RiskPerTrade > 0.05 {
		return fmt.Errorf("risk_per_trade should be in (0, 0.05]")
	}
	if c.Symbol.Leverage < 1 || c.Symbol.Leverage > 125 {
		return fmt.Errorf("leverage out of range")
	}
	return nil
}

func (c *Config) IsPaper() bool { return c.Mode == "paper" }
