package channels

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/release"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// The caps of a run's track set (BuildSet).
const (
	// AudioCap: at most 8 audio renditions. Each costs one AAC encode
	// (~2 % of a core, round 3's measure) unless its source is copied,
	// plus ~200 kbit/s of segments on disk; files on the owner's server
	// carry 2–6 tracks plus 0–2 voice-overs beside them, so 8 covers
	// practically every programme, and a TV's picker stays readable.
	AudioCap = 8
	// SubCap: at most 6 subtitle renditions. Subtitles cost nothing to
	// encode (the server writes WebVTT), so the cap is the picker's: a
	// TV list of six fits a screen, and files rarely carry more than
	// two or three text languages.
	SubCap = 6
)

// AudioSource is what an audio output reads: the file's stream Index
// (External ""), an external audio file (its first stream, shifted by
// OffsetMs, at Tempo), or silence (External "", Index -1). Copy: the
// source is AAC-LC, ≤ 2 channels, 48 kHz, played as it is (no encode).
type AudioSource struct {
	External        string
	Index           int
	OffsetMs, Tempo float64
	Copy            bool
}

var silence = AudioSource{Index: -1}

// AudioTrack is one audio track of a programme under its key.
type AudioTrack struct {
	// Key identifies the rendition the track plays in: "<lang>:<voice>"
	// for a Russian voice or another language's track, "<lang>:<voice>+clean"
	// for a cleaned voice-over, "orig:<lang>" for the programme's
	// original-language track ("#2" marks a second untitled track of
	// one language in a programme).
	Key string
	// Lang: ISO 639-1 ("" unknown); Voice: whose voice, as shown (the
	// studio, else the title without codec words; "" none).
	Lang, Voice string
	Original    bool
	Source      AudioSource
	// Studio: the voice-over studio ("" none known); Title the track's own.
	Studio, Title     string
	External, Cleaned bool
	// fav: its place in «Любимые озвучки» (len(Studios): none).
	fav int
}

// SubPick is a subtitle track: the file's stream Index (External "") or
// a subtitle file (External, Codec its codec).
type SubPick struct {
	External, Codec string
	Index           int
	Lang, Title     string
}

// SubTrack is one text subtitle track of a programme under its key
// ("<lang>", "<lang>#2" for the language's second track).
type SubTrack struct {
	Key  string
	Pick SubPick
}

// Tracks are a programme's audio and text subtitle tracks, each under its
// key, in the programme's own order (Russian voices first: favourite
// studios, the file's own before a file beside it, the default; then the
// original; then the others). Default is what plays when nothing else
// fits: the file's default track, else its first, else an external one,
// else silence.
type Tracks struct {
	Audio   []AudioTrack
	Subs    []SubTrack
	Default AudioSource
}

// PlanOptions are the household's settings the tracks follow (one
// encoder serves every profile of a channel).
type PlanOptions struct {
	// Studios: «Любимые озвучки», best first.
	Studios []string
	// PreferCleaned: a voice-over's cleaned version («Очищено») ranks
	// before the studio's own track.
	PreferCleaned bool
	// Lang: the server's metadata language, the language of the
	// renditions' names ("" Russian).
	Lang string
}

// externalBase is the index the API gives the external audio tracks of a
// file (track i of the list is externalBase+i); media.ExternalAudio's
// CleanedOf counts that way.
const externalBase = 2000

// cleanSuffix marks the key of a cleaned voice-over.
const cleanSuffix = "+clean"

// candidate is one audio track of a programme.
type candidate struct {
	id                  int // the file's stream index, or externalBase+i
	src                 AudioSource
	lang                string // ISO 639-1, "" unknown
	studio, title       string
	def, external       bool
	cleaned             bool
	cleanedOf, position int
}

// copyable: an AAC-LC source of ≤ 2 channels at 48 kHz plays as it is (an
// unknown profile — a probe stored before it was kept — is encoded).
func copyable(a media.AudioStream) bool {
	return strings.EqualFold(a.Codec, "aac") && a.Profile == "LC" && a.Channels >= 1 && a.Channels <= 2 && a.SampleRate == 48000
}

