package natsx_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/zubans/agy-pool/internal/natsx"
	"github.com/zubans/agy-pool/internal/testutil"
)

// TestDrainFlushesAndCloses проверяет, что Drain дожидается отправки исходящих
// сообщений и закрытия подключения, а не возвращается сразу.
func TestDrainFlushesAndCloses(t *testing.T) {
	ns := testutil.StartNATS(t)

	sub, err := nats.Connect(ns.URL)
	if err != nil {
		t.Fatalf("connect subscriber: %v", err)
	}
	t.Cleanup(sub.Close)

	const total = 1000
	received := make(chan struct{}, total)
	if _, err := sub.Subscribe("drain.test", func(*nats.Msg) { received <- struct{}{} }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := sub.Flush(); err != nil {
		t.Fatalf("flush subscriber: %v", err)
	}

	nc, _, err := natsx.Connect(ns.URL, "agy-pool-test")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	for range total {
		if err := nc.Publish("drain.test", []byte("x")); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	if err := natsx.Drain(nc); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !nc.IsClosed() {
		t.Fatal("connection is not closed after Drain")
	}
	// Повторный вызов на закрытом подключении — не ошибка.
	if err := natsx.Drain(nc); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for got := 0; got < total; got++ {
		select {
		case <-received:
		case <-deadline:
			t.Fatalf("received %d of %d messages published before Drain", got, total)
		}
	}
}
