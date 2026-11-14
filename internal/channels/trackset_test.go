package channels

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/subtitles"
)

func names(g []Rendition) []string {
	var out []string
	for _, r := range g {
		flags := ""
		if r.Default {
			flags += "D"
		}
		if r.Autoselect {
			flags += "A"
		}
		out = append(out, r.Name+"/"+r.Lang+"/"+flags)
	}
	return out
}

func aud(i int, lang, title string) media.AudioStream {
	return media.AudioStream{Index: i, Codec: "aac", Lang: lang, Title: title}
}

// three programmes of the next 48 h: an English film with Дубляж and
// LostFilm, a French one with LostFilm and English, one with Кубик в Кубе
// and two untitled Russian tracks; Кубик в Кубе is the household's
// favourite.
func setProgs(o PlanOptions) []Tracks {
	a := Plan(media.ProbeResult{Audio: []media.AudioStream{aud(1, "eng", ""), aud(2, "rus", "Дубляж"), aud(3, "rus", "LostFilm")},
		Subs: []media.SubStream{{Index: 4, Codec: "subrip", Lang: "rus"}, {Index: 5, Codec: "subrip", Lang: "eng"}}}, nil, nil, o)
	b := Plan(media.ProbeResult{Audio: []media.AudioStream{aud(1, "fre", ""), aud(2, "rus", "LostFilm"), aud(3, "eng", "")},
		Subs: []media.SubStream{{Index: 4, Codec: "subrip", Lang: "eng"}, {Index: 5, Codec: "subrip", Lang: "eng"}}}, nil, nil, o)
	c := Plan(media.ProbeResult{Audio: []media.AudioStream{aud(1, "rus", "MVO Кубик в Кубе"), aud(2, "rus", ""), aud(3, "rus", "")}}, nil, nil, o)
	return []Tracks{a, b, c}
}

func TestBuildSetRanksAcrossProgrammes(t *testing.T) {
	o := PlanOptions{Studios: []string{"Кубик в Кубе"}}
	set := BuildSet(setProgs(o), o)
	// favourite first, then Russian by frequency (LostFilm in two), then
	// the originals, then the others; names unique
	want := []string{"Кубик в Кубе/ru/DA", "LostFilm/ru/", "Дубляж/ru/", "Русский/ru/", "Русский 2/ru/",
		"Оригинал (English)/en/A", "Оригинал (Français)/fr/A", "English/en/"}
	if got := names(set.Audio); !reflect.DeepEqual(got, want) {
		t.Fatalf("audio\n%q\nwant\n%q", got, want)
	}
	if got := names(set.Subs); !reflect.DeepEqual(got, []string{"Русские/ru/A", "English/en/A", "English 2/en/"}) {
		t.Fatalf("subs %q", got)
	}
	// the server's language names them
	o.Lang = "en"
	set = BuildSet(setProgs(o), o)
	if got := names(set.Audio); got[2] != "Dubbed/ru/" || got[3] != "Russian/ru/" || got[5] != "Original (English)/en/A" {
		t.Fatalf("en %q", got)
	}
	// without favourites the most frequent Russian voice leads
	o = PlanOptions{}
	if set := BuildSet(setProgs(o), o); set.Audio[0].Name != "LostFilm" || !set.Audio[0].Default {
		t.Fatalf("no favourites %q", names(set.Audio))
	}
}

func TestBuildSetCaps(t *testing.T) {
	var as []media.AudioStream
	var ss []media.SubStream
	for i, l := range []string{"rus", "eng", "fre", "ger", "spa", "ita", "jpn", "kor", "chi", "pol"} {
		as = append(as, aud(i+1, l, ""))
		ss = append(ss, media.SubStream{Index: 20 + i, Codec: "subrip", Lang: l})
	}
	set := BuildSet([]Tracks{Plan(media.ProbeResult{Audio: as, Subs: ss}, nil, nil, PlanOptions{})}, PlanOptions{})
	if len(set.Audio) != AudioCap || len(set.Subs) != SubCap || set.Audio[0].Lang != "ru" || set.Subs[0].Lang != "ru" {
		t.Fatalf("%d %d %q", len(set.Audio), len(set.Subs), names(set.Audio))
	}
	// eight Russian voices and the English original (The Wolf of Wall
	// Street): the original takes the last place
	var wolf []media.AudioStream
	for i, v := range []string{"Пифагор", "Кубик в Кубе", "Пашин", "Пучков", "Гаврилов", "Сербин", "Визгунов", "Немахов"} {
		wolf = append(wolf, aud(i+1, "rus", "AVO - "+v))
	}
	wolf = append(wolf, aud(9, "eng", "Original"))
	set = BuildSet([]Tracks{Plan(media.ProbeResult{Audio: wolf}, nil, nil, PlanOptions{})}, PlanOptions{})
	got := names(set.Audio)
	if len(got) != AudioCap || got[AudioCap-1] != "Оригинал (English)/en/A" || got[AudioCap-2] != "Визгунов/ru/" || got[0] != "Pifagor/ru/DA" {
		t.Fatalf("wolf %q", got)
	}
	// nothing at all: one rendition (the default track, else silence)
	set = BuildSet(nil, PlanOptions{})
	if len(set.Audio) != 1 || set.Audio[0].Name != "Звук" || !set.Audio[0].Default || len(set.Subs) != 0 {
		t.Fatalf("empty %+v", set)
	}
}

