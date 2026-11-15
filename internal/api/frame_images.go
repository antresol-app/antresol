package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// A frame of a video (GET /api/images/frame/{itemId}): the picture of an
// episode without a still of its own (Card.Thumb).
const (
	frameWidth = 640
	// frameSlots: ffmpeg reads at once; a grid of forty episodes must not
	// take every core.
	frameSlots = 2
	// frameSlotWait: how long a request waits for a slot before it answers
	// 503 (the app shows its placeholder and asks again later).
	frameSlotWait = 2 * time.Second
	// frameLimit bounds one ffmpeg read (a slow disk, a broken file).
	frameLimit = 30 * time.Second
)

// frameSem bounds the frame reads of the whole server.
var frameSem = make(chan struct{}, frameSlots)

// framing: one read per file version, however many ask together.
var framing singleflight.Group

var errFrameBusy = errors.New("frame: every slot busy")

// frameFailed: files ffmpeg could not read a frame of, and when (not
// tried again for frameRetry).
var frameFailed sync.Map

const frameRetry = time.Hour

// frameAt is where an item's frame is read: between 20 % and 60 % of the
// duration, the same place for the same item (seeded by its id); 0 when
// the duration is unknown.
func frameAt(itemID, durationMs int64) float64 {
	if durationMs <= 0 {
		return 0
	}
	h := fnv.New32a()
	fmt.Fprintf(h, "frame/%d", itemID)
	frac := 0.2 + 0.4*float64(h.Sum32()%10000)/10000
	return float64(durationMs) / 1000 * frac
}

// frameName is the cache file of a file's frame: a replaced file (new
// size or mtime) gets a new one.
func frameName(f library.File) string {
	return fmt.Sprintf("%d-%d-%d.jpg", f.ID, f.Mtime, f.Size)
}

func (h *lib) frameDir() string { return filepath.Join(h.DataDir, "frames") }

// frameImage serves a JPEG frame of the item's first file, about 640 px
// wide, made once (lowest priority, two at a time) and kept in
// DataDir/frames. 404 when the item has no file or it is not on disk (the
// media disk away); 503 when every slot stays busy for a moment.
func (h *lib) frameImage(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "itemId")
	if !ok {
		return
	}
	notFound := func() {
		w.Header().Set("Cache-Control", "no-store")
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
	}
	if (it.Kind != library.KindMovie && it.Kind != library.KindEpisode) || h.DataDir == "" {
		notFound()
		return
	}
	fs, err := h.Library.FilesOf(r.Context(), it.ID)
	if err != nil || len(fs) == 0 {
		notFound()
		return
	}
	f := fs[0]
	out := filepath.Join(h.frameDir(), frameName(f))
	if _, err := os.Stat(out); err == nil {
		serveImage(w, r, out, "", false)
		return
	}
	if at, ok := frameFailed.Load(out); ok && time.Since(at.(time.Time)) < frameRetry {
		notFound()
		return
	}
	if _, err := os.Stat(f.Path); err != nil {
		notFound() // not on disk now: the media disk is away
		return
	}
	_, err, _ = framing.Do(out, func() (any, error) {
		select {
		case frameSem <- struct{}{}:
		case <-time.After(frameSlotWait):
			return nil, errFrameBusy
		}
		defer func() { <-frameSem }()
		// the read outlives a request that gives up: the next one finds it
		ctx, cancel := context.WithTimeout(context.Background(), frameLimit)
		defer cancel()
		err := h.makeFrame(ctx, f, frameAt(it.ID, f.DurationMs), out)
		if err != nil {
			frameFailed.Store(out, time.Now())
		}
		return nil, err
	})
	switch {
	case errors.Is(err, errFrameBusy):
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "5")
		writeErr(w, r, http.StatusServiceUnavailable, "Картинка ещё не готова")
		return
	case err != nil:
		log.Printf("api: frame of item %d: %v", it.ID, err)
		notFound()
		return
	}
	serveImage(w, r, out, "", false)
}

// makeFrame reads the keyframe at atSec of f into out (a JPEG), at the
// lowest priority, and removes the file's frames of its older versions.
func (h *lib) makeFrame(ctx context.Context, f library.File, atSec float64, out string) error {
	opts, in, err := media.InputArgs(f.Path)
	if err != nil {
		return err
	}
	ff := h.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	// -ss before -i: a seek of the input, one keyframe decoded
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-nostats",
		"-threads", "1", "-skip_frame", "nokey", "-ss", strconv.FormatFloat(atSec, 'f', 3, 64), "-noaccurate_seek"}
	args = append(args, opts...)
	args = append(args, "-i", in, "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1",
		"-vf", fmt.Sprintf("scale='if(gt(sar,0),iw*sar,iw)':ih,scale=%d:-2,setsar=1", frameWidth),
		"-threads", "1", "-q:v", "4", "-f", "image2", "-c:v", "mjpeg", "pipe:1")
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ff, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	media.LowPriority(cmd)
	if h.FrameRunner != nil {
		err = h.FrameRunner(cmd)
	} else {
		err = cmd.Run()
	}
	if err != nil {
		return fmt.Errorf("ffmpeg at %.0fs: %w: %s", atSec, err, strings.TrimSpace(stderr.String()))
	}
	b := stdout.Bytes()
	if len(b) < 3 || b[0] != 0xff || b[1] != 0xd8 {
		return fmt.Errorf("ffmpeg at %.0fs: no JPEG (%d bytes)", atSec, len(b))
	}
	if err := os.MkdirAll(h.frameDir(), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		return err
	}
	// the frames of the file before it was replaced
	old, _ := filepath.Glob(filepath.Join(h.frameDir(), strconv.FormatInt(f.ID, 10)+"-*.jpg"))
	for _, p := range old {
		if p != out {
			os.Remove(p)
		}
	}
	return nil
}
