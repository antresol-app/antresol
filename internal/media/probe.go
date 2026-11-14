// Package media wraps ffprobe/ffmpeg: stream inspection, the direct/remux/
// transcode playback decision and HLS transcoding sessions. It is standalone
// and consumes only values passed in by the caller.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type VideoStream struct {
	Codec, Profile string
	Width, Height  int
	HDR            bool
	BitRate        int64
	PixFmt         string
	BitDepth       int // 8 when unknown
	// CodecTag is ffprobe's codec_tag_string ("hvc1", "hev1", "avc1"…);
	// "" in probe-cache rows made before it was recorded.
	CodecTag string
	// ColorTransfer is ffprobe's color_transfer ("smpte2084" = PQ,
	// "arib-std-b67" = HLG, …); "" when unknown or in older cache rows.
	ColorTransfer string `json:",omitempty"`
	// HDRMastering and HDRLightLevel: an HDR10 (PQ) source carries
	// mastering-display and content-light-level metadata (in the container
	// or the first frame); nil = not looked at (SDR, HLG, a probe-cache row
	// made before they were recorded, a failed look).
	HDRMastering  *bool `json:",omitempty"`
	HDRLightLevel *bool `json:",omitempty"`
}

// NoHDRMastering: a PQ source known to lack mastering-display metadata
// (VA-API's tone mapping refuses it).
func (v *VideoStream) NoHDRMastering() bool {
	return v.HDRTransfer() == "smpte2084" && v.HDRMastering != nil && !*v.HDRMastering
}

// NeedsHDRMeta: a PQ source whose HDR10 metadata was not looked at yet
// (an older probe-cache row): ProbeHDRMeta fills it in.
func (v *VideoStream) NeedsHDRMeta() bool {
	return v.HDRTransfer() == "smpte2084" && v.HDRMastering == nil
}

// HDRMeta is the HDR10 metadata ProbeHDRMeta found.
type HDRMeta struct{ Mastering, LightLevel bool }

// SetHDRMeta records m on v.
func (v *VideoStream) SetHDRMeta(m HDRMeta) {
	v.HDRMastering, v.HDRLightLevel = &m.Mastering, &m.LightLevel
}

// HDRTransfer is the transfer of an HDR picture (PQ or HLG; PQ for a
// cache row that knows only HDR), "" for SDR.
func (v *VideoStream) HDRTransfer() string {
	switch {
	case v == nil || !v.HDR:
		return ""
	case v.ColorTransfer == "arib-std-b67":
		return "arib-std-b67"
	}
	return "smpte2084"
}

type AudioStream struct {
	Index              int
	Codec, Lang, Title string
	// Profile is ffprobe's ("LC", "HE-AAC"…); "" in probe-cache rows made
	// before it was recorded.
	Profile    string `json:",omitempty"`
	Channels   int
	Default    bool
	SampleRate int   // Hz, 0 when unknown
	BitDepth   int   // bits per sample of lossless audio, 0 when unknown
	BitRate    int64 // bit/s; the container's for an audio-only file without one
}

type SubStream struct {
	Index              int
	Codec, Lang, Title string
	Forced, Default    bool
}

type ProbeResult struct {
	Container  string
	DurationMs int64
	Video      *VideoStream
	Audio      []AudioStream
	Subs       []SubStream
	// StartMs is where the file's timeline begins (format start_time, ms:
	// ffmpeg presents every stream of the file from it); nil: unknown (a
	// probe cached before it was kept).
	StartMs *float64 `json:",omitempty"`
}

type ffprobeOut struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
		StartTime  string `json:"start_time"`
	} `json:"format"`
	Streams []struct {
		Index         int               `json:"index"`
		CodecType     string            `json:"codec_type"`
		CodecName     string            `json:"codec_name"`
		CodecTag      string            `json:"codec_tag_string"`
		Profile       string            `json:"profile"`
		Width         int               `json:"width"`
		Height        int               `json:"height"`
		ColorTransfer string            `json:"color_transfer"`
		BitRate       string            `json:"bit_rate"`
		PixFmt        string            `json:"pix_fmt"`
		RawBits       string            `json:"bits_per_raw_sample"`
		Channels      int               `json:"channels"`
		SampleRate    string            `json:"sample_rate"`
		BitsPerSample int               `json:"bits_per_sample"`
		Disposition   map[string]int    `json:"disposition"`
		Tags          map[string]string `json:"tags"`
		SideData      []ffprobeSideData `json:"side_data_list"`
	} `json:"streams"`
}

