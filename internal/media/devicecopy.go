package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SubDemuxer is the ffmpeg demuxer of each external text subtitle codec.
var SubDemuxer = map[string]string{"subrip": "srt", "ass": "ass", "ssa": "ass", "webvtt": "webvtt"}

// TextSubCodecs are the subtitle codecs that are text (an MP4 carries them
// as mov_text); the others (PGS, VobSub) are pictures.
var TextSubCodecs = map[string]bool{"subrip": true, "srt": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true}

// deviceVideoCopy and deviceAudioCopy are what AVPlayer plays from an MP4
// as they are.
var (
	deviceVideoCopy = map[string]bool{"h264": true, "hevc": true}
	deviceAudioCopy = map[string]bool{"aac": true, "ac3": true, "eac3": true, "alac": true, "mp3": true}
)

// ExternalSub is a subtitle file next to the video, as a device copy input.
// Lang is the two-letter code of subtitles.External ("ru"); CP1251 marks a
// file that is not UTF-8 (old Russian .srt files).
type ExternalSub struct {
	Path, Lang, Codec string
	CP1251            bool
}

// DeviceCopyPlan says how a device copy (an MP4 that AVPlayer plays
// offline) is made from a file.
type DeviceCopyPlan struct {
	VideoCopy    bool
	VideoCodec   string // source video codec
	SourcePixFmt string
	SourceHeight int
	TargetHeight int   // transcode: output height (0 = the source's)
	MaxBitrate   int64 // transcode: video bitrate cap (0 = none)
	AudioIndex   int   // ffprobe stream index; -1 = no audio
	AudioCopy    bool
	// AudioInput is an external audio file (extaudio) taken instead of the
	// file's tracks: the copy's second input, AudioIndex 0 of it ("" =
	// the file's own). AudioLang (ISO 639-1) and AudioTitle tag its track.
	AudioInput, AudioLang, AudioTitle string
	// AudioInputOffsetMs: Decision.AudioInputOffsetMs.
	AudioInputOffsetMs float64
	// AudioInputTempo: Decision.AudioInputTempo.
	AudioInputTempo float64
	SubIndexes      []int    // embedded text subtitles (ffprobe stream indexes)
	SubLangs        []string // their probed language tags, parallel to SubIndexes
	ExternalSubs    []ExternalSub
	DurationMs      int64
	Reason          string
}

// PlanDeviceCopy decides a device copy of p: the video is copied when it
// is H.264 (8-bit 4:2:0) or HEVC within maxHeight and maxBitrate (0 = any),
// otherwise re-encoded to H.264 at min(source, maxHeight); the selected
// audio (audioIndex < 0: the default track) is copied when AVPlayer plays
// it, otherwise AAC stereo; every text subtitle, embedded or external, is
// kept (pictures are dropped).
func PlanDeviceCopy(p ProbeResult, ext []ExternalSub, maxHeight int, maxBitrate int64, audioIndex int) DeviceCopyPlan {
	pl := DeviceCopyPlan{VideoCopy: true, AudioIndex: -1, DurationMs: p.DurationMs}
	var reasons []string
	if v := p.Video; v != nil {
		pl.VideoCodec, pl.SourcePixFmt, pl.SourceHeight = v.Codec, v.PixFmt, v.Height
		c := normCodec(v.Codec)
		switch {
		case !deviceVideoCopy[c]:
			pl.VideoCopy = false
			reasons = append(reasons, "video codec "+v.Codec)
		case c == "h264" && (v.BitDepth > 8 || strings.Contains(v.Profile, "High 10") || strings.Contains(v.Profile, "High 4:")):
			pl.VideoCopy = false
			reasons = append(reasons, "h264 not 8-bit 4:2:0")
		}
		if maxHeight > 0 && v.Height > maxHeight {
			pl.VideoCopy = false
			reasons = append(reasons, fmt.Sprintf("height %d > %d", v.Height, maxHeight))
		}
		if maxBitrate > 0 && v.BitRate > maxBitrate {
			pl.VideoCopy = false
			reasons = append(reasons, "bitrate over the cap")
		}
		if !pl.VideoCopy {
			pl.MaxBitrate = maxBitrate
			pl.TargetHeight = v.Height
			if maxHeight > 0 && (v.Height <= 0 || v.Height > maxHeight) {
				pl.TargetHeight = maxHeight
			}
		}
	}
	if sel, _ := pickAudio(p.Audio, audioIndex); sel != nil {
		pl.AudioIndex = sel.Index
		pl.AudioCopy = deviceAudioCopy[normCodec(sel.Codec)]
		if !pl.AudioCopy {
			reasons = append(reasons, "audio codec "+sel.Codec)
		}
	}
	for _, s := range p.Subs {
		if TextSubCodecs[s.Codec] {
			pl.SubIndexes = append(pl.SubIndexes, s.Index)
			pl.SubLangs = append(pl.SubLangs, s.Lang)
		}
	}
	for _, e := range ext {
		if SubDemuxer[e.Codec] != "" {
			pl.ExternalSubs = append(pl.ExternalSubs, e)
		}
	}
	pl.Reason = strings.Join(reasons, "; ")
	return pl
}

