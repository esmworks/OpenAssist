package bus

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublishSubscribe(t *testing.T) {
	b := New()
	runID := uuid.New()
	ch, cancel := b.Subscribe(runID)
	defer cancel()

	b.Publish(Event{RunID: runID, Kind: "tool.started"})
	b.Publish(Event{RunID: uuid.New(), Kind: "baska.kosu"}) // bize gelmemeli

	select {
	case ev := <-ch:
		if ev.Kind != "tool.started" {
			t.Fatalf("beklenmeyen olay: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("olay gelmedi")
	}
	select {
	case ev := <-ch:
		t.Fatalf("başka koşunun olayı sızdı: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSlowSubscriberNeverBlocks(t *testing.T) {
	b := New()
	runID := uuid.New()
	_, cancel := b.Subscribe(runID) // hiç okumayan abone
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ { // tampon 256 — taşacak
			b.Publish(Event{RunID: runID, Kind: "delta"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("yavaş abone yayını blokladı")
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	b := New()
	runID := uuid.New()
	_, cancel := b.Subscribe(runID)
	cancel()
	cancel() // ikinci çağrı panik yapmamalı

	b.Publish(Event{RunID: runID, Kind: "x"}) // abone yok — sorunsuz
}