// Plan lists a programme's tracks from its file's probe, its external
// audio tracks (as the API lists them: the files beside it, then the
// cleaned voice-overs) and its subtitle files, each under its key.
//
//   - A Russian track's key is its voice: a favourite studio its title
//     names, else the studio of its title or file, else its title without
//     codec and channel words; a studio's 5.1 and 2.0 are one voice (the
//     programme's first of them plays). A cleaned voice-over is its own
//     key («LostFilm (очищено)»).
//   - The original: a non-Russian track whose title says it is, else the
//     file's first track neither Russian nor English (a release of a
//     French film adds Russian and English voices to the French; the
//     title's original language is not kept in the library), else its
//     first English one, else an external one. Its key is its language.
//   - Every other track: its language and voice.
//   - Subtitles: the full (not forced) text tracks, the file's own before
//     the subtitle files, keyed by language and their place in it.
func Plan(p media.ProbeResult, ext []media.ExternalAudio, subs []subtitles.External, o PlanOptions) Tracks {
	var all []candidate
	for i, a := range p.Audio {
		all = append(all, candidate{id: a.Index, src: AudioSource{Index: a.Index, Copy: copyable(a)}, lang: audioLang(a.Lang, a.Title),
			title: a.Title, studio: studioOf(a.Title, o.Studios), def: a.Default, position: i})
	}
	var cleaned []candidate
	for i, e := range ext {
		c := candidate{id: externalBase + i, src: AudioSource{External: e.Path, OffsetMs: e.OffsetMs, Tempo: e.Tempo},
			lang: audioLang(e.Stream.Lang, e.Stream.Title), title: e.Stream.Title, studio: e.Studio, external: true,
			position: len(p.Audio) + i}
		// a file beside the video plays as it is only unshifted: an offset
		// or a tempo needs the filters
		c.src.Copy = copyable(e.Stream) && e.OffsetMs == 0 && !tempoOn(e.Tempo)
		if c.studio == "" {
			c.studio = studioOf(e.Stream.Title, o.Studios)
		}
		if c.lang == "" && c.studio != "" {
			c.lang = "ru" // a voice-over studio's file that names no language
		}
		if e.Kind == media.KindCleaned {
			c.cleaned, c.cleanedOf = true, e.CleanedOf
			cleaned = append(cleaned, c)
			continue
		}
		all = append(all, c)
	}
	rank := func(c candidate) int {
		for i, s := range o.Studios {
			if c.studio != "" && release.StudioKey(c.studio) == release.StudioKey(s) || release.NamesStudio(c.title, s) {
				return i
			}
		}
		return len(o.Studios)
	}
	var t Tracks
	// Russian voices, best first, each cleaned version beside its voice
	var ru []candidate
	for _, c := range all {
		if c.lang == "ru" {
			ru = append(ru, c)
		}
	}
	slices.SortStableFunc(ru, func(a, b candidate) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		if a.external != b.external { // the file's own before a file beside it
			if a.external {
				return 1
			}
			return -1
		}
		if a.def != b.def {
			if a.def {
				return -1
			}
			return 1
		}
		return a.position - b.position
	})
	keys := map[string]bool{}
	add := func(tr AudioTrack, generic bool) {
		if keys[tr.Key] {
			if !generic {
				return // the same voice again (its 5.1 and its 2.0)
			}
			base := tr.Key
			for n := 2; keys[tr.Key]; n++ {
				tr.Key = base + "#" + strconv.Itoa(n)
			}
		}
		keys[tr.Key] = true
		t.Audio = append(t.Audio, tr)
	}
	track := func(c candidate) AudioTrack {
		return AudioTrack{Lang: c.lang, Source: c.src, Studio: c.studio, Title: c.title, External: c.external, fav: rank(c)}
	}
	used := map[int]bool{}
	for _, c := range ru {
		vk, vshow := voiceOf(c)
		tr := track(c)
		tr.Key, tr.Voice = "ru:"+vk, vshow
		var cl []AudioTrack
		for _, x := range cleaned {
			if x.cleanedOf == c.id && !used[x.id] {
				used[x.id] = true
				ct := track(x)
				ct.Key, ct.Voice, ct.Lang, ct.Studio, ct.Cleaned, ct.fav = tr.Key+cleanSuffix, vshow, "ru", c.studio, true, tr.fav
				if ct.Title == "" {
					ct.Title = c.title
				}
				cl = append(cl, ct)
			}
		}
		if o.PreferCleaned {
			for _, x := range cl {
				add(x, false)
			}
		}
		add(tr, vk == "")
		if !o.PreferCleaned {
			for _, x := range cl {
				add(x, false)
			}
		}
	}
	// cleaned versions of a track that is not a Russian one (or unknown)
	for _, x := range cleaned {
		if used[x.id] {
			continue
		}
		vk, vshow := voiceOf(x)
		ct := track(x)
		ct.Lang = "ru"
		ct.Key, ct.Voice, ct.Cleaned = "ru:"+vk+cleanSuffix, vshow, true
		add(ct, false)
	}
	// the original
	var foreign []candidate
	for _, c := range all {
		if c.lang != "ru" {
			foreign = append(foreign, c)
		}
	}
	orig := -1
	if k := slices.IndexFunc(foreign, func(c candidate) bool { return saysOriginal(c.title) }); k >= 0 {
		orig = k
	} else if k := slices.IndexFunc(foreign, func(c candidate) bool { return !c.external && c.lang != "en" }); k >= 0 {
		orig = k
	} else if k := slices.IndexFunc(foreign, func(c candidate) bool { return !c.external }); k >= 0 {
		orig = k
	} else if len(foreign) > 0 {
		orig = 0
	}
	if orig >= 0 {
		c := foreign[orig]
		tr := track(c)
		tr.Key, tr.Original = "orig:"+c.lang, true
		_, tr.Voice = voiceOf(c)
		add(tr, false)
	}
	// the other languages
	for i, c := range foreign {
		if i == orig {
			continue
		}
		vk, vshow := voiceOf(c)
		tr := track(c)
		tr.Key, tr.Voice = c.lang+":"+vk, vshow
		add(tr, vk == "")
	}
	// what plays when nothing fits
	switch {
	case len(p.Audio) > 0:
		t.Default = all[0].src
		for i, a := range p.Audio {
			if a.Default {
				t.Default = all[i].src
				break
			}
		}
	case len(all) > 0:
		t.Default = all[0].src
	default:
		t.Default = silence
	}
	// subtitles
	n := map[string]int{}
	addSub := func(s SubPick) {
		lang := s.Lang
		n[lang]++
		key := bcp47(lang)
		if n[lang] > 1 {
			key += "#" + strconv.Itoa(n[lang])
		}
		t.Subs = append(t.Subs, SubTrack{Key: key, Pick: s})
	}
	for _, s := range p.Subs {
		if !s.Forced && media.TextSubCodecs[strings.ToLower(s.Codec)] {
			addSub(SubPick{Index: s.Index, Codec: s.Codec, Lang: langTag(s.Lang), Title: s.Title})
		}
	}
	for _, s := range subs {
		if !s.Forced && media.SubDemuxer[s.Codec] != "" {
			addSub(SubPick{External: s.Path, Codec: s.Codec, Lang: langTag(s.Lang), Title: s.Title})
		}
	}
	return t
}

