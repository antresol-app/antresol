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

// AudioInputArgs are the ffmpeg input options and operand of an external
// audio file: the file protocol and the audio demuxers only.
func AudioInputArgs(path string) []string {
	return []string{"-protocol_whitelist", "file", "-format_whitelist", AudioFormats, "-i", "file:" + strings.TrimPrefix(path, "file:")}
}

// inputOffsetArgs shift the next input by ms on the output timeline
// (-itsoffset); none under half a millisecond.
func inputOffsetArgs(ms float64) []string {
	if math.Abs(ms) < 0.5 {
		return nil
	}
	return []string{"-itsoffset", strconv.FormatFloat(math.Round(ms)/1000, 'f', -1, 64)}
}
