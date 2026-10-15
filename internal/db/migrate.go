package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Миграции встроены в бинарник, чтобы образу не нужны были файлы рядом.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Команды миграций.
const (
	MigrateUp     = "up"
	MigrateDown   = "down"
	MigrateStatus = "status"
)

// Migrate выполняет команду миграций goose: up — применить все, down — откатить
// последнюю, status — вывести состояние в журнал. Одновременный запуск в нескольких
// экземплярах безопасен: goose держит advisory-блокировку PostgreSQL на время работы.
func Migrate(ctx context.Context, pool *pgxpool.Pool, cmd string) error {
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	switch cmd {
	case MigrateUp:
		results, err := provider.Up(ctx)
		for _, r := range results {
			slog.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration.String())
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	case MigrateDown:
		r, err := provider.Down(ctx)
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		slog.Info("migration rolled back", "version", r.Source.Version, "file", r.Source.Path)
	case MigrateStatus:
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		for _, s := range statuses {
			slog.Info("migration", "version", s.Source.Version, "file", s.Source.Path, "state", string(s.State), "applied_at", s.AppliedAt)
		}
	default:
		return fmt.Errorf("unknown migrate command %q (want up, down or status)", cmd)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	slog.Info("schema version", "version", version)

	return nil
}
