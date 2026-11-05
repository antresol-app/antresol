package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

// fakeMB serves MusicBrainz under /ws/2 and the Cover Art Archive under /caa.
type fakeMB struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	groups map[string]string // query substring -> search JSON
	covers map[string]bool   // release group id -> has a cover
	broken map[string]int    // query substring -> HTTP status (0 = garbage JSON)
}

func newFakeMB(t *testing.T) *fakeMB {
	f := &fakeMB{groups: map[string]string{}, covers: map[string]bool{}, broken: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/ws/2/release-group":
			q := r.URL.Query().Get("query")
			for k, code := range f.broken {
				if strings.Contains(q, k) {
					if code == 0 {
						w.Write([]byte("<html>maintenance</html>"))
					} else {
						w.WriteHeader(code)
					}
					return
				}
			}
			for k, v := range f.groups {
				if strings.Contains(q, k) {
					w.Write([]byte(v))
					return
				}
			}
			w.Write([]byte(`{"release-groups":[]}`))
		case strings.HasPrefix(r.URL.Path, "/ws/2/release-group/"):
			id := strings.TrimPrefix(r.URL.Path, "/ws/2/release-group/")
			switch id {
			case "rg-surfer":
				w.Write([]byte(`{"id":"rg-surfer","title":"Surfer Rosa","first-release-date":"1988-03-21",
				  "artist-credit":[{"name":"Pixies","artist":{"id":"a-pixies","name":"Pixies"}}],
				  "genres":[{"name":"alternative rock","count":5},{"name":"indie rock","count":2}]}`))
			case "rg-va":
				w.Write([]byte(`{"id":"rg-va","title":"Now 1","first-release-date":"1983",
				  "artist-credit":[{"name":"Various Artists","artist":{"id":"89ad4ac3","name":"Various Artists"}}],"genres":[{"name":"pop","count":1}]}`))
			default:
				http.NotFound(w, r)
			}
		case strings.HasPrefix(r.URL.Path, "/caa/release-group/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/caa/release-group/"), "/front-500")
			if !f.covers[id] {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte("JPEGDATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMB) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		if strings.HasPrefix(h, prefix) {
			n++
		}
	}
	return n
}

func searchJSON(id, title, artist string, score int, date string) string {
	return fmt.Sprintf(`{"release-groups":[{"id":%q,"score":%d,"title":%q,"first-release-date":%q,
	  "artist-credit":[{"name":%q,"artist":{"id":"a1","name":%q}}]}]}`, id, score, title, date, artist, artist)
}

func setup(t *testing.T, base string) (*Enricher, *library.Repo) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	repo := library.NewRepo(d)
	return &Enricher{
		Client: &Client{BaseURL: base + "/ws/2", CoverBase: base + "/caa", UserAgent: testUA, Limiter: &metadata.Limiter{}},
		Repo:   repo,
		Images: &metadata.Images{Dir: filepath.Join(t.TempDir(), "images"), UserAgent: testUA},
	}, repo
}

