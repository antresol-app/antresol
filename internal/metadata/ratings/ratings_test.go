package ratings

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return b.Bytes()
}

// datasetServer serves body with an ETag and answers 304 to a matching
// If-None-Match; it counts full downloads.
type datasetServer struct {
	mu        sync.Mutex
	body      []byte
	etag      string
	downloads atomic.Int32
	requests  atomic.Int32
	fail      atomic.Bool
}

func (s *datasetServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	if s.fail.Load() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	s.mu.Lock()
	body, etag := s.body, s.etag
	s.mu.Unlock()
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	s.downloads.Add(1)
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", "Sat, 31 Oct 2026 12:52:38 GMT")
	w.Write(body)
}

func (s *datasetServer) set(body []byte, etag string) {
	s.mu.Lock()
	s.body, s.etag = body, etag
	s.mu.Unlock()
}

const tsv = "tconst\taverageRating\tnumVotes\n" +
	"tt0000001\t5.7\t2238\n" +
	"tt0111161\t9.3\t3200000\n" +
	"tt0068646\t9.2\t2200000\n" + // out of order: sorted on write
	"tt1160419\t8.0\t1000000\n" +
	"garbage line\n" +
	"tt15239678\t8.5\t700000\n"

func TestDatasetParseLookupAndRefresh(t *testing.T) {
	srv := &datasetServer{}
	srv.set(gz(t, tsv), `"v1"`)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	dir := t.TempDir()
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	d := &Dataset{URL: ts.URL, Dir: dir, now: func() time.Time { return clock }}
	defer d.Close()

	if _, ok := d.lookupNoRefresh("tt0111161"); ok {
		t.Fatal("rating before any download")
	}
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]IMDbRating{
		"tt0111161": {9.3, 3200000}, "tt0068646": {9.2, 2200000}, "tt0000001": {5.7, 2238}, "tt15239678": {8.5, 700000},
	} {
		if got, ok := d.Lookup(id); !ok || got != want {
			t.Errorf("%s: %+v %v, want %+v", id, got, ok, want)
		}
	}
	for _, id := range []string{"tt0000002", "tt9999999", "nm0000001", "tt", ""} {
		if r, ok := d.Lookup(id); ok {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, binName)); st == nil || st.Size() != headerLen+5*recLen {
		t.Fatalf("file %+v", st)
	}

	// unchanged: a 304, nothing downloaded again
	if err := d.Refresh(context.Background()); err != nil || srv.downloads.Load() != 1 {
		t.Fatalf("304: %v, downloads %d", err, srv.downloads.Load())
	}

	// a new day's dataset replaces the old one, found by a fresh Dataset too
	srv.set(gz(t, "tconst\taverageRating\tnumVotes\ntt0111161\t9.4\t3300000\n"), `"v2"`)
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Lookup("tt0111161"); got != (IMDbRating{9.4, 3300000}) {
		t.Fatalf("after refresh %+v", got)
	}
	if _, ok := d.Lookup("tt0068646"); ok {
		t.Fatal("title of the old dataset")
	}
	d2 := &Dataset{URL: ts.URL, Dir: dir, now: func() time.Time { return clock }}
	defer d2.Close()
	if got, ok := d2.lookupNoRefresh("tt0111161"); !ok || got.Rating != 9.4 {
		t.Fatalf("reopened %+v %v", got, ok)
	}
	if d2.state.ETag != `"v2"` || d2.state.Titles != 1 {
		t.Fatalf("state %+v", d2.state)
	}

	// a broken download keeps the dataset
	srv.set([]byte("not gzip"), `"v3"`)
	if err := d.Refresh(context.Background()); err == nil {
		t.Fatal("broken gzip accepted")
	}
	if got, _ := d.Lookup("tt0111161"); got.Rating != 9.4 {
		t.Fatalf("after a broken download %+v", got)
	}
}

// lookupNoRefresh looks up without starting a background refresh.
func (d *Dataset) lookupNoRefresh(id string) (IMDbRating, bool) {
	d.mu.Lock()
	d.open()
	d.state.CheckedAt = d.clock()
	d.mu.Unlock()
	return d.Lookup(id)
}

func TestDatasetStaleLookupRefreshesInBackground(t *testing.T) {
	srv := &datasetServer{}
	srv.set(gz(t, tsv), `"v1"`)
	ts := httptest.NewServer(srv)
	defer ts.Close()
	var mu sync.Mutex
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	done := make(chan struct{}, 4)
	d := &Dataset{URL: ts.URL, Dir: t.TempDir(), now: now, bgDone: done}
	defer d.Close()
	wait := func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("no background refresh")
		}
	}

	// never downloaded: the first lookup has nothing yet and starts one
	if _, ok := d.Lookup("tt0111161"); ok {
		t.Fatal("rating before the download")
	}
	wait()
	if r, ok := d.Lookup("tt0111161"); !ok || r.Rating != 9.3 {
		t.Fatalf("after the download %+v %v", r, ok)
	}
	// within a day: no request at all
	advance(23 * time.Hour)
	d.Lookup("tt0111161")
	if n := srv.requests.Load(); n != 1 {
		t.Fatalf("requests within a day: %d", n)
	}
	// a day later: a conditional GET (304), the data stays
	advance(2 * time.Hour)
	d.Lookup("tt0111161")
	wait()
	if srv.requests.Load() != 2 || srv.downloads.Load() != 1 {
		t.Fatalf("requests %d downloads %d", srv.requests.Load(), srv.downloads.Load())
	}
	// a failure is retried after an hour, not on every lookup
	srv.fail.Store(true)
	advance(25 * time.Hour)
	d.Lookup("tt0111161")
	wait()
	d.Lookup("tt0111161")
	advance(30 * time.Minute)
	d.Lookup("tt0111161")
	if n := srv.requests.Load(); n != 3 {
		t.Fatalf("requests after a failure: %d", n)
	}
	if r, ok := d.Lookup("tt0111161"); !ok || r.Rating != 9.3 {
		t.Fatal("the dataset lost on a failed refresh")
	}
	advance(31 * time.Minute)
	d.Lookup("tt0111161")
	wait()
	if n := srv.requests.Load(); n != 4 {
		t.Fatalf("requests after the retry delay: %d", n)
	}
}

