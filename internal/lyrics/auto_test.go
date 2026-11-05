package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

// fakeLRCLIB answers by track name: "synced", "plain" have words, anything
// else none; down makes every request a 502.
type fakeLRCLIB struct {
	srv  *httptest.Server
	hits atomic.Int32
	mu   sync.Mutex
	down bool
	got  []string // track names asked for (/api/get)
}

func newFakeLRCLIB(t *testing.T) *fakeLRCLIB {
	f := &fakeLRCLIB{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.mu.Lock()
		down := f.down
		if r.URL.Path == "/api/get" {
			f.got = append(f.got, r.URL.Query().Get("track_name"))
		}
		f.mu.Unlock()
		if r.UserAgent() != "ua-test" {
			t.Errorf("user agent %q", r.UserAgent())
		}
		if down {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		name := r.URL.Query().Get("track_name")
		switch {
		case r.URL.Path == "/api/search":
			w.Write([]byte(`[]`))
		case name == "synced":
			w.Write([]byte(`{"syncedLyrics":"[00:01.00]Раз","plainLyrics":"Раз"}`))
		case name == "plain":
			w.Write([]byte(`{"plainLyrics":"Два"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLRCLIB) setDown(v bool) { f.mu.Lock(); f.down = v; f.mu.Unlock() }

type autoEnv struct {
	repo *library.Repo
	auto *Auto
	now  time.Time
	f    *fakeLRCLIB
}

func newAutoEnv(t *testing.T) *autoEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &autoEnv{repo: library.NewRepo(d), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), f: newFakeLRCLIB(t)}
	c := NewAt(e.f.srv.URL, "ua-test")
	e.auto = &Auto{Client: c, Repo: e.repo, Now: func() time.Time { return e.now }}
	return e
}

// album adds tracks with these titles (and file words) and returns their ids.
func (e *autoEnv) album(t *testing.T, name string, titles []string, words map[string]string) []int64 {
	t.Helper()
	var in []library.TrackInput
	for i, ti := range titles {
		in = append(in, library.TrackInput{File: library.FileInput{Path: "/music/" + name + "/" + strconv.Itoa(i) + ".flac", Size: 1, Mtime: 1},
			AlbumDir: "/music/" + name, Album: name, Artist: "Кино", Title: ti, Disc: 1, Track: i + 1, Lyrics: words[ti]})
	}
	id, err := e.repo.UpsertAlbumTracks(context.Background(), "Кино", in)
	if err != nil {
		t.Fatal(err)
	}
	trs, err := e.repo.AlbumTracks(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, tr := range trs {
		ids = append(ids, tr.ID)
	}
	return ids
}

func (e *autoEnv) stored(t *testing.T, id int64) library.StoredLyrics {
	t.Helper()
	s, ok, err := e.repo.TrackLyrics(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("track %d: nothing stored (%v)", id, err)
	}
	return s
}

// Words found are kept (synced preferred), «none» is kept for NoneTTL,
// and a pass over a done library asks nothing.
func TestAutoPersistsFoundAndNone(t *testing.T) {
	e := newAutoEnv(t)
	ids := e.album(t, "Альбом", []string{"synced", "plain", "nothing"}, nil)
	ctx := context.Background()
	if err := e.auto.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if s := e.stored(t, ids[0]); s.Status != library.LyricsFound || s.Synced != "[00:01.00]Раз" || s.Source != "lrclib" || s.Fresh(e.now.Add(1000*24*time.Hour)) != true {
		t.Fatalf("synced: %+v", s)
	}
	if s := e.stored(t, ids[1]); s.Status != library.LyricsFound || s.Plain != "Два" || s.Synced != "" || !s.NextTry.Equal(e.now.Add(PlainTTL)) {
		t.Fatalf("plain: %+v", s)
	}
	if s := e.stored(t, ids[2]); s.Status != library.LyricsNone || !s.NextTry.Equal(e.now.Add(NoneTTL)) {
		t.Fatalf("none: %+v", s)
	}
	n := e.f.hits.Load()
	e.now = e.now.Add(30 * 24 * time.Hour)
	if err := e.auto.Pass(ctx); err != nil || e.f.hits.Load() != n {
		t.Fatalf("second pass asked again: %v, %d → %d", err, n, e.f.hits.Load())
	}
	// «none» is asked again after its TTL
	e.now = e.now.Add(NoneTTL)
	e.f.mu.Lock()
	e.f.got = nil
	e.f.mu.Unlock()
	if err := e.auto.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.f.got) != 1 || e.f.got[0] != "nothing" {
		t.Fatalf("after the TTL asked %v, want [nothing]", e.f.got)
	}
}

// A file with synced words is never asked about; one with plain words is
// (LRCLIB may have the synced version).
func TestAutoSkipsSyncedFileWords(t *testing.T) {
	e := newAutoEnv(t)
	ids := e.album(t, "Альбом", []string{"synced", "plain"}, map[string]string{"synced": "[00:02.00]Своё", "plain": "Своё"})
	if err := e.auto.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.f.got) != 1 || e.f.got[0] != "plain" {
		t.Fatalf("asked %v, want [plain]", e.f.got)
	}
	if _, ok, _ := e.repo.TrackLyrics(context.Background(), ids[0]); ok {
		t.Fatal("synced file words: nothing must be stored")
	}
}

// LRCLIB down: the failures are stored with backoff, the queue pauses after
// failRun of them (not a request per track), and once it answers again the
// words come in.
func TestAutoRetriesErrorsWithBackoff(t *testing.T) {
	e := newAutoEnv(t)
	ids := e.album(t, "Альбом", []string{"synced", "plain", "a", "b", "c", "d"}, nil)
	ctx := context.Background()
	e.f.setDown(true)
	if err := e.auto.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	asked := map[string]bool{}
	for _, n := range e.f.got {
		asked[n] = true
	}
	if len(asked) != failRun {
		t.Fatalf("asked %v while down, want %d tracks (paused)", e.f.got, failRun)
	}
	if !e.auto.resumeAt.Equal(e.now.Add(pauseBase)) {
		t.Fatalf("paused until %v", e.auto.resumeAt)
	}
	// newest first: the last tracks of the album were tried
	tried := e.stored(t, ids[len(ids)-1])
	if tried.Status != library.LyricsError || tried.Attempts != 1 || !tried.NextTry.Equal(e.now.Add(errorBase)) {
		t.Fatalf("error: %+v", tried)
	}
	// paused: nothing asked
	n := e.f.hits.Load()
	if err := e.auto.Pass(ctx); err != nil || e.f.hits.Load() != n {
		t.Fatalf("asked while paused")
	}
	// still down after the pause: the pause doubles; the failed tracks back off
	e.now = e.now.Add(pauseBase)
	if err := e.auto.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.auto.resumeAt.Equal(e.now.Add(2 * pauseBase)) {
		t.Fatalf("second pause until %v", e.auto.resumeAt)
	}
	// back up, everything due: all words in
	e.f.setDown(false)
	e.now = e.now.Add(48 * time.Hour)
	if err := e.auto.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if s := e.stored(t, ids[0]); s.Status != library.LyricsFound || s.Synced == "" || s.Attempts != 0 {
		t.Fatalf("after the outage: %+v", s)
	}
	for _, id := range ids[2:] {
		if s := e.stored(t, id); s.Status != library.LyricsNone {
			t.Fatalf("track %d after the outage: %+v", id, s)
		}
	}
}

// Run passes at once, then again on Kick (a scan added tracks).
func TestAutoKickRunsAPass(t *testing.T) {
	e := newAutoEnv(t)
	e.auto.Now = nil
	ids := e.album(t, "Альбом", []string{"synced"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.auto.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := e.repo.TrackLyrics(context.Background(), ids[0]); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first pass did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	more := e.album(t, "Второй", []string{"plain"}, nil)
	e.auto.Kick()
	for {
		if _, ok, _ := e.repo.TrackLyrics(context.Background(), more[0]); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("kick did not run a pass")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}
