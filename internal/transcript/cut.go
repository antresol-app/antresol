package transcript

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

const (
	// cutLimit is the running time (not held for playback) of one piece's
	// ffmpeg; a piece takes seconds.
	cutLimit   = 15 * time.Minute
	yieldEvery = 250 * time.Millisecond
)

// cutPiece reads durMs of a file from startMs as a 16 kHz mono MP3 of
// 32 kb/s (ten minutes are 2.4 MB, far under every service's upload
// limit; every service takes MP3). ffmpeg runs at the lowest CPU and I/O
// priority and is held stopped while busy says playback needs the disk.
func cutPiece(ctx context.Context, ffmpeg, path string, startMs, durMs int64, busy func() bool) ([]byte, error) {
	secs := func(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-ss", secs(startMs), "-t", secs(durMs), "-i", "file:"+path,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "32k",
		"-write_xing", "0", "-id3v2_version", "0", "-f", "mp3", "pipe:1")
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	media.LowPriority(cmd)
	if busy == nil {
		busy = func() bool { return false }
	}
	if err := media.RunYieldingLimit(cmd, busy, yieldEvery, cutLimit); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(errs.String())
		if r := []rune(msg); len(r) > 300 {
			msg = string(r[len(r)-300:])
		}
		return nil, fmt.Errorf("transcript: ffmpeg: %w: %s", err, msg)
	}
	return out.Bytes(), nil
}
