package events

import "testing"

func TestBusFanOutAndCancel(t *testing.T) {
	var b Bus
	a, cancelA := b.Subscribe()
	c, cancelC := b.Subscribe()
	if b.Subscribers() != 2 {
		t.Fatal(b.Subscribers())
	}
	b.Publish(Event{Type: LibraryUpdated, Data: 1})
	if e := <-a; e.Type != LibraryUpdated || e.Data != 1 {
		t.Fatalf("%+v", e)
	}
	if e := <-c; e.Type != LibraryUpdated {
		t.Fatalf("%+v", e)
	}
	cancelA()
	cancelA() // idempotent
	if _, ok := <-a; ok {
		t.Fatal("channel not closed")
	}
	b.Publish(Event{Type: "test.event"})
	if e := <-c; e.Type != "test.event" {
		t.Fatalf("%+v", e)
	}
	cancelC()
	if b.Subscribers() != 0 {
		t.Fatal(b.Subscribers())
	}
}

func TestBusFilter(t *testing.T) {
	var b Bus
	ch, cancel := b.Subscribe(func(typ string) bool { return typ == LibraryUpdated })
	defer cancel()
	if b.Wants("test.other") || !b.Wants(LibraryUpdated) {
		t.Fatal("Wants ignores the filter")
	}
	b.Publish(Event{Type: "test.other"})
	b.Publish(Event{Type: LibraryUpdated})
	if e := <-ch; e.Type != LibraryUpdated || len(ch) != 0 {
		t.Fatalf("%+v, %d queued", e, len(ch))
	}
}

func TestBusSlowSubscriberDoesNotBlock(t *testing.T) {
	var b Bus
	ch, cancel := b.Subscribe()
	defer cancel()
	for i := 0; i < subBuffer*3; i++ {
		b.Publish(Event{Type: "test.other", Data: i}) // must not block
	}
	if len(ch) != subBuffer {
		t.Fatalf("queued %d", len(ch))
	}
}

// F22: Close ends every subscription so SSE handlers return at shutdown.
func TestCloseEndsSubscriptions(t *testing.T) {
	var b Bus
	ch, cancel := b.Subscribe()
	b.Close()
	if _, ok := <-ch; ok {
		t.Fatal("channel still open after Close")
	}
	cancel() // safe after Close
	b.Publish(Event{Type: "x"})
	late, cancel2 := b.Subscribe()
	if _, ok := <-late; ok {
		t.Fatal("subscription after Close is open")
	}
	cancel2()
	b.Close() // idempotent
}
