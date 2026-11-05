package library

import (
	"database/sql"
	"math"
	"strconv"
	"strings"

	"github.com/dhowden/tag"
)

// ReplayGain is a track's loudness levelling as its tags give it: gains in
// dB relative to the ReplayGain reference (89 dB SPL, -18 LUFS), peaks as
// the largest sample (1 = full scale). nil: not tagged.
type ReplayGain struct {
	TrackGain, TrackPeak, AlbumGain, AlbumPeak *float64
}

// RGReferenceLUFS is the integrated loudness ReplayGain 2 levels to: a
// gain is RGReferenceLUFS minus the track's loudness.
const RGReferenceLUFS = -18.0

// r128Offset: Opus R128_* gains are relative to -23 LUFS (EBU R128), five
// dB below the ReplayGain reference.
const r128Offset = RGReferenceLUFS - -23.0

// replayGainOf reads the ReplayGain tags of a file's raw tag map (Vorbis
// comments of FLAC/Ogg/Opus, ID3v2 TXXX frames, MP4 freeform atoms):
// REPLAYGAIN_TRACK_GAIN/PEAK and REPLAYGAIN_ALBUM_GAIN/PEAK, else Opus
// R128_TRACK_GAIN/R128_ALBUM_GAIN (Q7.8 integers on -23 LUFS, no peaks).
// Implausible values (|gain| > 60 dB, a peak outside (0, 10]) are ignored.
func replayGainOf(raw map[string]any) ReplayGain {
	var rg ReplayGain
	var r128Track, r128Album *float64
	set := func(name, value string) {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "replaygain_track_gain":
			rg.TrackGain = firstSet(rg.TrackGain, parseGain(value))
		case "replaygain_track_peak":
			rg.TrackPeak = firstSet(rg.TrackPeak, parsePeak(value))
		case "replaygain_album_gain":
			rg.AlbumGain = firstSet(rg.AlbumGain, parseGain(value))
		case "replaygain_album_peak":
			rg.AlbumPeak = firstSet(rg.AlbumPeak, parsePeak(value))
		case "r128_track_gain":
			r128Track = firstSet(r128Track, parseR128(value))
		case "r128_album_gain":
			r128Album = firstSet(r128Album, parseR128(value))
		}
	}
	for k, v := range raw {
		switch x := v.(type) {
		case *tag.Comm: // ID3v2 TXXX: the name is the description
			if x != nil {
				set(x.Description, x.Text)
			}
		case string:
			set(k, x)
		case []byte:
			set(k, string(x))
		}
	}
	if rg.TrackGain == nil {
		rg.TrackGain = r128Track
	}
	if rg.AlbumGain == nil {
		rg.AlbumGain = r128Album
	}
	return rg
}

func firstSet(have, v *float64) *float64 {
	if have != nil {
		return have
	}
	return v
}

// tagNumber is the number at the start of a tag value: "-6.48 dB",
// "+2,10 dB" (a decimal comma), "0.988525" (MP4 data may start with NULs).
func tagNumber(s string) (float64, bool) {
	s = strings.Trim(s, "\x00 \t\r\n")
	end := 0
	for end < len(s) && strings.IndexByte("+-0123456789.,eE", s[end]) >= 0 {
		end++
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(s[:end], ",", "."), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

func parseGain(s string) *float64 {
	n, ok := tagNumber(s)
	if !ok || math.Abs(n) > 60 {
		return nil
	}
	return &n
}

func parsePeak(s string) *float64 {
	n, ok := tagNumber(s)
	if !ok || n <= 0 || n > 10 {
		return nil
	}
	return &n
}

func parseR128(s string) *float64 {
	q, err := strconv.Atoi(strings.Trim(s, "\x00 \t\r\n"))
	if err != nil || q < math.MinInt16 || q > math.MaxInt16 {
		return nil
	}
	g := float64(q)/256 + r128Offset
	return &g
}

// ReadReplayGain reads the ReplayGain tags of an audio file as the scanner
// does (image: a CUE image, whose tags are its disc's: the album gain only).
func ReadReplayGain(path string, image bool) (ReplayGain, error) {
	t, err := readTags(path)
	if err != nil {
		return ReplayGain{}, err
	}
	return trackReplayGain(&musicEntry{tags: t, image: image}), nil
}

// nullFloat is a *float64 as an SQL argument (nil: NULL).
func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// rgReference is RGReferenceLUFS in SQL.
const rgReference = "-18.0"

// Loudness is the levelling a client applies to a track: its gain (tags,
// else RGReferenceLUFS minus the measured loudness) and its album's
// (tags, else from every track of the album), with their peaks; nil:
// unknown (the album: not every track is known yet).
type Loudness struct {
	TrackGain, TrackPeak, AlbumGain, AlbumPeak *float64
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}