type fakeRT struct {
	calls  atomic.Int32
	scores map[string]wikidata.ReviewScores
	err    error
	delay  time.Duration
}

func (f *fakeRT) RottenTomatoes(ctx context.Context, id string) (wikidata.ReviewScores, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.scores[id], f.err
}

func TestServiceRottenTomatoesCache(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rt := &fakeRT{scores: map[string]wikidata.ReviewScores{"tt15239678": {Tomatometer: 92, Audience: 95}}}
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s := &Service{RT: rt, DB: database, now: func() time.Time { return clock }}
	ctx := context.Background()

	r := s.Get(ctx, "tt15239678")
	if r.RottenTomatoes == nil || *r.RottenTomatoes != (RTScores{92, 95}) || r.IMDb != nil {
		t.Fatalf("%+v", r)
	}
	s.Get(ctx, "tt15239678")
	if rt.calls.Load() != 1 {
		t.Fatalf("cached score asked again: %d", rt.calls.Load())
	}
	// none: cached as none for a week
	if r := s.Get(ctx, "tt0903747"); r.RottenTomatoes != nil {
		t.Fatalf("none: %+v", r)
	}
	s.Get(ctx, "tt0903747")
	if rt.calls.Load() != 2 {
		t.Fatalf("calls %d", rt.calls.Load())
	}
	clock = clock.Add(8 * 24 * time.Hour)
	s.Get(ctx, "tt0903747")  // a miss is asked again after a week
	s.Get(ctx, "tt15239678") // a score after a month
	if rt.calls.Load() != 3 {
		t.Fatalf("calls %d", rt.calls.Load())
	}
	// stale and Wikidata down: the old score
	clock = clock.Add(31 * 24 * time.Hour)
	rt.err = errors.New("down")
	if r := s.Get(ctx, "tt15239678"); r.RottenTomatoes == nil || r.RottenTomatoes.Tomatometer != 92 {
		t.Fatalf("stale on failure: %+v", r)
	}
	// unknown and down: nothing, and nothing cached
	if r := s.Get(ctx, "tt1160419"); r.RottenTomatoes != nil {
		t.Fatalf("%+v", r)
	}
	var n int
	database.QueryRow(`SELECT count(*) FROM rt_scores WHERE imdb_id='tt1160419'`).Scan(&n)
	if n != 0 {
		t.Fatal("a failure was cached")
	}
	// not an IMDb id: no lookup at all
	before := rt.calls.Load()
	if r := s.Get(ctx, "x' OR 1"); r != (Ratings{}) || rt.calls.Load() != before {
		t.Fatalf("invalid id: %+v", r)
	}
}

func TestServiceSlowWikidataIsCachedForTheNextRequest(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rt := &fakeRT{scores: map[string]wikidata.ReviewScores{"tt1160419": {Tomatometer: 83}}, delay: 200 * time.Millisecond}
	s := &Service{RT: rt, DB: database}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if r := s.Get(ctx, "tt1160419"); r.RottenTomatoes != nil {
		t.Fatalf("not waited for: %+v", r)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r := s.Get(context.Background(), "tt1160419"); r.RottenTomatoes != nil && r.RottenTomatoes.Tomatometer == 83 {
			if rt.calls.Load() != 1 {
				t.Fatalf("calls %d", rt.calls.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the score never reached the cache")
}

// A failed lookup is not tried again for half an hour, and no more than
// rtLookups run at once, whatever the ids asked for.
func TestServiceFailuresBackOffAndLookupsAreCapped(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var mu sync.Mutex
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	rt := &fakeRT{err: errors.New("down")}
	s := &Service{RT: rt, DB: database, now: now}
	ctx := context.Background()
	s.Get(ctx, "tt1160419")
	s.Get(ctx, "tt1160419")
	if rt.calls.Load() != 1 {
		t.Fatalf("a failure asked again at once: %d", rt.calls.Load())
	}
	mu.Lock()
	clock = clock.Add(31 * time.Minute)
	mu.Unlock()
	s.Get(ctx, "tt1160419")
	if rt.calls.Load() != 2 {
		t.Fatalf("not retried after the back-off: %d", rt.calls.Load())
	}

	release := make(chan struct{})
	slow := &blockingRT{release: release}
	s2 := &Service{RT: slow, DB: database}
	for i := 0; i < 3*rtLookups; i++ {
		short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		s2.Get(short, fmt.Sprintf("tt%07d", 1000000+i))
		cancel()
	}
	if n := slow.calls.Load(); n != rtLookups {
		t.Fatalf("%d lookups at once, want %d", n, rtLookups)
	}
	close(release)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		s2.mu.Lock()
		n := len(s2.inflight)
		s2.mu.Unlock()
		if n == 0 {
			return
		}
	}
	t.Fatal("lookups never ended")
}

type blockingRT struct {
	calls   atomic.Int32
	release chan struct{}
}

func (b *blockingRT) RottenTomatoes(ctx context.Context, id string) (wikidata.ReviewScores, error) {
	b.calls.Add(1)
	<-b.release
	return wikidata.ReviewScores{}, nil
}
