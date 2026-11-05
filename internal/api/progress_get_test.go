package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type progressOut struct {
	PositionMs int64    `json:"positionMs"`
	DurationMs int64    `json:"durationMs"`
	Finished   bool     `json:"finished"`
	Locator    *string  `json:"locator"`
	Fraction   *float64 `json:"fraction"`
	UpdatedAt  *string  `json:"updatedAt"`
}

func (e *libEnv) getProgress(id int64, hdr ...string) (int, progressOut, map[string]json.RawMessage) {
	e.t.Helper()
	w := e.do("GET", "/api/progress/"+itoa(id), "", e.admin, hdr...)
	var out progressOut
	var raw map[string]json.RawMessage
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			e.t.Fatal(err)
		}
		json.Unmarshal(w.Body.Bytes(), &raw)
	}
	return w.Code, out, raw
}

func TestProgressGet(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	movie, _ := e.movie("Брат", "16+", "x")
	at := time.Date(2026, 10, 25, 19, 30, 0, 123_000_000, time.UTC)
	if err := e.prog.SaveAt(ctx, 1, movie, 1_500_000, 6_000_000, at); err != nil {
		t.Fatal(err)
	}
	code, out, _ := e.getProgress(movie)
	if code != 200 || out.PositionMs != 1_500_000 || out.DurationMs != 6_000_000 || out.Finished ||
		out.Fraction == nil || *out.Fraction != 0.25 || out.Locator == nil || *out.Locator != "" ||
		out.UpdatedAt == nil || *out.UpdatedAt != "2026-10-25T19:30:00.123Z" {
		t.Fatalf("movie %d %+v", code, out)
	}

	res, err := e.db.Exec(`INSERT INTO items(kind,title) VALUES('book','Мастер и Маргарита')`)
	if err != nil {
		t.Fatal(err)
	}
	book, _ := res.LastInsertId()
	if err := e.prog.SaveLocator(ctx, 1, book, "epubcfi(/6/4!/4/2)", 0.4, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	code, out, _ = e.getProgress(book)
	if code != 200 || *out.Locator != "epubcfi(/6/4!/4/2)" || *out.Fraction != 0.4 || *out.UpdatedAt != "2026-10-25T18:30:00.123Z" {
		t.Fatalf("book %d %+v", code, out)
	}

	// no row: zeros, empty locator, null updatedAt
	other, _ := e.movie("Брат 2", "16+", "x")
	code, out, raw := e.getProgress(other)
	if code != 200 || out.PositionMs != 0 || out.DurationMs != 0 || out.Finished || *out.Locator != "" || *out.Fraction != 0 ||
		string(raw["updatedAt"]) != "null" {
		t.Fatalf("no row %d %+v %s", code, out, raw["updatedAt"])
	}

	// a whole second keeps the millisecond digits
	if err := e.prog.SaveAt(ctx, 1, other, 0, 0, time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := e.getProgress(other); *out.UpdatedAt != "2026-10-25T20:00:00.000Z" || *out.Fraction != 0 {
		t.Fatalf("zero duration %+v", out)
	}

	// hidden and unknown items
	adult, _ := e.movie("Груз 200", "18+", "x")
	kid := e.kid("12+")
	if w := e.do("GET", "/api/progress/"+itoa(adult), "", kid); w.Code != 404 {
		t.Fatalf("hidden %d", w.Code)
	}
	if code, _, _ := e.getProgress(999999); code != 404 {
		t.Fatalf("unknown %d", code)
	}
	if w := e.do("GET", "/api/progress/"+itoa(movie), "", nil); w.Code != 401 {
		t.Fatalf("anon %d", w.Code)
	}
}

// «Снять отметку»: another device's offline queue sent later cannot bring
// the mark (or the old position) back; GET shows the unmark's time.
func TestUnwatchedOutlivesOfflineQueue(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Брат", "16+", "x")
	rfc := func(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }
	if w := e.do("PUT", "/api/progress/"+itoa(movie), `{"positionMs":5800000,"durationMs":6000000,"updatedAt":"`+rfc(-2*time.Hour)+`"}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	unmarked := rfc(-time.Hour)
	if w := e.do("POST", "/api/items/"+itoa(movie)+"/watched", `{"watched":false,"updatedAt":"`+unmarked+`"}`, e.admin); w.Code != 204 {
		t.Fatalf("unmark %d %s", w.Code, w.Body)
	}
	// the phone's queue: finished 90 minutes ago
	if w := e.do("PUT", "/api/progress/"+itoa(movie), `{"positionMs":6000000,"durationMs":6000000,"updatedAt":"`+rfc(-90*time.Minute)+`"}`, e.admin); w.Code != 204 {
		t.Fatalf("stale %d %s", w.Code, w.Body)
	}
	code, out, _ := e.getProgress(movie)
	if code != 200 || out.Finished || out.PositionMs != 0 || out.UpdatedAt == nil {
		t.Fatalf("resurrected %d %+v", code, out)
	}
	if at, err := time.Parse(time.RFC3339, *out.UpdatedAt); err != nil || at.UTC().Format(time.RFC3339) != unmarked {
		t.Fatalf("updatedAt %v, want the unmark's %s", *out.UpdatedAt, unmarked)
	}
	// a position made after the unmark counts
	if w := e.do("PUT", "/api/progress/"+itoa(movie), `{"positionMs":60000,"durationMs":6000000}`, e.admin); w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	if _, out, _ := e.getProgress(movie); out.PositionMs != 60000 {
		t.Fatalf("new position %+v", out)
	}
	if w := e.do("POST", "/api/items/"+itoa(movie)+"/watched", `{"watched":true,"updatedAt":"вчера"}`, e.admin); w.Code != 400 {
		t.Fatalf("bad time %d", w.Code)
	}
}
