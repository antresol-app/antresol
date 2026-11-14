package voclean

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/media"
)

type wfix struct {
	t     *testing.T
	d     *sql.DB
	w     *Worker
	now   time.Time
	files map[string]int64 // by name
	mu    sync.Mutex
	calls []string // "measure name/idx", "clean name/idx"
	// eligible / cleanErr per "name/idx" (default: eligible, no error)
	notEligible map[string]bool
	cleanErr    map[string]error
	// onClean runs inside the fake cleaning (a download arriving, the
	// disk filling up meanwhile)
	onClean func()
	// gated records, per "name", whether its read yielded to playback
	gated map[string]bool
}

// probeOf: a video file's probe, its audio tracks given as lang:title
// (5.1) or lang:title:channels.
func probeOf(tracks ...string) string {
	p := media.ProbeResult{Container: "matroska", DurationMs: 3_600_000, Video: &media.VideoStream{Codec: "h264", Width: 1920, Height: 1080}}
	for i, tr := range tracks {
		f := strings.SplitN(tr, ":", 3)
		ch := 6
		if len(f) == 3 {
			ch, _ = strconv.Atoi(f[2])
		}
		f = append(f, "")
		p.Audio = append(p.Audio, media.AudioStream{Index: i + 1, Codec: "eac3", Lang: f[0], Title: f[1], Channels: ch, SampleRate: 48000})
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func newWFix(t *testing.T) *wfix {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	fx := &wfix{t: t, d: d, now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), files: map[string]int64{},
		notEligible: map[string]bool{}, cleanErr: map[string]error{}, gated: map[string]bool{}}
	fx.exec(`INSERT INTO users(id, name, avatar) VALUES(1,'u','a'),(2,'v','a')`)
	fx.w = &Worker{DB: d, DataDir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return fx.now },
		StartOf: func(context.Context, string) (float64, error) { return 21, nil },
		Eligible: func(_ context.Context, in Input, pairs []Pair) ([]Measure, error) {
			var out []Measure
			for _, p := range pairs {
				k := fx.key(in.Path, p.Vo.Index)
				fx.record("measure " + k)
				if p.Orig.Lang == "rus" || p.Orig.Lang == "" { // an original is of another known language
					t.Errorf("%s: original %+v", k, p.Orig)
				}
				if fx.notEligible[k] {
					out = append(out, Measure{FrontResidual: [2]float64{0.98, 0.97}, Why: "front residual"})
					continue
				}
				out = append(out, Measure{LagMs: 0, FrontResidual: [2]float64{0.15, 0.15}, CentreResidual: 0.8, Eligible: true})
			}
			return out, nil
		},
		Clean: func(_ context.Context, in Input, jobs []Job) []error {
			errs := make([]error, len(jobs))
			for i, j := range jobs {
				k := fx.key(in.Path, j.Vo.Index)
				fx.record("clean " + k)
				fx.gated[strings.Split(k, "/")[0]] = in.Busy != nil
				errs[i] = fx.cleanErr[k]
			}
			if fx.onClean != nil {
				fx.onClean()
			}
			for i, j := range jobs {
				if errs[i] == nil {
					errs[i] = os.WriteFile(j.Out, []byte("mka"), 0o644)
				}
			}
			return errs
		}}
	return fx
}

func (fx *wfix) exec(q string, args ...any) sql.Result {
	fx.t.Helper()
	r, err := fx.d.Exec(q, args...)
	if err != nil {
		fx.t.Fatalf("%s: %v", q, err)
	}
	return r
}

func (fx *wfix) record(s string) {
	fx.mu.Lock()
	fx.calls = append(fx.calls, s)
	fx.mu.Unlock()
}

func (fx *wfix) takeCalls() []string {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	c := fx.calls
	fx.calls = nil
	return c
}

