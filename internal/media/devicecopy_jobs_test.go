package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeCopies is a DeviceCopies whose ffmpeg writes size bytes to the output
// (after gate is closed, when set) and counts its runs.
type fakeCopies struct {
	*DeviceCopies
	runs  atomic.Int32
	gate  chan struct{}
	clock *testClock
	free  atomic.Int64
}

func newFakeCopies(t *testing.T, dir string, size int) *fakeCopies {
	t.Helper()
	f := &fakeCopies{clock: &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}}
	f.free.Store(1 << 50)
	f.DeviceCopies = &DeviceCopies{Dir: dir, MaxBytes: 1 << 40, Now: f.clock.Now,
		FreeBytes: func(string) (int64, error) { return f.free.Load(), nil },
		Run: func(ctx context.Context, args []string, progress func(int64)) error {
			f.runs.Add(1)
			if f.gate != nil {
				select {
				case <-f.gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			progress(500_000)
			return os.WriteFile(args[len(args)-1], make([]byte, size), 0o644)
		}}
	t.Cleanup(f.Close)
	return f
}

func testKey(fileID int64) DeviceCopyKey {
	return DeviceCopyKey{FileID: fileID, Size: 1000, ModTime: time.Unix(1_700_000_000, 0), AudioIndex: -1}
}

var testPlan = DeviceCopyPlan{VideoCopy: true, VideoCodec: "h264", AudioIndex: 1, AudioCopy: true, DurationMs: 1000}

func waitState(t *testing.T, c *DeviceCopies, id, state string) CopyStatus {
	t.Helper()
	var st CopyStatus
	waitFor(t, 5*time.Second, id+" "+state, func() bool {
		var err error
		st, err = c.Status(id)
		return err == nil && st.State == state
	})
	return st
}

func TestDeviceCopySameKeySharesJob(t *testing.T) {
	f := newFakeCopies(t, t.TempDir(), 100)
	f.gate = make(chan struct{})
	a, err := f.Request(t.Context(), 1, testKey(5), "/m/a.mkv", testPlan)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Request(t.Context(), 2, testKey(5), "/m/a.mkv", testPlan)
	if err != nil || a.ID != b.ID || b.FileID != 5 || (b.State != "queued" && b.State != "running") {
		t.Fatalf("%+v %+v %v", a, b, err)
	}
	close(f.gate)
	st := waitState(t, f.DeviceCopies, a.ID, "ready")
	if st.Size != 100 || st.Progress != 1 {
		t.Fatalf("%+v", st)
	}
	again, err := f.Request(t.Context(), 3, testKey(5), "/m/a.mkv", testPlan)
	if err != nil || again.State != "ready" {
		t.Fatalf("%+v %v", again, err)
	}
	if n := f.runs.Load(); n != 1 {
		t.Fatalf("ffmpeg ran %d times", n)
	}
	p, fid, err := f.Path(a.ID)
	if err != nil || fid != 5 || filepath.Base(p) != a.ID+".mp4" {
		t.Fatal(p, fid, err)
	}
	var meta map[string]any
	b2, _ := os.ReadFile(filepath.Join(f.Dir, a.ID+".json"))
	if json.Unmarshal(b2, &meta) != nil || meta["fileId"] != float64(5) || meta["size"] != float64(100) ||
		meta["createdAt"] == nil || meta["accessedAt"] == nil {
		t.Fatalf("%s", b2)
	}
}

func TestDeviceCopyPerUserLimit(t *testing.T) {
	f := newFakeCopies(t, t.TempDir(), 10)
	f.gate = make(chan struct{})
	defer close(f.gate)
	for i := int64(1); i <= 3; i++ {
		if _, err := f.Request(t.Context(), 1, testKey(i), "/m/a.mkv", testPlan); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := f.Request(t.Context(), 1, testKey(4), "/m/a.mkv", testPlan); !errors.Is(err, ErrTooMany) {
		t.Fatalf("4th: %v", err)
	}
	// a job already there is shared, whoever asks
	if _, err := f.Request(t.Context(), 1, testKey(2), "/m/a.mkv", testPlan); err != nil {
		t.Fatal(err)
	}
	st, err := f.Request(t.Context(), 2, testKey(4), "/m/a.mkv", testPlan)
	if err != nil || st.State != "queued" {
		t.Fatalf("other user: %+v %v", st, err)
	}
}

func TestDeviceCopyEvictsLRU(t *testing.T) {
	f := newFakeCopies(t, t.TempDir(), 1000)
	f.MaxBytes = 2000
	a, _ := f.Request(t.Context(), 1, testKey(1), "/m/a.mkv", testPlan)
	waitState(t, f.DeviceCopies, a.ID, "ready")
	b, _ := f.Request(t.Context(), 1, testKey(2), "/m/a.mkv", testPlan)
	waitState(t, f.DeviceCopies, b.ID, "ready")
	f.clock.add(49 * time.Hour)
	if _, _, err := f.Path(b.ID); err != nil { // b is watched now, a is not
		t.Fatal(err)
	}
	c, _ := f.Request(t.Context(), 1, testKey(3), "/m/a.mkv", testPlan)
	waitState(t, f.DeviceCopies, c.ID, "ready")
	if _, err := f.Status(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Dir, a.ID+".mp4")); !os.IsNotExist(err) {
		t.Fatal("a's file kept")
	}
	if _, err := os.Stat(filepath.Join(f.Dir, a.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("a's json kept")
	}
	for _, id := range []string{b.ID, c.ID} {
		if st, err := f.Status(id); err != nil || st.State != "ready" {
			t.Fatal(id, st, err)
		}
	}
}

func TestDeviceCopyKeepsRecentlyAccessed(t *testing.T) {
	f := newFakeCopies(t, t.TempDir(), 1000)
	f.MaxBytes = 2000
	var ids []string
	for i := int64(1); i <= 2; i++ {
		st, _ := f.Request(t.Context(), 1, testKey(i), "/m/a.mkv", testPlan)
		waitState(t, f.DeviceCopies, st.ID, "ready")
		ids = append(ids, st.ID)
		f.clock.add(time.Minute)
	}
	f.clock.add(time.Hour)
	st, _ := f.Request(t.Context(), 1, testKey(3), "/m/a.mkv", testPlan)
	waitState(t, f.DeviceCopies, st.ID, "ready")
	for _, id := range append(ids, st.ID) {
		if s, err := f.Status(id); err != nil || s.State != "ready" {
			t.Fatalf("%s evicted: %+v %v", id, s, err)
		}
	}
	// a full disk overrides the protection
	f.free.Store(1)
	f.evict()
	if _, err := f.Status(ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("low disk kept the oldest: %v", err)
	}
}

func TestDeviceCopyRestartKeepsReadyDropsPartial(t *testing.T) {
	dir := t.TempDir()
	f := newFakeCopies(t, dir, 100)
	st, _ := f.Request(t.Context(), 1, testKey(9), "/m/a.mkv", testPlan)
	waitState(t, f.DeviceCopies, st.ID, "ready")
	f.Close()
	part := filepath.Join(dir, "0123456789abcdef.part.mp4")
	os.WriteFile(part, []byte("half"), 0o644)
	os.MkdirAll(filepath.Join(dir, "subs"), 0o755)
	stale := filepath.Join(dir, "subs", "0123456789abcdef-0.srt")
	os.WriteFile(stale, []byte("x"), 0o644)

	g := newFakeCopies(t, dir, 100)
	got, err := g.Status(st.ID)
	if err != nil || got.State != "ready" || got.Size != 100 || got.FileID != 9 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("partial file kept")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale decoded subtitle kept")
	}
	if _, err := g.Request(t.Context(), 1, testKey(9), "/m/a.mkv", testPlan); err != nil || g.runs.Load() != 0 {
		t.Fatal("ready copy re-run", err)
	}
}

func TestDeviceCopyNoSpace(t *testing.T) {
	f := newFakeCopies(t, t.TempDir(), 100)
	f.free.Store(3 << 30) // 3 GiB: under a 2 GiB reserve + the 2 GiB source
	k := testKey(1)
	k.Size = 2 << 30
	if _, err := f.Request(t.Context(), 1, k, "/m/a.mkv", testPlan); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("%v", err)
	}
	// a capped transcode needs only bitrate × duration
	tr := DeviceCopyPlan{VideoCodec: "h264", MaxBitrate: 8_000_000, DurationMs: 60_000, AudioIndex: 1}
	if _, err := f.Request(t.Context(), 1, k, "/m/a.mkv", tr); err != nil {
		t.Fatal(err)
	}
	if f.runs.Load() > 1 {
		t.Fatal("runs")
	}
}