// PlanDeviceCopyExternal is PlanDeviceCopy with the external audio file
// path (its stream a, extaudio.Describe) as the copy's audio.
func PlanDeviceCopyExternal(p ProbeResult, ext []ExternalSub, maxHeight int, maxBitrate int64, a AudioStream, path string) DeviceCopyPlan {
	a.Index, a.Default = 0, true
	p.Audio = []AudioStream{a}
	pl := PlanDeviceCopy(p, ext, maxHeight, maxBitrate, 0)
	pl.AudioInput, pl.AudioLang, pl.AudioTitle = path, a.Lang, a.Title
	return pl
}

// PlanDeviceCopyExternalAudio is PlanDeviceCopyExternal of e: shifted by
// its offset, titled in lang for a player without badges (ExternalAudio.Title).
func PlanDeviceCopyExternalAudio(p ProbeResult, ext []ExternalSub, maxHeight int, maxBitrate int64, e ExternalAudio, lang string) DeviceCopyPlan {
	a := e.Stream
	a.Title = e.Title(lang)
	pl := PlanDeviceCopyExternal(p, ext, maxHeight, maxBitrate, a, e.Path)
	pl.AudioInputOffsetMs = e.OffsetMs
	if tempoOn(e.Tempo) {
		pl.AudioInputTempo, pl.AudioCopy = e.Tempo, false
	}
	return pl
}

// subLang639_2 maps an external subtitle's two-letter code to the ISO
// 639-2 code an MP4 track carries.
func subLang639_2(lang string) string {
	switch lang {
	case "ru":
		return "rus"
	case "en":
		return "eng"
	case "uk":
		return "ukr"
	}
	return "und"
}