func (fx *wfix) key(path string, idx int) string {
	for name, id := range fx.files {
		var p string
		if err := fx.d.QueryRow(`SELECT path FROM files WHERE id=?`, id).Scan(&p); err == nil && p == path {
			return fmt.Sprintf("%s/%d", name, idx)
		}
	}
	return fmt.Sprintf("%s/%d", path, idx)
}

// item adds an item (kind, parent, added day) and returns its id.
func (fx *wfix) item(kind string, parent int64, title string, day int, season, episode int) int64 {
	var p any
	if parent != 0 {
		p = parent
	}
	r := fx.exec(`INSERT INTO items(kind, parent_id, title, added_at, season_no, episode_no) VALUES(?,?,?,?,?,?)`,
		kind, p, title, time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), season, episode)
	id, _ := r.LastInsertId()
	return id
}

// file adds a file on disk of an item with the probe.
func (fx *wfix) file(name string, item int64, probe string) int64 {
	p := filepath.Join(fx.t.TempDir(), name+".mkv")
	if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
		fx.t.Fatal(err)
	}
	r := fx.exec(`INSERT INTO files(item_id, path, size, mtime, duration_ms, probe_json) VALUES(?,?,?,?,?,?)`,
		item, p, 1000, 1700000000, 3_600_000, probe)
	id, _ := r.LastInsertId()
	fx.files[name] = id
	return id
}

func (fx *wfix) pass() {
	fx.t.Helper()
	if err := fx.w.Pass(context.Background()); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *wfix) state(name string, idx int) (state string, fails int, retry sql.NullString) {
	fx.t.Helper()
	err := fx.d.QueryRow(`SELECT state, fails, retry_at FROM cleaned_audio WHERE file_id=? AND vo_index=?`, fx.files[name], idx).
		Scan(&state, &fails, &retry)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, retry
	}
	if err != nil {
		fx.t.Fatal(err)
	}
	return
}

const lfEng = "rus:LostFilm"

func TestWorkerOrder(t *testing.T) {
	fx := newWFix(t)
	show := fx.item("series", 0, "Billions", 1, 0, 0)
	s1 := fx.item("season", show, "S1", 1, 1, 0)
	e1 := fx.item("episode", s1, "E1", 2, 1, 1)
	e2 := fx.item("episode", s1, "E2", 2, 1, 2)
	e3 := fx.item("episode", s1, "E3", 2, 1, 3)
	old := fx.item("movie", 0, "Old", 3, 0, 0)
	newest := fx.item("movie", 0, "Newest", 9, 0, 0)
	fx.file("e1", e1, probeOf(lfEng, "eng:"))
	fx.file("e2", e2, probeOf(lfEng, "eng:"))
	fx.file("e3", e3, probeOf(lfEng, "eng:"))
	fx.file("old", old, probeOf(lfEng, "eng:"))
	fx.file("newest", newest, probeOf(lfEng, "eng:"))
	fx.file("english", fx.item("movie", 0, "English", 8, 0, 0), probeOf("eng:"))        // nothing to clean
	fx.file("dub", fx.item("movie", 0, "Russian only", 8, 0, 0), probeOf("rus:Дубляж")) // no original
	// user 1 finished E1: «Продолжить» shows E2
	fx.exec(`INSERT INTO progress(user_id, item_id, position_ms, duration_ms, finished, updated_at) VALUES(1,?,3600000,3600000,1,'2026-11-01T00:00:00.000000000Z')`, e1)
	fx.pass()
	got := fx.takeCalls()
	// E2 («Продолжить») and the episode after it, then the newest first
	want := []string{"measure e2/1", "clean e2/1", "measure e3/1", "clean e3/1", "measure newest/1", "clean newest/1",
		"measure old/1", "clean old/1", "measure e1/1", "clean e1/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	if st, _, _ := fx.state("e2", 1); st != "done" {
		t.Fatalf("state %q", st)
	}
	// done: nothing more
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("second pass: %v", c)
	}
}

