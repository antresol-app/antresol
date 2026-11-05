package trickplay

import (
	"context"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

// addPart gives title's film a second file (a film in parts, another version).
func (fx *wfix) addPart(t *testing.T, title, name string) {
	t.Helper()
	p := filepath.Join(filepath.Dir(fx.paths[title]), name+".mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := fx.w.DB.Exec(`INSERT INTO files(item_id, path, size, mtime, duration_ms, probe_json)
		SELECT item_id, ?, 100, 1, duration_ms, probe_json FROM files WHERE id=?`, p, fx.ids[title])
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	fx.paths[name], fx.ids[name] = p, id
}

func TestWorkerSharedTitlesOnlyWhenAsked(t *testing.T) {
	fx := newWorkerFixture(t)
	fx.addPart(t, "Newest", "Newest.part2")
	ctx := context.Background()
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fx.order(); !eq(got, []string{"Watching", "Old"}) {
		t.Fatalf("background read %v (the film in parts too)", got)
	}
	fx.reads = nil
	fx.w.Want(fx.ids["Newest.part2"])
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fx.order(); !eq(got, []string{"Newest.part2"}) {
		t.Fatalf("asked for part 2, read %v", got)
	}
	if len(fx.w.want) != 0 {
		t.Fatalf("wants left: %v", fx.w.want)
	}
}

// At the budget the background stops; a file asked for makes room.
func TestWorkerBudget(t *testing.T) {
	fx := newWorkerFixture(t)
	fx.w.Settings = func(context.Context) (bool, int64) { return true, 1 }
	ctx := context.Background()
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fx.order(); !eq(got, []string{"Watching"}) {
		t.Fatalf("over budget, read %v", got)
	}
	fx.reads = nil
	fx.w.Want(fx.ids["Old"])
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fx.order(); !eq(got, []string{"Old"}) {
		t.Fatalf("asked for Old, read %v", got)
	}
	if _, ok := fx.w.Index(fx.paths["Old"], 100, 1); !ok {
		t.Fatal("Old not kept")
	}
	if _, ok := fx.w.Index(fx.paths["Watching"], 100, 1); ok {
		t.Fatal("Watching not deleted for room")
	}
}

// fit deletes the least recently viewed first, never the file just made.
func TestFitLeastRecentlyViewed(t *testing.T) {
	dir := t.TempDir()
	w := &Worker{Dir: dir}
	t0 := time.Unix(1_800_000_000, 0)
	w.usage = map[string]used{}
	for i, k := range []string{"a", "b", "c", "d"} {
		os.MkdirAll(filepath.Join(dir, k), 0o755)
		w.usage[k] = used{bytes: 10, viewed: t0.Add(time.Duration(i) * time.Hour)}
	}
	w.usage["a"] = used{bytes: 10, viewed: t0.Add(10 * time.Hour)} // viewed last
	w.fit(20, "b")
	for k, kept := range map[string]bool{"a": true, "b": true, "c": false, "d": false} {
		_, err := os.Stat(filepath.Join(dir, k))
		if (err == nil) != kept {
			t.Fatalf("%s kept=%v", k, err == nil)
		}
	}
	if w.total() != 20 {
		t.Fatalf("total %d", w.total())
	}
}

// A view marks the previews used (at most hourly).
func TestIndexMarksViewed(t *testing.T) {
	fx := newWorkerFixture(t)
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(3 * time.Hour)
	fx.w.Now = func() time.Time { return later }
	ix, ok := fx.w.Index(fx.paths["Old"], 100, 1)
	if !ok {
		t.Fatal("no index")
	}
	st, _ := os.Stat(filepath.Join(fx.dir, ix.Key, "index.json"))
	if d := st.ModTime().Sub(later); d > time.Second || d < -time.Second {
		t.Fatalf("viewed at %v, want %v", st.ModTime(), later)
	}
}

// Turned off, every preview is deleted (the space comes back).
func TestWorkerOffDeletes(t *testing.T) {
	fx := newWorkerFixture(t)
	ctx := context.Background()
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	fx.w.Settings = func(context.Context) (bool, int64) { return false, 0 }
	if err := fx.w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(fx.dir); len(ents) != 0 {
		t.Fatalf("left %d entries", len(ents))
	}
}

// A file without a keyframe index (Matroska without Cues) is read in one
// pass; one in another container still seeks.
func TestWorkerLinearWithoutIndex(t *testing.T) {
	fx := newWorkerFixture(t)
	fx.w.KeyIndex = func(path string) error {
		switch path {
		case fx.paths["Newest"]:
			return fmt.Errorf("%w: matroska without Cues", media.ErrNoKeyIndex)
		case fx.paths["Old"]:
			return fmt.Errorf("%w: not MP4 or Matroska", media.ErrNoKeyIndex)
		}
		return nil
	}
	var linear []string
	fx.w.Linear = func(_ context.Context, dir, key string, dur float64, w, h int, path string) (Index, error) {
		linear = append(linear, path)
		s, err := newSheets(dir, key, dur, w, h)
		if err != nil {
			return Index{}, err
		}
		for i := range s.ix.Count {
			s.put(i, solid(w, h, 9))
		}
		return s.finish()
	}
	if err := fx.w.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(linear) != 1 || linear[0] != fx.paths["Newest"] {
		t.Fatalf("linear %v", linear)
	}
	if got := fx.order(); !eq(got, []string{"Watching", "Old"}) {
		t.Fatalf("seeks %v", got)
	}
	if _, ok := fx.w.Index(fx.paths["Newest"], 100, 1); !ok {
		t.Fatal("linear file has no previews")
	}
}

// Requests for files the queue never takes, or a day old, are forgotten.
func TestWorkerForgetsStaleWants(t *testing.T) {
	fx := newWorkerFixture(t)
	now := time.Now()
	fx.w.Now = func() time.Time { return now }
	fx.w.Want(fx.ids["Short"]) // under 5 min: never listed
	fx.w.Want(fx.ids["Old"])
	now = now.Add(25 * time.Hour)
	if _, err := fx.w.candidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fx.w.want) != 0 {
		t.Fatalf("wants %v", fx.w.want)
	}
}

func TestGenerateLinearFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mkv")
	if out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25",
		"-t", "60", "-c:v", "libx264", "-preset", "ultrafast", "-g", "50", src).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test clip: %v %s", err, out)
	}
	ix, err := GenerateLinear(context.Background(), filepath.Join(dir, "k"), "k", 60, 240, 136, ff, src, nil)
	if err != nil || ix.Count != 6 || ix.Sheets != 1 {
		t.Fatalf("%+v %v", ix, err)
	}
	f, err := os.Open(filepath.Join(dir, "k", "0.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	// frames 0…5 hold the picture, 6 (past the end) is black
	lum := func(i int) uint32 {
		r, g, b, _ := img.At((i%10)*240+120, (i/10)*136+68).RGBA()
		return (r + g + b) >> 8
	}
	for i := range 6 {
		if lum(i) == 0 {
			t.Fatalf("frame %d black", i)
		}
	}
	if lum(6) > 10 {
		t.Fatalf("past the end: %d", lum(6))
	}
}
