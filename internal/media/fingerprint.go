package media

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Runner runs an ffmpeg command (RunYielding in the background; cmd.Run in
// tests).
type Runner func(*exec.Cmd) error

func runCmd(run Runner, cmd *exec.Cmd) error {
	if run == nil {
		return cmd.Run()
	}
	return run(cmd)
}

// HasMuxer reports whether ffmpeg has the muxer name (`ffmpeg -muxers`):
// the server image's ffmpeg has chromaprint, a stock macOS build does not.
func HasMuxer(ctx context.Context, ffmpeg, name string) bool {
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-muxers").Output()
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[1] == name && strings.Contains(f[0], "E") {
			return true
		}
	}
	return false
}

func secArg(s float64) string { return strconv.FormatFloat(s, 'f', 3, 64) }

// Fingerprint is the chromaprint fingerprint (raw items, see
// segments.ItemSec) of the audio stream idx (ffprobe index) of a local
// file from fromSec for durSec, item 0 at fromSec. It needs ffmpeg's
// chromaprint muxer (HasMuxer).
func Fingerprint(ctx context.Context, ffmpeg, path string, idx int, fromSec, durSec float64, run Runner) ([]uint32, error) {
	opts, in, err := InputArgs(path)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if fromSec > 0 {
		args = append(args, "-ss", secArg(fromSec))
	}
	args = append(args, "-t", secArg(durSec))
	args = append(args, opts...)
	args = append(args, "-i", in, "-map", "0:"+strconv.Itoa(idx), "-vn", "-sn", "-dn", "-ac", "1",
		"-f", "chromaprint", "-fp_format", "raw", "pipe:1")
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := runCmd(run, cmd); err != nil {
		return nil, fmt.Errorf("ffmpeg chromaprint: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseRawFingerprint(stdout.Bytes()), nil
}

// parseRawFingerprint reads the muxer's raw output: little-endian uint32s.
func parseRawFingerprint(b []byte) []uint32 {
	out := make([]uint32, len(b)/4)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return out
}

// BlackRun is a stretch of black picture, seconds on the file timeline.
type BlackRun struct{ Start, End float64 }

// BlackRuns lists the black stretches (ffmpeg blackdetect, at least minSec)
// of a local file between fromSec and fromSec+durSec. keyframesOnly decodes
// the keyframes alone: a fraction of the work, positions to a keyframe
// interval (enough for the credits, not for a 2 s black run).
func BlackRuns(ctx context.Context, ffmpeg, path string, fromSec, durSec, minSec float64, keyframesOnly bool, run Runner) ([]BlackRun, error) {
	opts, in, err := InputArgs(path)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "info", "-nostdin", "-nostats"}
	if keyframesOnly {
		args = append(args, "-skip_frame", "nokey")
	}
	if fromSec > 0 {
		args = append(args, "-ss", secArg(fromSec))
	}
	args = append(args, "-t", secArg(durSec))
	args = append(args, opts...)
	// rolling credits leave most of the picture black: 85 % of the pixels
	args = append(args, "-i", in, "-map", "0:v:0", "-an", "-sn", "-dn",
		"-vf", "scale=320:-2,blackdetect=d="+secArg(minSec)+":pic_th=0.85:pix_th=0.10", "-f", "null", "-")
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stderr = &stderr
	if err := runCmd(run, cmd); err != nil {
		return nil, fmt.Errorf("ffmpeg blackdetect: %w: %s", err, lastLine(stderr.String()))
	}
	return parseBlackdetect(stderr.String(), fromSec), nil
}

var blackRe = regexp.MustCompile(`black_start:\s*([0-9.]+)\s+black_end:\s*([0-9.]+)`)

// parseBlackdetect reads blackdetect's log lines; times are from the seek
// point, shifted by from onto the file timeline.
func parseBlackdetect(log string, from float64) []BlackRun {
	var out []BlackRun
	for _, m := range blackRe.FindAllStringSubmatch(log, -1) {
		s, err1 := strconv.ParseFloat(m[1], 64)
		e, err2 := strconv.ParseFloat(m[2], 64)
		if err1 == nil && err2 == nil && e > s {
			out = append(out, BlackRun{from + s, from + e})
		}
	}
	return out
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