func TestWorkerInProgressAndNextEpisodeFirst(t *testing.T) {
	fx := newWFix(t)
	show := fx.item("series", 0, "Show", 1, 0, 0)
	s1 := fx.item("season", show, "S1", 1, 1, 0)
	e1 := fx.item("episode", s1, "E1", 2, 1, 1)
	e2 := fx.item("episode", s1, "E2", 2, 1, 2)
	newest := fx.item("movie", 0, "Newest", 9, 0, 0)
	fx.file("e1", e1, probeOf(lfEng, "eng:"))
	fx.file("e2", e2, probeOf(lfEng, "eng:"))
	fx.file("newest", newest, probeOf(lfEng, "eng:"))
	// user 2 is in the middle of E1: E1, then the episode after it
	fx.exec(`INSERT INTO progress(user_id, item_id, position_ms, duration_ms, finished, updated_at) VALUES(2,?,60000,3600000,0,'2026-11-01T00:00:00.000000000Z')`, e1)
	fx.pass()
	got := fx.takeCalls()
	want := []string{"measure e1/1", "clean e1/1", "measure e2/1", "clean e2/1", "measure newest/1", "clean newest/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
}

func TestWorkerStudioOrder(t *testing.T) {
	fx := newWFix(t)
	fx.w.Studios = func(context.Context) ([]string, error) { return []string{"LostFilm", "AlexFilm"}, nil }
	m := fx.item("movie", 0, "M", 1, 0, 0)
	// tracks: 1 an unlisted studio, 2 AlexFilm, 3 the original, 4 LostFilm,
	// 5 a studio-titled und track, 6 a commentary in English
	fx.file("m", m, probeOf("rus:NewStudio", "rus:Alex Film", "eng:", "rus:MVO LostFilm", "und:Кубик в Кубе", "eng:Commentary"))
	fx.pass()
	got := fx.takeCalls()
	want := []string{"measure m/4", "measure m/2", "measure m/1", "measure m/5", "clean m/4", "clean m/2", "clean m/1", "clean m/5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	var studio, lang, title string
	if err := fx.d.QueryRow(`SELECT studio, lang, title FROM cleaned_audio WHERE file_id=? AND vo_index=4`, fx.files["m"]).Scan(&studio, &lang, &title); err != nil {
		t.Fatal(err)
	}
	if studio != "LostFilm" || lang != "rus" || title != "MVO LostFilm" {
		t.Fatalf("row: %q %q %q", studio, lang, title)
	}
	tr, err := Tracks(context.Background(), fx.d, fx.files["m"])
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) != 4 || tr[0].VoIndex != 1 || tr[0].Method != "subtraction" || !strings.HasSuffix(tr[0].Path, fmt.Sprintf("%d-1.mka", fx.files["m"])) {
		t.Fatalf("tracks %+v", tr)
	}
}

func TestWorkerNotEligibleStoredNotRetried(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf("rus:Дубляж", "eng:"))
	fx.notEligible["m/1"] = true
	fx.pass()
	if c := fx.takeCalls(); !reflect.DeepEqual(c, []string{"measure m/1"}) {
		t.Fatalf("calls %v", c)
	}
	if st, _, _ := fx.state("m", 1); st != "not_eligible" {
		t.Fatalf("state %q", st)
	}
	var mj string
	fx.d.QueryRow(`SELECT measure_json FROM cleaned_audio WHERE file_id=?`, fx.files["m"]).Scan(&mj)
	var m Measure
	if err := json.Unmarshal([]byte(mj), &m); err != nil || m.FrontResidual[0] != 0.98 || m.Why == "" {
		t.Fatalf("measure %q: %+v %v", mj, m, err)
	}
	fx.now = fx.now.Add(30 * 24 * time.Hour)
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("retried: %v", c)
	}
	if tr, _ := Tracks(context.Background(), fx.d, fx.files["m"]); len(tr) != 0 {
		t.Fatalf("tracks %+v", tr)
	}
}

