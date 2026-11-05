package media

import (
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
)

// ClientCaps is what the browser (or the native app) reports it can play.
type ClientCaps struct {
	VideoCodecs, AudioCodecs, Containers []string
	MaxHeight                            int
	MaxBitrate                           int64
	// HevcNeedsHvc1: the client plays HEVC only with the hvc1 sample entry
	// (AVPlayer); other HEVC files are remuxed (HLS tags the copy hvc1).
	// The native app sets it; browsers never do.
	HevcNeedsHvc1 bool `json:"hevcNeedsHvc1"`
	// Lang is the interface language of Decision.Reason (i18n code; ""
	// = Russian). The handler sets it from the request.
	Lang string `json:"-"`
}

// Decision describes how to serve a file.
//
//	direct:    serve the file as is (HTTP range).
//	remux:     HLS, video stream copied (audio copied or re-encoded to AAC).
//	transcode: HLS, video re-encoded to H.264 (optionally downscaled).
type Decision struct {
	Mode         string
	VideoCopy    bool
	AudioCopy    bool
	TargetHeight int    // 0 = keep source height
	VideoCodec   string // source video codec
	SourceHeight int    // source video height, 0 = unknown
	SourcePixFmt string // source video pixel format ("" = unknown)
	AudioCodec   string // codec of the selected audio track
	AudioIndex   int    // resolved ffprobe stream index of the audio to use; -1 = no audio
	FMP4         bool   // HLS segments are fMP4 (otherwise MPEG-TS)
	MaxBitrate   int64  // cap for re-encoded video, 0 = none
	BurnSub      int    // image subtitle stream (ffprobe index) burnt into the picture; 0 = none
	// AudioInput is an external audio file (extaudio) played instead of the
	// file's own tracks: ffmpeg's second input, AudioIndex 0 of it ("" =
	// the audio is the file's stream AudioIndex). AudioInputCodec is its codec.
	AudioInput      string
	AudioInputCodec string
	// AudioInputOffsetMs shifts AudioInput on the video's timeline
	// (-itsoffset; positive: later): a cleaned voice-over made when the
	// file's timeline began elsewhere. 0 for the others.
	AudioInputOffsetMs float64
	// AudioInputTempo slows or speeds AudioInput (atempo; 0 or 1: none): a
	// voice-over of a PAL master fitted by vofit. The audio is re-encoded.
	AudioInputTempo float64
	Reason          string
}

// tempoOn: a tempo other than none.
func tempoOn(t float64) bool { return t > 0 && t != 1 }

// atempoArgs are the filter of a tempo (none for none).
func atempoArgs(t float64) []string {
	if !tempoOn(t) {
		return nil
	}
	return []string{"-af", "atempo=" + strconv.FormatFloat(t, 'f', -1, 64)}
}

// What each HLS segment container can carry when stream-copied.
var (
	copyVideo     = map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true}
	tsAudio       = map[string]bool{"aac": true, "mp3": true, "ac3": true, "eac3": true}
	fmp4OnlyAudio = map[string]bool{"flac": true, "opus": true}
)

func normCodec(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	switch {
	case strings.HasPrefix(c, "avc"), c == "h.264":
		return "h264"
	case strings.HasPrefix(c, "hvc"), strings.HasPrefix(c, "hev"), c == "h265", c == "h.265":
		return "hevc"
	case strings.HasPrefix(c, "mp4a"):
		return "aac"
	case c == "ac-3":
		return "ac3"
	case c == "ec-3":
		return "eac3"
	case strings.HasPrefix(c, "vp09"), c == "vp9.0":
		return "vp9"
	case strings.HasPrefix(c, "av01"):
		return "av1"
	}
	return c
}

// NormCodec is a codec name as Decide compares it ("avc1" → "h264",
// "hev1" → "hevc", "mp4a" → "aac"…).
func NormCodec(c string) string { return normCodec(c) }

func inList(list []string, v string) bool {
	v = normCodec(v)
	for _, x := range list {
		if normCodec(x) == v {
			return true
		}
	}
	return false
}

