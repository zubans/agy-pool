// Команда agy-pool — сервер пула аккаунтов Antigravity CLI и шлюза к моделям.
//
// Подкоманды:
//
//	agy-pool [serve]                 запустить сервер (по умолчанию)
//	agy-pool migrate [up|down|status] миграции БД (по умолчанию up)
//	agy-pool healthcheck             проверить /healthz запущенного сервера (для HEALTHCHECK образа)
//	agy-pool version                 вывести версию
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zubans/agy-pool/internal/config"
	"github.com/zubans/agy-pool/internal/db"
	"github.com/zubans/agy-pool/internal/db/sqlc"
	"github.com/zubans/agy-pool/internal/httpapi"
	"github.com/zubans/agy-pool/internal/logging"
	"github.com/zubans/agy-pool/internal/natsx"
	"github.com/zubans/agy-pool/internal/version"
)

const (
	// dbConnectTimeout — сколько ждать PostgreSQL при старте.
	dbConnectTimeout = 30 * time.Second
	// shutdownTimeout — сколько ждать завершения текущих запросов при остановке.
	shutdownTimeout = 30 * time.Second
	// writeTimeoutReserve — запас к AGY_TIMEOUT_MAX на запись ответа.
	writeTimeoutReserve = 30 * time.Second
)

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		sub := db.MigrateUp
		if len(args) > 0 {
			sub = args[0]
		}
		err = migrate(sub)
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version.Version)
	default:
		err = fmt.Errorf("unknown command %q (want serve, migrate, healthcheck or version)", cmd)
	}

	if err != nil {
		slog.Error("agy-pool failed", "command", cmd, "error", err)
		os.Exit(1)
	}
}

func setup() (config.Config, error) {
	// До чтения настроек пишем в JSON с уровнем info, чтобы ошибка конфигурации тоже была в JSON.
	if _, err := logging.New("info"); err != nil {
		return config.Config{}, err
	}

	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, err
	}

	if _, err := logging.New(cfg.LogLevel); err != nil {
		return config.Config{}, err
	}

	return cfg, nil
}

func serve() error {
	cfg, err := setup()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("starting agy-pool", "version", version.Version, "addr", cfg.HTTPAddr, "timezone", cfg.Timezone)

	for _, dir := range []string{cfg.AccountsDir(), cfg.SkillsDir(), cfg.WorkDir()} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			slog.Warn("cannot create data directory", "dir", dir, "error", err)
		}
	}

	openCtx, cancel := context.WithTimeout(ctx, dbConnectTimeout)
	pool, err := db.Open(openCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, db.MigrateUp); err != nil {
		return err
	}

	nc, js, err := natsx.Connect(cfg.NATSURL, "agy-pool")
	if err != nil {
		return err
	}
	defer func() {
		if err := natsx.Drain(nc); err != nil {
			slog.Warn("nats drain failed", "error", err)
		}
	}()

	metrics := httpapi.NewMetrics()
	health := httpapi.NewHealth(httpapi.DefaultCheckTimeout, metrics,
		httpapi.PostgresChecker{Queries: sqlc.New(pool)},
		httpapi.NATSChecker{Conn: nc, JetStream: js},
	)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(httpapi.Deps{Health: health, Metrics: metrics}),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      cfg.TimeoutMax + writeTimeoutReserve,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	slog.Info("stopped")

	return nil
}

func migrate(cmd string) error {
	cfg, err := setup()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, dbConnectTimeout)
	pool, err := db.Open(openCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()

	return db.Migrate(ctx, pool, cmd)
}

// healthcheck запрашивает /healthz своего сервера на localhost: в образе нет curl.
// Читает только AGY_HTTP_ADDR, чтобы не зависеть от остальных настроек.
func healthcheck() error {
	addr := os.Getenv("AGY_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("AGY_HTTP_ADDR: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}

	return nil
}