func TestWorkerFailureBackoff(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.cleanErr["m/1"] = errors.New("boom")
	fx.pass()
	st, fails, retry := fx.state("m", 1)
	if st != "failed" || fails != 1 || retry.String != fx.now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("after 1st: %q %d %v", st, fails, retry)
	}
	fx.takeCalls()
	fx.now = fx.now.Add(30 * time.Minute)
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("retried before the hour: %v", c)
	}
	fx.now = fx.now.Add(31 * time.Minute)
	fx.pass()
	st, fails, retry = fx.state("m", 1)
	if st != "failed" || fails != 2 || retry.String != fx.now.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("after 2nd: %q %d %v", st, fails, retry)
	}
	// given up after 5
	for range 3 {
		fx.now = fx.now.Add(8 * 24 * time.Hour)
		fx.pass()
	}
	st, fails, retry = fx.state("m", 1)
	if st != "failed" || fails != 5 || retry.Valid {
		t.Fatalf("after 5: %q %d %v", st, fails, retry)
	}
	fx.takeCalls()
	fx.now = fx.now.Add(30 * 24 * time.Hour)
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("given up, still tried: %v", c)
	}
	// no temp file or output left by a failure
	if m, _ := filepath.Glob(filepath.Join(fx.w.DataDir, "audio", "*")); len(m) != 0 {
		t.Fatalf("left: %v", m)
	}
}

func TestWorkerFileChanged(t *testing.T) {
	fx := newWFix(t)
	id := fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.pass()
	tr, _ := Tracks(context.Background(), fx.d, id)
	if len(tr) != 1 {
		t.Fatalf("tracks %+v", tr)
	}
	old := tr[0].Path
	fx.takeCalls()
	// a new download replaced the file: not offered any more
	fx.exec(`UPDATE files SET size=2000, mtime=1700000500 WHERE id=?`, id)
	if tr, _ := Tracks(context.Background(), fx.d, id); len(tr) != 0 {
		t.Fatalf("stale track offered: %+v", tr)
	}
	fx.cleanErr["m/1"] = errors.New("busy") // the re-run fails: the old file must be gone anyway
	fx.pass()
	if c := fx.takeCalls(); !reflect.DeepEqual(c, []string{"measure m/1", "clean m/1"}) {
		t.Fatalf("calls %v", c)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old cleaned track kept: %v", err)
	}
	var size int64
	fx.d.QueryRow(`SELECT file_size FROM cleaned_audio WHERE file_id=?`, id).Scan(&size)
	if size != 2000 {
		t.Fatalf("row file_size %d", size)
	}
}

func TestWorkerGarbage(t *testing.T) {
	fx := newWFix(t)
	gone := fx.file("gone", fx.item("movie", 0, "Gone", 1, 0, 0), probeOf(lfEng, "eng:"))
	away := fx.file("away", fx.item("movie", 0, "Away", 2, 0, 0), probeOf(lfEng, "eng:"))
	deleted := fx.file("deleted", fx.item("movie", 0, "Deleted", 3, 0, 0), probeOf(lfEng, "eng:"))
	fx.pass()
	paths := map[int64]string{}
	for _, id := range []int64{gone, away, deleted} {
		tr, _ := Tracks(context.Background(), fx.d, id)
		if len(tr) != 1 {
			t.Fatalf("file %d: %+v", id, tr)
		}
		paths[id] = tr[0].Path
	}
	// a crash left a temp file
	tmp := filepath.Join(fx.w.DataDir, "audio", "999-1.mka.tmp")
	os.WriteFile(tmp, []byte("x"), 0o644)
	fx.exec(`UPDATE files SET missing_since=? WHERE id=?`, fx.now.Add(-8*24*time.Hour).Format(time.RFC3339), gone)
	fx.exec(`UPDATE files SET missing_since=? WHERE id=?`, fx.now.Add(-2*24*time.Hour).Format(time.RFC3339), away)
	fx.exec(`DELETE FROM items WHERE id=(SELECT item_id FROM files WHERE id=?)`, deleted)
	fx.takeCalls()
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("calls %v", c)
	}
	for id, p := range paths {
		_, err := os.Stat(p)
		var n int
		fx.d.QueryRow(`SELECT COUNT(*) FROM cleaned_audio WHERE file_id=?`, id).Scan(&n)
		if id == away {
			if err != nil || n != 1 {
				t.Fatalf("missing 2 days: removed (%v, %d rows)", err, n)
			}
			continue
		}
		if !os.IsNotExist(err) || n != 0 {
			t.Fatalf("file %d: kept (%v, %d rows)", id, err, n)
		}
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp file kept: %v", err)
	}
}