func TestBuildSetCleanedAndSidecar(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{aud(1, "eng", ""), aud(2, "rus", "LostFilm")}}
	ext := []media.ExternalAudio{
		{Path: "/m/F.Кураж-Бамбей.mka", Studio: "Кураж-Бамбей", Stream: media.AudioStream{Lang: "ru"}},
		{Path: "/d/clean/2.mka", Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru", Title: "LostFilm"}, Kind: media.KindCleaned, CleanedOf: 2},
	}
	for _, c := range []struct {
		prefer bool
		want   []string
	}{
		{false, []string{"LostFilm/ru/DA", "LostFilm (очищено)/ru/", "Кураж-Бамбей/ru/", "Оригинал (English)/en/A"}},
		{true, []string{"LostFilm (очищено)/ru/DA", "LostFilm/ru/", "Кураж-Бамбей/ru/", "Оригинал (English)/en/A"}},
	} {
		o := PlanOptions{PreferCleaned: c.prefer}
		set := BuildSet([]Tracks{Plan(p, ext, nil, o)}, o)
		if got := names(set.Audio); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("prefer %v: %q", c.prefer, got)
		}
	}
}

func TestUniqueNamesAndQuotes(t *testing.T) {
	g := []Rendition{{Name: "English"}, {Name: "English"}, {Name: "English 2"}, {Name: `Say "hi"` + "\n"}}
	uniqueNames(g)
	if got := []string{g[0].Name, g[1].Name, g[2].Name, g[3].Name}; !reflect.DeepEqual(got, []string{"English", "English 2", "English 2 2", "Say 'hi'"}) {
		t.Fatalf("%q", got)
	}
}