type ffprobeSideData struct {
	Type string `json:"side_data_type"`
}

// The side data names of HDR10 metadata (stream and frame side_data_list).
const (
	sideMastering  = "Mastering display metadata"
	sideLightLevel = "Content light level metadata"
)

func hdrMetaOf(sd []ffprobeSideData) HDRMeta {
	var m HDRMeta
	for _, d := range sd {
		switch d.Type {
		case sideMastering:
			m.Mastering = true
		case sideLightLevel:
			m.LightLevel = true
		}
	}
	return m
}

// Probe runs ffprobe on path: a local file, or a loopback http URL (see
// InputArgs; other URLs are refused).
func Probe(ctx context.Context, ffprobe, path string) (ProbeResult, error) {
	opts, operand, err := InputArgs(path)
	if err != nil {
		return ProbeResult{}, err
	}
	args := append([]string{"-v", "error"}, opts...)
	args = append(args, "-print_format", "json", "-show_format", "-show_streams", operand)
	out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ProbeResult{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return ProbeResult{}, fmt.Errorf("ffprobe: %w", err)
	}
	r, err := parseProbe(out, path)
	if err == nil && r.Video != nil && r.Video.NeedsHDRMeta() {
		// HDR10 metadata is mostly in the bitstream (SEI), seen only by
		// decoding: the first frame tells
		if m, err := ProbeHDRMeta(ctx, ffprobe, path); err == nil {
			r.Video.SetHDRMeta(m)
		}
	}
	return r, err
}

// ProbeHDRMeta looks for HDR10 metadata of path's video: the stream's side
// data (the container's) and that of its first decoded frame (SEI).
func ProbeHDRMeta(ctx context.Context, ffprobe, path string) (HDRMeta, error) {
	opts, operand, err := InputArgs(path)
	if err != nil {
		return HDRMeta{}, err
	}
	args := append([]string{"-v", "error"}, opts...)
	args = append(args, "-select_streams", "V:0", "-read_intervals", "%+#1",
		"-print_format", "json", "-show_streams", "-show_frames", operand)
	out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
	if err != nil {
		return HDRMeta{}, fmt.Errorf("ffprobe hdr metadata: %w", err)
	}
	return parseHDRMeta(out)
}