func TestWorkerDiskNotOK(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.w.Disk = func() bool { return false }
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("calls with the disk full: %v", c)
	}
	var n int
	fx.d.QueryRow(`SELECT COUNT(*) FROM cleaned_audio`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows", n)
	}
	fx.w.Disk = func() bool { return true }
	fx.pass()
	if c := fx.takeCalls(); len(c) != 2 {
		t.Fatalf("calls %v", c)
	}
}

func TestWorkerUnwritableDataDir(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	// DataDir/audio cannot be made: a file is in the way
	if err := os.WriteFile(filepath.Join(fx.w.DataDir, "audio"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fx.w.Pass(context.Background()); err == nil {
		t.Fatal("no error")
	}
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("calls %v", c)
	}
}

// itemAt adds an item added at a given time.
func (fx *wfix) itemAt(kind string, parent int64, title string, at time.Time, season, episode int) int64 {
	id := fx.item(kind, parent, title, 1, season, episode)
	fx.exec(`UPDATE items SET added_at=? WHERE id=?`, at.UTC().Format(time.RFC3339), id)
	return id
}

// M3: the original is the English track of the voice-over's own layout.
func TestWorkerOriginalOfSameLayout(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf("eng::2", "eng:", "rus:LostFilm", "rus:Кубик в Кубе:2", "rus:AlexFilm:8"))
	origs := map[int]int{}
	inner := fx.w.Eligible
	fx.w.Eligible = func(ctx context.Context, in Input, pairs []Pair) ([]Measure, error) {
		for _, p := range pairs {
			origs[p.Vo.Index] = p.Orig.Index
		}
		return inner(ctx, in, pairs)
	}
	fx.pass()
	// 5.1 against the 5.1 English; stereo against the 5.1 one too (cleaned
	// from its centre); 7.1 with no 7.1 English: the first English
	// (measured: not eligible)
	if want := map[int]int{3: 2, 4: 2, 5: 1}; !reflect.DeepEqual(origs, want) {
		t.Fatalf("originals %v, want %v", origs, want)
	}
}

// A film made in another language: its own track is the original.
func TestWorkerOriginalInAnotherLanguage(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf("fre:", "rus:LostFilm:2", "und:"))
	origs := map[int]int{}
	inner := fx.w.Eligible
	fx.w.Eligible = func(ctx context.Context, in Input, pairs []Pair) ([]Measure, error) {
		for _, p := range pairs {
			origs[p.Vo.Index] = p.Orig.Index
		}
		return inner(ctx, in, pairs)
	}
	fx.pass()
	if want := map[int]int{2: 1}; !reflect.DeepEqual(origs, want) {
		t.Fatalf("originals %v, want %v", origs, want)
	}
}

