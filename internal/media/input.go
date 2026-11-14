package media

import (
	"errors"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrUnsafeInput: an http input that is not a loopback URL. Media files come
// from downloads, so ffmpeg must never be pointed at (or redirected to) the LAN.
var ErrUnsafeInput = errors.New("media: only local files and loopback http inputs are allowed")

// streamFormats are the demuxers allowed for an http input (the local
// stream on 127.0.0.1): video containers and plain audio files. Playlist-
// style demuxers (hls, dash, concat, sdp...) are left out: they would open
// further URLs taken from the hostile data.
const streamFormats = "matroska,mov,mpegts,avi,flv,asf,mpeg,ogg,rm,mpegvideo,h264,hevc,m4v," +
	"mp3,flac,wav,aac,ac3,eac3,dts,ape,wv,aiff"

// IsLoopbackURL reports whether in is a plain http URL to a loopback IP.
func IsLoopbackURL(in string) bool {
	u, err := url.Parse(in)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// InputArgs returns the ffmpeg/ffprobe input options and the input operand
// for in. A local file may only use the file protocol: demuxers that probe
// content (HLS/DASH playlists, ffconcat, SDP) would otherwise fetch URLs
// written inside a hostile file. The "file:" prefix also stops a name like
// "concat:..." from being read as a protocol. A loopback http input may use
// http over tcp and only stream demuxers; any other URL is ErrUnsafeInput.
func InputArgs(in string) (opts []string, operand string, err error) {
	if !strings.HasPrefix(in, "/") && strings.Contains(in, "://") {
		if !IsLoopbackURL(in) {
			return nil, "", ErrUnsafeInput
		}
		return []string{"-protocol_whitelist", "http,tcp", "-format_whitelist", streamFormats}, in, nil
	}
	if !strings.HasPrefix(in, "file:") {
		in = "file:" + in
	}
	return []string{"-protocol_whitelist", "file"}, in, nil
}

// AudioFormats are the demuxers an external audio file (a downloaded
// «RUS Sound» track) is read with: ffmpeg's names (ffmpeg -demuxers; "mov"
// and "mp4" match the mov,mp4,m4a… demuxer, "matroska" matroska,webm).
const AudioFormats = "matroska,webm,ac3,eac3,dts,aac,flac,mp3,wav,ogg,mov,mp4,m4a,truehd,mlp"

// httpReconnectArgs keep a loopback http input alive: a read waits while
// pieces are missing and the server keeps the request open as long as the
// source lives, so a swarm stall must not end the stream (ffmpeg would write
// ENDLIST). The I/O timeout is only a safety net; reconnects never give up.
// Shared by the video input and an http external audio input.
var httpReconnectArgs = []string{"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_on_network_error", "1",
	"-reconnect_delay_max", "30", "-reconnect_max_retries", "-1", "-reconnect_delay_total_max", "4294",
	"-rw_timeout", "600000000"}

// AudioInputArgs are the ffmpeg input options and operand of an external
// audio source: a local file (the file protocol) or a loopback http URL
// (http over tcp, with the reconnect flags), the audio demuxers only. Any
// other input yields nil: the caller refuses it.
func AudioInputArgs(in string) []string {
	if !strings.HasPrefix(in, "/") && strings.Contains(in, "://") {
		if !IsLoopbackURL(in) {
			return nil
		}
		a := []string{"-protocol_whitelist", "http,tcp", "-format_whitelist", AudioFormats}
		a = append(a, httpReconnectArgs...)
		return append(a, "-i", in)
	}
	if strings.HasPrefix(in, "concat:") || strings.HasPrefix(in, "file:") && strings.Contains(in[5:], "://") {
		return nil
	}
	return []string{"-protocol_whitelist", "file", "-format_whitelist", AudioFormats, "-i", "file:" + strings.TrimPrefix(in, "file:")}
}

// audioInputArgsOrNull is AudioInputArgs for the builders whose caller has
// already refused a bad input: never a command without its input, so an
// input that slipped through reads /dev/null and ffmpeg fails.
func audioInputArgsOrNull(in string) []string {
	if a := AudioInputArgs(in); a != nil {
		return a
	}
	return AudioInputArgs("/dev/null")
}

// inputOffsetArgs shift the next input by ms on the output timeline
// (-itsoffset); none under half a millisecond.
func inputOffsetArgs(ms float64) []string {
	if math.Abs(ms) < 0.5 {
		return nil
	}
	return []string{"-itsoffset", strconv.FormatFloat(math.Round(ms)/1000, 'f', -1, 64)}
}

// ByteSeek is how a constant-bitrate raw audio stream read over http is
// seeked approximately: from a byte offset worked out of its bitrate,
// instead of -ss. The demuxers of raw streams (ac3, dts, adts aac, mp3)
// have no index, so -ss reads, and over http fetches, every byte before
// the seek point. The zero value seeks with -ss.
type ByteSeek struct {
	// Format is the demuxer, given with -f: the stream is entered mid-file,
	// where content probing could guess wrong.
	Format      string
	BytesPerSec float64
	// FrameBytes: the size of the stream's frames when they all have it
	// (offsets are aligned down to it); 0: unknown.
	FrameBytes int64
	// DurationSec: a seek at or past it uses -ss (it would start past the end).
	DurationSec float64
}

// byteSeekFormats are the raw audio demuxers ByteSeek is for, with the
// samples of one frame when all frames have the same size (0: they do not).
// Only formats that are constant-bitrate in practice: aac (ADTS) and mp3
// may be VBR, where the average bitrate puts the offset far off, so they
// keep -ss.
var byteSeekFormats = map[string]int64{"ac3": 1536, "eac3": 1536, "dts": 0}

// RawAudioByteSeek is the ByteSeek of an audio file probed as p: zero
// unless it is a raw stream (byteSeekFormats) with a known bitrate and
// duration.
func RawAudioByteSeek(p ProbeResult) ByteSeek {
	spf, raw := byteSeekFormats[p.Container]
	if !raw || len(p.Audio) == 0 || p.Audio[0].BitRate <= 0 || p.DurationMs <= 0 {
		return ByteSeek{}
	}
	a := p.Audio[0]
	b := ByteSeek{Format: p.Container, BytesPerSec: float64(a.BitRate) / 8, DurationSec: float64(p.DurationMs) / 1000}
	if n := a.BitRate * spf; spf > 0 && a.SampleRate > 0 && n%(8*int64(a.SampleRate)) == 0 {
		b.FrameBytes = n / (8 * int64(a.SampleRate))
	}
	return b
}

// byteSeekArgs are the input options that start the http audio input in
// at sec by bytes (b), placed before AudioInputArgs(in) in place of -ss;
// false: seek with -ss (a local file, no ByteSeek, sec out of range).
// The input is opened at the offset and read as non-seekable, so a
// demuxer looking for tags at the end never seeks back to byte 0.
func byteSeekArgs(in string, b ByteSeek, sec float64) ([]string, bool) {
	if b.Format == "" || b.BytesPerSec <= 0 || sec <= 0 || sec >= b.DurationSec || !IsLoopbackURL(in) {
		return nil, false
	}
	off := int64(sec * b.BytesPerSec)
	if b.FrameBytes > 0 {
		off -= off % b.FrameBytes
	}
	return []string{"-f", b.Format, "-seekable", "0", "-offset", strconv.FormatInt(off, 10)}, true
}
