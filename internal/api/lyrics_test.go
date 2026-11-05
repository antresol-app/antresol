package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/lyrics"
)

func TestLyricsFromLRCLIB(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"syncedLyrics":"[00:01.00]Из сети","plainLyrics":"Из сети"}`))
	}))
	defer s.Close()
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Lyrics = &lyrics.Client{Base: s.URL, HTTP: s.Client(), UA: "t"} })
	_, tr := e.simpleAlbum("Кино", "Группа крови", 1)
	w := e.do("GET", "/api/music/tracks/"+itoa(tr[0])+"/lyrics", "", e.admin)
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, `"synced":true`) || !strings.Contains(b, `"lines"`) || !strings.Contains(b, "lrclib") {
		t.Fatalf("%d %s", w.Code, b)
	}
}

// LRCLIB down (its 502s, a timeout): 503 with a message, not «no words» (404).
func TestLyricsLRCLIBDown(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer s.Close()
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Lyrics = &lyrics.Client{Base: s.URL, HTTP: s.Client(), UA: "t"} })
	_, tr := e.simpleAlbum("Кино", "Группа крови", 1)
	if w := e.do("GET", "/api/music/tracks/"+itoa(tr[0])+"/lyrics", "", e.admin); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// hasLyrics: always with an online source; without one only for tracks
// whose file had words (the apps hide the lyrics button otherwise).
func TestTrackHasLyrics(t *testing.T) {
	has := func(e *libEnv, album int64) []bool {
		t.Helper()
		d := decodeJSON[albumDetailOut](t, e.do("GET", "/api/music/albums/"+itoa(album), "", e.admin).Body.Bytes())
		var out []bool
		for _, tr := range d.Tracks {
			out = append(out, tr.HasLyrics)
		}
		return out
	}
	off := newLibEnv(t)
	al, tr := off.simpleAlbum("Кино", "Группа крови", 2)
	if _, err := off.db.Exec(`UPDATE tracks SET lyrics='Тёплое место' WHERE item_id=?`, tr[0]); err != nil {
		t.Fatal(err)
	}
	if got := has(off, al); len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("no online source: %v, want [true false]", got)
	}
	if w := off.do("GET", "/api/music/tracks/"+itoa(tr[1])+"/lyrics", "", off.admin); w.Code != 404 {
		t.Fatalf("no words anywhere: %d", w.Code)
	}
	on := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Lyrics = &lyrics.Client{Base: "http://127.0.0.1:1", HTTP: http.DefaultClient}
	})
	al, _ = on.simpleAlbum("Кино", "Группа крови", 2)
	if got := has(on, al); len(got) != 2 || !got[0] || !got[1] {
		t.Fatalf("online source: %v, want all true", got)
	}
}

// Words LRCLIB gave are kept: with LRCLIB down later the player still
// gets them (and the kept answer is not asked for again).
func TestLyricsKeptForWhenLRCLIBIsDown(t *testing.T) {
	var down atomic.Bool
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"syncedLyrics":"[00:01.00]Из сети","plainLyrics":"Из сети"}`))
	}))
	defer s.Close()
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Lyrics = &lyrics.Client{Base: s.URL, HTTP: s.Client(), UA: "t"} })
	_, tr := e.simpleAlbum("Кино", "Группа крови", 1)
	path := "/api/music/tracks/" + itoa(tr[0]) + "/lyrics"
	if w := e.do("GET", path, "", e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	st, ok, err := e.repo.TrackLyrics(context.Background(), tr[0])
	if err != nil || !ok || st.Status != library.LyricsFound || st.Synced != "[00:01.00]Из сети" {
		t.Fatalf("not kept: %+v %v %v", st, ok, err)
	}
	down.Store(true)
	n := hits.Load()
	w := e.do("GET", path, "", e.admin)
	if b := w.Body.String(); w.Code != 200 || !strings.Contains(b, `"synced":true`) || !strings.Contains(b, `"source":"lrclib"`) || hits.Load() != n {
		t.Fatalf("down: %d %s (asked %d)", w.Code, b, hits.Load()-n)
	}
}

