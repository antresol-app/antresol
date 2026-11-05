package metadata

import (
	"context"
	"testing"
	"time"
)

func TestLimiterSpacing(t *testing.T) {
	l := &Limiter{Interval: 200 * time.Millisecond}
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 400*time.Millisecond {
		t.Fatalf("3 calls took %v, want >= 400ms", d)
	}
}

func TestLimiterCancel(t *testing.T) {
	l := &Limiter{Interval: time.Hour}
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("want ctx error")
	}
}

func TestLimiterNil(t *testing.T) {
	var l *Limiter
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
