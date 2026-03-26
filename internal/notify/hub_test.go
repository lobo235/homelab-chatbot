package notify

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHub_RegisterUnregister(t *testing.T) {
	h := NewHub(testLogger())

	c1 := h.Register(1)
	c2 := h.Register(1)
	c3 := h.Register(2)

	if h.ClientCount() != 3 {
		t.Fatalf("expected 3 clients, got %d", h.ClientCount())
	}
	if !h.HasClients(1) {
		t.Fatal("expected HasClients(1) to be true")
	}
	if !h.HasClients(2) {
		t.Fatal("expected HasClients(2) to be true")
	}

	h.Unregister(c1)
	if h.ClientCount() != 2 {
		t.Fatalf("expected 2 clients after unregister, got %d", h.ClientCount())
	}
	if !h.HasClients(1) {
		t.Fatal("user 1 should still have a client")
	}

	h.Unregister(c2)
	if h.HasClients(1) {
		t.Fatal("user 1 should have no clients")
	}

	h.Unregister(c3)
	if h.ClientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", h.ClientCount())
	}
}

func TestHub_Send(t *testing.T) {
	h := NewHub(testLogger())

	c1 := h.Register(1)
	c2 := h.Register(1)
	c3 := h.Register(2)

	event := Event{Type: "async_complete", OpID: "dl-123", Status: "done"}
	h.Send(1, event)

	// Both clients for user 1 should receive the event.
	select {
	case e := <-c1.Events:
		if e.OpID != "dl-123" {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("c1 did not receive event")
	}

	select {
	case e := <-c2.Events:
		if e.OpID != "dl-123" {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("c2 did not receive event")
	}

	// User 2's client should NOT receive it.
	select {
	case <-c3.Events:
		t.Fatal("c3 should not have received the event")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestHub_SendDropsWhenFull(t *testing.T) {
	h := NewHub(testLogger())
	c := h.Register(1)

	// Fill the buffer (capacity 32).
	for i := 0; i < 32; i++ {
		h.Send(1, Event{Type: "async_progress", OpID: "fill"})
	}

	// Next send should be dropped (not block).
	h.Send(1, Event{Type: "async_progress", OpID: "dropped"})

	// Drain and verify we got 32, not 33.
	count := 0
	for {
		select {
		case <-c.Events:
			count++
		default:
			goto done
		}
	}
done:
	if count != 32 {
		t.Fatalf("expected 32 events, got %d", count)
	}
}

func TestHub_DoneChannel(t *testing.T) {
	h := NewHub(testLogger())
	c := h.Register(1)

	select {
	case <-c.Done():
		t.Fatal("done channel should not be closed yet")
	default:
	}

	h.Unregister(c)

	select {
	case <-c.Done():
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("done channel should be closed after unregister")
	}
}

func TestHub_HasClientsEmpty(t *testing.T) {
	h := NewHub(testLogger())
	if h.HasClients(999) {
		t.Fatal("should return false for unknown user")
	}
}
