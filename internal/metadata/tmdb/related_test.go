package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRelated answers /movie/603 (recommendations, similar and its
// collection appended), /tv/1 and /collection/2344. gate, when set, holds
// every answer until it is closed; v2 renames the first recommendation.
type fakeRelated struct {
	mu     sync.Mutex
	hits   []string
	status int
	gate   chan struct{}
	v2     bool
	srv    *httptest.Server
}

func newFakeRelated(t *testing.T) *fakeRelated {
	f := &fakeRelated{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.URL.Path+"?"+r.URL.Query().Get("language")+"&"+r.URL.Query().Get("append_to_response"))
		status, gate, v2 := f.status, f.gate, f.v2
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		switch r.URL.Path {
		case "/3/movie/603":
			if r.URL.Query().Get("append_to_response") != "recommendations,similar" {
				t.Errorf("append %q", r.URL.Query().Get("append_to_response"))
			}
			first := "The Matrix Reloaded"
			if v2 {
				first = "Reloaded v2"
			}
			w.Write([]byte(`{"id":603,"title":"The Matrix",
			  "belongs_to_collection":{"id":2344,"name":"The Matrix Collection","poster_path":"/c.jpg","backdrop_path":"/cb.jpg"},
			  "recommendations":{"results":[
			    {"id":604,"title":"` + first + `","release_date":"2003-05-15","poster_path":"/r.jpg","genre_ids":[28]},
			    {"id":603,"title":"The Matrix"},
			    {"id":9999,"title":"Adult","adult":true},
			    {"id":680,"title":"Pulp Fiction","release_date":"1994-09-10"}]},
			  "similar":{"results":[
			    {"id":680,"title":"Pulp Fiction"},
			    {"id":11,"title":"Star Wars","release_date":"1977-05-25","backdrop_path":"/sw.jpg"}]}}`))
		case "/3/tv/1":
			w.Write([]byte(`{"id":1,"name":"Shogun","recommendations":{"results":[{"id":2,"name":"Taboo","first_air_date":"2017-01-07"}]},"similar":{"results":[]}}`))
		case "/3/collection/2344":
			w.Write([]byte(`{"id":2344,"name":"The Matrix Collection","overview":"Neo","poster_path":"/c.jpg","backdrop_path":"/cb.jpg",
			  "parts":[
			    {"id":624860,"title":"The Matrix Resurrections","release_date":"2021-12-16"},
			    {"id":999,"title":"Untitled Matrix","release_date":""},
			    {"id":603,"title":"The Matrix","release_date":"1999-03-31","poster_path":"/m.jpg"},
			    {"id":604,"title":"The Matrix Reloaded","release_date":"2003-05-15"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelated) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

// countOf is the requests of one path ("/3/movie/603").
func (f *fakeRelated) countOf(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		if strings.HasPrefix(h, path+"?") {
			n++
		}
	}
	return n
}

func (f *fakeRelated) set(fn func(f *fakeRelated)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (f *fakeRelated) fail(status int) { f.set(func(f *fakeRelated) { f.status = status }) }

func newRelated(t *testing.T, f *fakeRelated, ttl time.Duration, withDB bool) *Related {
	rc := &Related{Client: &Client{BaseURL: f.srv.URL + "/3"}, TTL: ttl}
	if withDB {
		rc.DB = translationsDB(t)
	}
	t.Cleanup(rc.Close)
	return rc
}

func TestRelatedMovie(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, 0, true)
	got, ok := rc.Titles(context.Background(), "movie", 603, "en-US")
	if !ok {
		t.Fatal("no answer")
	}
	// recommendations first, then similar; no repeats, not the title itself, no adult titles
	var ids []int
	for _, r := range got.Similar {
		ids = append(ids, r.ID)
	}
	if want := []int{604, 680, 11}; !equalInts(ids, want) {
		t.Fatalf("similar %v, want %v", ids, want)
	}
	if s := got.Similar[0]; s.Kind != "movie" || s.Title != "The Matrix Reloaded" || s.Year != 2003 || s.Poster != "/r.jpg" || len(s.GenreIDs) != 1 {
		t.Fatalf("first %+v", s)
	}
	if got.Collection == nil || got.Collection.ID != 2344 || got.Collection.Name != "The Matrix Collection" {
		t.Fatalf("collection %+v", got.Collection)
	}
	f.mu.Lock()
	first := f.hits[0]
	f.mu.Unlock()
	if first != "/3/movie/603?en-US&recommendations,similar" {
		t.Fatalf("request %q", first)
	}
	// cached: in memory, then in the table for a new cache
	rc.Titles(context.Background(), "movie", 603, "en-US")
	rc2 := newRelated(t, f, 0, false)
	rc2.DB = rc.DB
	if again, ok := rc2.Titles(context.Background(), "movie", 603, "en-US"); !ok || len(again.Similar) != 3 {
		t.Fatalf("from the table: %v %+v", ok, again)
	}
	if n := f.countOf("/3/movie/603"); n != 1 {
		t.Fatalf("%d requests, want 1", n)
	}
	// another language is another entry
	rc.Titles(context.Background(), "movie", 603, "ru-RU")
	if n := f.countOf("/3/movie/603"); n != 2 {
		t.Fatalf("%d requests, want 2", n)
	}
}

// The details name the collection: its parts are fetched right away, so
// the page's Collection that follows finds them (or their fetch running).
func TestRelatedPrefetchesTheCollection(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, 0, false)
	rc.Titles(context.Background(), "movie", 603, "en-US")
	c, ok := rc.Collection(context.Background(), 2344, "en-US")
	if !ok || len(c.Parts) != 4 {
		t.Fatalf("%v %+v", ok, c)
	}
	rc.idle()
	if n := f.countOf("/3/collection/2344"); n != 1 {
		t.Fatalf("%d collection requests, want 1", n)
	}
}

func TestRelatedTV(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, 0, false)
	got, ok := rc.Titles(context.Background(), "tv", 1, "en-US")
	if !ok || len(got.Similar) != 1 || got.Similar[0].Kind != "tv" || got.Similar[0].Title != "Taboo" || got.Similar[0].Year != 2017 {
		t.Fatalf("%v %+v", ok, got)
	}
	if got.Collection != nil {
		t.Fatalf("a show has no collection: %+v", got.Collection)
	}
	if _, ok := rc.Titles(context.Background(), "episode", 1, "en-US"); ok {
		t.Fatal("an unknown kind answered")
	}
}

func TestRelatedCollectionInReleaseOrder(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, 0, true)
	c, ok := rc.Collection(context.Background(), 2344, "en-US")
	if !ok {
		t.Fatal("no collection")
	}
	var ids []int
	for _, p := range c.Parts {
		ids = append(ids, p.ID)
	}
	// dated parts by date, undated (announced) last
	if want := []int{603, 604, 624860, 999}; !equalInts(ids, want) {
		t.Fatalf("parts %v, want %v", ids, want)
	}
	if c.Name != "The Matrix Collection" || c.Overview != "Neo" || c.Backdrop != "/cb.jpg" || c.Parts[0].Poster != "/m.jpg" || c.Parts[0].Kind != "movie" {
		t.Fatalf("collection %+v", c)
	}
	rc.Collection(context.Background(), 2344, "en-US")
	if f.count() != 1 {
		t.Fatalf("%d requests, want 1 (cached)", f.count())
	}
}

// After the TTL the stale entry is served at once and refreshed in the
// background; the next read has the new one.
func TestRelatedStaleWhileRevalidate(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, time.Nanosecond, false)
	rc.Titles(context.Background(), "movie", 603, "en-US")
	rc.idle()
	gate := make(chan struct{})
	f.set(func(f *fakeRelated) { f.v2, f.gate = true, gate })
	start := time.Now()
	got, ok := rc.Titles(context.Background(), "movie", 603, "en-US")
	if !ok || got.Similar[0].Title != "The Matrix Reloaded" || time.Since(start) > time.Second {
		t.Fatalf("stale not served at once: %v %+v after %v", ok, got.Similar, time.Since(start))
	}
	close(gate)
	rc.idle()
	if got, _ := rc.Titles(context.Background(), "movie", 603, "en-US"); got.Similar[0].Title != "Reloaded v2" {
		t.Fatalf("not refreshed: %+v", got.Similar[0])
	}
}

// Readers of a missing entry share one TMDB request.
func TestRelatedConcurrentFetchesShareOne(t *testing.T) {
	f := newFakeRelated(t)
	gate := make(chan struct{})
	f.set(func(f *fakeRelated) { f.gate = gate })
	rc := newRelated(t, f, 0, false)
	var wg sync.WaitGroup
	oks := make([]bool, 5)
	for i := range oks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, oks[i] = rc.Titles(context.Background(), "movie", 603, "en-US")
		}()
	}
	for f.countOf("/3/movie/603") == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // the others reach the shared fetch
	close(gate)
	wg.Wait()
	for i, ok := range oks {
		if !ok {
			t.Fatalf("reader %d got nothing", i)
		}
	}
	if n := f.countOf("/3/movie/603"); n != 1 {
		t.Fatalf("%d requests, want 1", n)
	}
}

// A reader that gives up (its request cancelled) or a fetch that runs out
// of time is no TMDB failure: the next reader asks again.
func TestRelatedCancelledIsNoFailure(t *testing.T) {
	f := newFakeRelated(t)
	gate := make(chan struct{})
	f.set(func(f *fakeRelated) { f.gate = gate })
	rc := newRelated(t, f, 0, false)
	rc.Wait = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := rc.Titles(ctx, "movie", 603, "en-US"); ok {
		t.Fatal("a cancelled reader got an answer")
	}
	// out of time (TMDB held, or the client's slots all busy)
	if _, ok := rc.Titles(context.Background(), "movie", 603, "en-US"); ok {
		t.Fatal("answered while TMDB held")
	}
	rc.idle()
	close(gate)
	f.set(func(f *fakeRelated) { f.gate = nil })
	if _, ok := rc.Titles(context.Background(), "movie", 603, "en-US"); !ok {
		t.Fatal("backed off after a timeout")
	}
	if rc.failures() != 0 {
		t.Fatalf("%d failures recorded", rc.failures())
	}
}

func TestRelatedFailureBacksOffAndKeepsStale(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, time.Nanosecond, false)
	if _, ok := rc.Titles(context.Background(), "movie", 603, "en-US"); !ok {
		t.Fatal("first fetch failed")
	}
	rc.idle()
	f.fail(http.StatusInternalServerError)
	// stale but kept when TMDB fails (the refresh fails in the background)
	if got, ok := rc.Titles(context.Background(), "movie", 603, "en-US"); !ok || len(got.Similar) != 3 {
		t.Fatalf("stale: %v %+v", ok, got)
	}
	rc.idle()
	n := f.count()
	// a failure is not retried at once
	rc.Titles(context.Background(), "movie", 603, "en-US")
	rc.idle()
	if f.count() != n {
		t.Fatalf("retried at once: %d → %d", n, f.count())
	}
	if _, ok := rc.Collection(context.Background(), 1, "en-US"); ok {
		t.Fatal("a failing collection answered")
	}
}

// Failures older than the back-off are dropped, so the map stays small.
func TestRelatedFailuresExpire(t *testing.T) {
	f := newFakeRelated(t)
	f.fail(http.StatusInternalServerError)
	rc := newRelated(t, f, 0, false)
	rc.mu.Lock()
	rc.failed = map[string]time.Time{}
	for i := range 100 {
		rc.failed["movie/"+string(rune('a'+i))+"/en-US"] = time.Now().Add(-2 * relatedRetry)
	}
	rc.mu.Unlock()
	rc.Titles(context.Background(), "movie", 603, "en-US")
	if n := rc.failures(); n != 1 {
		t.Fatalf("%d failures kept, want 1 (the fresh one)", n)
	}
}

func TestRelatedNotFoundIsEmpty(t *testing.T) {
	f := newFakeRelated(t)
	rc := newRelated(t, f, 0, false)
	got, ok := rc.Titles(context.Background(), "movie", 42, "en-US")
	if !ok || len(got.Similar) != 0 || got.Collection != nil {
		t.Fatalf("%v %+v", ok, got)
	}
	rc.Titles(context.Background(), "movie", 42, "en-US")
	if f.count() != 1 {
		t.Fatalf("%d requests, want 1 (a 404 is cached)", f.count())
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
