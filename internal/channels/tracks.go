package channels

import (
	"slices"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/release"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// Slot is a rendition of the live stream. The slots are fixed, so a
// channel's master playlist never changes while it runs; what fills a slot
// changes from programme to programme (Tracks).
type Slot struct {
	ID   string // ru, ru2, en, orig (audio); ru, en (subtitles)
	Name string // the HLS NAME (the API's trackJSON.Name)
	Lang string // the HLS LANGUAGE
}

// AudioSlots and SubSlots in the master playlist's order.
var (
	AudioSlots = []Slot{{"ru", "Русский", "ru"}, {"ru2", "Русский 2", "ru"}, {"en", "English", "en"}, {"orig", "Оригинал", "mul"}}
	SubSlots   = []Slot{{"ru", "Русские", "ru"}, {"en", "English", "en"}}
)

// Slot positions in AudioSlots / SubSlots.
const (
	slotRU, slotRU2, slotEN, slotOrig = 0, 1, 2, 3
	subRU, subEN                      = 0, 1
)

// AudioSource is what an audio output reads: the file's stream Index
// (External ""), an external audio file (its first stream, shifted by
// OffsetMs, at Tempo), or silence (External "", Index -1).
type AudioSource struct {
	External        string
	Index           int
	OffsetMs, Tempo float64
}

var silence = AudioSource{Index: -1}

// AudioPick is the track a slot plays in a programme.
type AudioPick struct {
	Source AudioSource
	// Lang: ISO 639-1 ("" unknown); Studio: the voice-over studio ("" none
	// known); Title: the track's own title.
	Lang, Studio, Title string
	External, Cleaned   bool
}

// SubPick is the subtitle track a slot shows in a programme: the file's
// stream Index (External "") or a subtitle file (External, Codec its
// codec).
type SubPick struct {
	External, Codec string
	Index           int
	Lang, Title     string
}

// Tracks are the renditions of one programme: Audio[i] fills AudioSlots[i]
// (nil: the programme has no such track, the slot plays Fallback, the
// programme's «Русский» choice, else its default track, else silence);
// Subs[i] fills SubSlots[i] (nil: empty WebVTT segments).
type Tracks struct {
	Audio    [4]*AudioPick
	Fallback AudioSource
	Subs     [2]*SubPick
}

// Source of slot i: its own track, else the fallback.
func (t Tracks) source(i int) AudioSource {
	if p := t.Audio[i]; p != nil {
		return p.Source
	}
	return t.Fallback
}

// PlanOptions are the household's settings the slots follow (one encoder
// serves every profile of a channel).
type PlanOptions struct {
	// Studios: «Любимые озвучки», best first.
	Studios []string
	// PreferCleaned: a voice-over's cleaned version («Очищено») plays in
	// its slot instead of the studio's track.
	PreferCleaned bool
}

// externalBase is the index the API gives the external audio tracks of a
// file (track i of the list is externalBase+i); media.ExternalAudio's
// CleanedOf counts that way.
const externalBase = 2000

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

func (c candidate) pick() *AudioPick {
	return &AudioPick{Source: c.src, Lang: c.lang, Studio: c.studio, Title: c.title, External: c.external, Cleaned: c.cleaned}
}

// voice identifies whose voice a track is (its studio, else its title).
func (c candidate) voice() string {
	if c.studio != "" {
		return release.StudioKey(c.studio)
	}
	if genericTitle(c.title) {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.title))
}

