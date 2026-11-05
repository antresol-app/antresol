package loudness

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

func fp(v float64) *float64 { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 0.011 }

const summary = `[Parsed_ebur128_0 @ 0x1] t: 2.9   TARGET:-23 LUFS    M: -27.1 S: -27.1     I: -27.1 LUFS       LRA:  20.0 LU  SPK: -24.1 dBFS
[Parsed_ebur128_0 @ 0x1] Summary:

  Integrated loudness:
    I:         -14.2 LUFS
    Threshold: -24.2 LUFS

  Loudness range:
    LRA:         5.2 LU
    Threshold: -34.3 LUFS
    LRA low:   -18.0 LUFS
    LRA high:  -12.8 LUFS

  Sample peak:
    Peak:       -0.5 dBFS
[out#0/null @ 0x2] video:0KiB audio:258KiB
`

func TestParseEBUR128(t *testing.T) {
	r, err := ParseEBUR128(summary)
	if err != nil {
		t.Fatal(err)
	}
	if r.LUFS == nil || *r.LUFS != -14.2 || math.Abs(r.Peak-math.Pow(10, -0.5/20)) > 1e-9 {
		t.Fatalf("got %+v", r)
	}
	silent := "Summary:\n\n  Integrated loudness:\n    I:         -70.0 LUFS\n\n  Sample peak:\n    Peak:       -inf dBFS\n"
	if r, err := ParseEBUR128(silent); err != nil || r.LUFS != nil || r.Peak != 0 {
		t.Fatalf("silence: %+v %v", r, err)
	}
	if _, err := ParseEBUR128("Error opening input"); err == nil {
		t.Fatal("no summary parsed")
	}
}

func TestAlbumGain(t *testing.T) {
	// two equal tracks: the album is as loud as each
	g, p, ok := AlbumGain([]AlbumTrack{{Known: true, LUFS: fp(-10), Peak: fp(0.9), DurationMs: 1000}, {Known: true, LUFS: fp(-10), Peak: fp(0.5), DurationMs: 1000}})
	if !ok || g != -8 || p == nil || *p != 0.9 {
		t.Fatalf("equal: %v %v %v", g, p, ok)
	}
	// energy mean weighted by length: 3 min at -10 and 1 min at -20
	g, _, ok = AlbumGain([]AlbumTrack{{Known: true, LUFS: fp(-10), DurationMs: 180_000}, {Known: true, LUFS: fp(-20), DurationMs: 60_000}})
	want := -18 - 10*math.Log10((3*math.Pow(10, -1)+math.Pow(10, -2))/4)
	if !ok || !near(g, want) {
		t.Fatalf("weighted: %v, want %v", g, want)
	}
	// silence counts for nothing; an unknown track holds the album back
	if g, _, ok := AlbumGain([]AlbumTrack{{Known: true, LUFS: fp(-12)}, {Known: true}}); !ok || g != -6 {
		t.Fatalf("with silence: %v %v", g, ok)
	}
	if _, _, ok := AlbumGain([]AlbumTrack{{Known: true, LUFS: fp(-12)}, {}}); ok {
		t.Fatal("an unknown track gave an album gain")
	}
	if _, _, ok := AlbumGain([]AlbumTrack{{Known: true}}); ok {
		t.Fatal("an all-silent album gave a gain")
	}
	// one track's peak unknown: the album's is unknown
	if _, p, ok := AlbumGain([]AlbumTrack{{Known: true, LUFS: fp(-10), Peak: fp(0.5)}, {Known: true, LUFS: fp(-10)}}); !ok || p != nil {
		t.Fatalf("album peak with an unknown track peak: %v", p)
	}
}

type fix struct {
	db     *sql.DB
	repo   *library.Repo
	album  int64
	tracks []int64
	dir    string
}

// newFix: an album of three tracks; the first tagged (-6 dB, peak 0.9),
// the others not.
func newFix(t *testing.T) *fix {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f := &fix{db: d, repo: library.NewRepo(d), dir: t.TempDir()}
	var in []library.TrackInput
	for i := 1; i <= 3; i++ {
		p := filepath.Join(f.dir, strconv.Itoa(i)+".flac")
		if err := os.WriteFile(p, []byte("audio"), 0o644); err != nil {
			t.Fatal(err)
		}
		ti := library.TrackInput{File: library.FileInput{Path: p, Size: 5, Mtime: 1}, AlbumDir: f.dir, Album: "Al",
			Artist: "Ar", Title: "T" + strconv.Itoa(i), Disc: 1, Track: i, Codec: "flac"}
		if i == 1 {
			ti.ReplayGain = library.ReplayGain{TrackGain: fp(-6), TrackPeak: fp(0.9)}
		}
		in = append(in, ti)
	}
	if f.album, err = f.repo.UpsertAlbumTracks(context.Background(), "Ar", in); err != nil {
		t.Fatal(err)
	}
	trs, err := f.repo.AlbumTracks(context.Background(), f.album)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range trs {
		f.tracks = append(f.tracks, tr.ID)
	}
	return f
}