// Decide picks direct play, remux or transcode. audioIndex is the ffprobe
// stream index of the wanted audio track; <0 or unknown means the default one.
func Decide(p ProbeResult, c ClientCaps, audioIndex int) Decision {
	d := Decision{VideoCopy: true, AudioCopy: true, AudioIndex: -1, MaxBitrate: c.MaxBitrate}
	var reasons []string
	needsTag := false
	l := c.Lang

	if p.Video != nil {
		v := p.Video
		d.VideoCodec, d.SourceHeight, d.SourcePixFmt = v.Codec, v.Height, v.PixFmt
		if !inList(c.VideoCodecs, v.Codec) {
			d.VideoCopy = false
			reasons = append(reasons, i18n.TL(l, "видеокодек %s не поддерживается браузером", v.Codec))
		}
		// Browsers decode only 8-bit 4:2:0 H.264 (Hi10P, 4:2:2 and 4:4:4 give a black screen).
		if normCodec(v.Codec) == "h264" && (v.BitDepth > 8 || strings.Contains(v.Profile, "High 10") ||
			strings.Contains(v.Profile, "High 4:")) {
			d.VideoCopy = false
			reasons = append(reasons, i18n.TL(l, "H.264 с 10 битами или без 4:2:0 браузеры не воспроизводят"))
		}
		if c.MaxHeight > 0 && v.Height > c.MaxHeight {
			d.VideoCopy = false
			d.TargetHeight = c.MaxHeight
			reasons = append(reasons, i18n.TL(l, "высота %d больше допустимой %d", v.Height, c.MaxHeight))
		}
		if c.MaxBitrate > 0 && v.BitRate > c.MaxBitrate {
			d.VideoCopy = false
			reasons = append(reasons, i18n.TL(l, "битрейт выше допустимого"))
		}
		if c.HevcNeedsHvc1 && normCodec(v.Codec) == "hevc" && v.CodecTag != "hvc1" {
			// the picture is fine to copy, only the file's tag is not
			needsTag = true
		}
	}

	sel, defaultSel := pickAudio(p.Audio, audioIndex)
	if sel != nil {
		d.AudioIndex = sel.Index
		d.AudioCodec = sel.Codec
		if !inList(c.AudioCodecs, sel.Codec) {
			d.AudioCopy = false
			reasons = append(reasons, i18n.TL(l, "аудиокодек %s не поддерживается браузером", sel.Codec))
		}
	}

	containerOK := inList(c.Containers, p.Container)
	if d.VideoCopy && d.AudioCopy && containerOK && defaultSel && !needsTag {
		d.Mode = "direct"
		return d
	}
	if needsTag && d.VideoCopy {
		reasons = append(reasons, i18n.TL(l, "HEVC без метки hvc1: видео перепаковывается"))
	}

	// HLS path: stream copy is only possible for what the segment container carries.
	if d.VideoCopy && p.Video != nil && !copyVideo[normCodec(p.Video.Codec)] {
		d.VideoCopy = false
		reasons = append(reasons, i18n.TL(l, "видеокодек %s нельзя упаковать в HLS без перекодирования", p.Video.Codec))
	}
	acodec := normCodec(d.AudioCodec)
	d.FMP4 = (d.VideoCopy && p.Video != nil && normCodec(p.Video.Codec) != "h264") ||
		(d.AudioCopy && sel != nil && fmp4OnlyAudio[acodec])
	if d.AudioCopy && sel != nil && !(tsAudio[acodec] || (d.FMP4 && fmp4OnlyAudio[acodec])) {
		d.AudioCopy = false
		reasons = append(reasons, i18n.TL(l, "аудио %s нельзя упаковать в HLS без перекодирования", sel.Codec))
	}

	if d.VideoCopy {
		d.Mode = "remux"
		if !containerOK {
			reasons = append(reasons, i18n.TL(l, "контейнер %s не поддерживается браузером", p.Container))
		}
		if d.AudioCopy && !defaultSel {
			reasons = append(reasons, i18n.TL(l, "выбрана не основная звуковая дорожка"))
		}
	} else {
		d.Mode = "transcode"
	}
	d.Reason = strings.Join(reasons, "; ")
	return d
}