// Plan fills the slots of a programme from its file's probe, its external
// audio tracks (as the API lists them: the files beside it, then the
// cleaned voice-overs) and its subtitle files:
//
//   - ru: the best Russian voice: the household's favourite studios in
//     their order, else the file's default Russian track, else its first
//     Russian one, else an external Russian voice-over; ru2 the next
//     Russian track of another voice. With PreferCleaned a chosen voice's
//     cleaned version plays instead.
//   - en: the default English track, else the first.
//   - orig: a non-Russian track whose title says it is the original, else
//     the file's first track neither Russian nor English (a release of a
//     French film adds Russian and English voices to the French; the
//     title's original language is not kept in the library), else its
//     first English one, else an external one.
//   - subtitles ru / en: the first full (not forced) text track of the
//     language, the file's own before the subtitle files.
func Plan(p media.ProbeResult, ext []media.ExternalAudio, subs []subtitles.External, o PlanOptions) Tracks {
	var all []candidate
	for i, a := range p.Audio {
		all = append(all, candidate{id: a.Index, src: AudioSource{Index: a.Index}, lang: audioLang(a.Lang, a.Title), title: a.Title,
			studio: firstStudio(a.Title), def: a.Default, position: i})
	}
	var cleaned []candidate
	for i, e := range ext {
		c := candidate{id: externalBase + i, src: AudioSource{External: e.Path, OffsetMs: e.OffsetMs, Tempo: e.Tempo},
			lang: audioLang(e.Stream.Lang, e.Stream.Title), title: e.Stream.Title, studio: e.Studio, external: true,
			position: len(p.Audio) + i}
		if c.studio == "" {
			c.studio = firstStudio(e.Stream.Title)
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
	var t Tracks
	// Russian
	var ru []candidate
	for _, c := range all {
		if c.lang == "ru" {
			ru = append(ru, c)
		}
	}
	rank := func(c candidate) int {
		for i, s := range o.Studios {
			if c.studio != "" && release.StudioKey(c.studio) == release.StudioKey(s) || release.NamesStudio(c.title, s) {
				return i
			}
		}
		return len(o.Studios)
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
	withCleaned := func(c candidate) *AudioPick {
		if o.PreferCleaned {
			for _, x := range cleaned {
				if x.cleanedOf == c.id {
					x.studio, x.lang = c.studio, c.lang
					if x.title == "" {
						x.title = c.title
					}
					return x.pick()
				}
			}
		}
		return c.pick()
	}
	if len(ru) > 0 {
		t.Audio[slotRU] = withCleaned(ru[0])
		for _, c := range ru[1:] {
			if v := c.voice(); v == "" || v != ru[0].voice() {
				t.Audio[slotRU2] = withCleaned(c)
				break
			}
		}
	}
	// English
	var en []candidate
	for _, c := range all {
		if c.lang == "en" {
			en = append(en, c)
		}
	}
	if k := slices.IndexFunc(en, func(c candidate) bool { return c.def && !c.external }); k >= 0 {
		t.Audio[slotEN] = en[k].pick()
	} else if len(en) > 0 {
		t.Audio[slotEN] = en[0].pick()
	}
	// the original
	var foreign []candidate
	for _, c := range all {
		if c.lang != "ru" {
			foreign = append(foreign, c)
		}
	}
	if k := slices.IndexFunc(foreign, func(c candidate) bool { return saysOriginal(c.title) }); k >= 0 {
		t.Audio[slotOrig] = foreign[k].pick()
	} else if k := slices.IndexFunc(foreign, func(c candidate) bool { return !c.external && c.lang != "en" }); k >= 0 {
		t.Audio[slotOrig] = foreign[k].pick()
	} else if k := slices.IndexFunc(foreign, func(c candidate) bool { return !c.external }); k >= 0 {
		t.Audio[slotOrig] = foreign[k].pick()
	} else if len(foreign) > 0 {
		t.Audio[slotOrig] = foreign[0].pick()
	}
	// what an empty slot plays
	switch {
	case t.Audio[slotRU] != nil:
		t.Fallback = t.Audio[slotRU].Source
	case len(p.Audio) > 0:
		t.Fallback = AudioSource{Index: p.Audio[0].Index}
		for _, a := range p.Audio {
			if a.Default {
				t.Fallback = AudioSource{Index: a.Index}
				break
			}
		}
	case len(all) > 0:
		t.Fallback = all[0].src
	default:
		t.Fallback = silence
	}
	// subtitles
	for i, slot := range SubSlots {
		for _, s := range p.Subs {
			if !s.Forced && media.TextSubCodecs[strings.ToLower(s.Codec)] && subtitles.NormLang(s.Lang) == slot.Lang {
				t.Subs[i] = &SubPick{Index: s.Index, Codec: s.Codec, Lang: slot.Lang, Title: s.Title}
				break
			}
		}
		if t.Subs[i] != nil {
			continue
		}
		for _, s := range subs {
			if !s.Forced && media.SubDemuxer[s.Codec] != "" && subtitles.NormLang(s.Lang) == slot.Lang {
				t.Subs[i] = &SubPick{External: s.Path, Codec: s.Codec, Lang: slot.Lang, Title: s.Title}
				break
			}
		}
	}
	return t
}

// audioLang is a track's ISO 639-1 language: its tag, else a Cyrillic
// title's (Russian unless Ukrainian letters say otherwise); "" unknown.
func audioLang(tag, title string) string {
	if c := subtitles.NormLang(tag); c != "" {
		return c
	}
	if t := strings.ToLower(strings.TrimSpace(tag)); t != "" && t != "und" && len(t) <= 3 {
		return t // a code NormLang does not list ("tr", "hun")
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
	"audio": true, "track": true, "default": true, "original": true, "orig": true}

// genericTitle: a title of codec and channel words only («AC3 5.1»).
func genericTitle(title string) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !genericWords[w] && strings.Trim(w, "0123456789") != "" {
			return false
		}
	}
	return true
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

// TrackLabel is a slot as a programme fills it, for the API.
type TrackLabel struct {
	Slot, Name, Label, Lang string
}

// Labels are the slots present in the programme with descriptive labels
// in lang («Русский — LostFilm», «Русский — Кураж-Бамбей (внешняя)»,
// «English», «Оригинал (fr)»). Never nil.
func (t Tracks) Labels(lang string) (audio, subs []TrackLabel) {
	audio, subs = []TrackLabel{}, []TrackLabel{}
	for i, p := range t.Audio {
		if p == nil {
			continue
		}
		s := AudioSlots[i]
		l := TrackLabel{Slot: s.ID, Name: s.Name, Lang: p.Lang}
		switch i {
		case slotRU, slotRU2:
			l.Lang = "ru"
			voice := p.Studio
			if voice == "" && !genericTitle(p.Title) && !saysOriginal(p.Title) {
				voice = withoutLanguage(p.Title)
			}
			voice = voiceKind(lang, voice)
			switch {
			case voice == "":
				l.Label = i18n.TL(lang, "Русский")
			case p.Cleaned:
				l.Label = i18n.TL(lang, "Русский — %s (очищено)", voice)
			case p.External:
				l.Label = i18n.TL(lang, "Русский — %s (внешняя)", voice)
			default:
				l.Label = i18n.TL(lang, "Русский — %s", voice)
			}
		case slotEN:
			l.Lang, l.Label = "en", "English"
		case slotOrig:
			if p.Lang == "" {
				l.Lang, l.Label = "und", i18n.TL(lang, "Оригинал")
			} else {
				l.Label = i18n.TL(lang, "Оригинал (%s)", p.Lang)
			}
		}
		audio = append(audio, l)
	}
	for i, p := range t.Subs {
		if p == nil {
			continue
		}
		s := SubSlots[i]
		l := TrackLabel{Slot: s.ID, Name: s.Name, Lang: s.Lang}
		if i == subRU {
			l.Label = i18n.TL(lang, "Русские")
		} else {
			l.Label = "English"
		}
		subs = append(subs, l)
	}
	return audio, subs
}
