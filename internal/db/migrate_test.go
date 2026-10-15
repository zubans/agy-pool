package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/zubans/agy-pool/internal/db"
	"github.com/zubans/agy-pool/internal/db/sqlc"
	"github.com/zubans/agy-pool/internal/testutil"
)

func TestMigrateAndPing(t *testing.T) {
	pg := testutil.StartPostgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool, err := db.Open(ctx, pg.URL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, db.MigrateUp); err != nil {
		t.Fatalf("first migrate up: %v", err)
	}
	if err := db.Migrate(ctx, pool, db.MigrateUp); err != nil {
		t.Fatalf("second migrate up must be a no-op: %v", err)
	}
	if err := db.Migrate(ctx, pool, db.MigrateStatus); err != nil {
		t.Fatalf("migrate status: %v", err)
	}

	var version int64
	if err := pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
		t.Fatalf("read goose_db_version: %v", err)
	}
	if version < 1 {
		t.Fatalf("schema version = %d, want >= 1", version)
	}

	// Пул остаётся рабочим после миграций: goose работает через отдельный sql.DB поверх пула.
	got, err := sqlc.New(pool).Ping(ctx)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if got != 1 {
		t.Fatalf("ping = %d, want 1", got)
	}
}

func TestMigrateUnknownCommand(t *testing.T) {
	pg := testutil.StartPostgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool, err := db.Open(ctx, pg.URL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, "sideways"); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestOpenGivesUpWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Порт 1 на localhost никто не слушает.
	if _, err := db.Open(ctx, "postgres://agy:agy@127.0.0.1:1/agy?sslmode=disable&connect_timeout=1"); err == nil {
		t.Fatal("expected error")
	}
}
