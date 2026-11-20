package channels

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
)

// Rendition is one EXT-X-MEDIA of a run's master playlist: the key it
// plays, its NAME (unique in its group), its LANGUAGE (BCP 47) and its
// DEFAULT / AUTOSELECT.
type Rendition struct {
	Key, Name, Lang     string
	Default, Autoselect bool
	// what the NAME is made from, so it can be said in a viewer's language
	// (Localized): the voice's track, or the subtitles' language (named)
	track   *AudioTrack
	subLang string
	named   bool
}

// TrackSet is a channel run's renditions (audio-<n>.m3u8 is Audio[n],
// subs-<n>.m3u8 Subs[n]): fixed for the run, so a player's master never
// goes stale while it watches.
type TrackSet struct {
	Audio, Subs []Rendition
}

// keyLang is the ISO 639 language of an audio or subtitle key ("" unknown).
func keyLang(key string) string {
	if l, ok := strings.CutPrefix(key, "orig:"); ok {
		return l
	}
	l, _, _ := strings.Cut(key, ":")
	l, _, _ = strings.Cut(l, "#")
	if l == "und" {
		return ""
	}
	return l
}

// setAgg is a key across the programmes of a set.
type setAgg struct {
	track      AudioTrack
	sub        SubPick
	count      int // programmes that have it
	first      int // the order it was first met
	group, fav int
}

// BuildSet makes a run's track set from the tracks of the programmes it
// can air (the one on air first). Audio: Russian voices in favourite-
// studio order, then by how many programmes have them (a cleaned voice
// beside its voice: before it with PreferCleaned); then each original
// language, then the other languages, by frequency; the first AudioCap,
// with a place for every language (its original first) before a
// language's further voices.
// Subtitles: Russian first, then by frequency; the first SubCap. A set
// with no audio at all still has one rendition (each programme's default
// track, else silence).
func BuildSet(progs []Tracks, o PlanOptions) TrackSet {
	aggs := map[string]*setAgg{}
	subAggs := map[string]*setAgg{}
	seen := 0
	for _, t := range progs {
		for _, a := range t.Audio {
			x := aggs[a.Key]
			if x == nil {
				g := 2
				switch {
				case a.Lang == "ru":
					g = 0
				case a.Original:
					g = 1
				}
				x = &setAgg{track: a, first: seen, group: g, fav: a.fav}
				aggs[a.Key] = x
				seen++
			}
			x.count++
			x.fav = min(x.fav, a.fav)
		}
		for _, s := range t.Subs {
			x := subAggs[s.Key]
			if x == nil {
				g := 1
				if s.Pick.Lang == "ru" {
					g = 0
				}
				x = &setAgg{sub: s.Pick, first: seen, group: g}
				subAggs[s.Key] = x
				seen++
			}
			x.count++
		}
	}
	// a cleaned voice ranks as its voice does (when the set has it)
	rankOf := func(k string) *setAgg {
		if v, ok := strings.CutSuffix(k, cleanSuffix); ok && aggs[v] != nil {
			return aggs[v]
		}
		return aggs[k]
	}
	keys := make([]string, 0, len(aggs))
	for k := range aggs {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		x, y := aggs[a], aggs[b]
		if x.group != y.group {
			return x.group - y.group
		}
		rx, ry := rankOf(a), rankOf(b)
		if x.group == 0 && rx.fav != ry.fav {
			return rx.fav - ry.fav
		}
		if rx.count != ry.count {
			return ry.count - rx.count
		}
		if rx.first != ry.first {
			return rx.first - ry.first
		}
		if ca, cb := strings.HasSuffix(a, cleanSuffix), strings.HasSuffix(b, cleanSuffix); ca != cb {
			if ca == o.PreferCleaned {
				return -1
			}
			return 1
		}
		return x.first - y.first
	})
	if len(keys) > AudioCap {
		// every known language of the horizon keeps a place (its best key:
		// an original before another track of its language), then the best of
		// the rest fill the set: a programme's tracks of a language whose
		// voices did not make it play in that language's renditions
		// (Tracks.own), so a cap cut can cost a voice's own name, never a
		// language
		pick := map[string]bool{}
		langs := map[string]bool{}
		for _, k := range keys {
			if l := aggs[k].track.Lang; l != "" && len(pick) < AudioCap && !langs[l] {
				langs[l], pick[k] = true, true
			}
		}
		for _, k := range keys {
			if len(pick) < AudioCap {
				pick[k] = true
			}
		}
		keys = slices.DeleteFunc(keys, func(k string) bool { return !pick[k] })
	}
	var set TrackSet
	ml := o.Lang
	for _, k := range keys {
		tr := aggs[k].track
		set.Audio = append(set.Audio, Rendition{Key: k, Name: audioRenditionName(ml, aggs[k]), Lang: bcp47(tr.Lang), track: &tr, named: true})
	}
	if len(set.Audio) == 0 {
		set.Audio = []Rendition{{Key: "", Name: i18n.TL(ml, "Звук"), Lang: "und", named: true}}
	}
	subKeys := make([]string, 0, len(subAggs))
	for k := range subAggs {
		subKeys = append(subKeys, k)
	}
	slices.SortFunc(subKeys, func(a, b string) int {
		x, y := subAggs[a], subAggs[b]
		if x.group != y.group {
			return x.group - y.group
		}
		if x.count != y.count {
			return y.count - x.count
		}
		return x.first - y.first
	})
	if len(subKeys) > SubCap {
		subKeys = subKeys[:SubCap]
	}
	for _, k := range subKeys {
		set.Subs = append(set.Subs, Rendition{Key: k, Name: subName(ml, subAggs[k].sub.Lang), Lang: bcp47(subAggs[k].sub.Lang), subLang: subAggs[k].sub.Lang, named: true})
	}
	uniqueNames(set.Audio)
	uniqueNames(set.Subs)
	// DEFAULT: the first Russian voice, else the first (audio only: a
	// default subtitle rendition would switch subtitles on); AUTOSELECT:
	// the first of each language
	def := slices.IndexFunc(set.Audio, func(r Rendition) bool { return r.Lang == "ru" })
	set.Audio[max(def, 0)].Default = true
	for _, g := range [][]Rendition{set.Audio, set.Subs} {
		langs := map[string]bool{}
		for i := range g {
			if !langs[g[i].Lang] {
				langs[g[i].Lang], g[i].Autoselect = true, true
			}
		}
	}
	return set
}

