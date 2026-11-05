package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/internal/i18n"
)

// ErrAudioBusy: every audio transcoding slot is taken. Audio transcodes do
// not use HLS slots (ErrBusy); they have a limit of their own.
var ErrAudioBusy = errors.New("audio transcoder busy")

// DefaultAudioAccept is what a client accepts when it does not say.
var DefaultAudioAccept = []string{"flac", "alac", "mp3", "aac", "opus", "vorbis"}

// AudioRequest describes a source audio file and what the client wants.
type AudioRequest struct {
	Codec       string   // source codec (tracks.codec, or by file extension)
	BitrateKbps int      // source bitrate, 0 when unknown
	Accept      []string // codecs the client plays (empty = DefaultAudioAccept)
	MaxKbps     int      // 0 = no limit
	// MaxKbpsSet: the client gave MaxKbps explicitly (even 0); then the
	// remote cap («Музыка вне дома») does not apply.
	MaxKbpsSet bool
	Format     string // "" | "raw" | "aac" | "mp3" | "opus"
	Segment    bool   // a CUE track: a part of a longer file
	Lossless   bool
	// Lang is the interface language of AudioPlan.Reason (i18n code; "" =
	// Russian).
	Lang string
}

// AudioPlan is how to send the audio: the file as is, or a transcode.
type AudioPlan struct {
	Direct      bool
	Codec       string // "copy" | "flac" | "aac" | "mp3" | "opus"
	Kbps        int    // 0 for copy and flac
	ContentType string // "" for Direct (by the file's extension)
	Reason      string // for logs and the UI, in Russian
}

var audioContentTypes = map[string]string{
	"aac":  "audio/aac", // ADTS
	"mp3":  "audio/mpeg",
	"opus": "audio/ogg; codecs=opus",
	"flac": "audio/flac",
}

// maxAudioKbps is the highest bitrate of each lossy target.
var maxAudioKbps = map[string]int{"aac": 256, "opus": 256, "mp3": 320}

const minAudioKbps = 64

func transcodePlan(codec string, maxKbps int, reason string) AudioPlan {
	kbps := maxAudioKbps[codec]
	if maxKbps > 0 && maxKbps < kbps {
		kbps = max(maxKbps, minAudioKbps)
	}
	return AudioPlan{Codec: codec, Kbps: kbps, ContentType: audioContentTypes[codec], Reason: reason}
}

// AudioDecide picks direct play or a transcode, in this order: an explicit
// "raw" plays as is (not a CUE segment: that needs cutting); a segment is
// cut to FLAC when lossless, unlimited and accepted, else transcoded; a
// bitrate limit transcodes a lossless or heavier source; an explicit lossy
// format other than the source's transcodes; an unsupported codec becomes
// AAC; everything else plays direct.
func AudioDecide(r AudioRequest) AudioPlan {
	codec := strings.ToLower(strings.TrimSpace(r.Codec))
	format := strings.ToLower(strings.TrimSpace(r.Format))
	accept := r.Accept
	if len(accept) == 0 {
		accept = DefaultAudioAccept
	}
	accepts := func(c string) bool {
		for _, a := range accept {
			if strings.EqualFold(strings.TrimSpace(a), c) {
				return true
			}
		}
		return false
	}
	l := r.Lang
	target, explicit := "aac", false
	if _, ok := maxAudioKbps[format]; ok {
		target, explicit = format, true
	}
	switch {
	case format == "raw" && !r.Segment:
		return AudioPlan{Direct: true, Codec: "copy", Reason: i18n.TL(l, "Оригинал по запросу клиента")}
	case r.Segment:
		if r.Lossless && r.MaxKbps == 0 && accepts("flac") && !explicit {
			return AudioPlan{Codec: "flac", ContentType: audioContentTypes["flac"], Reason: i18n.TL(l, "Трек из образа: вырезается без потерь")}
		}
		return transcodePlan(target, r.MaxKbps, i18n.TL(l, "Трек из образа: перекодируется"))
	case r.MaxKbps > 0 && (r.Lossless || r.BitrateKbps > r.MaxKbps):
		return transcodePlan(target, r.MaxKbps, i18n.TL(l, "Ограничение битрейта: %d кбит/с", r.MaxKbps))
	case explicit && codec != target:
		return transcodePlan(target, r.MaxKbps, i18n.TL(l, "Формат по запросу клиента"))
	case !accepts(codec):
		return transcodePlan(target, r.MaxKbps, i18n.TL(l, "Клиент не воспроизводит %s", codec))
	}
	return AudioPlan{Direct: true, Codec: "copy", Reason: i18n.TL(l, "Оригинал")}
}