func tempoOn(t float64) bool { return t > 0 && t != 1 }

// studioOf is the studio a track title names: a favourite one first (the
// household may type studios outside the table), else the table's first.
func studioOf(title string, favourites []string) string {
	for _, s := range favourites {
		if release.NamesStudio(title, s) {
			return s
		}
	}
	return firstStudio(title)
}

// voiceOf is a track's voice: its key part and how it is shown (the
// studio, else the title without its language and codec words).
func voiceOf(c candidate) (key, show string) {
	if c.studio != "" {
		return release.StudioKey(c.studio), c.studio
	}
	show = voiceTitle(c.title)
	return strings.ToLower(show), show
}

// voiceTitle is a track title without the language it begins with and
// without codec and channel words («Русский — Дубляж AC3 5.1» → «Дубляж»);
// a kind-of-voice abbreviation is dropped beside a name («AVO - Дмитрий
// Пучков» → «Дмитрий Пучков») and is the kind alone («MVO» →
// «Многоголосый»); "" when nothing is left.
func voiceTitle(title string) string {
	var kept []string
	kind := ""
	for _, w := range strings.Fields(withoutLanguage(title)) {
		bare := strings.ToLower(strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
		if bare == "" || genericWords[bare] || strings.Trim(bare, "0123456789.") == "" || genericTitle(bare) {
			continue
		}
		if k, ok := voiceAbbrevs[bare]; ok {
			if kind == "" {
				kind = k
			}
			continue
		}
		kept = append(kept, strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
	}
	if len(kept) == 0 {
		return kind
	}
	return strings.Join(kept, " ")
}

// voiceAbbrevs are the release abbreviations of a kind of voice-over (the
// voiceKinds word they stand for).
var voiceAbbrevs = map[string]string{"dub": "Дубляж", "mvo": "Многоголосый", "dvo": "Двухголосый",
	"avo": "Авторский", "vo": "Одноголосый", "svo": "Одноголосый", "pmvo": "Многоголосый", "lmvo": "Многоголосый"}

// audioLang is a track's ISO 639-1 language: its tag, else a Cyrillic
// title's (Russian unless Ukrainian letters say otherwise); "" unknown.
func audioLang(tag, title string) string {
	if c := langTag(tag); c != "" {
		return c
	}
	cyr := false
	for _, r := range title {
		if unicode.Is(unicode.Cyrillic, r) {
			cyr = true
		}
		if strings.ContainsRune("іїєґІЇЄҐ", r) {
			return "uk"
		}
	}
	if cyr {
		return "ru"
	}
	return ""
}

func firstStudio(title string) string {
	if s := release.Studios(title); len(s) > 0 {
		return s[0]
	}
	return ""
}

// saysOriginal: the title names the track the original («Original»,
// «Оригинал», «Orig»).
func saysOriginal(title string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if w == "original" || w == "orig" || strings.HasPrefix(w, "оригинал") {
			return true
		}
	}
	return false
}

// genericWords say what a track is technically, not whose voice it is.
var genericWords = map[string]bool{"ac3": true, "eac3": true, "dts": true, "hd": true, "ma": true, "truehd": true, "atmos": true,
	"aac": true, "flac": true, "mp3": true, "opus": true, "pcm": true, "lpcm": true, "dd": true, "ddp": true, "dolby": true,
	"digital": true, "plus": true, "stereo": true, "mono": true, "surround": true, "ch": true, "x": true, "kbps": true,
	"audio": true, "track": true, "default": true, "original": true, "orig": true, "оригинал": true, "оригинальная": true,
	"оригинальный": true, "дорожка": true, "стерео": true, "моно": true}

// genericTitle: a title of codec and channel words only («AC3 5.1»).
func genericTitle(title string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !genericWords[w] && strings.Trim(w, "0123456789") != "" && !isChannelWord(w) {
			return false
		}
	}
	return true
}

