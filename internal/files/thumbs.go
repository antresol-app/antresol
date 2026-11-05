package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

// ErrNoThumb: this entry has no thumbnail (or ffmpeg could not make one).
var ErrNoThumb = errors.New("files: no thumbnail")

const (
	thumbTimeout     = 10 * time.Second
	thumbConcurrency = 2
	thumbFailTTL     = 10 * time.Minute
)

// thumbWidths are the sizes generated; a request is rounded up to one of them.
var thumbWidths = []int{160, 320, 480, 640, 960, 1280}

// folderArt are the artwork names a movie/series folder thumbnail is taken from.
var folderArt = []string{"poster.jpg", "folder.jpg", "cover.jpg", "poster.png", "folder.png", "cover.png"}

// Thumbnailer renders JPEG thumbnails with ffmpeg into Dir (DataDir/thumbs).
type Thumbnailer struct {
	FFmpeg  string
	FFprobe string // for the video duration; "" uses a fixed offset
	Dir     string
	// Probe overrides media.Probe (tests).
	Probe func(ctx context.Context, path string) (media.ProbeResult, error)

	once   sync.Once
	sem    chan struct{}
	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	failed map[string]time.Time
}

func (t *Thumbnailer) init() {
	t.once.Do(func() {
		t.sem = make(chan struct{}, thumbConcurrency)
		t.locks = map[string]*sync.Mutex{}
		t.failed = map[string]time.Time{}
	})
}

// Acquire takes one of the shared ffmpeg/ffprobe slots (also used by the
// file browser's ffprobe) and returns its release.
func (t *Thumbnailer) Acquire(ctx context.Context) (func(), error) {
	t.init()
	select {
	case t.sem <- struct{}{}:
		return func() { <-t.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ThumbWidth rounds a requested width up to a generated size.
func ThumbWidth(w int) int {
	for _, s := range thumbWidths {
		if w <= s {
			return s
		}
	}
	return thumbWidths[len(thumbWidths)-1]
}

// folderPoster returns the artwork file of a folder, if any. Only a regular
// file counts: a symlinked poster.jpg could point anywhere on the disk.
func folderPoster(dir string) (string, bool) {
	for _, n := range folderArt {
		p := filepath.Join(dir, n)
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// hasThumb reports whether an entry can have a thumbnail (cheap check).
func hasThumb(abs, kind string) bool {
	switch kind {
	case "video", "image":
		return true
	case "folder":
		_, ok := folderPoster(abs)
		return ok
	}
	return false
}

// Thumb returns the path of a cached JPEG thumbnail of abs, rendering it on
// first use. kind is the entry kind (video, image or folder).
func (t *Thumbnailer) Thumb(ctx context.Context, abs, kind string, w int) (string, error) {
	t.init()
	src := abs
	switch kind {
	case "video", "image":
	case "folder":
		p, ok := folderPoster(abs)
		if !ok {
			return "", ErrNoThumb
		}
		src, kind = p, "image"
	default:
		return "", ErrNoThumb
	}
	st, err := os.Stat(src)
	if err != nil || !st.Mode().IsRegular() {
		return "", ErrNoThumb
	}
	w = ThumbWidth(w)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d", src, st.ModTime().UnixNano(), st.Size(), w)))
	key := hex.EncodeToString(sum[:])
	out := filepath.Join(t.Dir, key[:2], key+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	unlock := t.lock(key)
	defer unlock()
	if _, err := os.Stat(out); err == nil { // made while we waited
		return out, nil
	}
	t.mu.Lock()
	failedAt, failed := t.failed[key]
	t.mu.Unlock()
	if failed && time.Since(failedAt) < thumbFailTTL {
		return "", ErrNoThumb
	}

	select {
	case t.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-t.sem }()

	if err := t.render(ctx, src, kind, w, out); err != nil {
		if ctx.Err() == nil { // a cancelled client is not a broken file
			t.mu.Lock()
			_, seen := t.failed[key]
			t.failed[key] = time.Now()
			t.mu.Unlock()
			if !seen { // once per file version and size, not on every retry
				log.Printf("files: thumbnail %s: %v", src, err)
			}
		}
		return "", fmt.Errorf("%w: %v", ErrNoThumb, err)
	}
	return out, nil
}

func (t *Thumbnailer) lock(key string) func() {
	t.mu.Lock()
	m, ok := t.locks[key]
	if !ok {
		m = &sync.Mutex{}
		t.locks[key] = m
	}
	t.mu.Unlock()
	m.Lock()
	return func() {
		m.Unlock()
		t.mu.Lock()
		if t.locks[key] == m {
			delete(t.locks, key)
		}
		t.mu.Unlock()
	}
}

func (t *Thumbnailer) offset(ctx context.Context, src string) float64 {
	probe := t.Probe
	if probe == nil {
		if t.FFprobe == "" {
			return 10
		}
		probe = func(ctx context.Context, p string) (media.ProbeResult, error) { return media.Probe(ctx, t.FFprobe, p) }
	}
	p, err := probe(ctx, src)
	if err != nil || p.DurationMs <= 0 {
		return 10
	}
	return float64(p.DurationMs) / 1000 * 0.1
}

// imageDemuxer forces the demuxer of an image by its extension instead of
// letting ffmpeg probe the content. image2 with pattern_type none also keeps
// a "%d" in a name from being read as a sequence pattern.
func imageDemuxer(src string) []string {
	switch strings.ToLower(filepath.Ext(src)) {
	case ".gif":
		return []string{"-f", "gif"}
	case ".heic", ".heif", ".avif":
		return []string{"-f", "mov"}
	}
	return []string{"-f", "image2", "-pattern_type", "none"}
}

func (t *Thumbnailer) render(ctx context.Context, src, kind string, w int, out string) error {
	ctx, cancel := context.WithTimeout(ctx, thumbTimeout)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ".jpg"
	defer os.Remove(tmp)
	ff := t.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	args := []string{"-nostdin", "-v", "error", "-y"}
	if kind == "video" {
		args = append(args, "-ss", strconv.FormatFloat(t.offset(ctx, src), 'f', 2, 64))
	}
	// Files come from outside: only the file protocol (no playlist may make
	// ffmpeg fetch URLs), images through an explicit image demuxer.
	opts, in, err := media.InputArgs(src)
	if err != nil {
		return err
	}
	args = append(args, opts...)
	if kind == "image" {
		args = append(args, imageDemuxer(src)...)
	}
	args = append(args, "-i", in, "-frames:v", "1", "-an", "-sn",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", w), "-q:v", "4", "-f", "image2", "-c:v", "mjpeg", tmp)
	cmd := exec.CommandContext(ctx, ff, args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		if len(b) > 300 {
			b = b[:300]
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, b)
	}
	if st, err := os.Stat(tmp); err != nil || st.Size() == 0 {
		return errors.New("ffmpeg produced no frame")
	}
	return os.Rename(tmp, out)
}
