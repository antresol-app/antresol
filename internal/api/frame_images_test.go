package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
)

// fakeFrames stands in for ffmpeg: it answers a small JPEG and records
// the command lines.
type fakeFrames struct {
	mu   sync.Mutex
	args [][]string
	fail bool
}

func (f *fakeFrames) run(cmd *exec.Cmd) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.args = append(f.args, cmd.Args)
	if f.fail {
		return exec.ErrNotFound
	}
	cmd.Stdout.Write([]byte{0xff, 0xd8, 0xff, 0xe0, 'j', 'p', 'g', 0xff, 0xd9})
	return nil
}

func (f *fakeFrames) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.args)
}

func TestFrameImage(t *testing.T) {
	ff := &fakeFrames{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.FrameRunner = ff.run })
	ctx := context.Background()
	series, eps := e.series("Кадры", "", 2)
	fs, err := e.repo.FilesOf(ctx, eps[0])
	if err != nil || len(fs) != 1 {
		t.Fatal(err, fs)
	}
	probe, _ := json.Marshal(media.ProbeResult{DurationMs: 1_000_000, Video: &media.VideoStream{Width: 1920, Height: 1080}})
	if err := e.repo.SetProbe(ctx, fs[0].ID, string(probe), 1_000_000); err != nil {
		t.Fatal(err)
	}
	path := "/api/images/frame/" + itoa(eps[0])
	w := e.do("GET", path, "", e.admin)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, max-age=604800" || w.Body.Len() != 9 {
		t.Fatalf("%d %v %q", w.Code, w.Header(), w.Body)
	}
	if ff.calls() != 1 {
		t.Fatalf("%d ffmpeg runs", ff.calls())
	}
	args := ff.args[0]
	ss, in := slices.Index(args, "-ss"), slices.Index(args, "-i")
	if ss < 0 || in < 0 || ss > in {
		t.Fatalf("-ss must come before -i: %v", args)
	}
	at, _ := strconv.ParseFloat(args[ss+1], 64)
	if at < 200 || at > 600 {
		t.Fatalf("frame at %vs of 1000", at)
	}
	if at2 := frameAt(eps[0], 1_000_000); strconv.FormatFloat(at2, 'f', 3, 64) != args[ss+1] {
		t.Fatalf("not deterministic: %v vs %s", at2, args[ss+1])
	}
	// the next request is served from the cache
	if w := e.do("GET", path, "", e.admin); w.Code != 200 || ff.calls() != 1 {
		t.Fatalf("%d, %d runs", w.Code, ff.calls())
	}

	// a series has no frame; a file not on disk (the media disk away) neither
	if w := e.do("GET", "/api/images/frame/"+itoa(series), "", e.admin); w.Code != 404 {
		t.Fatalf("series: %d", w.Code)
	}
	other, _ := e.repo.FilesOf(ctx, eps[1])
	if err := os.Remove(other[0].Path); err != nil {
		t.Fatal(err)
	}
	if w := e.do("GET", "/api/images/frame/"+itoa(eps[1]), "", e.admin); w.Code != 404 || ff.calls() != 1 {
		t.Fatalf("missing file: %d, %d runs", w.Code, ff.calls())
	}
}

// Every slot busy: a quick 503, nothing queued for long.
func TestFrameImageBusy(t *testing.T) {
	ff := &fakeFrames{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.FrameRunner = ff.run })
	id, _ := e.movie("Занято", "", "0123456789abcdef")
	for range frameSlots {
		frameSem <- struct{}{}
	}
	defer func() {
		for range frameSlots {
			<-frameSem
		}
	}()
	w := e.do("GET", "/api/images/frame/"+itoa(id), "", e.admin)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" || ff.calls() != 0 {
		t.Fatalf("%d %v, %d runs", w.Code, w.Header(), ff.calls())
	}
}

// A file ffmpeg cannot read is not read again at once.
func TestFrameImageFailureRemembered(t *testing.T) {
	ff := &fakeFrames{fail: true}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.FrameRunner = ff.run })
	id, _ := e.movie("Битый", "", "0123456789abcdef")
	for range 2 {
		if w := e.do("GET", "/api/images/frame/"+itoa(id), "", e.admin); w.Code != 404 {
			t.Fatalf("%d", w.Code)
		}
	}
	if ff.calls() != 1 {
		t.Fatalf("%d runs", ff.calls())
	}
}