// DeviceCopyArgs assembles the ffmpeg command line (without the binary
// name) writing the device copy of input to out (a .part.mp4). External
// subtitles are read like the subtitle API reads them: file protocol only,
// the demuxer of their codec, CP1251 for files that are not UTF-8.
func DeviceCopyArgs(hw, input string, pl DeviceCopyPlan, out string) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	if !pl.VideoCopy {
		a = append(a, videoDecodeArgs(hw, pl.VideoCodec, pl.SourcePixFmt)...)
	}
	opts, operand, err := InputArgs(input)
	if err != nil {
		// Request refuses such inputs; never hand ffmpeg a LAN URL anyway.
		opts, operand = []string{"-protocol_whitelist", "file"}, "file:/dev/null"
	}
	a = append(a, opts...)
	a = append(a, "-i", operand)
	subInput := 1 // the first external subtitle's input
	if pl.AudioInput != "" {
		// the external audio comes from the same source as the video: file protocol only
		a = append(a, inputOffsetArgs(pl.AudioInputOffsetMs)...)
		a = append(a, AudioInputArgs(pl.AudioInput)...)
		subInput = 2
	}
	for _, e := range pl.ExternalSubs {
		if e.CP1251 {
			a = append(a, "-sub_charenc", "CP1251")
		}
		a = append(a, "-protocol_whitelist", "file")
		if f := SubDemuxer[e.Codec]; f != "" {
			a = append(a, "-f", f)
		}
		a = append(a, "-i", "file:"+strings.TrimPrefix(e.Path, "file:"))
	}

	a = append(a, "-map", "0:v:0")
	if pl.AudioInput != "" && pl.AudioIndex >= 0 {
		a = append(a, "-map", "1:a:0")
	} else if pl.AudioIndex >= 0 {
		a = append(a, "-map", "0:"+strconv.Itoa(pl.AudioIndex))
	}
	for _, i := range pl.SubIndexes {
		a = append(a, "-map", "0:"+strconv.Itoa(i))
	}
	for n := range pl.ExternalSubs {
		a = append(a, "-map", strconv.Itoa(n+subInput)+":s:0")
	}
	a = append(a, "-dn")

	if pl.VideoCopy {
		a = append(a, "-c:v", "copy")
		if normCodec(pl.VideoCodec) == "hevc" {
			a = append(a, "-tag:v", "hvc1") // AVPlayer plays only hvc1-tagged HEVC
		}
	} else {
		a = append(a, videoEncodeArgs(hw, pl.VideoCodec, pl.SourcePixFmt, pl.TargetHeight, pl.SourceHeight, pl.MaxBitrate)...)
	}
	if pl.AudioIndex >= 0 {
		if pl.AudioCopy {
			a = append(a, "-c:a", "copy")
		} else {
			if pl.AudioInput != "" {
				a = append(a, atempoArgs(pl.AudioInputTempo)...)
			}
			a = append(a, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
		}
		if pl.AudioInput != "" {
			// a loose .ac3 carries no tags: the player names the track by them
			a = append(a, "-metadata:s:a:0", "language="+subLang639_2(pl.AudioLang))
			if pl.AudioTitle != "" {
				a = append(a, "-metadata:s:a:0", "title="+pl.AudioTitle)
			}
		}
	}
	if len(pl.SubIndexes)+len(pl.ExternalSubs) > 0 {
		a = append(a, "-c:s", "mov_text")
		k := 0
		for i := range pl.SubIndexes {
			lang := "und"
			if i < len(pl.SubLangs) && pl.SubLangs[i] != "" {
				lang = pl.SubLangs[i]
			}
			a = append(a, "-metadata:s:s:"+strconv.Itoa(k), "language="+lang)
			k++
		}
		for _, e := range pl.ExternalSubs {
			a = append(a, "-metadata:s:s:"+strconv.Itoa(k), "language="+subLang639_2(e.Lang))
			k++
		}
	}
	return append(a, "-movflags", "+faststart", "-progress", "pipe:1", "-nostats", "-f", "mp4", out)
}

// DeviceCopyKey names a device copy: the same file (size and mtime), the
// same quality and audio track and the same external subtitles give the
// same copy.
type DeviceCopyKey struct {
	FileID, Size int64
	ModTime      time.Time
	MaxHeight    int
	MaxBitrate   int64
	AudioIndex   int
	ExtSubsHash  string // ExternalSubsHash of the external subtitle files
	// ExtAudioHash is ExternalAudioHash of the external audio file chosen
	// ("" = the file's own track: the id is the one it always was).
	ExtAudioHash string
}

// ID is the copy's id: the first 16 hex characters of the key's sha256.
func (k DeviceCopyKey) ID() string {
	s := fmt.Sprintf("v1|%d|%d|%d|%d|%d|%d|%s", k.FileID, k.Size, k.ModTime.UnixNano(), k.MaxHeight, k.MaxBitrate, k.AudioIndex, k.ExtSubsHash)
	if k.ExtAudioHash != "" {
		s += "|a" + k.ExtAudioHash
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// ExternalAudioHash is the sha256 of an external audio file's path, size
// and mtime ("" for no file).
func ExternalAudioHash(path string) string {
	if path == "" {
		return ""
	}
	var size, mod int64
	if st, err := os.Stat(path); err == nil {
		size, mod = st.Size(), st.ModTime().UnixNano()
	}
	sum := sha256.Sum256([]byte(path + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(mod, 10)))
	return hex.EncodeToString(sum[:])
}

// ExternalSubsHash is the sha256 of the sorted paths and mtimes of the
// external subtitle files (a file that is gone counts with mtime 0).
func ExternalSubsHash(paths []string) string {
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		var mod int64
		if st, err := os.Stat(p); err == nil {
			mod = st.ModTime().UnixNano()
		}
		lines = append(lines, p+"\x00"+strconv.FormatInt(mod, 10))
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