// Localized is the set with its NAMEs in language lang (a viewer's
// interface language): the master playlist a profile gets and the
// programme entries of its guide name the renditions alike, in its
// language. Keys, LANGUAGE, DEFAULT and order stay as they are, so the
// renditions are the same ones whatever the language. A rendition made
// elsewhere (no source kept) keeps its name.
func (s TrackSet) Localized(lang string) TrackSet {
	if lang == "" {
		return s
	}
	out := TrackSet{Audio: slices.Clone(s.Audio), Subs: slices.Clone(s.Subs)}
	for i := range out.Audio {
		r := &out.Audio[i]
		switch {
		case r.track != nil:
			r.Name = audioRenditionName(lang, &setAgg{track: *r.track})
		case r.named && r.Key == "":
			r.Name = i18n.TL(lang, "Звук")
		}
	}
	for i := range out.Subs {
		if r := &out.Subs[i]; r.named {
			r.Name = subName(lang, r.subLang)
		}
	}
	uniqueNames(out.Audio)
	uniqueNames(out.Subs)
	return out
}

// audioRenditionName is a rendition's NAME in the server's language ml: a Russian
// voice its studio or title («Дубляж», «LostFilm (очищено)», else
// «Русский»), the original «Оригинал (English)», another language its own
// name («Français»).
func audioRenditionName(ml string, x *setAgg) string {
	t := x.track
	switch {
	case t.Lang == "ru":
		v := voiceKind(ml, t.Voice)
		switch {
		case v == "" && t.Cleaned:
			return i18n.TL(ml, "Русский (очищено)")
		case v == "":
			return i18n.TL(ml, "Русский")
		case t.Cleaned:
			return i18n.TL(ml, "%s (очищено)", v)
		}
		return v
	case t.Original && t.Lang == "":
		return i18n.TL(ml, "Оригинал")
	case t.Original:
		return i18n.TL(ml, "Оригинал (%s)", endonym(t.Lang))
	case t.Lang == "":
		return i18n.TL(ml, "Другой язык")
	}
	return endonym(t.Lang)
}