func album(t *testing.T, repo *library.Repo, artist, title, dir, cover string, extra ...func(*library.TrackInput)) int64 {
	t.Helper()
	tr := library.TrackInput{File: library.FileInput{Path: dir + "/01.flac", Size: 1, Mtime: 1}, AlbumDir: dir,
		Album: title, Artist: artist, Title: "One", Track: 1, Cover: cover}
	for _, f := range extra {
		f(&tr)
	}
	id, err := repo.UpsertAlbumTracks(context.Background(), artist, []library.TrackInput{tr})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func meta(t *testing.T, it library.Item) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func item(t *testing.T, repo *library.Repo, id int64) library.Item {
	t.Helper()
	it, err := repo.Item(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestEnrichAlbumSetsCover(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`releasegroup:"Surfer Rosa"`] = searchJSON("rg-surfer", "Surfer Rosa", "Pixies", 100, "1988-03-21")
	f.covers["rg-surfer"] = true
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	bare := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	local := album(t, repo, "Pixies", "Surfer Rosa", "/m/b", "file:/m/b/cover.jpg")
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	b := item(t, repo, bare)
	if !strings.HasSuffix(b.Poster, ".jpg") || strings.HasPrefix(b.Poster, "file:") || b.Year != 1988 {
		t.Fatalf("bare album: %+v", b)
	}
	m := meta(t, b)
	if m["mb_id"] != "rg-surfer" || m["mb_artist_id"] != "a-pixies" || fmt.Sprint(m["genres"]) != "[alternative rock indie rock]" {
		t.Fatalf("meta %v", m)
	}
	if _, err := time.Parse(time.RFC3339, m["mb_at"].(string)); err != nil || m["compilation"] != false {
		t.Fatalf("meta %v", m)
	}
	l := item(t, repo, local)
	if l.Poster != "file:/m/b/cover.jpg" || l.Year != 1988 || fmt.Sprint(meta(t, l)["genres"]) != "[alternative rock indie rock]" {
		t.Fatalf("local album: %+v", l)
	}
	if n := f.count("/caa/"); n != 1 {
		t.Fatalf("cover requests %d, want 1 (the local cover is kept)", n)
	}
	// a second run asks nothing
	before := len(f.hits)
	if err := e.EnrichPending(ctx); err != nil || len(f.hits) != before {
		t.Fatalf("second run: %v, %d new requests", err, len(f.hits)-before)
	}
	// a rescan keeps the enricher's data and does not undo the poster
	album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	if b2 := item(t, repo, bare); b2.Poster != b.Poster || meta(t, b2)["mb_id"] != "rg-surfer" {
		t.Fatalf("after rescan: %+v", b2)
	}
}

func TestEnrichKeepsScannerFields(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`releasegroup:"Surfer Rosa"`] = searchJSON("rg-surfer", "Surfer Rosa", "Pixies", 100, "1988-03-21")
	e, repo := setup(t, f.srv.URL)
	id := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "", func(tr *library.TrackInput) { tr.Year = 2007; tr.Genre = "Rock" })
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	it := item(t, repo, id)
	m := meta(t, it)
	if it.Year != 2007 || fmt.Sprint(m["genres"]) != "[Rock]" || it.Title != "Surfer Rosa" {
		t.Fatalf("%+v %v", it, m)
	}
	// no cover at CAA: stamped, poster stays empty
	if it.Poster != "" || m["mb_cover_miss"] == nil {
		t.Fatalf("cover miss: %+v %v", it, m)
	}
}

func TestEnrichByTagReleaseGroup(t *testing.T) {
	f := newFakeMB(t)
	e, repo := setup(t, f.srv.URL)
	id := album(t, repo, "Пиксис", "Сёрфер", "/m/a", "", func(tr *library.TrackInput) { tr.MBReleaseGroupID = "rg-surfer" })
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count("/ws/2/release-group?") != 0 || f.count("/ws/2/release-group/rg-surfer") != 1 {
		t.Fatalf("hits %v", f.hits)
	}
	it := item(t, repo, id)
	if it.Year != 1988 || meta(t, it)["mb_id"] != "rg-surfer" || it.Title != "Сёрфер" {
		t.Fatalf("%+v", it)
	}
}

func TestEnrichRejectsWeakMatches(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`releasegroup:"Low Score"`] = searchJSON("rg-1", "Low Score", "Band", 89, "2000")
	f.groups[`releasegroup:"Other Title"`] = searchJSON("rg-2", "Other Title (Live)", "Band", 100, "2000")
	f.groups[`releasegroup:"Other Artist"`] = searchJSON("rg-3", "Other Artist", "Band Tribute", 100, "2000")
	e, repo := setup(t, f.srv.URL)
	var ids []int64
	for i, title := range []string{"Low Score", "Other Title", "Other Artist"} {
		ids = append(ids, album(t, repo, "Band", title, fmt.Sprintf("/m/%d", i), ""))
	}
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		it := item(t, repo, id)
		m := meta(t, it)
		if m["mb_miss"] == nil || m["mb_at"] != nil || m["mb_id"] != nil || it.Year != 0 {
			t.Fatalf("%q: %v", it.Title, m)
		}
	}
	if f.count("/ws/2/release-group/") != 0 {
		t.Fatal("lookup of a rejected match")
	}
}

