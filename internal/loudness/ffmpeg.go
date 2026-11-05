package loudness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/media"
)

// silenceLUFS: ebur128 reports this (its absolute gate) when nothing in
// the track is loud enough to measure.
const silenceLUFS = -69.5

// FFmpegLoudness measures a track (startMs to endMs of the file; endMs nil:
// to its end) with ffmpeg's ebur128 filter: the integrated loudness and the
// sample peak. ffmpeg runs at the lowest priority (media.LowPriority);
// run starts and waits for it (nil: cmd.Run).
func FFmpegLoudness(ctx context.Context, ffmpeg, path string, startMs int64, endMs *int64, run func(*exec.Cmd) error) (Result, error) {
	args := []string{"-hide_banner", "-nostats", "-nostdin", "-loglevel", "info"}
	if startMs > 0 {
		args = append(args, "-ss", msArg(startMs))
	}
	if endMs != nil && *endMs > startMs {
		args = append(args, "-t", msArg(*endMs-startMs))
	}
	args = append(args, "-i", path, "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-af", "ebur128=peak=sample:framelog=quiet", "-f", "null", "-")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &tail{buf: &stderr, max: 64 << 10}
	media.LowPriority(cmd)
	if run == nil {
		run = (*exec.Cmd).Run
	}
	if err := run(cmd); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("ffmpeg ebur128: %w: %s", err, lastLine(stderr.String()))
	}
	return ParseEBUR128(stderr.String())
}

func msArg(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }

// ParseEBUR128 reads the summary ebur128 prints at the end: «I: -14.2
// LUFS» under «Integrated loudness» and «Peak: -0.3 dBFS» under «Sample
// peak» (or «True peak»).
func ParseEBUR128(out string) (Result, error) {
	i := strings.LastIndex(out, "Summary:")
	if i < 0 {
		return Result{}, errors.New("ebur128: no summary")
	}
	sum := out[i:]
	var lufs, peak float64
	var haveI, haveP bool
	section := ""
	for _, line := range strings.Split(sum, "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasSuffix(l, ":"):
			section = strings.ToLower(l)
		case strings.HasPrefix(l, "I:") && strings.Contains(section, "integrated"):
			lufs, haveI = summaryNumber(strings.TrimPrefix(l, "I:"))
		case strings.HasPrefix(l, "Peak:") && strings.Contains(section, "peak"):
			peak, haveP = summaryNumber(strings.TrimPrefix(l, "Peak:"))
		}
	}
	if !haveI {
		return Result{}, errors.New("ebur128: no integrated loudness")
	}
	r := Result{}
	if lufs > silenceLUFS {
		r.LUFS = &lufs
	}
	if haveP && !math.IsInf(peak, -1) {
		r.Peak = math.Pow(10, peak/20)
	}
	return r, nil
}

// summaryNumber parses «-14.2 LUFS», «-0.3 dBFS», «-inf dBFS».
func summaryNumber(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	if f[0] == "-inf" {
		return math.Inf(-1), true
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil || math.IsNaN(n) {
		return 0, false
	}
	return n, true
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// tail keeps the last max bytes written (the summary is at the end).
type tail struct {
	buf *bytes.Buffer
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}
