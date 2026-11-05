package trickplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/media"
)

// FrameFunc reads one frame near atSec as w×h RGB (3 bytes a pixel, rows
// top to bottom).
type FrameFunc func(ctx context.Context, atSec float64) ([]byte, error)

// ErrTooManyFailed: most frames of a file could not be read.
var ErrTooManyFailed = errors.New("trickplay: most frames could not be read")

// Frames is how many frames a file of durSec seconds gets.
func Frames(durSec float64) int {
	if durSec <= 0 {
		return 0
	}
	n := int(durSec / IntervalSec)
	if float64(n*IntervalSec) < durSec {
		n++
	}
	return n
}

// sheets assembles frames into the sheets of one file, written to dir as
// each fills (a 2 h film never sits in memory whole).
type sheets struct {
	dir     string
	ix      Index
	cur     int // the sheet being filled (-1 none)
	img     *image.RGBA
	got     int // frames placed
	written int // sheets written
}

func newSheets(dir, key string, durSec float64, w, h int) (*sheets, error) {
	n := Frames(durSec)
	ix := Index{Version: Version, Key: key, Interval: IntervalSec, Width: w, Height: h,
		Columns: Columns, Rows: Rows, Count: n, Sheets: (n + Columns*Rows - 1) / (Columns * Rows)}
	if n == 0 || w <= 0 || h <= 0 {
		return nil, errors.New("trickplay: nothing to read")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &sheets{dir: dir, ix: ix, cur: -1}, nil
}

// put places frame i (w×h RGB); frames come in order, a skipped one stays black.
func (s *sheets) put(i int, px []byte) error {
	if i < 0 || i >= s.ix.Count {
		return nil
	}
	per := Columns * Rows
	for s.cur < i/per {
		if err := s.flush(); err != nil {
			return err
		}
		s.cur++
		s.img = image.NewRGBA(image.Rect(0, 0, Columns*s.ix.Width, Rows*s.ix.Height))
		for k := range len(s.img.Pix) / 4 {
			s.img.Pix[4*k+3] = 0xff // opaque black
		}
	}
	if px == nil {
		return nil
	}
	w, h := s.ix.Width, s.ix.Height
	j := i % per
	x0, y0 := (j%Columns)*w, (j/Columns)*h
	for y := range h {
		row := s.img.Pix[s.img.PixOffset(x0, y0+y):]
		src := px[y*w*3 : (y+1)*w*3]
		for x := range w {
			row[4*x], row[4*x+1], row[4*x+2] = src[3*x], src[3*x+1], src[3*x+2]
		}
	}
	s.got++
	return nil
}

func (s *sheets) flush() error {
	if s.img == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, s.img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.dir, strconv.Itoa(s.cur)+".jpg"), buf.Bytes(), 0o644); err != nil {
		return err
	}
	s.img = nil
	s.written++
	return nil
}

// finish writes the last sheets and the index; fewer than half the frames
// read is ErrTooManyFailed.
func (s *sheets) finish() (Index, error) {
	if s.got*2 < s.ix.Count {
		return s.ix, ErrTooManyFailed
	}
	if err := s.put(s.ix.Count-1, nil); err != nil { // every sheet exists
		return s.ix, err
	}
	if err := s.flush(); err != nil {
		return s.ix, err
	}
	b, err := json.Marshal(s.ix)
	if err != nil {
		return s.ix, err
	}
	return s.ix, os.WriteFile(filepath.Join(s.dir, "index.json"), b, 0o644)
}

// Generate reads the frames of a durSec file into sheets in dir (made if
// missing), one read per frame: frame i is read at the middle of its
// interval (the keyframe before it, see FFmpegFrame). A frame that cannot
// be read stays black; more than half of them failing is ErrTooManyFailed
// (no index is written then). ctx ending stops it at once with ctx's error.
func Generate(ctx context.Context, dir, key string, durSec float64, w, h int, frame FrameFunc) (Index, error) {
	s, err := newSheets(dir, key, durSec, w, h)
	if err != nil {
		return Index{}, err
	}
	failed := 0
	for i := range s.ix.Count {
		at := min(float64(i*IntervalSec)+IntervalSec/2.0, durSec-0.5)
		px, err := frame(ctx, max(at, 0))
		if ctx.Err() != nil {
			return s.ix, ctx.Err()
		}
		if err != nil || len(px) != w*h*3 {
			if failed++; failed*2 > s.ix.Count {
				return s.ix, ErrTooManyFailed
			}
			continue
		}
		if err := s.put(i, px); err != nil {
			return s.ix, err
		}
	}
	return s.finish()
}

