// Package testutil — помощники тестов: PostgreSQL и NATS в контейнерах testcontainers-go.
package testutil

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Образы те же, что в compose.yaml.
const (
	PostgresImage = "postgres:16"
	NATSImage     = "nats:2-alpine"
)

// Postgres — запущенный PostgreSQL для теста.
type Postgres struct {
	Container *tcpostgres.PostgresContainer
	URL       string
}

// NATS — запущенный NATS с JetStream для теста.
type NATS struct {
	Container *tcnats.NATSContainer
	URL       string
}

// StartPostgres поднимает чистый PostgreSQL. Контейнер удаляется по окончании теста.
// В режиме -short тест пропускается: нужен Docker.
func StartPostgres(t testing.TB) *Postgres {
	t.Helper()
	skipShort(t)

	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("agy"),
		tcpostgres.WithUsername("agy"),
		tcpostgres.WithPassword("agy"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}

	return &Postgres{Container: ctr, URL: url}
}

// StartNATS поднимает NATS с JetStream. Контейнер удаляется по окончании теста.
// Порт клиента на хосте закреплён: после Stop и Start контейнера адрес не меняется,
// и тест может проверить, что клиент переподключился сам.
// В режиме -short тест пропускается: нужен Docker.
func StartNATS(t testing.TB) *NATS {
	t.Helper()
	skipShort(t)

	hostPort := strconv.Itoa(freePort(t))
	clientPort := network.MustParsePort("4222/tcp")

	ctx := context.Background()
	ctr, err := tcnats.Run(ctx, NATSImage,
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			if hc.PortBindings == nil {
				hc.PortBindings = network.PortMap{}
			}
			hc.PortBindings[clientPort] = []network.PortBinding{{HostPort: hostPort}}
		}),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start nats: %v", err)
	}

	url, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("nats connection string: %v", err)
	}

	return &NATS{Container: ctr, URL: url}
}

// freePort возвращает свободный сейчас TCP-порт хоста.
func freePort(t testing.TB) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}

func skipShort(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker: skipped in -short mode")
	}
}
