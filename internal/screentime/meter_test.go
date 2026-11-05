package screentime

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// A metered response counts what it sends over the bitrate and fails its
// writes once the time is over.
func TestMeterCountsAndCuts(t *testing.T) {
	MeterEvery = 0
	t.Cleanup(func() { MeterEvery = 5 * time.Second })
	s, c, u := newSvc(t, "2026-11-02 12:00")
	ctx := context.Background()
	if err := s.SetLimits(ctx, u.ID, Limits{AudioMin: ip(1)}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	m := s.Meter(ctx, w, u, Signal{Category: Audio, Key: "item:9", ItemID: 9}, 1000) // 1000 B/s
	chunk := make([]byte, 10_000)                                                    // 10 s each
	var err error
	writes := 0
	for err == nil && writes < 20 {
		_, err = m.Write(chunk)
		writes++
		c.add(10 * time.Second) // played as it was sent
	}
	m.Done()
	if !errors.Is(err, ErrOver) || writes < 6 || writes > 8 {
		t.Fatalf("%v after %d writes", err, writes)
	}
	if st, _ := s.Status(ctx, u); st.Audio.Allowed {
		t.Fatalf("%+v", st.Audio)
	}
	adult := u
	adult.IsKid = false
	if s.Meter(ctx, w, adult, Signal{Category: Audio}, 1000) != nil {
		t.Fatal("an adult is metered")
	}
}
