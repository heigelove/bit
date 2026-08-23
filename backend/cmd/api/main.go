package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/work/bit/internal/api"
	"github.com/work/bit/internal/config"
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

	db, err := sqlite.Open(cfg.SQLite.Path)
	if err != nil {
		slog.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var rdb *redisx.Store
	rdb, err = redisx.Connect(ctx, redisx.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		Prefix:   cfg.Redis.Prefix,
		TTL:      cfg.Redis.TTL,
	})
	if err != nil {
		slog.Warn("redis unavailable, account endpoints will be empty", "err", err)
	} else {
		defer rdb.Close()
	}

	srv := api.New(cfg, db, rdb)
	addr := cfg.API.Listen
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Engine(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("api listening", "framework", "gin", "addr", addr, "mode", cfg.Mode, "symbol", cfg.Symbol.Name)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = httpSrv.Shutdown(shutdownCtx)
	fmt.Println("api stopped")
}