// pickAudio returns the wanted track and whether a browser would play it by
// itself (it is the file's default track: first flagged default, else first).
func pickAudio(a []AudioStream, index int) (sel *AudioStream, isBrowserDefault bool) {
	if len(a) == 0 {
		return nil, true
	}
	def := &a[0]
	for i := range a {
		if a[i].Default {
			def = &a[i]
			break
		}
	}
	for i := range a {
		if a[i].Index == index {
			return &a[i], a[i].Index == def.Index
		}
	}
	return def, true
}

// ExternalAudio is an audio file beside a video (extaudio) as another API
// of the server lists it: Stream is its first audio stream (Index 0 of its
// file, with the language and title of its name), Studio the studio its
// name or folder names.
type ExternalAudio struct {
	Path, Studio string
	Stream       AudioStream
	// Kind is "cleaned" for a voice-over with the original speech
	// subtracted (voclean; CleanedOf: the file's stream it was made from),
	// "" for a file found beside the video. OffsetMs: Decision.AudioInputOffsetMs.
	Kind      string
	CleanedOf int
	OffsetMs  float64
	// Tempo: Decision.AudioInputTempo (0 or 1: none).
	Tempo float64
}

// KindCleaned is ExternalAudio.Kind of a cleaned voice-over.
const KindCleaned = "cleaned"

// Title is the track's title in lang for players that show no badge: a
// cleaned voice-over's says so («LostFilm (очищено)», "LostFilm (cleaned)").
func (e ExternalAudio) Title(lang string) string {
	if e.Kind != KindCleaned {
		return e.Stream.Title
	}
	if e.Stream.Title == "" {
		return i18n.TL(lang, "Очищено")
	}
	return i18n.TL(lang, "%s (очищено)", e.Stream.Title)
}

// DecideExternalAudio is DecideExternal of e, shifted by its offset.
func DecideExternalAudio(p ProbeResult, c ClientCaps, e ExternalAudio, burnSub int) Decision {
	d := DecideExternal(p, c, e.Stream, e.Path, burnSub)
	d.AudioInputOffsetMs = e.OffsetMs
	if tempoOn(e.Tempo) {
		d.AudioInputTempo, d.AudioCopy = e.Tempo, false
	}
	return d
}

// DecideExternal is Decide for the external audio file path whose stream
// is a (extaudio.Describe): its codec is copied or re-encoded by the rules
// of an embedded track, and the session is always HLS (the file's own
// stream has no such track), so direct play becomes a remux. burnSub > 0
// burns that image subtitle in (DecideBurn).
func DecideExternal(p ProbeResult, c ClientCaps, a AudioStream, path string, burnSub int) Decision {
	a.Index, a.Default = 0, false
	// the file's own tracks stay listed (none of them is picked), so the
	// external one never counts as the browser's default
	q := p
	q.Audio = []AudioStream{a, {Index: -1, Default: true}}
	var d Decision
	if burnSub > 0 {
		d = DecideBurn(q, c, 0, burnSub)
	} else {
		d = Decide(q, c, 0)
	}
	d.AudioInput, d.AudioInputCodec = path, a.Codec
	return d
}

// DecideBurn is Decide for a session that burns the image subtitle stream
// sub into the picture: the video is always re-encoded (the copy and direct
// paths are off), the rest follows Decide.
func DecideBurn(p ProbeResult, c ClientCaps, audioIndex, sub int) Decision {
	c.VideoCodecs, c.Containers = nil, nil
	d := Decide(p, c, audioIndex)
	d.BurnSub = sub
	d.Reason = strings.TrimPrefix(d.Reason+"; "+i18n.TL(c.Lang, "субтитры вшиваются в картинку"), "; ")
	return d
}
