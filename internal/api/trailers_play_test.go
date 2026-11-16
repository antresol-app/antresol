package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/trailers"
)

// playFetcher writes the trailers package's test MP4; block (when set)
// holds each fetch until it is closed; fail fails every fetch.
type playFetcher struct {
	runs  atomic.Int32
	block chan struct{}
	fail  bool
	mu    sync.Mutex
	keys  []string
}

func (f *playFetcher) Fetch(ctx context.Context, key, dst string) error {
	f.runs.Add(1)
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.fail {
		return errors.New("fake failure")
	}
	b, err := os.ReadFile(filepath.Join("..", "trailers", "testdata", "trailer.mp4"))
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

const (
	trKey1 = "trailer0001"
	trKey2 = "trailer0002"
	trKey3 = "trailer0003"
)

type trailerEnv struct {
	*libEnv
	f     *playFetcher
	cache *trailers.Cache
	movie int64
}

// newTrailerEnv: a movie rated 18+ whose trailers are trKey1..3, a cache
// with fetcher f (nil: none) and media keys.
func newTrailerEnv(t *testing.T, f *playFetcher) *trailerEnv {
	te := &trailerEnv{f: f}
	src := &fakeTrailers{vs: []tmdb.Video{
		{Name: "Main", Site: "YouTube", Key: trKey1, Type: "Trailer", Lang: "ru", Official: true},
		{Name: "Second", Site: "YouTube", Key: trKey2, Type: "Trailer", Lang: "ru"},
		{Name: "Teaser", Site: "YouTube", Key: trKey3, Type: "Teaser", Lang: "ru"},
	}}
	te.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		withMediaKeys(d, db)
		d.Trailers = src
		if f != nil {
			te.cache = trailers.New(trailers.Config{Dir: filepath.Join(d.DataDir, "trailers"), Fetcher: f, SlotWait: 50 * time.Millisecond})
			d.TrailerCache = te.cache
		}
	})
	te.movie, _ = te.libEnv.movie("Фильм", "18+", "0123456789abcdef")
	if _, err := te.db.Exec(`UPDATE items SET tmdb_id=603 WHERE id=?`, te.movie); err != nil {
		t.Fatal(err)
	}
	return te
}

type trailerAnswer struct {
	URL        string `json:"url"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error"`
}

func (te *trailerEnv) play(key string, hdr ...string) (int, trailerAnswer, string) {
	te.t.Helper()
	w := te.do("POST", "/api/trailers/"+key+"/play", `{}`, te.admin, hdr...)
	var a trailerAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return w.Code, a, w.Header().Get("Retry-After")
}

func TestTrailerPlayOmittedWithoutFetcher(t *testing.T) {
	te := newTrailerEnv(t, nil)
	for _, tr := range te.trailersOf(te.movie) {
		if tr.Play != "" {
			t.Fatalf("play without a fetcher: %+v", tr)
		}
	}
	if code, _, _ := te.play(trKey1); code != 404 {
		t.Fatalf("play without a fetcher: %d", code)
	}
	if w := te.do("GET", "/api/trailers/"+trKey1+".mp4", "", te.admin); w.Code != 404 {
		t.Fatalf("file without a fetcher: %d", w.Code)
	}
}

func TestTrailerPlayListedOnlyAndPrefetch(t *testing.T) {
	f := &playFetcher{}
	te := newTrailerEnv(t, f)
	// never listed: 404, nothing fetched
	if code, _, _ := te.play(trKey1); code != 404 {
		t.Fatalf("unlisted: %d", code)
	}
	for _, bad := range []string{"short", "trailer0001x", "trail%2F0001", "..%2F..%2Fetc"} {
		if code, _, _ := te.play(bad); code != 404 {
			t.Fatalf("bad key %q: %d", bad, code)
		}
	}
	if f.runs.Load() != 0 {
		t.Fatalf("fetched for an unlisted key: %v", f.keys)
	}
	ts := te.trailersOf(te.movie)
	for _, tr := range ts {
		if tr.Play != "/api/trailers/"+tr.Key+"/play" {
			t.Fatalf("play %+v", tr)
		}
	}
	// the first one is prefetched, once
	te.cache.Wait()
	te.trailersOf(te.movie)
	te.cache.Wait()
	if f.runs.Load() != 1 || f.keys[0] != trKey1 || !te.cache.Cached(trKey1) {
		t.Fatalf("prefetch %v", f.keys)
	}
	// a key the server listed, but not this one
	if code, _, _ := te.play("unlisted001"); code != 404 {
		t.Fatalf("other key: %d", code)
	}
	// cached: an instant answer, no fetch
	code, a, _ := te.play(trKey1)
	if code != 200 || a.URL != "/api/trailers/"+trKey1+".mp4?mk="+url.QueryEscape(mkOf(t, a.URL)) || a.DurationMs < 1900 || a.DurationMs > 2100 {
		t.Fatalf("%d %+v", code, a)
	}
	// the second one is fetched on its play
	code, a, _ = te.play(trKey2)
	if code != 200 || !strings.HasPrefix(a.URL, "/api/trailers/"+trKey2+".mp4") || f.runs.Load() != 2 {
		t.Fatalf("%d %+v runs %d", code, a, f.runs.Load())
	}
}

