package api

import (
	"strings"
	"testing"
	"time"
)

// track adds a track item and returns its id.
func (e *libEnv) track(title string) int64 {
	e.t.Helper()
	res, err := e.db.Exec(`INSERT INTO items(kind,title) VALUES('album','Группа крови')`)
	if err != nil {
		e.t.Fatal(err)
	}
	album, _ := res.LastInsertId()
	res, err = e.db.Exec(`INSERT INTO items(kind,parent_id,title) VALUES('track',?,?)`, album, title)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *libEnv) plays(track int64) []string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT played_at FROM plays WHERE item_id=? ORDER BY played_at`, track)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestPlaysAt(t *testing.T) {
	e := newLibEnv(t)
	tr := e.track("Кукушка")
	post := func(body string) int {
		return e.do("POST", "/api/music/plays", body, e.admin).Code
	}
	at := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)
	if c := post(`{"trackId":` + itoa(tr) + `,"at":"` + at.Format(time.RFC3339Nano) + `"}`); c != 204 {
		t.Fatalf("at: %d", c)
	}
	got := e.plays(tr)
	if len(got) != 1 || got[0] != at.Format("2006-01-02T15:04:05.000000000Z") {
		t.Fatalf("stored %v, want %s", got, at)
	}

	tr2 := e.track("Звезда по имени Солнце")
	before := time.Now().UTC()
	if c := post(`{"trackId":` + itoa(tr2) + `,"at":"` + time.Now().Add(72*time.Hour).Format(time.RFC3339) + `"}`); c != 204 {
		t.Fatalf("future: %d", c)
	}
	got = e.plays(tr2)
	if len(got) != 1 {
		t.Fatalf("future %v", got)
	}
	ts, _ := time.Parse("2006-01-02T15:04:05.000000000Z", got[0])
	if ts.Before(before.Add(-time.Second)) || ts.After(time.Now().Add(time.Second)) {
		t.Fatalf("future not clamped to now: %v", ts)
	}

	tr3 := e.track("Пачка сигарет")
	if c := post(`{"trackId":` + itoa(tr3) + `,"at":"` + time.Now().Add(-31*24*time.Hour).Format(time.RFC3339) + `"}`); c != 204 {
		t.Fatalf("old: %d", c)
	}
	if got := e.plays(tr3); len(got) != 0 {
		t.Fatalf("31-day-old play recorded: %v", got)
	}

	w := e.do("POST", "/api/music/plays", `{"trackId":`+itoa(tr3)+`,"at":"вчера"}`, e.admin)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Неверное время записи") {
		t.Fatalf("garbage: %d %s", w.Code, w.Body)
	}
	// without at: now, as before
	if c := post(`{"trackId":` + itoa(tr3) + `}`); c != 204 || len(e.plays(tr3)) != 1 {
		t.Fatalf("no at: %d %v", c, e.plays(tr3))
	}
}

func TestPlaysAtIdempotent(t *testing.T) {
	e := newLibEnv(t)
	tr := e.track("Кукушка")
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
	body := `{"trackId":` + itoa(tr) + `,"at":"` + at + `"}`
	for i := 0; i < 2; i++ {
		if w := e.do("POST", "/api/music/plays", body, e.admin); w.Code != 204 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if got := e.plays(tr); len(got) != 1 {
		t.Fatalf("plays %v", got)
	}
	// another profile's play at the same moment is its own row
	mama := e.login(`{"name":"Мама"}`)
	if w := e.do("POST", "/api/music/plays", body, mama); w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	if got := e.plays(tr); len(got) != 2 {
		t.Fatalf("plays %v", got)
	}
}

// A future-dated play is stored at the server's now; the app's retry of
// the same play (same trackId and at) records nothing new and keeps the
// first stored time.
func TestPlaysAtClampedRetryIdempotent(t *testing.T) {
	e := newLibEnv(t)
	tr := e.track("Кукушка")
	body := `{"trackId":` + itoa(tr) + `,"at":"` + time.Now().Add(72*time.Hour).UTC().Format(time.RFC3339Nano) + `"}`
	if c := e.do("POST", "/api/music/plays", body, e.admin).Code; c != 204 {
		t.Fatalf("first: %d", c)
	}
	first := e.plays(tr)
	if len(first) != 1 {
		t.Fatalf("first %v", first)
	}
	time.Sleep(5 * time.Millisecond)
	if c := e.do("POST", "/api/music/plays", body, e.admin).Code; c != 204 {
		t.Fatalf("retry: %d", c)
	}
	if got := e.plays(tr); len(got) != 1 || got[0] != first[0] {
		t.Fatalf("retry stored %v, first %v", got, first)
	}
	// a different future play of the same track is a second play
	other := `{"trackId":` + itoa(tr) + `,"at":"` + time.Now().Add(73*time.Hour).UTC().Format(time.RFC3339Nano) + `"}`
	if c := e.do("POST", "/api/music/plays", other, e.admin).Code; c != 204 {
		t.Fatalf("other: %d", c)
	}
	if got := e.plays(tr); len(got) != 2 {
		t.Fatalf("second play %v", got)
	}
}