func TestDeviceCopyComicZip(t *testing.T) {
	f := newFakeCopies(t, t.TempDir(), 0)
	mod := time.Unix(1_700_000_000, 0)
	var builds atomic.Int32
	build := func(w io.Writer) error {
		builds.Add(1)
		time.Sleep(20 * time.Millisecond)
		_, err := w.Write([]byte("zipdata"))
		return err
	}
	var wg sync.WaitGroup
	paths := make([]string, 3)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.ComicZip(t.Context(), 4, 99, mod, build)
			if err != nil {
				t.Error(err)
			}
			paths[i] = p
		}()
	}
	wg.Wait()
	want := filepath.Join(f.Dir, "comics", "4-99-1700000000.zip")
	if builds.Load() != 1 || paths[0] != want || paths[1] != want || paths[2] != want {
		t.Fatalf("%d %v", builds.Load(), paths)
	}
	if b, _ := os.ReadFile(want); string(b) != "zipdata" {
		t.Fatal(string(b))
	}
	// an access is recorded beside the zip, never on it (its mtime is the
	// Last-Modified of Range resumes), and survives a restart
	built, _ := os.Stat(want)
	f.clock.add(2 * time.Hour)
	if _, err := f.ComicZip(t.Context(), 4, 99, mod, build); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(want); !st.ModTime().Equal(built.ModTime()) {
		t.Fatal("zip mtime moved")
	}
	if st, err := os.Stat(want + ".atime"); err != nil || !st.ModTime().Equal(f.clock.Now()) {
		t.Fatal("access not recorded", err)
	}
	g := &DeviceCopies{Dir: f.Dir, Now: f.clock.Now}
	g.Start()
	g.mu.Lock()
	acc := g.files[want].accessed
	g.mu.Unlock()
	g.Close()
	if !acc.Equal(f.clock.Now()) {
		t.Fatalf("restart access %v", acc)
	}
	// a changed volume gets a new zip; the old one goes
	p2, err := f.ComicZip(t.Context(), 4, 100, mod, build)
	if err != nil || p2 == want {
		t.Fatal(p2, err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatal("old zip kept")
	}
	if _, err := os.Stat(want + ".atime"); !os.IsNotExist(err) {
		t.Fatal("old zip's access file kept")
	}
	// zips count in MaxBytes and are evicted like copies
	f.MaxBytes = 5
	f.clock.add(49 * time.Hour)
	st, _ := f.Request(t.Context(), 1, testKey(1), "/m/a.mkv", testPlan)
	waitState(t, f.DeviceCopies, st.ID, "ready")
	if _, err := os.Stat(p2); !os.IsNotExist(err) {
		t.Fatal("zip not evicted")
	}
}

