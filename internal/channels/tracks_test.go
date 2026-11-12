package channels

import (
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/subtitles"
)

func labels(t Tracks, lang string) []string {
	a, s := t.Labels(lang)
	var out []string
	for _, l := range a {
		out = append(out, l.Slot+"="+l.Label+"/"+l.Lang)
	}
	for _, l := range s {
		out = append(out, "sub:"+l.Slot+"="+l.Label)
	}
	return out
}

func TestPlanTwoRussianStudiosEnglishFrench(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{
		{Index: 1, Lang: "fre", Title: "Original", Default: true},
		{Index: 2, Lang: "rus", Title: "MVO Кубик в Кубе"},
		{Index: 3, Lang: "rus", Title: "LostFilm"},
		{Index: 4, Lang: "eng", Title: "English"},
	}, Subs: []media.SubStream{
		{Index: 5, Codec: "hdmv_pgs_subtitle", Lang: "rus"},
		{Index: 6, Codec: "subrip", Lang: "rus", Forced: true},
		{Index: 7, Codec: "subrip", Lang: "rus", Title: "Full"},
		{Index: 8, Codec: "ass", Lang: "eng"},
	}}
	// no favourites: the first Russian track (none is default), then the next voice
	tr := Plan(p, nil, nil, PlanOptions{})
	if tr.Audio[slotRU].Source.Index != 2 || tr.Audio[slotRU2].Source.Index != 3 || tr.Audio[slotEN].Source.Index != 4 || tr.Audio[slotOrig].Source.Index != 1 {
		t.Fatalf("%+v", tr)
	}
	if tr.Subs[subRU].Index != 7 || tr.Subs[subEN].Index != 8 || tr.Fallback.Index != 2 {
		t.Fatalf("subs %+v %+v fallback %+v", tr.Subs[0], tr.Subs[1], tr.Fallback)
	}
	// favourite studios decide
	tr = Plan(p, nil, nil, PlanOptions{Studios: []string{"LostFilm", "Кубик в Кубе"}})
	if tr.Audio[slotRU].Source.Index != 3 || tr.Audio[slotRU2].Source.Index != 2 || tr.Fallback.Index != 3 {
		t.Fatalf("favourites %+v", tr)
	}
	want := []string{"ru=Русский — LostFilm/ru", "ru2=Русский — Кубик в Кубе/ru", "en=English/en", "orig=Оригинал (fr)/fr",
		"sub:ru=Русские", "sub:en=English"}
	if got := labels(tr, "ru"); !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %q", got)
	}
	want = []string{"ru=Russian — LostFilm/ru", "ru2=Russian — Кубик в Кубе/ru", "en=English/en", "orig=Original (fr)/fr",
		"sub:ru=Russian", "sub:en=English"}
	if got := labels(tr, "en"); !reflect.DeepEqual(got, want) {
		t.Fatalf("en labels %q", got)
	}
}

func TestPlanSameVoiceTwiceIsNoSecondRussian(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{
		{Index: 1, Lang: "rus", Title: "LostFilm 5.1", Default: true}, {Index: 2, Lang: "rus", Title: "LostFilm 2.0"}}}
	tr := Plan(p, nil, nil, PlanOptions{})
	if tr.Audio[slotRU].Source.Index != 1 || tr.Audio[slotRU2] != nil || tr.Audio[slotEN] != nil || tr.Audio[slotOrig] != nil {
		t.Fatalf("%+v", tr)
	}
}

func TestPlanExternalVoiceOver(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "eng", Default: true}}}
	ext := []media.ExternalAudio{{Path: "/m/Film.Kuraj-Bambey.mka", Studio: "Кураж-Бамбей", Stream: media.AudioStream{Lang: "ru", Title: "Кураж-Бамбей"}, OffsetMs: 40}}
	tr := Plan(p, ext, nil, PlanOptions{})
	ru := tr.Audio[slotRU]
	if ru == nil || ru.Source.External != "/m/Film.Kuraj-Bambey.mka" || ru.Source.OffsetMs != 40 || tr.Audio[slotEN].Source.Index != 1 ||
		tr.Audio[slotOrig].Source.Index != 1 || tr.Fallback.External == "" {
		t.Fatalf("%+v", tr)
	}
	if got := labels(tr, "ru"); got[0] != "ru=Русский — Кураж-Бамбей (внешняя)/ru" || got[2] != "orig=Оригинал (en)/en" {
		t.Fatalf("labels %q", got)
	}
	// a favourite embedded voice is still before an unranked external one, the file's own before a file beside it
	p.Audio = append(p.Audio, media.AudioStream{Index: 2, Lang: "rus", Title: "Дубляж"})
	if tr := Plan(p, ext, nil, PlanOptions{}); tr.Audio[slotRU].Source.Index != 2 || tr.Audio[slotRU2].Source.External == "" {
		t.Fatalf("own first %+v", tr)
	}
	if tr := Plan(p, ext, nil, PlanOptions{Studios: []string{"Кураж-Бамбей"}}); tr.Audio[slotRU].Source.External == "" {
		t.Fatalf("favourite external %+v", tr)
	}
}

