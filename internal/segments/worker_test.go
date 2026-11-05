package segments

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

type fixture struct {
	w        *Worker
	repo     *library.Repo
	eps      []string // episode paths, E01..E03
	movie    string
	tidb     map[string]int // requests by query
	idb      map[string]int
	fpCalls  int
	chCalls  int
	blCalls  int
	fpTracks map[int]int
	mu       sync.Mutex
}

const probeJSON = `{"Container":"matroska","DurationMs":1300000,"Audio":[{"Index":1,"Lang":"rus","Codec":"ac3"},{"Index":2,"Lang":"eng","Codec":"ac3"}]}`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	fx := &fixture{repo: library.NewRepo(d), tidb: map[string]int{}, idb: map[string]int{}, fpTracks: map[int]int{}}
	dir := t.TempDir()
	touch := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for i := 1; i <= 3; i++ {
		p := touch("Show.S01E0" + string(rune('0'+i)) + ".mkv")
		fx.eps = append(fx.eps, p)
		if _, err := fx.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Show", SeriesTmdbID: 1418,
			SeriesExtIDs: map[string]string{"imdb_id": "tt0898266"}, Season: 1, Episode: i,
			File: library.FileInput{Path: p, Size: 100, Mtime: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	fx.movie = touch("Film.mkv")
	if _, err := fx.repo.UpsertMovie(ctx, library.MovieInput{Title: "Film", TmdbID: 603, File: library.FileInput{Path: fx.movie, Size: 100, Mtime: 1}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range append(append([]string{}, fx.eps...), fx.movie) {
		f, err := fx.repo.FileByPath(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.repo.SetProbe(ctx, f.ID, probeJSON, 1_300_000); err != nil {
			t.Fatal(err)
		}
	}

	tidb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		fx.tidb[r.URL.Query().Get("tmdb_id")+"/"+r.URL.Query().Get("episode")]++
		fx.mu.Unlock()
		if r.URL.Query().Get("episode") == "2" {
			w.Write([]byte(`{"tvdb_id":1,"type":"tv","intro":[{"start_ms":100000,"end_ms":130000}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(tidb.Close)
	idb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		fx.idb[r.URL.Query().Get("imdb_id")+"/"+r.URL.Query().Get("episode")]++
		fx.mu.Unlock()
		if r.URL.Query().Get("episode") == "2" {
			w.Write([]byte(`{"intro":null,"recap":null,"outro":{"start_ms":1250000,"end_ms":null}}`))
			return
		}
		w.Write([]byte(`{"intro":null,"recap":null,"outro":null}`))
	}))
	t.Cleanup(idb.Close)

	r := rand.New(rand.NewSource(9))
	theme, outro := noise(r, 40), noise(r, 45)
	heads, tails := map[string][]uint32{}, map[string][]uint32{}
	for i, p := range fx.eps {
		h := noise(r, HeadSec(1300))
		put(r, h, float64(60+10*i), theme)
		heads[p] = h
		tl := noise(r, TailSec(1300, false))
		put(r, tl, TailSec(1300, false)-50, outro)
		tails[p] = tl
	}
	yes := true
	fx.w = &Worker{Store: Store{DB: d}, Chromaprint: &yes,
		Online: &Online{TheIntroDB: tidb.URL, IntroDB: idb.URL},
		Chapters: func(_ context.Context, path string) ([]media.Chapter, error) {
			fx.mu.Lock()
			fx.chCalls++
			fx.mu.Unlock()
			if path == fx.eps[0] {
				return []media.Chapter{{Title: "Opening", StartMs: 30_000}, {Title: "Part 1", StartMs: 70_000},
					{Title: "End Credits", StartMs: 1_240_000}}, nil
			}
			return nil, nil
		},
		Fingerprint: func(_ context.Context, path string, idx int, from, dur float64) ([]uint32, error) {
			fx.mu.Lock()
			fx.fpCalls++
			fx.fpTracks[idx]++
			fx.mu.Unlock()
			if from == 0 {
				return heads[path], nil
			}
			return tails[path], nil
		},
		Black: func(_ context.Context, path string, from, dur, minSec float64, keyOnly bool) ([]media.BlackRun, error) {
			fx.mu.Lock()
			fx.blCalls++
			fx.mu.Unlock()
			if path == fx.movie {
				return []media.BlackRun{{Start: 1200, End: 1299}}, nil
			}
			return nil, nil
		},
	}
	return fx
}

func (fx *fixture) play(t *testing.T, path string, movie bool) []Play {
	t.Helper()
	f, err := fx.repo.FileByPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return fx.w.ForPlay(context.Background(), f.ID, 1_300_000, movie)
}

func TestWorkerPass(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	if got := fx.play(t, fx.eps[0], false); len(got) != 0 {
		t.Fatalf("before a pass: %+v", got)
	}
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	// E01: its chapters (the databases are still asked: for a recap)
	if got := fx.play(t, fx.eps[0], false); len(got) != 2 || got[0] != (Play{Intro, 30, 70}) || got[1] != (Play{Credits, 1240, 1300}) {
		t.Errorf("E01 %+v", got)
	}
	// E02: the intro from TheIntroDB, the credits from IntroDB
	if got := fx.play(t, fx.eps[1], false); len(got) != 2 || got[0] != (Play{Intro, 100, 130}) || got[1] != (Play{Credits, 1250, 1300}) {
		t.Errorf("E02 %+v", got)
	}
	// E03: the analysis (theme at 80 s, 40 s; the outro 50 s before the end)
	got := fx.play(t, fx.eps[2], false)
	if len(got) != 2 || got[0].Kind != Intro || !near(got[0].StartSec, 80, 0.3) || !near(got[0].EndSec, 120, 0.5) ||
		got[1].Kind != Credits || !near(got[1].StartSec, 1250, 0.3) || got[1].EndSec != 1300 {
		t.Errorf("E03 %+v", got)
	}
	// the film: credits from black frames
	if got := fx.play(t, fx.movie, true); len(got) != 1 || got[0] != (Play{Credits, 1200, 1300}) {
		t.Errorf("film %+v", got)
	}
	if fx.tidb["1418/1"] != 1 || fx.tidb["1418/2"] != 1 || fx.tidb["1418/3"] != 1 || fx.tidb["603/"] != 1 {
		t.Errorf("TheIntroDB requests %v", fx.tidb)
	}
	// E01: its chapters give intro and credits, TheIntroDB answered: IntroDB is not asked just for a recap
	if fx.idb["tt0898266/1"] != 0 || fx.idb["tt0898266/2"] != 1 || fx.idb["tt0898266/3"] != 1 || len(fx.idb) != 2 {
		t.Errorf("IntroDB requests %v", fx.idb)
	}
	if fx.fpTracks[1] != 0 || fx.fpTracks[2] != 6 {
		t.Errorf("fingerprinted tracks %v (the English one, head and tail of 3 episodes)", fx.fpTracks)
	}

	// a second pass has nothing to do
	calls := fx.fpCalls
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls != calls || fx.tidb["1418/3"] != 1 || fx.idb["tt0898266/3"] != 1 {
		t.Errorf("second pass worked again: fp %d→%d, %v %v", calls, fx.fpCalls, fx.tidb, fx.idb)
	}

	// a replaced file starts over (the online answers come from the store)
	if _, err := fx.w.Store.DB.Exec(`UPDATE files SET size=200 WHERE path=?`, fx.eps[2]); err != nil {
		t.Fatal(err)
	}
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls == calls || fx.tidb["1418/3"] != 1 {
		t.Errorf("replaced file: fp %d→%d, %v", calls, fx.fpCalls, fx.tidb)
	}
	if got := fx.play(t, fx.eps[2], false); len(got) != 2 {
		t.Errorf("E03 again %+v", got)
	}
}

// A 429 pauses the lookups but not the analysis; the files are asked when
// the lookups resume.
func TestWorkerOnlineLimitedDoesNotHoldTheAnalysis(t *testing.T) {
	fx := newFixture(t)
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer limited.Close()
	fx.w.Online.TheIntroDB = limited.URL
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx.w.resumeAt.IsZero() {
		t.Fatal("not paused")
	}
	if got := fx.play(t, fx.eps[2], false); len(got) != 2 {
		t.Fatalf("E03 not analysed while the lookups wait: %+v", got)
	}
	var due int
	fx.w.Store.DB.QueryRow(`SELECT COUNT(*) FROM segment_status WHERE online_key=''`).Scan(&due)
	if due < 3 {
		t.Fatalf("the paused files are marked looked up: %d left", due)
	}
}

// A bad answer about one episode is retried for it alone, later; it
// neither pauses the lookups nor holds the analysis.
func TestWorkerOnlineBadAnswerRetriedPerFile(t *testing.T) {
	fx := newFixture(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("episode") == "2" {
			w.Write([]byte(`{"intro": "garbage"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer bad.Close()
	fx.w.Online.TheIntroDB = bad.URL
	now := time.Unix(1_800_000_000, 0)
	fx.w.Now = func() time.Time { return now }
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fx.w.resumeAt.IsZero() {
		t.Fatal("one bad answer paused the lookups")
	}
	var fails int
	var retry int64
	fx.w.Store.DB.QueryRow(`SELECT online_fails, online_retry FROM segment_status s JOIN files f ON f.id=s.file_id WHERE f.path=?`, fx.eps[1]).Scan(&fails, &retry)
	if fails != 1 || retry != now.Add(time.Hour).Unix() {
		t.Fatalf("fails %d retry %d", fails, retry)
	}
	if got := fx.play(t, fx.eps[1], false); len(got) != 2 {
		t.Fatalf("E02 not analysed: %+v", got)
	}
}

// A chapter read that fails is retried later, not stored as done.
func TestWorkerChaptersFailureRetried(t *testing.T) {
	fx := newFixture(t)
	fx.w.Chapters = func(context.Context, string) ([]media.Chapter, error) { return nil, errors.New("ffprobe: timeout") }
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	var v, fails int
	fx.w.Store.DB.QueryRow(`SELECT chapters_v, chapters_fails FROM segment_status s JOIN files f ON f.id=s.file_id WHERE f.path=?`, fx.eps[0]).Scan(&v, &fails)
	if v != 0 || fails != 1 {
		t.Fatalf("chapters_v %d fails %d", v, fails)
	}
}

// A play asks only for the cheap steps (chapters, online), once an hour:
// the analysis waits for a full pass.
func TestWorkerPlayAsksOnlyCheapSteps(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	fx.w.init()
	fx.play(t, fx.eps[1], false)
	select {
	case <-fx.w.light:
	default:
		t.Fatal("a play of a file never looked at asked for nothing")
	}
	if err := fx.w.pass(ctx, false); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls != 0 {
		t.Fatalf("a play started the analysis: %d fingerprints", fx.fpCalls)
	}
	if got := fx.play(t, fx.eps[1], false); len(got) != 2 || got[0] != (Play{Intro, 100, 130}) {
		t.Fatalf("E02 after the light pass: %+v", got)
	}
	// a file that gets no status asks again only after wantEvery
	if _, err := fx.w.Store.DB.Exec(`DELETE FROM segment_status`); err != nil {
		t.Fatal(err)
	}
	fx.play(t, fx.eps[1], false)
	select {
	case <-fx.w.light:
		t.Fatal("asked again at once")
	default:
	}
}

// Nothing is fingerprinted while something plays.
func TestWorkerWaitsForPlayback(t *testing.T) {
	fx := newFixture(t)
	fx.w.Busy = func() bool { return true }
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	fx.w.Pass(ctx)
	if fx.fpCalls != 0 {
		t.Fatalf("fingerprinted during playback: %d", fx.fpCalls)
	}
}

// A direct play still streaming an hour after its request began (one long
// range read) holds the analysis as main wires it (media.Activity.Reading).
func TestWorkerWaitsForALongStreamRead(t *testing.T) {
	fx := newFixture(t)
	var mu sync.Mutex
	clock := time.Unix(1000, 0)
	a := &media.Activity{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }}
	fx.w.Busy = func() bool { return a.Reading(PlaybackIdle) }
	a.Begin() // the stream's request starts and keeps reading
	mu.Lock()
	clock = clock.Add(time.Hour)
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	fx.w.Pass(ctx)
	if fx.fpCalls != 0 {
		t.Fatalf("fingerprinted during an hour-long stream read: %d", fx.fpCalls)
	}
	a.End()
	mu.Lock()
	clock = clock.Add(PlaybackIdle + time.Second)
	mu.Unlock()
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls == 0 {
		t.Fatal("not fingerprinted once the read ended and the quiet passed")
	}
}

// Fingerprints are cached: a new episode re-analyses its season reading
// only itself; a file gone from the library loses its cache.
func TestWorkerFingerprintCache(t *testing.T) {
	fx := newFixture(t)
	fx.w.CacheDir = t.TempDir()
	ctx := context.Background()
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls != 6 {
		t.Fatalf("first pass: %d", fx.fpCalls)
	}
	p := filepath.Join(filepath.Dir(fx.eps[0]), "Show.S01E04.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Show", SeriesTmdbID: 1418, Season: 1, Episode: 4,
		File: library.FileInput{Path: p, Size: 100, Mtime: 1}}); err != nil {
		t.Fatal(err)
	}
	f4, _ := fx.repo.FileByPath(ctx, p)
	fx.repo.SetProbe(ctx, f4.ID, probeJSON, 1_300_000)
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls != 8 {
		t.Fatalf("a new episode re-read the season: %d fingerprints", fx.fpCalls)
	}
	n := func() int { e, _ := os.ReadDir(fx.w.CacheDir); return len(e) }
	before := n()
	if _, err := fx.w.Store.DB.Exec(`UPDATE files SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, f4.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if after := n(); after >= before {
		t.Fatalf("cache of a gone file kept: %d → %d", before, after)
	}
}

// An episode without a number is not looked up online (it would be asked
// as episode 0).
func TestWorkerUnnumberedEpisodeNotLookedUp(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.w.Store.DB.Exec(`UPDATE items SET episode_no=NULL WHERE kind='episode' AND episode_no=3`); err != nil {
		t.Fatal(err)
	}
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx.tidb["1418/0"] != 0 || fx.idb["tt0898266/0"] != 0 || fx.tidb["1418/"] != 0 {
		t.Fatalf("asked as episode 0: %v %v", fx.tidb, fx.idb)
	}
}

func TestWorkerWithoutOnline(t *testing.T) {
	fx := newFixture(t)
	fx.w.Online = nil
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fx.play(t, fx.eps[1], false); len(got) != 2 || got[0].Kind != Intro || !near(got[0].StartSec, 70, 0.3) {
		t.Fatalf("E02 from the analysis: %+v", got)
	}
}

func TestAudioTrack(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "rus"}, {Index: 2, Lang: "eng", Title: "Commentary"}, {Index: 3, Lang: "eng"}}}
	if got := audioTrack(p); got != 3 {
		t.Fatal(got)
	}
	if got := audioTrack(media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "rus"}}}); got != 1 {
		t.Fatal(got)
	}
	if got := audioTrack(media.ProbeResult{}); got != -1 {
		t.Fatal(got)
	}
}

func TestUnprobedCounted(t *testing.T) {
	fx := newFixture(t)
	if n, err := fx.w.Store.unprobed(context.Background()); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := fx.w.Store.DB.Exec(`UPDATE files SET duration_ms=NULL WHERE path=?`, fx.eps[0]); err != nil {
		t.Fatal(err)
	}
	if n, _ := fx.w.Store.unprobed(context.Background()); n != 1 {
		t.Fatalf("%d", n)
	}
}

// A full pass waiting for playback to end still serves a play's request:
// the online lookup runs meanwhile, the disk is not touched (no chapters,
// no fingerprints).
func TestWorkerLightPassWhileFullPassWaits(t *testing.T) {
	fx := newFixture(t)
	fx.w.Busy = func() bool { return true }
	fx.w.init()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { fx.w.Pass(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	fx.play(t, fx.eps[1], false)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for {
		fx.mu.Lock()
		n := fx.tidb["1418/2"]
		fx.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the play's lookup waited for the full pass: %v", fx.tidb)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if fx.chCalls != 0 || fx.fpCalls != 0 || fx.blCalls != 0 {
		t.Fatalf("read the disk during playback: chapters %d fingerprints %d black %d", fx.chCalls, fx.fpCalls, fx.blCalls)
	}
}

// A play's light pass reads no chapters while something plays: they wait
// for an idle pass.
func TestWorkerLightPassDefersChapters(t *testing.T) {
	fx := newFixture(t)
	busy := true
	fx.w.Busy = func() bool { return busy }
	fx.w.init()
	fx.play(t, fx.eps[0], false)
	if err := fx.w.pass(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if fx.chCalls != 0 {
		t.Fatalf("chapters read during playback: %d", fx.chCalls)
	}
	busy = false
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fx.play(t, fx.eps[0], false); len(got) != 2 || got[0] != (Play{Intro, 30, 70}) {
		t.Fatalf("chapters not read in the idle pass: %+v", got)
	}
}

// Chapters give intro and credits for a whole season: the analysis only
// looks for a recap, in the head windows, and only with three or more
// episodes.
func TestWorkerRecapOnlyAnalysis(t *testing.T) {
	fx := newFixture(t)
	fx.w.Online = nil
	fx.w.Chapters = func(context.Context, string) ([]media.Chapter, error) {
		return []media.Chapter{{Title: "Opening", StartMs: 30_000}, {Title: "Part 1", StartMs: 70_000}, {Title: "End Credits", StartMs: 1_240_000}}, nil
	}
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx.fpCalls != 3 {
		t.Fatalf("fingerprints %d, want the 3 heads only", fx.fpCalls)
	}
	if fx.blCalls != 0 { // no tail windows (the film's chapters give its credits too)
		t.Fatalf("black runs %d", fx.blCalls)
	}
	// two episodes: nothing at all
	fx2 := newFixture(t)
	fx2.w.Online = nil
	fx2.w.Chapters = fx.w.Chapters
	if _, err := fx2.w.Store.DB.Exec(`UPDATE files SET missing_since='2026-01-01T00:00:00Z' WHERE path=?`, fx2.eps[2]); err != nil {
		t.Fatal(err)
	}
	if err := fx2.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx2.fpCalls != 0 {
		t.Fatalf("two covered episodes fingerprinted: %d", fx2.fpCalls)
	}
}
