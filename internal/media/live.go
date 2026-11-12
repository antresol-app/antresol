package media

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// LiveSegmentSeconds is the segment length of live channels (internal/channels).
const LiveSegmentSeconds = segmentSeconds

// LiveSubOffset (seconds) is added to the timestamps of a programme's
// subtitle outputs: WebVTT cannot write a negative time, and a cue begun
// before the programme's start point would otherwise shift them all.
const LiveSubOffset = 1000

// LiveInput is one programme of a live channel: a stretch of a file encoded
// into the channel's segment directory, continuing the channel's numbering
// and timeline. Every programme comes out the same (H.264 Width×Height,
// the picture fitted and boxed, square pixels, video only; each audio
// output AAC stereo 48 kHz), so a player crosses a programme boundary
// with a discontinuity and nothing else to re-initialise.
type LiveInput struct {
	Input string // a local file
	// URL, when set, is read instead of Input: a loopback http URL (Check),
	// a stream or an HLS playlist, with Headers sent on every request.
	URL         string
	Headers     map[string]string
	StartSec    float64 // where in the file to start
	DurationSec float64 // how much of it (0: to the end)
	// Audio are the programme's audio outputs: output k writes
	// a<k>-<N>.ts and its own playlist AudioPlaylist(k) in OutDir, on the
	// video's numbering and timeline.
	Audio []LiveAudio
	// Subs are text subtitle outputs (WebVTT files in OutDir, timestamps
	// from the programme's start plus LiveSubOffset).
	Subs []LiveSub
	// The source video (from its probe; "" / 0 when unknown).
	VideoCodec, PixFmt  string
	SrcWidth, SrcHeight int
	// Width×Height of the channel (default 1920×1080).
	Width, Height int
	// Bitrate of the video (default 6 Mbit/s).
	Bitrate int64
	// TsOffset moves the programme's timestamps to where the channel's
	// timeline is (seconds already encoded).
	TsOffset float64
	// StartNumber is the number of the programme's first segment.
	StartNumber int64
	Throttle    Throttle
	// OutDir holds the segments (seg<N>.ts); Playlist is the run's own
	// playlist (in OutDir) the channel reads the new video segments from.
	OutDir, Playlist string
}

// LiveAudio is an audio output of a live programme: the file's stream
// Index (Input ""), an external audio file (Input, its first stream,
// shifted by OffsetMs and played at Tempo), or silence (Input "", Index -1).
// Copy: the source already is AAC-LC, ≤ 2 channels, 48 kHz and goes out
// as it is (no encode, no padding; never with a Tempo).
type LiveAudio struct {
	Input           string
	Index           int
	OffsetMs, Tempo float64
	Copy            bool
}

// LiveSub is a text subtitle output: the file's stream Index (Input "")
// or a subtitle file (Input, read with the demuxer of Codec), written as
// WebVTT to Out (a name in OutDir).
type LiveSub struct {
	Input, Codec string
	Index        int
	Out          string
}

// LiveAudioPlaylist is the run playlist of audio output k.
func LiveAudioPlaylist(k int) string { return "run-a" + strconv.Itoa(k) + ".m3u8" }

// LiveAudioSegment is the file of audio output k's segment n.
func LiveAudioSegment(k int, n int64) string {
	return "a" + strconv.Itoa(k) + "-" + strconv.FormatInt(n, 10) + ".ts"
}

// LiveFirstAudio is LiveInput.AudioIndex of an input whose audio is not
// known in advance: its first audio stream.
const LiveFirstAudio = -2

// liveURLFormats are the demuxers of a URL input: the stream formats and
// HLS (the URL is this server's own relay on loopback, which rewrites
// every URI of a playlist to itself).
const liveURLFormats = streamFormats + ",hls"

// ErrLiveInput: a LiveInput URL that is not a loopback http URL, or a
// header with a line break.
var ErrLiveInput = errors.New("media: a live URL input must be a loopback http URL with clean headers")

// Check refuses a URL input LiveArgs must not be given: one that is not a
// plain loopback http URL (ffmpeg never reaches the LAN or the internet
// itself), or headers with control characters.
func (in LiveInput) Check() error {
	if in.URL == "" {
		return nil
	}
	if !IsLoopbackURL(in.URL) {
		return ErrLiveInput
	}
	for k, v := range in.Headers {
		if k == "" || strings.ContainsAny(k, ":\r\n\x00") || strings.ContainsAny(v, "\r\n\x00") {
			return ErrLiveInput
		}
	}
	return nil
}