var audioEncoders = map[string][2]string{ // codec → encoder, muxer
	"aac":  {"aac", "adts"},
	"mp3":  {"libmp3lame", "mp3"},
	"opus": {"libopus", "ogg"},
	"flac": {"flac", "flac"},
}

func msSeconds(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }

// AudioArgs builds the ffmpeg arguments that write the first audio stream of
// input from startMs (to endMs when set) to stdout as p.Codec. The start is
// an input -ss, so the output clock starts at 0 and the end is a duration
// (-t), not -to. input goes through InputArgs (file protocol only, or a
// loopback stream with stream demuxers only).
func AudioArgs(input string, p AudioPlan, startMs int64, endMs *int64) ([]string, error) {
	enc, ok := audioEncoders[p.Codec]
	if !ok {
		return nil, fmt.Errorf("media: no audio encoder for %q", p.Codec)
	}
	opts, operand, err := InputArgs(input)
	if err != nil {
		return nil, err
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if startMs > 0 {
		args = append(args, "-ss", msSeconds(startMs))
	}
	args = append(args, opts...)
	args = append(args, "-i", operand)
	if endMs != nil {
		d := *endMs - max(startMs, 0)
		if d <= 0 {
			return nil, fmt.Errorf("media: empty audio range %d-%d ms", startMs, *endMs)
		}
		args = append(args, "-t", msSeconds(d))
	}
	args = append(args, "-vn", "-map", "0:a:0", "-ac", "2", "-c:a", enc[0])
	if p.Codec != "flac" && p.Kbps > 0 {
		args = append(args, "-b:a", strconv.Itoa(p.Kbps)+"k")
	}
	return append(args, "-f", enc[1], "pipe:1"), nil
}

// AudioTranscoder runs ffmpeg audio transcodes into a writer, at most Limit
// at a time (4 when unset).
type AudioTranscoder struct {
	FFmpeg string
	Limit  int

	mu      sync.Mutex
	running int
}

func (t *AudioTranscoder) acquire() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	limit := t.Limit
	if limit <= 0 {
		limit = 4
	}
	if t.running >= limit {
		return false
	}
	t.running++
	return true
}

func (t *AudioTranscoder) release() {
	t.mu.Lock()
	t.running--
	t.mu.Unlock()
}

// tailBuffer keeps the last bytes of ffmpeg's stderr for the error.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

// Stream transcodes input (see AudioArgs) into w until the end or until ctx
// is done. ErrAudioBusy when all slots are taken (nothing is written then).
func (t *AudioTranscoder) Stream(ctx context.Context, w io.Writer, input string, p AudioPlan, startMs int64, endMs *int64) error {
	args, err := AudioArgs(input, p, startMs, endMs)
	if err != nil {
		return err
	}
	if !t.acquire() {
		return ErrAudioBusy
	}
	defer t.release()
	ff := t.FFmpeg
	if ff == "" {
		ff = "ffmpeg"
	}
	var stderr tailBuffer
	cmd := exec.CommandContext(ctx, ff, args...)
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg audio: %w: %s", err, bytes.TrimSpace(stderr.b))
	}
	return nil
}