func parseHDRMeta(js []byte) (HDRMeta, error) {
	var o struct {
		Streams []struct {
			SideData []ffprobeSideData `json:"side_data_list"`
		} `json:"streams"`
		Frames []struct {
			SideData []ffprobeSideData `json:"side_data_list"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(js, &o); err != nil {
		return HDRMeta{}, fmt.Errorf("ffprobe json: %w", err)
	}
	if len(o.Frames) == 0 {
		return HDRMeta{}, fmt.Errorf("ffprobe hdr metadata: no video frame")
	}
	var m HDRMeta
	for _, s := range o.Streams {
		sm := hdrMetaOf(s.SideData)
		m.Mastering, m.LightLevel = m.Mastering || sm.Mastering, m.LightLevel || sm.LightLevel
	}
	fm := hdrMetaOf(o.Frames[0].SideData)
	m.Mastering, m.LightLevel = m.Mastering || fm.Mastering, m.LightLevel || fm.LightLevel
	return m, nil
}

// ProbeAudioFile is Probe of an external audio file, read with the audio
// demuxers only (AudioFormats): a downloaded file named .ac3 that is
// something else is an error, not a guess.
func ProbeAudioFile(ctx context.Context, ffprobe, path string) (ProbeResult, error) {
	args := []string{"-v", "error", "-protocol_whitelist", "file", "-format_whitelist", AudioFormats,
		"-print_format", "json", "-show_format", "-show_streams", "file:" + strings.TrimPrefix(path, "file:")}
	out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ProbeResult{}, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return ProbeResult{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out, path)
}

func parseProbe(js []byte, path string) (ProbeResult, error) {
	var o ffprobeOut
	if err := json.Unmarshal(js, &o); err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe json: %w", err)
	}
	r := ProbeResult{Container: containerName(o.Format.FormatName, path)}
	if f, err := strconv.ParseFloat(o.Format.Duration, 64); err == nil {
		r.DurationMs = int64(f*1000 + 0.5)
	}
	if f, err := strconv.ParseFloat(o.Format.StartTime, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		ms := math.Round(f*1e6) / 1e3
		r.StartMs = &ms
	}
	for _, s := range o.Streams {
		lang, title := s.Tags["language"], s.Tags["title"]
		if lang == "" {
			lang = s.Tags["LANGUAGE"]
		}
		if title == "" {
			title = s.Tags["TITLE"]
		}
		switch s.CodecType {
		case "video":
			if r.Video != nil || s.Disposition["attached_pic"] == 1 {
				continue
			}
			br, _ := strconv.ParseInt(s.BitRate, 10, 64)
			if br == 0 { // mkv rarely stores per-stream bitrate; fall back to the container's
				br, _ = strconv.ParseInt(o.Format.BitRate, 10, 64)
			}
			r.Video = &VideoStream{
				Codec: s.CodecName, Profile: s.Profile, Width: s.Width, Height: s.Height,
				HDR:     s.ColorTransfer == "smpte2084" || s.ColorTransfer == "arib-std-b67",
				BitRate: br, PixFmt: s.PixFmt, BitDepth: bitDepth(s.RawBits, s.PixFmt), CodecTag: s.CodecTag,
				ColorTransfer: s.ColorTransfer,
			}
			// the container's HDR10 metadata settles it; else the first
			// frame's (Probe)
			if m := hdrMetaOf(s.SideData); m.Mastering && r.Video.HDRTransfer() == "smpte2084" {
				r.Video.SetHDRMeta(m)
			}
		case "audio":
			a := AudioStream{Index: s.Index, Codec: s.CodecName, Lang: lang,
				Title: title, Profile: s.Profile, Channels: s.Channels, Default: s.Disposition["default"] == 1}
			a.SampleRate, _ = strconv.Atoi(s.SampleRate)
			a.BitRate, _ = strconv.ParseInt(s.BitRate, 10, 64)
			if n, err := strconv.Atoi(s.RawBits); err == nil && n > 0 {
				a.BitDepth = n
			} else if s.BitsPerSample > 0 {
				a.BitDepth = s.BitsPerSample
			}
			r.Audio = append(r.Audio, a)
		case "subtitle":
			r.Subs = append(r.Subs, SubStream{Index: s.Index, Codec: s.CodecName, Lang: lang,
				Title: title, Forced: s.Disposition["forced"] == 1, Default: s.Disposition["default"] == 1})
		}
	}
	// FLAC and Vorbis rarely store a stream bitrate: an audio-only file's
	// container bitrate is the stream's.
	if r.Video == nil && len(r.Audio) == 1 && r.Audio[0].BitRate == 0 {
		r.Audio[0].BitRate, _ = strconv.ParseInt(o.Format.BitRate, 10, 64)
	}
	return r, nil
}

// containerName normalizes ffprobe's format_name ("matroska,webm",
// "mov,mp4,m4a,...") to the short names used in ClientCaps.Containers.
func containerName(format, path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	has := func(n string) bool {
		for _, p := range strings.Split(format, ",") {
			if p == n {
				return true
			}
		}
		return false
	}
	switch {
	case has("matroska"):
		if ext == "webm" {
			return "webm"
		}
		return "mkv"
	case has("mp4"):
		if ext == "mov" {
			return "mov"
		}
		return "mp4"
	case has("mpegts"):
		return "ts"
	}
	if i := strings.IndexByte(format, ','); i >= 0 {
		return format[:i]
	}
	return format
}

var highDepthPixFmt = regexp.MustCompile(`(?:p(9|1[0246])(?:le|be)?$)|(?:^p01[026])`)

// bitDepth prefers bits_per_raw_sample and falls back to the pixel format.
func bitDepth(raw, pixFmt string) int {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	if m := highDepthPixFmt.FindStringSubmatch(pixFmt); m != nil {
		if m[1] != "" {
			n, _ := strconv.Atoi(m[1])
			return n
		}
		return 10
	}
	return 8
}