// M1: files older than 48 h that nobody is about to watch wait for quiet
// and yield to playback; fresh downloads and next episodes run anyway.
func TestWorkerPlaybackGate(t *testing.T) {
	fx := newWFix(t)
	busy := true
	fx.w.Busy = func() bool { return busy }
	fx.file("fresh", fx.itemAt("movie", 0, "Fresh", fx.now.Add(-time.Hour), 0, 0), probeOf(lfEng, "eng:"))
	fx.file("old", fx.itemAt("movie", 0, "Old", fx.now.Add(-72*time.Hour), 0, 0), probeOf(lfEng, "eng:"))
	show := fx.itemAt("series", 0, "S", fx.now.Add(-90*24*time.Hour), 0, 0)
	s1 := fx.itemAt("season", show, "S1", fx.now.Add(-90*24*time.Hour), 1, 0)
	e1 := fx.itemAt("episode", s1, "E1", fx.now.Add(-90*24*time.Hour), 1, 1)
	e2 := fx.itemAt("episode", s1, "E2", fx.now.Add(-90*24*time.Hour), 1, 2)
	fx.file("e1", e1, probeOf("eng:"))
	fx.file("e2", e2, probeOf(lfEng, "eng:"))
	fx.exec(`INSERT INTO progress(user_id, item_id, position_ms, duration_ms, finished, updated_at) VALUES(1,?,3600000,3600000,1,'2026-11-01T00:00:00.000000000Z')`, e1)
	fx.pass()
	if c, want := fx.takeCalls(), []string{"measure e2/1", "clean e2/1", "measure fresh/1", "clean fresh/1"}; !reflect.DeepEqual(c, want) {
		t.Fatalf("while playing: %v, want %v", c, want)
	}
	if fx.gated["e2"] || fx.gated["fresh"] {
		t.Fatalf("next episode / fresh download gated: %v", fx.gated)
	}
	busy = false
	fx.pass()
	if c := fx.takeCalls(); !reflect.DeepEqual(c, []string{"measure old/1", "clean old/1"}) {
		t.Fatalf("quiet: %v", c)
	}
	if !fx.gated["old"] {
		t.Fatal("the backlog file's read does not yield to playback")
	}
}

// M2: a download that arrives while a file is cleaned goes ahead of the
// backlog.
func TestWorkerRequeuesBetweenFiles(t *testing.T) {
	fx := newWFix(t)
	fx.file("a", fx.item("movie", 0, "A", 9, 0, 0), probeOf(lfEng, "eng:"))
	fx.file("b", fx.item("movie", 0, "B", 3, 0, 0), probeOf(lfEng, "eng:"))
	added := false
	fx.onClean = func() {
		if !added {
			added = true
			fx.file("n", fx.item("movie", 0, "N", 10, 0, 0), probeOf(lfEng, "eng:"))
		}
	}
	fx.pass()
	want := []string{"measure a/1", "clean a/1", "measure n/1", "clean n/1", "measure b/1", "clean b/1"}
	if c := fx.takeCalls(); !reflect.DeepEqual(c, want) {
		t.Fatalf("order %v, want %v", c, want)
	}
}

// M4: a failure while the disk filled up is not the track's fault.
func TestWorkerDiskFullNotCounted(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.cleanErr["m/1"] = errors.New("No space left on device")
	full := false
	fx.w.Disk = func() bool { return !full }
	fx.onClean = func() { full = true }
	fx.pass()
	if st, fails, _ := fx.state("m", 1); fails != 0 || st == "failed" {
		t.Fatalf("counted: %q %d", st, fails)
	}
	full, fx.onClean = false, nil
	delete(fx.cleanErr, "m/1")
	fx.takeCalls()
	fx.pass()
	if st, _, _ := fx.state("m", 1); st != "done" {
		t.Fatalf("state %q", st)
	}
}

// H1: the row keeps where the cleaned track starts on the file's timeline.
func TestWorkerStoresStartOffset(t *testing.T) {
	fx := newWFix(t)
	id := fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.pass()
	tr, err := Tracks(context.Background(), fx.d, id)
	if err != nil || len(tr) != 1 || tr[0].StartMs != 21 {
		t.Fatalf("%+v %v", tr, err)
	}
}

