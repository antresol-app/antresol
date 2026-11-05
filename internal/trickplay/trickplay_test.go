package trickplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

func TestTileHeight(t *testing.T) {
	for _, c := range []struct{ w, h, want int }{
		{1920, 1080, 136}, {3840, 1600, 100}, {720, 576, 192}, {1440, 1080, 180},
		{1080, 1920, 240}, {4000, 100, 68}, {0, 0, 136},
	} {
		if got := TileHeight(c.w, c.h); got != c.want {
			t.Errorf("TileHeight(%d,%d) = %d, want %d", c.w, c.h, got, c.want)
		}
	}
}

func TestFrames(t *testing.T) {
	for _, c := range []struct {
		dur  float64
		want int
	}{{0, 0}, {5, 1}, {10, 1}, {10.5, 2}, {7200, 720}} {
		if got := Frames(c.dur); got != c.want {
			t.Errorf("Frames(%v) = %d, want %d", c.dur, got, c.want)
		}
	}
}

// solid is a w×h frame of one grey level.
func solid(w, h int, v byte) []byte { return bytes.Repeat([]byte{v}, w*h*3) }

func TestGenerateSheets(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "k")
	const w, h = 32, 18
	var ats []float64
	// 1234 s: 124 frames, two sheets; frame 105 (sheet 1, column 5, row 0) is white
	ix, err := Generate(context.Background(), dir, "k", 1234, w, h, func(_ context.Context, at float64) ([]byte, error) {
		ats = append(ats, at)
		if len(ats)-1 == 105 {
			return solid(w, h, 255), nil
		}
		if len(ats)-1 == 3 {
			return nil, errors.New("unreadable") // stays black
		}
		return solid(w, h, 60), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Index{Version: Version, Key: "k", Interval: 10, Width: w, Height: h, Columns: 10, Rows: 10, Count: 124, Sheets: 2}
	if ix != want {
		t.Fatalf("index %+v", ix)
	}
	if ats[0] != 5 || ats[1] != 15 || ats[123] != 1233.5 {
		t.Fatalf("read at %v %v … %v", ats[0], ats[1], ats[123])
	}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored Index
	if json.Unmarshal(b, &stored) != nil || stored != want {
		t.Fatalf("stored index %s", b)
	}
	f, err := os.Open(filepath.Join(dir, "1.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 10*w || b.Dy() != 10*h {
		t.Fatalf("sheet %v", b)
	}
	lum := func(x, y int) uint32 { r, _, _, _ := img.At(x, y).RGBA(); return r >> 8 }
	if l := lum(5*w+w/2, h/2); l < 240 {
		t.Fatalf("frame 105 at column 5: %d", l)
	}
	if l := lum(4*w+w/2, h/2); l < 50 || l > 70 {
		t.Fatalf("frame 104: %d", l)
	}
	if l := lum(5*w, 5*h); l > 10 {
		t.Fatalf("past the last frame: %d, want black", l)
	}
	s0, _ := os.Open(filepath.Join(dir, "0.jpg"))
	defer s0.Close()
	img0, _ := jpeg.Decode(s0)
	if r, _, _, _ := img0.At(3*w+w/2, h/2).RGBA(); r>>8 > 10 {
		t.Fatalf("a failed frame: %d, want black", r>>8)
	}
}

func TestGenerateTooManyFailed(t *testing.T) {
	n := 0
	_, err := Generate(context.Background(), t.TempDir(), "k", 100, 8, 8, func(context.Context, float64) ([]byte, error) {
		n++
		return nil, errors.New("no")
	})
	if !errors.Is(err, ErrTooManyFailed) || n != 6 {
		t.Fatalf("err %v after %d reads", err, n)
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	if _, ok := s.Index("/m/a.mkv", 10, 1); ok {
		t.Fatal("nothing made: index")
	}
	key := Key("/m/a.mkv", 10, 1)
	if _, err := Generate(context.Background(), filepath.Join(dir, key), key, 400, 8, 8, func(context.Context, float64) ([]byte, error) {
		return solid(8, 8, 1), nil
	}); err != nil {
		t.Fatal(err)
	}
	ix, ok := s.Index("/m/a.mkv", 10, 1)
	if !ok || ix.Sheets != 1 || ix.Count != 40 || ix.Key != key {
		t.Fatalf("index %+v %v", ix, ok)
	}
	if p, ok := s.Sheet("/m/a.mkv", 10, 1, 0); !ok || filepath.Base(p) != "0.jpg" {
		t.Fatalf("sheet 0: %q %v", p, ok)
	}
	if _, ok := s.Sheet("/m/a.mkv", 10, 1, 1); ok {
		t.Fatal("sheet 1 of 1")
	}
	if _, ok := s.Index("/m/a.mkv", 11, 1); ok {
		t.Fatal("a replaced file (other size): the old previews")
	}
	if Key("/m/a.mkv", 10, 1) == Key("/m/a.mkv", 10, 2) {
		t.Fatal("mtime not in the key")
	}
}

// ---------- the worker ----------

type wfix struct {
	w     *Worker
	repo  *library.Repo
	dir   string
	paths map[string]string // title → path
	ids   map[string]int64
	mu    sync.Mutex
	reads []string // paths of the frames read, in order
}

func newWorkerFixture(t *testing.T) *wfix {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	media := t.TempDir()
	fx := &wfix{repo: library.NewRepo(d), dir: filepath.Join(t.TempDir(), "trickplay"), paths: map[string]string{}, ids: map[string]int64{}}
	add := func(title string, durMs int64, probe string) {
		p := filepath.Join(media, title+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.repo.UpsertMovie(ctx, library.MovieInput{Title: title, File: library.FileInput{Path: p, Size: 100, Mtime: 1}}); err != nil {
			t.Fatal(err)
		}
		f, err := fx.repo.FileByPath(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.repo.SetProbe(ctx, f.ID, probe, durMs); err != nil {
			t.Fatal(err)
		}
		fx.paths[title], fx.ids[title] = p, f.ID
	}
	video := `{"Container":"matroska","DurationMs":600000,"Video":{"Codec":"h264","Width":1920,"Height":800}}`
	add("Old", 600_000, video)
	add("Short", 200_000, video) // under 5 min: none
	add("Watching", 600_000, video)
	add("Newest", 600_000, video)
	add("NoPicture", 600_000, `{"Container":"matroska","DurationMs":600000}`)
	// Old was added first, Newest last
	for i, title := range []string{"Old", "Short", "Watching", "Newest", "NoPicture"} {
		if _, err := d.Exec(`UPDATE items SET added_at=? WHERE id=(SELECT item_id FROM files WHERE id=?)`,
			time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), fx.ids[title]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO users(id, name, avatar) VALUES(1,'u','a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO progress(user_id, item_id, position_ms, duration_ms, finished, updated_at)
		SELECT 1, item_id, 30000, 600000, 0, '2026-02-01T00:00:00Z' FROM files WHERE id=?`, fx.ids["Watching"]); err != nil {
		t.Fatal(err)
	}
	fx.w = &Worker{DB: d, Dir: fx.dir, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Frame: func(_ context.Context, path string, _ float64, w, h int) ([]byte, error) {
		fx.mu.Lock()
		if n := len(fx.reads); n == 0 || fx.reads[n-1] != path {
			fx.reads = append(fx.reads, path)
		}
		fx.mu.Unlock()
		if w != 240 || h != 100 {
			return nil, errors.New("tile size")
		}
		return solid(w, h, 90), nil
	}}
	return fx
}

func (fx *wfix) order() []string {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	byPath := map[string]string{}
	for title, p := range fx.paths {
		byPath[p] = title
	}
	var out []string
	for _, p := range fx.reads {
		out = append(out, byPath[p])
	}
	return out
}

func eq(a, b []string) bool {
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

func TestWorkerOrderAndSkips(t *testing.T) {
	fx := newWorkerFixture(t)
	ctx := context.Background()
	fx.w.Want(fx.ids["Old"]) // a play here
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := fx.order(), []string{"Old", "Watching", "Newest"}; !eq(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	for _, title := range []string{"Old", "Watching", "Newest"} {
		ix, ok := fx.w.Index(fx.paths[title], 100, 1)
		if !ok || ix.Count != 60 || ix.Sheets != 1 || ix.Height != 100 {
			t.Fatalf("%s: %+v %v", title, ix, ok)
		}
	}
	for _, title := range []string{"Short", "NoPicture"} {
		if _, ok := fx.w.Index(fx.paths[title], 100, 1); ok {
			t.Fatalf("%s got previews", title)
		}
	}
	// done: a second pass reads nothing
	fx.reads = nil
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fx.reads) != 0 {
		t.Fatalf("second pass read %v", fx.order())
	}
}

func TestWorkerDisabled(t *testing.T) {
	fx := newWorkerFixture(t)
	fx.w.Settings = func(context.Context) (bool, int64) { return false, 0 }
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fx.reads) != 0 {
		t.Fatalf("off, yet read %v", fx.order())
	}
}

// A file that fails is not tried again until its retry; half-made
// previews never show.
func TestWorkerFailureBackoff(t *testing.T) {
	fx := newWorkerFixture(t)
	now := time.Unix(1_800_000_000, 0)
	fx.w.Now = func() time.Time { return now }
	calls := 0
	fx.w.Frame = func(_ context.Context, path string, _ float64, w, h int) ([]byte, error) {
		if path == fx.paths["Newest"] {
			calls++
			return nil, errors.New("broken")
		}
		return solid(w, h, 1), nil
	}
	ctx := context.Background()
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := fx.w.Index(fx.paths["Newest"], 100, 1); ok || calls == 0 {
		t.Fatalf("broken file: previews %v, calls %d", ok, calls)
	}
	if ents, _ := filepath.Glob(filepath.Join(fx.dir, "*.tmp")); len(ents) != 0 {
		t.Fatalf("left %v", ents)
	}
	calls = 0
	now = now.Add(30 * time.Minute)
	fx.w.Pass(ctx)
	if calls != 0 {
		t.Fatal("retried before its hour")
	}
	now = now.Add(31 * time.Minute)
	fx.w.Pass(ctx)
	if calls == 0 {
		t.Fatal("not retried after an hour")
	}
}

// Playback holds the reads: none while Busy, they go on once it clears.
func TestWorkerWaitsForQuiet(t *testing.T) {
	fx := newWorkerFixture(t)
	var mu sync.Mutex
	busy := true
	fx.w.Busy = func() bool { mu.Lock(); defer mu.Unlock(); return busy }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fx.w.Pass(ctx) }()
	time.Sleep(1500 * time.Millisecond)
	fx.mu.Lock()
	n := len(fx.reads)
	fx.mu.Unlock()
	if n != 0 {
		t.Fatal("read while playing")
	}
	mu.Lock()
	busy = false
	mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(fx.order()) != 3 {
		t.Fatalf("after playback: %v", fx.order())
	}
}

func TestWorkerPrune(t *testing.T) {
	fx := newWorkerFixture(t)
	ctx := context.Background()
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	// a stray directory and a replaced file's previews go; Old's stay
	stray := filepath.Join(fx.dir, Key("/gone.mkv", 1, 1))
	os.MkdirAll(stray, 0o755)
	if _, err := fx.w.DB.Exec(`UPDATE files SET size=200 WHERE id=?`, fx.ids["Newest"]); err != nil {
		t.Fatal(err)
	}
	fx.w.prunedAt = time.Time{}
	fx.w.Frame = func(context.Context, string, float64, int, int) ([]byte, error) { return nil, errors.New("no") }
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("stray previews kept")
	}
	if _, err := os.Stat(filepath.Join(fx.dir, Key(fx.paths["Newest"], 100, 1))); !os.IsNotExist(err) {
		t.Fatal("replaced file's old previews kept")
	}
	if _, ok := fx.w.Index(fx.paths["Old"], 100, 1); !ok {
		t.Fatal("Old's previews pruned")
	}
}

// ---------- ffmpeg ----------

func TestFFmpegFrame(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mkv")
	// 30 s, 4:3, a keyframe every 2 s
	if out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x480:rate=25",
		"-t", "30", "-c:v", "libx264", "-preset", "ultrafast", "-g", "50", src).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test clip: %v %s", err, out)
	}
	px, err := FFmpegFrame(context.Background(), ff, src, 15, 320, 180, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(px) != 320*180*3 {
		t.Fatalf("%d bytes", len(px))
	}
	// 4:3 in 16:9: black bars left and right, picture in the middle
	if px[0] > 10 || px[(90*320+160)*3]+px[(90*320+160)*3+1]+px[(90*320+160)*3+2] == 0 {
		t.Fatalf("bars/picture: %v %v", px[:3], px[(90*320+160)*3:(90*320+160)*3+3])
	}
	ix, err := Generate(context.Background(), filepath.Join(dir, "k"), "k", 30, 320, 180, func(ctx context.Context, at float64) ([]byte, error) {
		return FFmpegFrame(ctx, ff, src, at, 320, 180, nil)
	})
	if err != nil || ix.Count != 3 {
		t.Fatalf("generate: %+v %v", ix, err)
	}
	if _, err := FFmpegFrame(context.Background(), ff, filepath.Join(dir, "none.mkv"), 1, 320, 180, nil); err == nil {
		t.Fatal("a missing file read")
	}
}