func (f *fix) gain(t *testing.T, i int) library.Loudness {
	t.Helper()
	tr, err := f.repo.TrackByID(context.Background(), f.tracks[i])
	if err != nil {
		t.Fatal(err)
	}
	return tr.Gain
}

func TestWorkerMeasuresUntaggedAndAlbum(t *testing.T) {
	f := newFix(t)
	var calls []string
	w := &Worker{DB: f.db, Measure: func(_ context.Context, path string, _ int64, _ *int64) (Result, error) {
		calls = append(calls, filepath.Base(path))
		return Result{LUFS: fp(-6), Peak: 0.5}, nil // the tagged track is at -12 LUFS
	}}
	if err := w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("measured %v, want the two untagged tracks", calls)
	}
	g := f.gain(t, 1)
	if g.TrackGain == nil || *g.TrackGain != -12 || g.TrackPeak == nil || *g.TrackPeak != 0.5 {
		t.Fatalf("measured track gain %+v", g)
	}
	// album: one track at -12 LUFS, two at -6 (equal lengths), peak 0.9
	want := -18 - 10*math.Log10((math.Pow(10, -1.2)+2*math.Pow(10, -0.6))/3)
	for i := range f.tracks {
		g := f.gain(t, i)
		if g.AlbumGain == nil || !near(*g.AlbumGain, want) || g.AlbumPeak == nil || *g.AlbumPeak != 0.9 {
			t.Fatalf("track %d album gain %v peak %v, want %v", i, g.AlbumGain, g.AlbumPeak, want)
		}
	}
	// a second pass measures nothing again
	calls = nil
	if err := w.Pass(context.Background()); err != nil || len(calls) != 0 {
		t.Fatalf("second pass measured %v (%v)", calls, err)
	}
}

func TestWorkerRereadsTagsAndBacksOff(t *testing.T) {
	f := newFix(t)
	// a row from before tags were read: the worker reads them (tagged: not measured)
	if _, err := f.db.Exec(`UPDATE tracks SET rg_scanned=0 WHERE item_id=?`, f.tracks[2]); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var calls int
	var read []string
	w := &Worker{DB: f.db, Now: func() time.Time { return now }, Measure: func(context.Context, string, int64, *int64) (Result, error) {
		calls++
		return Result{}, errors.New("broken file")
	}, ReadTags: func(path string, image bool) (library.ReplayGain, error) {
		read = append(read, filepath.Base(path))
		return library.ReplayGain{TrackGain: fp(-3), TrackPeak: fp(0.7)}, nil
	}}
	if err := w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(read) != 1 || read[0] != "3.flac" {
		t.Fatalf("measured %d, read %v: want the tags of 3 read, only 2 measured", calls, read)
	}
	if g := f.gain(t, 2); g.TrackGain == nil || *g.TrackGain != -3 {
		t.Fatalf("re-read tags not used: %+v", g)
	}
	if g := f.gain(t, 0); g.AlbumGain != nil {
		t.Fatalf("album gain with unknown tracks: %v", *g.AlbumGain)
	}
	// within the hour: not again; after it: again, then two hours
	if err := w.Pass(context.Background()); err != nil || calls != 1 {
		t.Fatalf("retried too soon: %d %v", calls, err)
	}
	now = now.Add(61 * time.Minute)
	if err := w.Pass(context.Background()); err != nil || calls != 2 {
		t.Fatalf("not retried after an hour: %d %v", calls, err)
	}
	var fails int
	var retry string
	if err := f.db.QueryRow(`SELECT loud_fails, loud_retry FROM tracks WHERE item_id=?`, f.tracks[1]).Scan(&fails, &retry); err != nil {
		t.Fatal(err)
	}
	if fails != 2 || retry != now.Add(2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("fails %d retry %s", fails, retry)
	}
	// the third failure gives up: the album's gain comes from the rest,
	// its peak unknown (no client boosts)
	now = now.Add(121 * time.Minute)
	if err := w.Pass(context.Background()); err != nil || calls != 3 {
		t.Fatalf("not retried after two hours: %d %v", calls, err)
	}
	var state int
	if err := f.db.QueryRow(`SELECT loud_state FROM tracks WHERE item_id=?`, f.tracks[1]).Scan(&state); err != nil || state != 2 {
		t.Fatalf("state %d %v, want 2 (given up)", state, err)
	}
	g := f.gain(t, 0)
	// tracks 1 (-6 dB → -12 LUFS) and 3 (-3 dB → -15 LUFS)
	want := -18 - 10*math.Log10((math.Pow(10, -1.2)+math.Pow(10, -1.5))/2)
	if g.AlbumGain == nil || !near(*g.AlbumGain, want) || g.AlbumPeak != nil {
		t.Fatalf("album gain %v peak %v, want %v and no peak", g.AlbumGain, g.AlbumPeak, want)
	}
	now = now.Add(30 * 24 * time.Hour)
	if err := w.Pass(context.Background()); err != nil || calls != 3 {
		t.Fatalf("a given-up track measured again: %d %v", calls, err)
	}
}

