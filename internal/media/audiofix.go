package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// An MPEG-TS stream from outside (an IPTV provider's) often carries audio
// that browsers and apps cannot decode: MP2, AC-3, E-AC-3, DTS, LATM AAC. The
// picture plays and the channel is silent. These helpers see it and mend it
// by copying the video as it is and encoding only the audio as AAC stereo.
//
// ffmpeg is never given the stream's address: the caller reads the stream
// (under its own rules for what it may dial) and hands ffmpeg the bytes on
// its standard input, so the only protocol ffmpeg opens is the pipe and the
// only demuxer it may use is MPEG-TS (the same whitelists as InputArgs).

// tsPacket is the size of an MPEG-TS packet; each starts with tsSync.
const (
	tsPacket = 188
	tsSync   = 0x47
)

// LooksLikeTS reports whether b starts with MPEG-TS packets (three sync
// bytes a packet apart; b shorter than that is not decided).
func LooksLikeTS(b []byte) bool {
	return len(b) > 2*tsPacket && b[0] == tsSync && b[tsPacket] == tsSync && b[2*tsPacket] == tsSync
}

// AudioPlayable reports whether clients play an audio codec in a live
// stream as it is: AAC and MP3 (ffprobe's names). Anything else, and AAC in
// LATM framing, which only some players read, is encoded.
func AudioPlayable(codec string) bool { return codec == "aac" || codec == "mp3" }

// FirstAudioToFix: the codec of the first audio track clients cannot play
// ("" when ffprobe named none), and whether there is one.
func (p ProbeResult) FirstAudioToFix() (codec string, ok bool) {
	for _, a := range p.Audio {
		if !AudioPlayable(a.Codec) {
			return a.Codec, true
		}
	}
	return "", false
}

// tsStdinArgs read an MPEG-TS from standard input and nothing else.
var tsStdinArgs = []string{"-protocol_whitelist", "pipe", "-format_whitelist", "mpegts", "-f", "mpegts"}

// ProbeTSHead is Probe of the start of an MPEG-TS stream, given as bytes.
func ProbeTSHead(ctx context.Context, ffprobe string, head []byte) (ProbeResult, error) {
	args := append([]string{"-v", "error"}, tsStdinArgs...)
	args = append(args, "-probesize", "512k", "-analyzeduration", "1M",
		"-print_format", "json", "-show_format", "-show_streams", "-i", "pipe:0")
	cmd := exec.CommandContext(ctx, ffprobe, args...)
	cmd.Stdin = bytes.NewReader(head)
	var stderr tail
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseProbe(out, "")
}

// RemuxAudioArgs are ffmpeg's arguments for RemuxAudio: every video track
// copied, every audio track AAC stereo, timestamps as they were, nothing
// held back.
func RemuxAudioArgs() []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostats"}
	args = append(args, tsStdinArgs...)
	return append(args, "-probesize", "512k", "-analyzeduration", "1M", "-i", "pipe:0",
		"-map", "0:v?", "-map", "0:a?", "-c:v", "copy", "-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-copyts", "-muxdelay", "0", "-muxpreload", "0", "-flush_packets", "1", "-f", "mpegts", "pipe:1")
}

// RemuxAudio reads an MPEG-TS stream from in and writes it to out with its
// video copied and its audio as AAC stereo, until in ends, out fails or ctx
// ends (ffmpeg is then stopped).
func RemuxAudio(ctx context.Context, ffmpeg string, in io.Reader, out io.Writer) error {
	cmd := exec.CommandContext(ctx, ffmpeg, RemuxAudioArgs()...)
	cmd.Stdin, cmd.Stdout = in, out
	var stderr tail
	cmd.Stderr = &stderr
	// ffmpeg gone, the goroutine still waiting on a silent source for the
	// next bytes to write: let go of it
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// tail keeps the last 2 KB written to it (an error message's worth of
// ffmpeg's stderr).
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.b) }