// mkOf is the media key of a play URL.
func mkOf(t *testing.T, u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Query().Get("mk") == "" {
		t.Fatalf("no media key in %q", u)
	}
	return p.Query().Get("mk")
}

func TestTrailerFileRangeAndMediaKey(t *testing.T) {
	te := newTrailerEnv(t, &playFetcher{})
	te.trailersOf(te.movie)
	te.cache.Wait()
	_, a, _ := te.play(trKey1)
	want, _ := os.ReadFile(filepath.Join("..", "trailers", "testdata", "trailer.mp4"))
	// with the URL alone (the media key in it): Range works
	w := te.do("GET", a.URL, "", nil, "Range", "bytes=0-99")
	if w.Code != 206 || w.Body.String() != string(want[:100]) || w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	// a session works too; no auth does not
	if w := te.do("GET", "/api/trailers/"+trKey1+".mp4", "", te.admin); w.Code != 200 || w.Body.Len() != len(want) {
		t.Fatalf("session %d", w.Code)
	}
	if w := te.do("GET", "/api/trailers/"+trKey1+".mp4", "", nil); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}
	if w := te.do("GET", "/api/trailers/"+trKey1+".mp4?mk=v1.forged", "", nil); w.Code != 401 {
		t.Fatalf("forged key %d", w.Code)
	}
	// a media key never opens play (POST)
	if w := te.do("POST", "/api/trailers/"+trKey1+"/play?mk="+url.QueryEscape(mkOf(t, a.URL)), `{}`, nil); w.Code != 401 {
		t.Fatalf("keyed POST %d", w.Code)
	}
	// listed, not cached
	if w := te.do("GET", "/api/trailers/"+trKey3+".mp4", "", te.admin); w.Code != 404 {
		t.Fatalf("uncached %d", w.Code)
	}
	if w := te.do("GET", "/api/trailers/"+trKey1+".webm", "", te.admin); w.Code != 404 {
		t.Fatalf("not .mp4 %d", w.Code)
	}
}

func TestTrailerPlayKidGating(t *testing.T) {
	te := newTrailerEnv(t, &playFetcher{})
	te.trailersOf(te.movie)
	te.cache.Wait()
	kid := te.kid("12+")
	if w := te.do("POST", "/api/trailers/"+trKey1+"/play", `{}`, kid); w.Code != 404 {
		t.Fatalf("kid play %d", w.Code)
	}
	if w := te.do("GET", "/api/trailers/"+trKey1+".mp4", "", kid); w.Code != 404 {
		t.Fatalf("kid file %d", w.Code)
	}
}

func TestTrailerPlayBusyStillFetchingAndFailure(t *testing.T) {
	old := trailerPlayWait
	trailerPlayWait = 150 * time.Millisecond
	t.Cleanup(func() { trailerPlayWait = old })
	f := &playFetcher{block: make(chan struct{})}
	te := newTrailerEnv(t, f)
	te.trailersOf(te.movie) // prefetches trKey1 (blocked): one slot
	// still fetching after the wait: 503, the fetch goes on
	code, a, ra := te.play(trKey2)
	if code != 503 || ra != "2" || a.Error == "" {
		t.Fatalf("still fetching %d %q %+v", code, ra, a)
	}
	// both slots taken: busy
	if code, _, ra := te.play(trKey3); code != 503 || ra != "2" {
		t.Fatalf("busy %d %q", code, ra)
	}
	close(f.block)
	te.cache.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for !te.cache.Cached(trKey2) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if code, _, _ := te.play(trKey2); code != 200 {
		t.Fatalf("after the background fetch %d", code)
	}

	// a failed fetch: 502, translated
	ff := &playFetcher{fail: true}
	te2 := newTrailerEnv(t, ff)
	te2.trailersOf(te2.movie)
	te2.cache.Wait()
	code, a, _ = te2.play(trKey1)
	if code != 502 || a.Error != "Не удалось загрузить трейлер" {
		t.Fatalf("%d %+v", code, a)
	}
	if _, a, _ := te2.play(trKey1, "Accept-Language", "en"); a.Error != "Couldn’t load the trailer" {
		t.Fatalf("en %+v", a)
	}
}