// Words the background queue stored are served with LRCLIB unreachable or
// off; the file's synced words still win over them.
func TestLyricsServesStoredWords(t *testing.T) {
	for _, online := range []bool{false, true} {
		e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
			if online {
				d.Lyrics = &lyrics.Client{Base: "http://127.0.0.1:1", HTTP: http.DefaultClient}
			}
		})
		al, tr := e.simpleAlbum("Кино", "Группа крови", 3)
		now := time.Now()
		for _, id := range tr {
			if err := e.repo.SaveTrackLyrics(context.Background(), id, lyrics.Stored(&lyrics.Result{Synced: "[00:03.00]Хранится", Plain: "Хранится"}, now)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.db.Exec(`UPDATE tracks SET lyrics='[00:05.00]Из файла' WHERE item_id=?`, tr[1]); err != nil {
			t.Fatal(err)
		}
		// LRCLIB had none for the third: kept as none, no request, 404
		if err := e.repo.SaveTrackLyrics(context.Background(), tr[2], lyrics.Stored(nil, now)); err != nil {
			t.Fatal(err)
		}
		w := e.do("GET", "/api/music/tracks/"+itoa(tr[0])+"/lyrics", "", e.admin)
		if b := w.Body.String(); w.Code != 200 || !strings.Contains(b, "Хранится") || !strings.Contains(b, `"source":"lrclib"`) {
			t.Fatalf("online=%v stored: %d %s", online, w.Code, b)
		}
		w = e.do("GET", "/api/music/tracks/"+itoa(tr[1])+"/lyrics", "", e.admin)
		if b := w.Body.String(); w.Code != 200 || !strings.Contains(b, "Из файла") || !strings.Contains(b, `"source":"file"`) {
			t.Fatalf("online=%v file first: %d %s", online, w.Code, b)
		}
		if w := e.do("GET", "/api/music/tracks/"+itoa(tr[2])+"/lyrics", "", e.admin); w.Code != 404 {
			t.Fatalf("online=%v kept none: %d %s", online, w.Code, w.Body)
		}
		d := decodeJSON[albumDetailOut](t, e.do("GET", "/api/music/albums/"+itoa(al), "", e.admin).Body.Bytes())
		if len(d.Tracks) != 3 || !d.Tracks[0].HasLyrics || !d.Tracks[1].HasLyrics || d.Tracks[2].HasLyrics {
			t.Fatalf("online=%v hasLyrics %+v", online, d.Tracks)
		}
	}
}

// The lyrics offset: 0 until set, set by an adult profile for everyone
// (clamped to ±10 s), not by a kid; kept across a refetch of the words, and
// settable on a track whose synced words come from its file (no stored row).
func TestLyricsOffset(t *testing.T) {
	e := newLibEnv(t)
	_, tr := e.simpleAlbum("Кино", "Группа крови", 2)
	ctx := context.Background()
	if err := e.repo.SaveTrackLyrics(ctx, tr[0], lyrics.Stored(&lyrics.Result{Synced: "[00:03.00]Хранится"}, time.Now())); err != nil {
		t.Fatal(err)
	}
	get := func(id int64) map[string]any {
		t.Helper()
		w := e.do("GET", "/api/music/tracks/"+itoa(id)+"/lyrics", "", e.admin)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return decodeJSON[map[string]any](t, w.Body.Bytes())
	}
	if got := get(tr[0])["offsetMs"]; got != 0.0 {
		t.Fatalf("default offset %v", got)
	}
	path := "/api/music/tracks/" + itoa(tr[0]) + "/lyrics/offset"
	w := e.do("PUT", path, `{"offsetMs":1900}`, e.login(`{"name":"Мама"}`))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"offsetMs":1900`) {
		t.Fatalf("adult: %d %s", w.Code, w.Body)
	}
	if got := get(tr[0])["offsetMs"]; got != 1900.0 {
		t.Fatalf("offset %v", got)
	}
	if w := e.do("PUT", path, `{"offsetMs":-500}`, e.kid("")); w.Code != http.StatusForbidden {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", path, `{"offsetMs":-25000}`, e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"offsetMs":-10000`) {
		t.Fatalf("clamp: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{}`, `{"offsetMs":"x"}`, `nope`} {
		if w := e.do("PUT", path, bad, e.admin); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	// a refetch (the stored words replaced) keeps it
	if err := e.repo.SaveTrackLyrics(ctx, tr[0], lyrics.Stored(&lyrics.Result{Synced: "[00:04.00]Заново"}, time.Now())); err != nil {
		t.Fatal(err)
	}
	if b := get(tr[0]); b["offsetMs"] != -10000.0 || !strings.Contains(b["lyrics"].(string), "Заново") {
		t.Fatalf("after refetch %v", b)
	}
	// the file's synced words: no stored row until the offset makes one
	if _, err := e.db.Exec(`UPDATE tracks SET lyrics='[00:05.00]Из файла' WHERE item_id=?`, tr[1]); err != nil {
		t.Fatal(err)
	}
	if w := e.do("PUT", "/api/music/tracks/"+itoa(tr[1])+"/lyrics/offset", `{"offsetMs":-1250}`, e.admin); w.Code != 200 {
		t.Fatalf("file words: %d %s", w.Code, w.Body)
	}
	if b := get(tr[1]); b["offsetMs"] != -1250.0 || b["source"] != "file" {
		t.Fatalf("file words %v", b)
	}
	if w := e.do("PUT", "/api/music/tracks/999999/lyrics/offset", `{"offsetMs":1}`, e.admin); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
}
