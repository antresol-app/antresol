// Package releaseprefs holds the household's release preferences (settings
// key release_prefs): wanted quality, voice-over order and favourite
// studios, book languages and the music format. Both editions store and
// edit them; the sources build's indexer ranks releases by them and the
// voice-over cleaning orders its work by the studios.
package releaseprefs

import "github.com/antresol-app/antresol/internal/lang"

// Prefs are the user's release preferences (settings key release_prefs).
type Prefs struct {
	MaxHeight      int     `json:"maxHeight"`      // 1080
	PreferDub      bool    `json:"preferDub"`      // true
	PreferOriginal bool    `json:"preferOriginal"` // false
	MaxSizeGB      float64 `json:"maxSizeGB"`      // 0 = no limit
	MinSeeders     int     `json:"minSeeders"`     // 1
	// PreferLossless: music in FLAC/ALAC over MP3 320 (true), or the
	// other way round.
	PreferLossless bool `json:"preferLossless"`
	// MusicFormat: «Формат загрузки» — lossless | mp3 | any ("" = from
	// PreferLossless); MP3Bitrate: «Битрейт» of MP3 — 320 | v0 | 256 | any
	// ("" = 320). A release of another format is never picked by itself.
	MusicFormat string `json:"musicFormat,omitempty"`
	MP3Bitrate  string `json:"mp3Bitrate,omitempty"`
	// Voices: «Озвучка» of films and series in order of preference —
	// "dub" (a Russian dub or voice-over) and "original"; the first gets
	// the full bonus, the second less. Empty: from PreferDub/PreferOriginal.
	Voices []string `json:"voices,omitempty"`
	// Studios: «Любимые озвучки» — voice-over studios in the household's
	// order, best first (canonical names; others as typed).
	Studios []string `json:"studios,omitempty"`
	// BookLangs, AudiobookLangs, ComicLangs: the languages wanted for
	// books, audiobooks and comics/manga, best first (lang codes). Nil:
	// the defaults (Russian; comics Russian, then English).
	BookLangs      []string `json:"bookLangs"`
	AudiobookLangs []string `json:"audiobookLangs"`
	ComicLangs     []string `json:"comicLangs"`
}

// Default: Full HD, Russian dubbing preferred, at least one seeder,
// lossless music.
func Default() Prefs {
	return Prefs{MaxHeight: 1080, PreferDub: true, MinSeeders: 1, PreferLossless: true}
}

// VoiceWeights are the dub and original bonus factors (in tenths) from
// Voices: the first preference 10, the second 6, absent 0.
func (p Prefs) VoiceWeights() (dub, original int) {
	if len(p.Voices) == 0 {
		if p.PreferDub {
			dub = 10
		}
		if p.PreferOriginal {
			original = 10
		}
		return dub, original
	}
	w := func(v string) int {
		switch lang.Rank(p.Voices, v) {
		case 0:
			return 10
		case 1:
			return 6
		}
		return 0
	}
	return w("dub"), w("original")
}

// MediaLangs are the languages wanted for a book, audiobook or comic
// release, best first.
func (p Prefs) MediaLangs(kind string) []string {
	switch kind {
	case "book":
		if p.BookLangs != nil {
			return p.BookLangs
		}
	case "audiobook":
		if p.AudiobookLangs != nil {
			return p.AudiobookLangs
		}
	case "comic":
		if p.ComicLangs != nil {
			return p.ComicLangs
		}
		return []string{"ru", "en"}
	}
	return []string{"ru"}
}

// «Настройки» → «Музыка» → «Формат загрузки».
const (
	MusicLossless = "lossless" // «Без потерь (FLAC)»
	MusicMP3      = "mp3"      // «MP3» (with MP3Bitrate)
	MusicAny      = "any"      // «Любой — лучший доступный»
)

// «Битрейт» of MP3.
const (
	Bitrate320 = "320" // CBR 320
	BitrateV0  = "v0"  // LAME V0 (VBR)
	Bitrate256 = "256" // 256 kbps or more, V0 included
	BitrateAny = "any"
)

// ValidMusicFormat: lossless | mp3 | any.
func ValidMusicFormat(f string) bool { return f == MusicLossless || f == MusicMP3 || f == MusicAny }

// ValidMP3Bitrate: 320 | v0 | 256 | any.
func ValidMP3Bitrate(b string) bool {
	return b == Bitrate320 || b == BitrateV0 || b == Bitrate256 || b == BitrateAny
}

// MusicFormatOrDefault is MusicFormat; unset, the legacy PreferLossless
// tells (on: lossless, off: MP3).
func (p Prefs) MusicFormatOrDefault() string {
	if ValidMusicFormat(p.MusicFormat) {
		return p.MusicFormat
	}
	if p.PreferLossless {
		return MusicLossless
	}
	return MusicMP3
}

// MP3BitrateOrDefault is MP3Bitrate, 320 when unset (the legacy «MP3 320»).
func (p Prefs) MP3BitrateOrDefault() string {
	if ValidMP3Bitrate(p.MP3Bitrate) {
		return p.MP3Bitrate
	}
	return Bitrate320
}

// MusicFormatLabel names the wanted format: «FLAC», «MP3 320», «MP3 V0»,
// «MP3 от 256», «MP3», «любой».
func (p Prefs) MusicFormatLabel() string {
	switch p.MusicFormatOrDefault() {
	case MusicLossless:
		return "FLAC"
	case MusicAny:
		return "любой"
	}
	switch p.MP3BitrateOrDefault() {
	case Bitrate320:
		return "MP3 320"
	case BitrateV0:
		return "MP3 V0"
	case Bitrate256:
		return "MP3 от 256"
	}
	return "MP3"
}