// subName is a subtitle rendition's NAME: «Русские», else the language's
// own name.
func subName(ml, lang string) string {
	switch lang {
	case "ru":
		return i18n.TL(ml, "Русские")
	case "":
		return i18n.TL(ml, "Субтитры")
	}
	return endonym(lang)
}

// uniqueNames makes the names of a group unique: a repeated one gets
// « 2», « 3»…; and safe in a quoted HLS attribute (no quotes, no line
// breaks).
func uniqueNames(g []Rendition) {
	used := map[string]bool{}
	for i := range g {
		n := strings.Map(func(r rune) rune {
			switch {
			case r == '"':
				return '\''
			case r < ' ' || r == 0x7f:
				return -1
			}
			return r
		}, g[i].Name)
		base := n
		for k := 2; used[n]; k++ {
			n = base + " " + strconv.Itoa(k)
		}
		used[n], g[i].Name = true, n
	}
}

// Renditions are what the programme plays in each rendition of set:
// audio[n] the source of set.Audio[n], subs[n] the track of set.Subs[n]
// (nil: empty WebVTT). A key the programme lacks plays its fallback: a
// cleaned voice's own voice, else its track in the key's language, else
// its best Russian one, else its default track — never silence when it
// has sound.
func (t Tracks) Renditions(set TrackSet) (audio []AudioSource, subs []*SubPick) {
	own := t.own(set)
	for n, r := range set.Audio {
		if k := own[n]; k >= 0 {
			audio = append(audio, t.Audio[k].Source)
			continue
		}
		audio = append(audio, t.audioFor(r.Key))
	}
	for _, r := range set.Subs {
		var p *SubPick
		if k := slices.IndexFunc(t.Subs, func(s SubTrack) bool { return s.Key == r.Key }); k >= 0 {
			pick := t.Subs[k].Pick
			p = &pick
		}
		subs = append(subs, p)
	}
	return audio, subs
}

// own is the programme's track each rendition of set plays as its own:
// own[n] indexes t.Audio, -1 none (the rendition plays a fallback). A
// track plays in the rendition of its key; a track whose key the set
// lacks (a run's set holds the voices of 48 h, AudioCap of them) takes the
// first rendition of its language the programme has no track for — the
// programme's best tracks first, a cleaned voice-over a cleaned rendition
// when one is free — so every track of the programme plays somewhere
// while its language has renditions enough.
func (t Tracks) own(set TrackSet) []int {
	own := make([]int, len(set.Audio))
	for n := range own {
		own[n] = -1
	}
	placed := make([]bool, len(t.Audio))
	for n, r := range set.Audio {
		if k := slices.IndexFunc(t.Audio, func(a AudioTrack) bool { return a.Key == r.Key }); k >= 0 && !placed[k] {
			own[n], placed[k] = k, true
		}
	}
	for k, a := range t.Audio {
		if placed[k] {
			continue
		}
		free := func(alike bool) int {
			for n, r := range set.Audio {
				if own[n] < 0 && r.Key != "" && keyLang(r.Key) == a.Lang && (!alike || strings.HasSuffix(r.Key, cleanSuffix) == a.Cleaned) {
					return n
				}
			}
			return -1
		}
		n := free(true)
		if n < 0 {
			n = free(false)
		}
		if n >= 0 {
			own[n], placed[k] = k, true
		}
	}
	return own
}