func TestPlanCleaned(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "eng", Default: true}, {Index: 2, Lang: "rus", Title: "LostFilm"}}}
	ext := []media.ExternalAudio{{Path: "/d/clean/2.mka", Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru", Title: "LostFilm"},
		Kind: media.KindCleaned, CleanedOf: 2, OffsetMs: -12}}
	tr := Plan(p, ext, nil, PlanOptions{})
	if tr.Audio[slotRU].Source.Index != 2 || tr.Audio[slotRU].Cleaned || tr.Audio[slotRU2] != nil {
		t.Fatalf("not preferred %+v", tr.Audio[slotRU])
	}
	tr = Plan(p, ext, nil, PlanOptions{PreferCleaned: true})
	if ru := tr.Audio[slotRU]; !ru.Cleaned || ru.Source.External != "/d/clean/2.mka" || ru.Source.OffsetMs != -12 || tr.Fallback.External == "" {
		t.Fatalf("preferred %+v", ru)
	}
	if got := labels(tr, "ru")[0]; got != "ru=Русский — LostFilm (очищено)/ru" {
		t.Fatal(got)
	}
	if got := labels(tr, "en")[0]; got != "ru=Russian — LostFilm (cleaned)/ru" {
		t.Fatal(got)
	}
}

func TestPlanNoAudioAndOnlyEnglish(t *testing.T) {
	tr := Plan(media.ProbeResult{}, nil, nil, PlanOptions{})
	if tr.Fallback != silence || tr.Audio != [4]*AudioPick{} {
		t.Fatalf("no audio %+v", tr)
	}
	if a, s := tr.Labels("ru"); a == nil || s == nil || len(a)+len(s) != 0 {
		t.Fatal("labels of nothing are empty, not unknown")
	}
	tr = Plan(media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "eng"}, {Index: 2, Lang: "eng", Title: "Commentary", Default: true}}},
		nil, []subtitles.External{{Path: "/m/F.ru.srt", Lang: "ru", Codec: "subrip"}, {Path: "/m/F.en.forced.srt", Lang: "en", Codec: "subrip", Forced: true}}, PlanOptions{})
	if tr.Audio[slotRU] != nil || tr.Audio[slotEN].Source.Index != 2 || tr.Audio[slotOrig].Source.Index != 1 || tr.Fallback.Index != 2 {
		t.Fatalf("english %+v", tr)
	}
	if tr.Subs[subRU] == nil || tr.Subs[subRU].External != "/m/F.ru.srt" || tr.Subs[subEN] != nil {
		t.Fatalf("subs %+v", tr.Subs)
	}
	// the language at the start of a title is not said twice
	tr = Plan(media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "rus", Title: "Русский — Дубляж"}, {Index: 2, Lang: "rus", Title: "Русский"}}}, nil, nil, PlanOptions{})
	if got := labels(tr, "ru"); got[0] != "ru=Русский — Дубляж/ru" || got[1] != "ru2=Русский/ru" {
		t.Fatalf("%q", got)
	}
	// an untagged Cyrillic title is Russian
	tr = Plan(media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Title: "Дубляж"}, {Index: 2, Lang: "und"}}}, nil, nil, PlanOptions{})
	if tr.Audio[slotRU].Source.Index != 1 || tr.Audio[slotOrig].Source.Index != 2 {
		t.Fatalf("untagged %+v", tr)
	}
	if got := labels(tr, "ru"); got[0] != "ru=Русский — Дубляж/ru" || got[1] != "orig=Оригинал/und" {
		t.Fatalf("%q", got)
	}
}
