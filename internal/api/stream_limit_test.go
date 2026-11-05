package api

import (
	"testing"
)

func TestKeyedLimit(t *testing.T) {
	l := keyedLimit{max: 2}
	if !l.acquire("a") || !l.acquire("a") {
		t.Fatal("two slots")
	}
	if l.acquire("a") {
		t.Fatal("a third slot for the same key")
	}
	if !l.acquire("b") {
		t.Fatal("another key is not limited by a")
	}
	l.release("a")
	if !l.acquire("a") {
		t.Fatal("a released slot is free again")
	}
	l.release("a")
	l.release("a")
	l.release("b")
	if len(l.n) != 0 {
		t.Fatalf("counters left: %v", l.n)
	}
}
