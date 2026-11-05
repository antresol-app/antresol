package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/antresol-app/antresol/internal/segments"
)

type fakeSegments struct {
	asked []int64
	movie bool
}

func (f *fakeSegments) ForPlay(_ context.Context, fileID, durationMs int64, movie bool) []segments.Play {
	f.asked, f.movie = append(f.asked, fileID), movie
	return []segments.Play{{Kind: segments.Credits, StartSec: 1.5, EndSec: float64(durationMs) / 1000}}
}

const directProbe = `{"Container":"mp4","DurationMs":5000,"Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`

const directCaps = `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`

// POST /api/play returns the file's segments (the file timeline, seconds).
func TestPlayReturnsSegments(t *testing.T) {
	fs := &fakeSegments{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Segments = fs })
	_, f := e.movie("Брат", "16+", "0123456789")
	e.probeJSON(f, directProbe)
	w := e.do("POST", "/api/play/"+itoa(f), directCaps, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Segments []map[string]any `json:"segments"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Segments) != 1 || out.Segments[0]["kind"] != "credits" || out.Segments[0]["startSec"] != 1.5 || out.Segments[0]["endSec"] != 5.0 {
		t.Fatalf("%s", w.Body)
	}
	if len(fs.asked) != 1 || fs.asked[0] != f || !fs.movie {
		t.Fatalf("asked %v movie %v", fs.asked, fs.movie)
	}
}

// Without segments the list is there, empty.
func TestPlaySegmentsEmptyList(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	e.probeJSON(f, directProbe)
	w := e.do("POST", "/api/play/"+itoa(f), directCaps, e.admin)
	var out map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || string(out["segments"]) != "[]" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