// A programme without a key's track plays its fallback: a cleaned voice's
// own voice, its track in the key's language, its Russian one, its
// default — never silence; subtitles it lacks are empty.
func TestRenditionsFallback(t *testing.T) {
	o := PlanOptions{Studios: []string{"Кубик в Кубе"}}
	progs := setProgs(o)
	set := BuildSet(progs, o)
	src := func(tr Tracks) string {
		a, s := tr.Renditions(set)
		var out []string
		for _, x := range a {
			out = append(out, fmt.Sprint(x.Index))
		}
		for _, x := range s {
			if x == nil {
				out = append(out, "-")
			} else {
				out = append(out, fmt.Sprint(x.Index))
			}
		}
		return strings.Join(out, ",")
	}
	// set: Кубик, LostFilm, Дубляж, Русский, Русский 2, orig en, orig fr, English | Русские, English, English 2
	// A: no Кубик → its first Russian (Дубляж 2); orig fr → none in French → Russian; untitled → Russian
	if got := src(progs[0]); got != "2,3,2,2,2,1,2,1,4,5,-" {
		t.Fatalf("A %s", got)
	}
	// B: orig en → its English track (3)
	if got := src(progs[1]); got != "2,2,2,2,2,3,1,3,-,4,5" {
		t.Fatalf("B %s", got)
	}
	// C: only Russian; the untitled ones are its own
	if got := src(progs[2]); got != "1,1,1,2,3,1,1,1,-,-,-" {
		t.Fatalf("C %s", got)
	}
	// no Russian at all: the default track
	en := Plan(media.ProbeResult{Audio: []media.AudioStream{aud(1, "ger", ""), {Index: 2, Lang: "spa", Default: true}}}, nil, nil, o)
	if got := src(en); got != "2,2,2,2,2,2,2,2,-,-,-" {
		t.Fatalf("no Russian %s", got)
	}
	// a cleaned voice the programme lacks: its own voice before other Russian ones
	p := media.ProbeResult{Audio: []media.AudioStream{aud(1, "rus", "Дубляж"), aud(2, "rus", "LostFilm")}}
	cl := Plan(p, []media.ExternalAudio{{Path: "/c.mka", Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru"}, Kind: media.KindCleaned, CleanedOf: 2}}, nil, PlanOptions{})
	cset := BuildSet([]Tracks{cl}, PlanOptions{})
	k := -1
	for i, r := range cset.Audio {
		if strings.HasSuffix(r.Key, cleanSuffix) {
			k = i
		}
	}
	plain := Plan(p, nil, nil, PlanOptions{})
	if a, _ := plain.Renditions(cset); k < 0 || a[k].Index != 2 {
		t.Fatalf("cleaned fallback %d %+v", k, a)
	}
	// nothing at all plays silence
	if a, _ := Plan(media.ProbeResult{}, nil, nil, o).Renditions(set); a[0] != silence {
		t.Fatalf("%+v", a)
	}
}

// The API lists only the renditions a programme really has, named as the
// master names them, labelled in the viewer's language.
func TestLabelsOnlyWhatTheProgrammeHas(t *testing.T) {
	o := PlanOptions{Studios: []string{"Кубик в Кубе"}}
	progs := setProgs(o)
	set := BuildSet(progs, o)
	str := func(ls []TrackLabel) string {
		var out []string
		for _, l := range ls {
			out = append(out, l.Slot+"|"+l.Name+"|"+l.Label+"|"+l.Lang)
		}
		return strings.Join(out, " ")
	}
	a, s := progs[0].Labels(set, "ru")
	if got := str(a); got != "a1|LostFilm|Русский — LostFilm|ru a2|Дубляж|Русский — Дубляж|ru a5|Оригинал (English)|Оригинал (English)|en" {
		t.Fatalf("A audio %s", got)
	}
	if got := str(s); got != "s0|Русские|Русские|ru s1|English|English|en" {
		t.Fatalf("A subs %s", got)
	}
	a, _ = progs[1].Labels(set, "en")
	if got := str(a); got != "a1|LostFilm|Russian — LostFilm|ru a6|Оригинал (Français)|Original (Français)|fr a7|English|English|en" {
		t.Fatalf("B audio %s", got)
	}
	// a sidecar and a cleaned voice-over say so
	p := media.ProbeResult{Audio: []media.AudioStream{aud(1, "eng", ""), aud(2, "rus", "LostFilm")}}
	tr := Plan(p, []media.ExternalAudio{
		{Path: "/m/F.mka", Studio: "Кураж-Бамбей", Stream: media.AudioStream{Lang: "ru"}},
		{Path: "/c.mka", Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru"}, Kind: media.KindCleaned, CleanedOf: 2},
	}, []subtitles.External{{Path: "/m/F.ru.srt", Lang: "ru", Codec: "subrip", Title: "Notabenoid"}}, PlanOptions{})
	a, s = tr.Labels(BuildSet([]Tracks{tr}, PlanOptions{}), "ru")
	if got := str(a) + " " + str(s); got != "a0|LostFilm|Русский — LostFilm|ru a1|LostFilm (очищено)|Русский — LostFilm (очищено)|ru "+
		"a2|Кураж-Бамбей|Русский — Кураж-Бамбей (внешняя)|ru a3|Оригинал (English)|Оригинал (English)|en s0|Русские|Русские — Notabenoid|ru" {
		t.Fatalf("%s", got)
	}
	// the cleaned voice-over names its own voice-over's rendition (one voice, a switch in the players)
	if a[1].CleanedOf != "LostFilm" || a[0].CleanedOf != "" {
		t.Fatalf("cleaned of: %+v", a)
	}
	// a subtitle title that only names its language is not repeated
	tr = Plan(media.ProbeResult{Subs: []media.SubStream{{Index: 1, Codec: "subrip", Lang: "ukr", Title: "Українські"},
		{Index: 2, Codec: "subrip", Lang: "eng", Title: "English"}, {Index: 3, Codec: "subrip", Lang: "eng", Title: "English (SDH)"}}}, nil, nil, PlanOptions{})
	if _, s := tr.Labels(BuildSet([]Tracks{tr}, PlanOptions{}), "ru"); str(s) != "s0|Українська|Українська|uk s1|English|English|en s2|English 2|English — SDH|en" {
		t.Fatalf("%s", str(s))
	}
	// none: empty, not unknown
	if a, s := Plan(media.ProbeResult{}, nil, nil, o).Labels(set, "ru"); a == nil || s == nil || len(a)+len(s) != 0 {
		t.Fatal("labels of nothing")
	}
}