func TestDeviceCopyJobRealFFmpeg(t *testing.T) {
	ff := needFFmpeg(t)
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "in.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:01,500\nHello\n"), 0o644)
	src := filepath.Join(dir, "clip.mkv")
	cmd := exec.Command(ff, "-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=15:d=2",
		"-f", "lavfi", "-i", "sine=frequency=440:d=2", "-i", srt,
		"-map", "0", "-map", "1", "-map", "2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "flac", "-c:s", "srt", src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clip: %v %s", err, b)
	}
	ext := filepath.Join(dir, "clip.ru.srt")
	os.WriteFile(ext, []byte("1\n00:00:00,500 --> 00:00:01,500\nПривет\n"), 0o644)

	p, err := Probe(t.Context(), fp, src)
	if err != nil {
		t.Fatal(err)
	}
	pl := PlanDeviceCopy(p, []ExternalSub{{Path: ext, Lang: "ru", Codec: "subrip"}}, 0, 0, -1)
	if !pl.VideoCopy || pl.AudioCopy || len(pl.SubIndexes) != 1 {
		t.Fatalf("%+v", pl)
	}
	c := &DeviceCopies{Dir: filepath.Join(dir, "device"), FFmpeg: ff, HW: "none", MinFreeBytes: -1}
	t.Cleanup(c.Close)
	st, err := c.Request(t.Context(), 1, DeviceCopyKey{FileID: 1, Size: 1}, src, pl)
	if err != nil {
		t.Fatal(err)
	}
	var got CopyStatus
	waitFor(t, 60*time.Second, "copy", func() bool {
		got, _ = c.Status(st.ID)
		if got.State == "failed" {
			t.Fatalf("failed: %+v", got)
		}
		return got.State == "ready"
	})
	out := filepath.Join(c.Dir, st.ID+".mp4")
	b, err := exec.Command(fp, "-v", "error", "-show_entries", "format=format_name:stream=codec_name,codec_type:stream_tags=language",
		"-of", "json", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	var pr struct {
		Format struct {
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecName string            `json:"codec_name"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
	}
	json.Unmarshal(b, &pr)
	var codecs, langs []string
	for _, s := range pr.Streams {
		codecs = append(codecs, s.CodecName)
		if s.CodecName == "mov_text" {
			langs = append(langs, s.Tags["language"])
		}
	}
	if !strings.HasPrefix(pr.Format.FormatName, "mov,mp4") || !slices.Equal(codecs, []string{"h264", "aac", "mov_text", "mov_text"}) ||
		langs[1] != "rus" || got.Size <= 0 || got.Progress != 1 {
		t.Fatalf("%s %+v", b, got)
	}
}
