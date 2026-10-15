// Package db — подключение к PostgreSQL, миграции и сгенерированные sqlc-запросы.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// retryInterval — пауза между попытками подключиться, пока PostgreSQL не готов.
const retryInterval = time.Second

// Open создаёт пул подключений и ждёт, пока PostgreSQL ответит на Ping.
// Сколько ждать, задаёт ctx: при его отмене возвращается последняя ошибка.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	for attempt := 1; ; attempt++ {
		err = pool.Ping(ctx)
		if err == nil {
			return pool, nil
		}

		slog.Warn("postgres is not ready", "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			pool.Close()

			return nil, fmt.Errorf("connect to postgres: %w", err)
		case <-time.After(retryInterval):
		}
	}
}