func (t Tracks) audioFor(key string) AudioSource {
	find := func(f func(AudioTrack) bool) (AudioSource, bool) {
		if k := slices.IndexFunc(t.Audio, f); k >= 0 {
			return t.Audio[k].Source, true
		}
		return AudioSource{}, false
	}
	if s, ok := find(func(a AudioTrack) bool { return a.Key == key }); ok {
		return s
	}
	if v, ok := strings.CutSuffix(key, cleanSuffix); ok {
		if s, ok := find(func(a AudioTrack) bool { return a.Key == v }); ok {
			return s
		}
	}
	if l := keyLang(key); l != "" {
		if s, ok := find(func(a AudioTrack) bool { return a.Lang == l }); ok {
			return s
		}
	}
	if s, ok := find(func(a AudioTrack) bool { return a.Lang == "ru" }); ok {
		return s
	}
	return t.Default
}

// TrackLabel is a rendition the programme really has, for the API: Slot
// "a<n>" / "s<n>" (its place in the set), Name the rendition's NAME,
// Label the programme's own track in the viewer's language, Lang its
// language.
type TrackLabel struct {
	Slot, Name, Label, Lang string
	// CleanedOf: a cleaned voice-over's own rendition (its NAME) when the
	// programme has it too ("" otherwise) — players show the pair as one
	// voice with «Очищенный перевод» on or off.
	CleanedOf string
}

// Labels are the renditions of set the programme has its own track for,
// in the set's order, labelled in lang («Русский — LostFilm»,
// «Русский — Кураж-Бамбей (внешняя)», «Оригинал (Français)», «English»).
// Never nil.
func (t Tracks) Labels(set TrackSet, lang string) (audio, subs []TrackLabel) {
	audio, subs = []TrackLabel{}, []TrackLabel{}
	own := t.own(set)
	for n, r := range set.Audio {
		k := own[n]
		if k < 0 {
			continue
		}
		a := t.Audio[k]
		l := TrackLabel{Slot: "a" + strconv.Itoa(n), Name: r.Name, Lang: bcp47(a.Lang)}
		if a.Cleaned {
			base := strings.TrimSuffix(a.Key, cleanSuffix)
			if bk := slices.IndexFunc(t.Audio, func(x AudioTrack) bool { return x.Key == base }); bk >= 0 {
				if b := slices.Index(own, bk); b >= 0 {
					l.CleanedOf = set.Audio[b].Name
				}
			}
		}
		voice := voiceKind(lang, a.Voice)
		switch {
		case a.Lang == "ru" && voice == "":
			l.Label = i18n.TL(lang, "Русский")
			if a.Cleaned {
				l.Label = i18n.TL(lang, "Русский (очищено)")
			}
		case a.Lang == "ru" && a.Cleaned:
			l.Label = i18n.TL(lang, "Русский — %s (очищено)", voice)
		case a.Lang == "ru" && a.External:
			l.Label = i18n.TL(lang, "Русский — %s (внешняя)", voice)
		case a.Lang == "ru":
			l.Label = i18n.TL(lang, "Русский — %s", voice)
		case a.Original && a.Lang == "":
			l.Label = i18n.TL(lang, "Оригинал")
		case a.Original:
			l.Label = i18n.TL(lang, "Оригинал (%s)", endonym(a.Lang))
		case a.Lang == "":
			l.Label = i18n.TL(lang, "Другой язык")
		default:
			l.Label = endonym(a.Lang)
		}
		if !a.Original && a.Lang != "ru" && voice != "" {
			l.Label += " — " + voice
		}
		audio = append(audio, l)
	}
	for n, r := range set.Subs {
		k := slices.IndexFunc(t.Subs, func(s SubTrack) bool { return s.Key == r.Key })
		if k < 0 {
			continue
		}
		s := t.Subs[k].Pick
		l := TrackLabel{Slot: "s" + strconv.Itoa(n), Name: r.Name, Lang: bcp47(s.Lang), Label: subName(lang, s.Lang)}
		if v := voiceTitle(s.Title); v != "" && !genericSubTitle(v) && !namesLanguage(v, s.Lang) {
			l.Label += " — " + v
		}
		subs = append(subs, l)
	}
	return audio, subs
}

// genericSubTitle: a subtitle title that says only what subtitles are
// («Full», «Полные», «SRT»).
func genericSubTitle(v string) bool {
	switch strings.ToLower(v) {
	case "full", "полные", "полный", "subtitles", "subs", "субтитры", "srt", "ass", "text", "текст":
		return true
	}
	return false
}

