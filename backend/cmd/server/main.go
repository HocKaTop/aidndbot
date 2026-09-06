package main

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/bot"
	"dnd-bot/backend/internal/server"
	"dnd-bot/backend/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if e := run(); e != nil {
		slog.Error("startup failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	cfg, e := server.LoadConfig()
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, e := pgxpool.New(ctx, cfg.DatabaseURL)
	if e != nil {
		return e
	}
	defer pool.Close()
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	e = migrations.Run(initCtx, pool)
	cancel()
	if e != nil {
		return e
	}
	s := &server.Server{Config: cfg, Pool: pool, AI: ai.New(cfg.OllamaURL)}
	s.Hub = server.NewHub(ctx, s)
	if cfg.BotEnabled {
		b := &bot.Bot{Token: cfg.BotToken, AppURL: cfg.AppURL, Handle: s.TextMessage}
		go b.Run(ctx)
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: s.Router(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errs := make(chan error, 1)
	go func() { slog.Info("server listening", "addr", cfg.Addr); errs <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case e := <-errs:
		return e
	}
}