// liveInputArgs are the input options and -i of in: a local file through
// the file protocol, else (Check passed) the URL over http with the
// stream demuxers and HLS, its headers sent with every request.
func liveInputArgs(in LiveInput) []string {
	if in.URL == "" {
		return []string{"-i", "file:" + in.Input}
	}
	a := []string{"-protocol_whitelist", "http,tcp,crypto", "-format_whitelist", liveURLFormats}
	if len(in.Headers) > 0 {
		keys := make([]string, 0, len(in.Headers))
		for k := range in.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var h strings.Builder
		for _, k := range keys {
			h.WriteString(k + ": " + in.Headers[k] + "\r\n")
		}
		a = append(a, "-headers", h.String())
	}
	return append(a, "-rw_timeout", "20000000", "-i", in.URL)
}

// LiveArgs is the ffmpeg command line (without the binary) of one live
// programme with the hardware mode hw ("qsv", "videotoolbox", "none"):
// the video encoded once, each audio output its own AAC encode (or a
// copy), each subtitle output a WebVTT file.
func LiveArgs(hw string, in LiveInput) []string {
	w, h := in.Width, in.Height
	if w <= 0 || h <= 0 {
		w, h = 1920, 1080
	}
	br := in.Bitrate
	if br <= 0 {
		br = 6_000_000
	}
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	throttle := func() []string {
		if in.Throttle.Rate <= 0 {
			return nil
		}
		return []string{"-readrate", ff(in.Throttle.Rate), "-readrate_initial_burst", ff(max(in.Throttle.Burst, 0))}
	}
	// The iGPU decodes and scales when it can and the picture's size is
	// known (vpp_qsv needs the fitted size in numbers); else the CPU does.
	gpu := hw == "qsv" && qsvDecodable(in.VideoCodec, in.PixFmt) && in.SrcWidth > 0 && in.SrcHeight > 0
	switch {
	case gpu:
		a = append(a, "-hwaccel", "qsv", "-hwaccel_output_format", "qsv")
	case hw == "videotoolbox":
		a = append(a, "-hwaccel", "videotoolbox")
	}
	if in.StartSec > 0 {
		a = append(a, "-ss", ff(in.StartSec))
	}
	a = append(a, throttle()...)
	if in.DurationSec > 0 {
		// on the input: every output of the file ends there
		a = append(a, "-t", ff(in.DurationSec))
	}
	a = append(a, liveInputArgs(in)...)
	// the other inputs: external audio files (each once), silence, subtitle files
	inputs := 1
	audioIn := make([]string, len(in.Audio))
	extIn := map[string]int{}
	silence := -1
	for k, x := range in.Audio {
		switch {
		case x.Input != "":
			key := x.Input + "|" + ff(x.OffsetMs) + "|" + ff(x.Tempo)
			n, ok := extIn[key]
			if !ok {
				n, extIn[key] = inputs, inputs
				inputs++
				a = append(a, inputOffsetArgs(x.OffsetMs)...)
				if in.StartSec > 0 {
					// at a tempo the track runs on its own time: video time s
					// is its time s·tempo (vofit's fit)
					ss := in.StartSec
					if tempoOn(x.Tempo) {
						ss = math.Round(in.StartSec*x.Tempo*1000) / 1000
					}
					a = append(a, "-ss", ff(ss))
				}
				a = append(a, throttle()...)
				a = append(a, AudioInputArgs(x.Input)...)
			}
			audioIn[k] = strconv.Itoa(n) + ":a:0"
		case x.Index == LiveFirstAudio:
			audioIn[k] = "0:a:0"
		case x.Index >= 0:
			audioIn[k] = "0:" + strconv.Itoa(x.Index)
		default:
			if silence < 0 {
				silence = inputs
				inputs++
				a = append(a, throttle()...)
				a = append(a, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
			}
			audioIn[k] = strconv.Itoa(silence) + ":a:0"
		}
	}
	subIn := make([]string, len(in.Subs))
	for j, s := range in.Subs {
		if s.Input == "" {
			subIn[j] = "0:" + strconv.Itoa(s.Index)
			continue
		}
		if in.StartSec > 0 {
			a = append(a, "-ss", ff(in.StartSec))
		}
		a = append(a, "-protocol_whitelist", "file")
		if f := SubDemuxer[s.Codec]; f != "" {
			a = append(a, "-f", f)
		}
		a = append(a, "-i", "file:"+s.Input)
		subIn[j] = strconv.Itoa(inputs) + ":0"
		inputs++
	}

	// the video
	a = append(a, "-map", "0:v:0", "-an", "-sn", "-dn")
	pad := fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,setsar=1", w, h)
	switch {
	case gpu:
		fw, fh := fit(in.SrcWidth, in.SrcHeight, w, h)
		a = append(a, "-vf", fmt.Sprintf("vpp_qsv=w=%d:h=%d:format=nv12,hwdownload,format=nv12,%s", fw, fh, pad))
	case hw == "qsv":
		a = append(a, "-vf", swFit(w, h, pad, "nv12"))
	default:
		a = append(a, "-vf", swFit(w, h, pad, "yuv420p"))
	}
	maxrate := br * 4 / 3
	switch hw {
	case "qsv":
		a = append(a, "-fps_mode", "passthrough", "-c:v", "h264_qsv", "-low_power", "1", "-preset", "veryfast",
			"-b:v", strconv.FormatInt(br, 10), "-maxrate", strconv.FormatInt(maxrate, 10),
			"-bufsize", strconv.FormatInt(2*maxrate, 10), "-forced_idr", "1")
	case "videotoolbox":
		a = append(a, "-c:v", "h264_videotoolbox", "-b:v", strconv.FormatInt(br, 10), "-profile:v", "high")
	default:
		a = append(a, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-sc_threshold", "0",
			"-maxrate", strconv.FormatInt(maxrate, 10), "-bufsize", strconv.FormatInt(2*maxrate, 10))
	}
	a = append(a, "-force_key_frames", "expr:gte(t,n_forced*"+strconv.Itoa(segmentSeconds)+")")
	a = append(a, liveHLS(in, "seg%d.ts", in.Playlist)...)

	// the audio outputs: padded with silence a second past the video, so
	// every video segment has its audio (the extra is never listed); an
	// AAC-LC stereo 48 kHz source is copied (its packets end with the
	// input's -t)
	for k, x := range in.Audio {
		a = append(a, "-map", audioIn[k], "-vn", "-sn", "-dn")
		if x.Copy && !tempoOn(x.Tempo) && (x.Input != "" || x.Index >= 0) {
			a = append(a, "-c:a", "copy")
			a = append(a, liveHLS(in, "a"+strconv.Itoa(k)+"-%d.ts", LiveAudioPlaylist(k))...)
			continue
		}
		af := "apad"
		if x.Input != "" && tempoOn(x.Tempo) {
			af = "atempo=" + strconv.FormatFloat(x.Tempo, 'f', -1, 64) + ",apad"
		}
		if in.DurationSec > 0 {
			a = append(a, "-af", af, "-t", ff(in.DurationSec+1))
		} else if af != "apad" {
			a = append(a, "-af", strings.TrimSuffix(af, ",apad"))
		}
		a = append(a, "-c:a", "aac", "-b:a", "192k", "-ac", "2", "-ar", "48000")
		a = append(a, liveHLS(in, "a"+strconv.Itoa(k)+"-%d.ts", LiveAudioPlaylist(k))...)
	}
	for j, s := range in.Subs {
		a = append(a, "-map", subIn[j])
		if in.DurationSec > 0 {
			// its own end: with -readrate, an input cut by -t before its
			// end leaves a subtitle output waiting and ffmpeg never exits
			a = append(a, "-t", ff(in.DurationSec))
		}
		a = append(a, "-c:s", "webvtt", "-output_ts_offset", strconv.Itoa(LiveSubOffset),
			"-flush_packets", "1", "-f", "webvtt", filepath.Join(in.OutDir, s.Out))
	}
	return a
}

// liveHLS are the options of one HLS output of a live programme.
func liveHLS(in LiveInput, segments, playlist string) []string {
	var a []string
	if in.TsOffset > 0 {
		a = append(a, "-output_ts_offset", ff(in.TsOffset))
	}
	return append(a, "-f", "hls", "-hls_time", strconv.Itoa(segmentSeconds), "-hls_list_size", "0",
		"-hls_flags", "independent_segments", "-hls_segment_type", "mpegts",
		// each programme is a new ffmpeg whose continuity counters start
		// over: its first packets say so, demuxers then see no corruption
		"-hls_segment_options", "mpegts_flags=+initial_discontinuity",
		"-start_number", strconv.FormatInt(in.StartNumber, 10),
		"-hls_segment_filename", filepath.Join(in.OutDir, segments),
		filepath.Join(in.OutDir, playlist))
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

// swFit scales the picture into w×h keeping its shape and boxes it.
func swFit(w, h int, pad, pixFmt string) string {
	return fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2,%s,format=%s", w, h, pad, pixFmt)
}

// fit is the largest even size of sw×sh's shape inside w×h.
func fit(sw, sh, w, h int) (int, int) {
	fw, fh := w, sh*w/sw
	if fh > h {
		fw, fh = sw*h/sh, h
	}
	return max(fw&^1, 2), max(fh&^1, 2)
}