func TestEnrichMusicOffline(t *testing.T) {
	f := newFakeMB(t)
	e, repo := setup(t, f.srv.URL)
	f.srv.Close()
	id := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	before := item(t, repo, id)
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatalf("offline must not fail the run: %v", err)
	}
	after := item(t, repo, id)
	if after.MetaJSON != before.MetaJSON || after.Poster != "" || after.Year != 0 {
		t.Fatalf("changed offline: %+v", after)
	}
	if strings.Contains(after.MetaJSON, "mb_miss") {
		t.Fatal("offline is not a miss")
	}
}

func TestEnrichCoverHostOffline(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`releasegroup:"Surfer Rosa"`] = searchJSON("rg-surfer", "Surfer Rosa", "Pixies", 100, "1988")
	e, repo := setup(t, f.srv.URL)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	e.Client.CoverBase = dead.URL
	first := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	it := item(t, repo, first)
	m := meta(t, it)
	if m["mb_id"] != "rg-surfer" || m["mb_cover_miss"] == nil || it.Poster != "" {
		t.Fatalf("%v", m)
	}
}

func TestEnrichMissRetry7d(t *testing.T) {
	f := newFakeMB(t)
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	id := album(t, repo, "Nobody", "Unknown Album", "/m/a", "")
	setMiss := func(ago time.Duration) {
		it := item(t, repo, id)
		m := meta(t, it)
		m["mb_miss"] = now.Add(-ago).Format(time.RFC3339)
		b, _ := json.Marshal(m)
		if err := repo.SetMetadata(ctx, id, library.Metadata{MetaJSON: string(b)}); err != nil {
			t.Fatal(err)
		}
	}
	setMiss(6 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || len(f.hits) != 0 {
		t.Fatalf("fresh miss: %v hits=%v", err, f.hits)
	}
	setMiss(8 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/ws/2/release-group?") != 1 {
		t.Fatalf("stale miss: %v hits=%v", err, f.hits)
	}
	if m := meta(t, item(t, repo, id)); m["mb_miss"] != now.Format(time.RFC3339) {
		t.Fatalf("mb_miss %v", m["mb_miss"])
	}
}

func TestEnrichCoverMissRetry7d(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`releasegroup:"Surfer Rosa"`] = searchJSON("rg-surfer", "Surfer Rosa", "Pixies", 100, "1988")
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	id := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	if err := e.EnrichPending(ctx); err != nil || f.count("/caa/") != 1 {
		t.Fatalf("%v %v", err, f.hits)
	}
	now = now.Add(6 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/caa/") != 1 {
		t.Fatalf("fresh cover miss retried: %v", f.hits)
	}
	f.covers["rg-surfer"] = true
	now = now.Add(2 * 24 * time.Hour)
	mbBefore := f.count("/ws/2/")
	if err := e.EnrichPending(ctx); err != nil || f.count("/caa/") != 2 || f.count("/ws/2/") != mbBefore {
		t.Fatalf("stale cover miss: %v", f.hits)
	}
	it := item(t, repo, id)
	if it.Poster == "" || meta(t, it)["mb_cover_miss"] != nil {
		t.Fatalf("%+v", it)
	}
}

func TestEnrichVariousArtists(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`artist:"Various Artists"`] = searchJSON("rg-va", "Now 1", "Various Artists", 98, "1983")
	e, repo := setup(t, f.srv.URL)
	id := album(t, repo, "VA", "Now 1", "/m/va", "", func(tr *library.TrackInput) { tr.Compilation = true })
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.count("/ws/2/release-group?") != 1 {
		t.Fatalf("hits %v", f.hits)
	}
	it := item(t, repo, id)
	if m := meta(t, it); m["mb_id"] != "rg-va" || it.Year != 1983 || fmt.Sprint(m["genres"]) != "[pop]" {
		t.Fatalf("%+v %v", it, m)
	}
}