// frameWriter cuts ffmpeg's raw output into frames for sheets.
type frameWriter struct {
	s    *sheets
	size int
	buf  []byte
	n    int
	err  error
}

func (fw *frameWriter) Write(p []byte) (int, error) {
	if fw.err != nil {
		return 0, fw.err
	}
	total := len(p)
	for len(p) > 0 {
		k := min(fw.size-len(fw.buf), len(p))
		fw.buf = append(fw.buf, p[:k]...)
		p = p[k:]
		if len(fw.buf) == fw.size {
			if fw.err = fw.s.put(fw.n, fw.buf); fw.err != nil {
				return 0, fw.err
			}
			fw.n++
			fw.buf = fw.buf[:0]
		}
	}
	return total, nil
}

// GenerateLinear reads the frames of a file in ONE pass over it, for a
// file that has no index to seek by (Matroska without Cues: a seek there
// reads from the start): its keyframes only (-skip_frame nokey), one
// every IntervalSec by their pts (fps). run runs the command (its time
// limit bounds the read); a run that stops early keeps what it read when
// that is at least half.
func GenerateLinear(ctx context.Context, dir, key string, durSec float64, w, h int, ffmpeg, path string, run media.Runner) (Index, error) {
	s, err := newSheets(dir, key, durSec, w, h)
	if err != nil {
		return Index{}, err
	}
	opts, in, err := media.InputArgs(path)
	if err != nil {
		return s.ix, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-nostats", "-threads", "1", "-skip_frame", "nokey"}
	args = append(args, opts...)
	// frame n is the keyframe nearest n·interval (the last one kept at the end)
	vf := fmt.Sprintf("fps=fps=1/%d:eof_action=pass,", IntervalSec) + fitFilter(w, h)
	args = append(args, "-i", in, "-map", "0:v:0", "-an", "-sn", "-dn", "-vf", vf, "-threads", "1", "-f", "rawvideo", "pipe:1")
	fw := &frameWriter{s: s, size: w * h * 3}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stdout, cmd.Stderr = fw, &stderr
	media.LowPriority(cmd)
	if run == nil {
		err = cmd.Run()
	} else {
		err = run(cmd)
	}
	if ctx.Err() != nil {
		return s.ix, ctx.Err()
	}
	if fw.err != nil {
		return s.ix, fw.err
	}
	if err != nil && s.got*2 < s.ix.Count {
		return s.ix, fmt.Errorf("ffmpeg linear read: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return s.finish()
}

// fitFilter fits the picture into w×h (square pixels first, black bars
// around) as RGB.
func fitFilter(w, h int) string {
	return fmt.Sprintf("scale='if(gt(sar,0),iw*sar,iw)':ih,scale=%d:%d:force_original_aspect_ratio=decrease,"+
		"pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,format=rgb24", w, h, w, h)
}

// FFmpegFrame reads the keyframe at or before atSec of path, w×h (the
// picture fitted in, black bars around), as RGB. It seeks there and
// decodes that one keyframe (-skip_frame nokey), so a frame costs one
// seek and one keyframe's read, not the file's; one decoding thread.
// run runs the command (nil: cmd.Run); the command runs at the lowest
// priority (media.LowPriority).
func FFmpegFrame(ctx context.Context, ffmpeg, path string, atSec float64, w, h int, run media.Runner) ([]byte, error) {
	opts, in, err := media.InputArgs(path)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-nostats",
		"-threads", "1", "-skip_frame", "nokey", "-ss", strconv.FormatFloat(atSec, 'f', 3, 64), "-noaccurate_seek"}
	args = append(args, opts...)
	args = append(args, "-i", in, "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1",
		"-vf", fitFilter(w, h), "-threads", "1", "-f", "rawvideo", "pipe:1")
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	media.LowPriority(cmd)
	if run == nil {
		err = cmd.Run()
	} else {
		err = run(cmd)
	}
	if err != nil {
		return nil, fmt.Errorf("ffmpeg frame at %.0fs: %w: %s", atSec, err, strings.TrimSpace(stderr.String()))
	}
	if out.Len() != w*h*3 {
		return nil, fmt.Errorf("ffmpeg frame at %.0fs: %d bytes, not %d", atSec, out.Len(), w*h*3)
	}
	return out.Bytes(), nil
}

var _ io.Writer = (*frameWriter)(nil)