// TestRereadTagsWaitsForQuiet: the tag re-read reads no file while
// something plays.
func TestRereadTagsWaitsForQuiet(t *testing.T) {
	f := newFix(t)
	if _, err := f.db.Exec(`UPDATE tracks SET rg_scanned=0`); err != nil {
		t.Fatal(err)
	}
	var busy atomic.Bool
	busy.Store(true)
	var reads atomic.Int32
	w := &Worker{DB: f.db, Busy: busy.Load, Measure: func(context.Context, string, int64, *int64) (Result, error) {
		return Result{LUFS: fp(-9), Peak: 0.4}, nil
	}, ReadTags: func(string, bool) (library.ReplayGain, error) {
		reads.Add(1)
		return library.ReplayGain{}, nil
	}}
	done := make(chan error, 1)
	go func() { done <- w.Pass(context.Background()) }()
	time.Sleep(1200 * time.Millisecond)
	if n := reads.Load(); n != 0 {
		t.Fatalf("read %d files while playing", n)
	}
	busy.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pass did not finish")
	}
	if n := reads.Load(); n != 3 {
		t.Fatalf("read %d files", n)
	}
}

// TestRunWaitsForStartDelay: a scan's Kick at boot does not start the
// worker before StartDelay.
func TestRunWaitsForStartDelay(t *testing.T) {
	f := newFix(t)
	var calls atomic.Int32
	w := &Worker{DB: f.db, StartDelay: 400 * time.Millisecond, Measure: func(context.Context, string, int64, *int64) (Result, error) {
		calls.Add(1)
		return Result{LUFS: fp(-9), Peak: 0.4}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Kick()
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("measured before the start delay")
	}
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("measured %d after the delay", calls.Load())
	}
}

// TestWorkerWaitsForQuiet: nothing is measured while something plays.
func TestWorkerWaitsForQuiet(t *testing.T) {
	f := newFix(t)
	var busy atomic.Bool
	busy.Store(true)
	var calls atomic.Int32
	w := &Worker{DB: f.db, Busy: busy.Load, Measure: func(context.Context, string, int64, *int64) (Result, error) {
		calls.Add(1)
		return Result{LUFS: fp(-9), Peak: 0.4}, nil
	}}
	done := make(chan error, 1)
	go func() { done <- w.Pass(context.Background()) }()
	time.Sleep(1500 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("measured %d while playing", n)
	}
	busy.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pass did not finish after playback stopped")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("measured %d after playback stopped", n)
	}
}

// TestFFmpegLoudness measures a real file (skipped without ffmpeg): a
// full-scale sine at 0.5 amplitude peaks at 0.5.
func TestFFmpegLoudness(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	p := filepath.Join(t.TempDir(), "s.flac")
	if out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
		"sine=frequency=1000:duration=4", "-af", "volume=4", p).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	end := int64(3000)
	r, err := FFmpegLoudness(context.Background(), ff, p, 1000, &end, nil)
	if err != nil {
		t.Fatal(err)
	}
	// lavfi's sine is 1/8 of full scale: ×4 gives 0.5
	if r.LUFS == nil || *r.LUFS > -5 || *r.LUFS < -12 || math.Abs(r.Peak-0.5) > 0.01 {
		t.Fatalf("got lufs %v peak %v", r.LUFS, r.Peak)
	}
}