func TestEnrichCorruptMetaSkipped(t *testing.T) {
	f := newFakeMB(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	if err := repo.SetMetadata(ctx, id, library.Metadata{MetaJSON: "{broken"}); err != nil {
		t.Fatal(err)
	}
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if it := item(t, repo, id); it.MetaJSON != "{broken" || len(f.hits) != 0 {
		t.Fatalf("%q %v", it.MetaJSON, f.hits)
	}
}

func TestEnrichBadAnswerIsPerItem(t *testing.T) {
	f := newFakeMB(t)
	f.groups[`releasegroup:"Surfer Rosa"`] = searchJSON("rg-surfer", "Surfer Rosa", "Pixies", 100, "1988")
	f.broken[`releasegroup:"Poison"`] = 500
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	good := album(t, repo, "Pixies", "Surfer Rosa", "/m/a", "")
	bad := album(t, repo, "Pixies", "Poison", "/m/b", "") // newer: listed first
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if m := meta(t, item(t, repo, good)); m["mb_id"] != "rg-surfer" {
		t.Fatalf("the album after a broken one was not enriched: %v", m)
	}
	m := meta(t, item(t, repo, bad))
	if m["mb_err"] != now.Format(time.RFC3339) || m["mb_miss"] != nil || m["mb_at"] != nil {
		t.Fatalf("bad: %v", m)
	}
	n := f.count("/ws/2/release-group?")
	now = now.Add(23 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/ws/2/release-group?") != n {
		t.Fatalf("retried within 24 h: %v", f.hits)
	}
	delete(f.broken, `releasegroup:"Poison"`)
	now = now.Add(2 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if m := meta(t, item(t, repo, bad)); m["mb_err"] != nil || m["mb_miss"] == nil {
		t.Fatalf("after 24 h: %v", m)
	}
}

func TestEnrichThreeBadAnswersAbort(t *testing.T) {
	f := newFakeMB(t)
	f.broken[`artist:"Pixies"`] = 0 // garbage JSON for every Pixies album
	f.groups[`releasegroup:"Doolittle"`] = searchJSON("rg-d", "Doolittle", "Other", 100, "1989")
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	last := album(t, repo, "Other", "Doolittle", "/m/z", "") // oldest: listed last
	var bad []int64
	for i, title := range []string{"A", "B", "C", "D"} {
		bad = append(bad, album(t, repo, "Pixies", title, fmt.Sprintf("/m/%d", i), ""))
	}
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/ws/2/release-group?"); n != 3 {
		t.Fatalf("searches %d, want 3 then abort", n)
	}
	errs := 0
	for _, id := range bad {
		if meta(t, item(t, repo, id))["mb_err"] != nil {
			errs++
		}
	}
	if errs != 3 || meta(t, item(t, repo, last))["mb_at"] != nil {
		t.Fatalf("errs=%d", errs)
	}
	// the next run skips the three, fails the fourth, and goes on
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if meta(t, item(t, repo, last))["mb_id"] != "rg-d" {
		t.Fatal("the run did not go on after one bad answer")
	}
}

func TestEnrichMissJitter(t *testing.T) {
	f := newFakeMB(t)
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	var ids []int64
	for i := 0; i < 30; i++ {
		ids = append(ids, album(t, repo, "Nobody", fmt.Sprintf("Album %d", i), fmt.Sprintf("/m/%d", i), ""))
	}
	if err := e.EnrichPending(ctx); err != nil || f.count("/ws/2/release-group?") != 30 {
		t.Fatal(err)
	}
	// exactly 7 days later only the albums with a small jitter are due
	now = now.Add(7*24*time.Hour + 12*time.Hour)
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/ws/2/release-group?") - 30; n == 0 || n == 30 {
		t.Fatalf("%d of 30 retried at 7.5 days: no spread", n)
	}
	now = now.Add(2 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/ws/2/release-group?") != 60 {
		t.Fatalf("all due after 9.5 days: %d", f.count("/ws/2/release-group?"))
	}
}
