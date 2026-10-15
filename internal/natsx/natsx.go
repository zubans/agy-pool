// Package natsx — подключение к NATS и JetStream.
package natsx

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// reconnectWait — пауза между попытками переподключения.
	reconnectWait = 2 * time.Second
	// DrainTimeout — сколько ждать при остановке, пока отпишутся подписки и уйдут исходящие сообщения.
	DrainTimeout = 10 * time.Second
)

// Connect подключается к NATS. Подключение не падает, если сервер пока недоступен:
// клиент переподключается бесконечно, а /healthz показывает, что NATS не готов.
// Закрывать подключение при остановке — через Drain.
func Connect(url, name string) (*nats.Conn, jetstream.JetStream, error) {
	nc, err := nats.Connect(url,
		nats.Name(name),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(reconnectWait),
		nats.DrainTimeout(DrainTimeout),
		nats.ConnectHandler(func(c *nats.Conn) {
			slog.Info("nats connected", "url", c.ConnectedUrlRedacted())
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.Warn("nats disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			slog.Info("nats reconnected", "url", c.ConnectedUrlRedacted())
		}),
		nats.ReconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("nats reconnect failed", "error", err)
		}),
		nats.ClosedHandler(logClosed),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			slog.Error("nats error", "error", err)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()

		return nil, nil, fmt.Errorf("create jetstream context: %w", err)
	}

	return nc, js, nil
}

// Drain корректно закрывает подключение: отписывает подписки, отправляет исходящие
// сообщения и ждёт закрытия. nc.Drain сам по себе асинхронный — без ожидания процесс
// завершился бы раньше, чем буфер уйдёт на сервер. Если подключение не готово
// к сбросу (например, переподключается), оно закрывается сразу.
func Drain(nc *nats.Conn) error {
	closed := make(chan struct{})
	var once sync.Once
	nc.SetClosedHandler(func(c *nats.Conn) {
		logClosed(c)
		once.Do(func() { close(closed) })
	})

	if err := nc.Drain(); err != nil {
		nc.Close()
		if errors.Is(err, nats.ErrConnectionClosed) {
			return nil
		}

		return fmt.Errorf("drain nats: %w", err)
	}

	// После DrainTimeout клиент сам закрывает подключение; запас — на вызов обработчика.
	select {
	case <-closed:
		return nil
	case <-time.After(DrainTimeout + time.Second):
		nc.Close()

		return errors.New("drain nats: timed out")
	}
}

func logClosed(*nats.Conn) {
	slog.Info("nats connection closed")
}
