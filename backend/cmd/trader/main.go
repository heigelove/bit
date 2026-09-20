package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/work/bit/internal/config"
	"github.com/work/bit/internal/engine"
	"github.com/work/bit/internal/store/redisx"
	"github.com/work/bit/internal/store/sqlite"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config yaml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	_ = os.MkdirAll(cfg.Engine.LogDir, 0o755)

	db, err := sqlite.Open(cfg.SQLite.Path)
	if err != nil {
		slog.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rdb, err := redisx.Connect(ctx, redisx.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		Prefix:   cfg.Redis.Prefix,
		TTL:      cfg.Redis.TTL,
	})
	if err != nil {
		slog.Error("connect redis", "err", err, "hint", "start redis or fix redis.addr in config")
		os.Exit(1)
	}
	defer rdb.Close()

	stdout := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	sqlHandler := sqlite.NewSlogHandler(db, slog.LevelInfo)
	log := slog.New(&teeHandler{primary: stdout, secondary: sqlHandler})

	log.Info("bit trader starting",
		"mode", cfg.Mode,
		"symbol", cfg.Symbol.Name,
		"strategy", cfg.Strategy.Name,
		"primary_tf", cfg.Timeframes.Primary,
		"entry_tf", cfg.Timeframes.Entry,
		"leverage", cfg.Symbol.Leverage,
		"sqlite", cfg.SQLite.Path,
		"redis", cfg.Redis.Addr,
	)

	eng, err := engine.New(cfg, log, engine.Deps{DB: db, Redis: rdb})
	if err != nil {
		log.Error("build engine", "err", err)
		os.Exit(1)
	}
	if err := eng.Run(ctx); err != nil && err != context.Canceled {
		log.Error("engine stopped", "err", err)
		os.Exit(1)
	}
	time.Sleep(50 * time.Millisecond)
}

type teeHandler struct {
	primary   slog.Handler
	secondary slog.Handler
}

func (t *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return t.primary.Enabled(ctx, level)
}

func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	_ = t.secondary.Handle(ctx, r.Clone())
	return t.primary.Handle(ctx, r)
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{
		primary:   t.primary.WithAttrs(attrs),
		secondary: t.secondary.WithAttrs(attrs),
	}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{
		primary:   t.primary.WithGroup(name),
		secondary: t.secondary.WithGroup(name),
	}
}