// namesLanguage: a title that is only the language's name in some form
// («Українські» for uk, «Russian» for ru).
func namesLanguage(title, lang string) bool {
	if lang == "" {
		return false
	}
	if langTag(title) == lang {
		return true
	}
	t, e := []rune(strings.ToLower(title)), []rune(strings.ToLower(endonym(lang)))
	n := 0
	for n < len(t) && n < len(e) && t[n] == e[n] {
		n++
	}
	return n >= 5 && !strings.ContainsRune(string(t), ' ')
}

type nameLangKey struct{}

// WithNameLanguage is ctx asking for rendition NAMEs in lang (the viewer's
// interface language): Live.Playlist's master and ProgrammeTracks name
// them so (TrackSet.Localized). Without it the run's own names stay.
func WithNameLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, nameLangKey{}, lang)
}

func nameLanguage(ctx context.Context) string {
	l, _ := ctx.Value(nameLangKey{}).(string)
	return l
}

// TrackPrefs are a profile's audio and subtitle defaults («Мои языки»):
// AudioLangs best first; SubMode off | foreign | always; SubLangs ([]: the
// audio languages).
type TrackPrefs struct {
	AudioLangs []string
	SubMode    string
	SubLangs   []string
}

type trackPrefsKey struct{}

// WithTrackPrefs is ctx asking for the master's DEFAULT renditions as the
// profile p would pick them (TrackSet.WithDefaults).
func WithTrackPrefs(ctx context.Context, p TrackPrefs) context.Context {
	return context.WithValue(ctx, trackPrefsKey{}, p)
}

func trackPrefs(ctx context.Context) (TrackPrefs, bool) {
	p, ok := ctx.Value(trackPrefsKey{}).(TrackPrefs)
	return p, ok
}

// primary is a language tag's primary subtag, lower case ("pt-BR" → "pt").
func primary(l string) string {
	l, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(l)), "-")
	l, _, _ = strings.Cut(l, "_")
	return l
}

// WithDefaults is the set with DEFAULT where profile p would pick, so a
// player that follows the master (AVPlayer, Media3, hls.js) starts in the
// profile's languages, as a film does: audio — the first rendition in the
// first of p's audio languages the set has (none: the set's own default);
// subtitles — DEFAULT on the first rendition in p's subtitle languages
// when p shows subtitles («always», or «foreign» with a default voice in
// none of its languages), else on none (subtitles off). The renditions,
// their names and order are unchanged.
func (s TrackSet) WithDefaults(p TrackPrefs) TrackSet {
	out := TrackSet{Audio: slices.Clone(s.Audio), Subs: slices.Clone(s.Subs)}
	in := func(l string, langs []string) bool {
		return slices.ContainsFunc(langs, func(x string) bool { return primary(x) == primary(l) })
	}
	for _, l := range p.AudioLangs {
		k := slices.IndexFunc(out.Audio, func(r Rendition) bool { return primary(r.Lang) == primary(l) })
		if k < 0 {
			continue
		}
		for i := range out.Audio {
			out.Audio[i].Default = i == k
		}
		out.Audio[k].Autoselect = true
		break
	}
	for i := range out.Subs {
		out.Subs[i].Default = false
	}
	voice := ""
	if k := slices.IndexFunc(out.Audio, func(r Rendition) bool { return r.Default }); k >= 0 {
		voice = out.Audio[k].Lang
	}
	show := p.SubMode == "always" || (p.SubMode == "foreign" && voice != "" && voice != "und" && !in(voice, p.AudioLangs))
	if !show {
		return out
	}
	langs := p.SubLangs
	if len(langs) == 0 {
		langs = p.AudioLangs
	}
	for _, l := range langs {
		if k := slices.IndexFunc(out.Subs, func(r Rendition) bool { return primary(r.Lang) == primary(l) }); k >= 0 {
			out.Subs[k].Default, out.Subs[k].Autoselect = true, true
			break
		}
	}
	return out
}