// A badly interleaved file: its tracks are not eligible, not failed.
func TestWorkerBadlyInterleaved(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:", "rus:AlexFilm"))
	fx.w.Eligible = func(context.Context, Input, []Pair) ([]Measure, error) {
		return nil, fmt.Errorf("measure: %w", ErrBadlyInterleaved)
	}
	fx.pass()
	for _, idx := range []int{1, 3} {
		st, fails, _ := fx.state("m", idx)
		if st != "not_eligible" || fails != 0 {
			t.Fatalf("track %d: %q %d", idx, st, fails)
		}
	}
	var mj string
	fx.d.QueryRow(`SELECT measure_json FROM cleaned_audio WHERE file_id=? AND vo_index=1`, fx.files["m"]).Scan(&mj)
	if !strings.Contains(mj, "badly interleaved") {
		t.Fatalf("measure %s", mj)
	}
}

// One track of a file failing leaves the other done.
func TestWorkerOneTrackFails(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:", "rus:AlexFilm"))
	fx.cleanErr["m/3"] = errors.New("encoder died")
	fx.pass()
	if st, _, _ := fx.state("m", 1); st != "done" {
		t.Fatalf("track 1: %q", st)
	}
	if st, fails, _ := fx.state("m", 3); st != "failed" || fails != 1 {
		t.Fatalf("track 3: %q %d", st, fails)
	}
}

// A stalled read that is not the file's interleaving is an ordinary
// failure: counted, retried after the backoff.
func TestWorkerStallIsFailure(t *testing.T) {
	fx := newWFix(t)
	fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.w.Eligible = func(context.Context, Input, []Pair) ([]Measure, error) {
		return nil, fmt.Errorf("measure: %w", ErrStalled)
	}
	fx.pass()
	st, fails, retry := fx.state("m", 1)
	if st != "failed" || fails != 1 || retry.String != fx.now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("%q %d %v", st, fails, retry)
	}
}

// The offset a cleaned track plays at: 0 on the timeline it was made on.
func TestCleanedOffset(t *testing.T) {
	c := Cleaned{StartMs: 21}
	same, earlier := 21.0, 0.0
	if c.OffsetMs(&same) != 0 || c.OffsetMs(&earlier) != 21 || c.OffsetMs(nil) != 0 {
		t.Fatal(c.OffsetMs(&same), c.OffsetMs(&earlier), c.OffsetMs(nil))
	}
}

// A track cleaned before the gain bound followed the voice-over's level
// (its measure has no frontGain) is made again.
func TestWorkerRemakesTracksWithoutFrontGain(t *testing.T) {
	fx := newWFix(t)
	id := fx.file("m", fx.item("movie", 0, "M", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.pass()
	fx.takeCalls()
	fx.pass()
	if c := fx.takeCalls(); len(c) != 0 {
		t.Fatalf("a track with frontGain made again: %v", c)
	}
	if _, err := fx.d.Exec(`UPDATE cleaned_audio SET measure_json=json_remove(measure_json, '$.frontGain') WHERE file_id=?`, id); err != nil {
		t.Fatal(err)
	}
	fx.pass()
	if c := fx.takeCalls(); len(c) == 0 {
		t.Fatal("an old track was not made again")
	}
	if st, _, _ := fx.state("m", 1); st != "done" {
		t.Fatalf("state %q", st)
	}
}

// Files listed but not on disk (an unmounted drive) are walked past
// without reading the queue again for each of them.
func TestWorkerMissingFilesFewQueues(t *testing.T) {
	fx := newWFix(t)
	for i := range 20 {
		id := fx.file(fmt.Sprintf("m%d", i), fx.item("movie", 0, fmt.Sprintf("M%d", i), 1+i%9, 0, 0), probeOf(lfEng, "eng:"))
		fx.exec(`UPDATE files SET path=? WHERE id=?`, filepath.Join(t.TempDir(), "gone.mkv"), id)
	}
	fx.file("here", fx.item("movie", 0, "Here", 1, 0, 0), probeOf(lfEng, "eng:"))
	fx.pass()
	if c := fx.takeCalls(); !reflect.DeepEqual(c, []string{"measure here/1", "clean here/1"}) {
		t.Fatalf("calls %v", c)
	}
	if fx.w.queues > 3 {
		t.Fatalf("%d queue reads for 20 missing files", fx.w.queues)
	}
}
