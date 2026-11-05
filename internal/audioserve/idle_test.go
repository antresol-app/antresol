package audioserve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// stallWriter takes the first write, then blocks every later one until its
// write deadline passes (a paused player whose buffer is full).
type stallWriter struct {
	h        http.Header
	mu       sync.Mutex
	deadline time.Time
	writes   int
}

func (s *stallWriter) Header() http.Header { return s.h }
func (s *stallWriter) WriteHeader(int)     {}

func (s *stallWriter) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.deadline = t
	s.mu.Unlock()
	return nil
}

func (s *stallWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.writes++
	first, d := s.writes == 1, s.deadline
	s.mu.Unlock()
	if first {
		return len(p), nil
	}
	if d.IsZero() {
		select {} // no watchdog: stuck for good
	}
	time.Sleep(time.Until(d))
	return 0, os.ErrDeadlineExceeded
}

func TestStalledReaderFreesSlot(t *testing.T) {
	ff, p := sineFLAC(t, "60")
	tr := &media.AudioTranscoder{FFmpeg: ff, Limit: 1}
	d := Deps{Transcoder: tr, WriteIdle: 200 * time.Millisecond}
	f := library.File{ID: 1, Path: p, DurationMs: 60000}
	req := media.AudioRequest{Codec: "flac", Lossless: true, MaxKbps: 128, MaxKbpsSet: true}
	done := make(chan any, 1)
	go func() {
		// the cut-off stream is broken off, not ended cleanly (net/http
		// recovers this panic and drops the connection)
		defer func() { done <- recover() }()
		Serve(&stallWriter{h: http.Header{}}, httptest.NewRequest("GET", "/x", nil), d, f, 0, nil, req)
	}()
	select {
	case r := <-done:
		if r != http.ErrAbortHandler {
			t.Fatalf("stalled stream ended with %v, want ErrAbortHandler", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled reader keeps its transcode")
	}
	// the only slot is free again
	w := serve(d, f, 0, nil, req, "")
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("second transcode: %d %s", w.Code, w.Body)
	}
}
