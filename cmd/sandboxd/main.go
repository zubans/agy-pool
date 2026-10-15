// Команда sandboxd — исполнитель песочниц для навыков.
//
// Пока это заглушка: отвечает только на GET /healthz, чтобы у контейнера был
// healthcheck. Запуск команд в песочницах появится вместе с навыками.
//
//	sandboxd [serve]     запустить сервер (по умолчанию)
//	sandboxd healthcheck проверить /healthz запущенного сервера
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

	"github.com/zubans/agy-pool/internal/logging"
	"github.com/zubans/agy-pool/internal/version"
)

func main() {
	if _, err := logging.New(envOr("SANDBOXD_LOG_LEVEL", "info")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	addr := envOr("SANDBOXD_ADDR", ":8090")

	var err error
	switch cmd := firstArg(); cmd {
	case "serve":
		err = serve(addr)
	case "healthcheck":
		err = healthcheck(addr)
	default:
		err = fmt.Errorf("unknown command %q (want serve or healthcheck)", cmd)
	}

	if err != nil {
		slog.Error("sandboxd failed", "error", err)
		os.Exit(1)
	}
}

func serve(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("sandboxd listening", "addr", addr, "version", version.Version)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}

func healthcheck(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}

	return nil
}

func firstArg() string {
	if len(os.Args) > 1 {
		return os.Args[1]
	}

	return "serve"
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	return def
}