// isChannelWord: «2ch», «6ch», «5.1ch» parts, «kbps» rates («192kbps»).
func isChannelWord(w string) bool {
	for _, suf := range []string{"ch", "kbps", "k"} {
		if d, ok := strings.CutSuffix(w, suf); ok && d != "" && strings.Trim(d, "0123456789") == "" {
			return true
		}
	}
	return false
}

// withoutLanguage is a track title without the language it begins with
// («Русский — Дубляж» → «Дубляж», «Rus: MVO» → «MVO»): the label says
// the language already.
func withoutLanguage(title string) string {
	t := strings.TrimSpace(title)
	end := strings.IndexFunc(t, func(r rune) bool { return !unicode.IsLetter(r) })
	if end < 0 {
		end = len(t)
	}
	if subtitles.NormLang(t[:end]) == "" {
		return t
	}
	return strings.TrimLeftFunc(t[end:], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// voiceKinds are the Russian words a track title uses for a kind of
// voice-over rather than a studio («Дубляж», «Многоголосый»): shown in the
// viewer's language.
var voiceKinds = map[string]string{
	"дубляж": "Дубляж", "дублированный": "Дубляж", "многоголосый": "Многоголосый", "многоголосая": "Многоголосый",
	"двухголосый": "Двухголосый", "двухголосая": "Двухголосый", "одноголосый": "Одноголосый", "одноголосая": "Одноголосый",
	"авторский": "Авторский", "авторская": "Авторский", "профессиональный": "Профессиональный", "любительский": "Любительский",
}

// voiceKind is voice in lang when it is one of voiceKinds, else voice.
func voiceKind(lang, voice string) string {
	if k, ok := voiceKinds[strings.ToLower(strings.TrimSpace(voice))]; ok {
		return i18n.TL(lang, k)
	}
	return voice
}
