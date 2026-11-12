package channels

import (
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/release"
	"github.com/antresol-app/antresol/internal/subtitles"
)

func keysOf(t Tracks) []string {
	var out []string
	for _, a := range t.Audio {
		out = append(out, a.Key)
	}
	for _, s := range t.Subs {
		out = append(out, "sub:"+s.Key)
	}
	return out
}

func TestPlanKeysTwoRussianStudiosEnglishFrench(t *testing.T) {
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
		{Index: 9, Codec: "subrip", Lang: "eng", Title: "SDH"},
	}}
	kub, lf := "ru:"+release.StudioKey("Кубик в Кубе"), "ru:"+release.StudioKey("LostFilm")
	// no favourites: the Russian voices in the file's order, the original, the others
	tr := Plan(p, nil, nil, PlanOptions{})
	want := []string{kub, lf, "orig:fr", "en:", "sub:ru", "sub:en", "sub:en#2"}
	if got := keysOf(tr); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %q want %q", got, want)
	}
	if tr.Audio[0].Voice != "Кубик в Кубе" || !tr.Audio[2].Original || tr.Default.Index != 1 || tr.Subs[0].Pick.Index != 7 {
		t.Fatalf("%+v", tr)
	}
	// favourite studios order the Russian voices
	tr = Plan(p, nil, nil, PlanOptions{Studios: []string{"LostFilm", "Кубик в Кубе"}})
	if got := keysOf(tr); got[0] != lf || got[1] != kub || tr.Audio[0].fav != 0 || tr.Audio[1].fav != 1 || tr.Audio[2].fav != 2 {
		t.Fatalf("favourites %q %+v", got, tr.Audio)
	}
}

func TestPlanSameVoiceTwiceIsOneKeyUntitledTwiceIsTwo(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{
		{Index: 1, Lang: "rus", Title: "LostFilm 5.1", Default: true}, {Index: 2, Lang: "rus", Title: "LostFilm 2.0"},
		{Index: 3, Lang: "rus"}, {Index: 4, Lang: "rus", Title: "AC3 5.1"}, {Index: 5, Lang: "rus", Title: "Русский — Дубляж AC3 5.1"}}}
	tr := Plan(p, nil, nil, PlanOptions{})
	want := []string{"ru:" + release.StudioKey("LostFilm"), "ru:", "ru:#2", "ru:дубляж"}
	if got := keysOf(tr); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %q want %q", got, want)
	}
	if tr.Audio[0].Source.Index != 1 || tr.Audio[3].Voice != "Дубляж" {
		t.Fatalf("%+v", tr.Audio)
	}
}

func TestPlanExternalVoiceOverAndSubtitleFiles(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "eng", Default: true}}}
	ext := []media.ExternalAudio{{Path: "/m/Film.Kuraj-Bambey.mka", Studio: "Кураж-Бамбей", Stream: media.AudioStream{Lang: "ru", Title: "Кураж-Бамбей"}, OffsetMs: 40}}
	subs := []subtitles.External{{Path: "/m/F.ru.srt", Lang: "ru", Codec: "subrip"}, {Path: "/m/F.en.forced.srt", Lang: "en", Codec: "subrip", Forced: true}}
	tr := Plan(p, ext, subs, PlanOptions{})
	if got := keysOf(tr); !reflect.DeepEqual(got, []string{"ru:" + release.StudioKey("Кураж-Бамбей"), "orig:en", "sub:ru"}) {
		t.Fatalf("keys %q", got)
	}
	if a := tr.Audio[0]; !a.External || a.Source.External != "/m/Film.Kuraj-Bambey.mka" || a.Source.OffsetMs != 40 || tr.Default.Index != 1 {
		t.Fatalf("%+v", tr)
	}
	if tr.Subs[0].Pick.External != "/m/F.ru.srt" {
		t.Fatalf("subs %+v", tr.Subs)
	}
	// a favourite embedded voice is still before an unranked external one, the file's own before a file beside it
	p.Audio = append(p.Audio, media.AudioStream{Index: 2, Lang: "rus", Title: "Дубляж"})
	if tr := Plan(p, ext, nil, PlanOptions{}); tr.Audio[0].Source.Index != 2 || tr.Audio[1].Source.External == "" {
		t.Fatalf("own first %+v", tr.Audio)
	}
	if tr := Plan(p, ext, nil, PlanOptions{Studios: []string{"Кураж-Бамбей"}}); tr.Audio[0].Source.External == "" {
		t.Fatalf("favourite external %+v", tr.Audio)
	}
}

func TestPlanCleanedIsItsOwnKey(t *testing.T) {
	p := media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "eng", Default: true}, {Index: 2, Lang: "rus", Title: "LostFilm"}}}
	ext := []media.ExternalAudio{{Path: "/d/clean/2.mka", Studio: "LostFilm", Stream: media.AudioStream{Lang: "ru", Title: "LostFilm"},
		Kind: media.KindCleaned, CleanedOf: 2, OffsetMs: -12}}
	lf := "ru:" + release.StudioKey("LostFilm")
	tr := Plan(p, ext, nil, PlanOptions{})
	if got := keysOf(tr); !reflect.DeepEqual(got, []string{lf, lf + cleanSuffix, "orig:en"}) {
		t.Fatalf("keys %q", got)
	}
	if c := tr.Audio[1]; !c.Cleaned || c.Source.External != "/d/clean/2.mka" || c.Source.OffsetMs != -12 || c.Voice != "LostFilm" {
		t.Fatalf("cleaned %+v", c)
	}
	// preferred: before its voice
	tr = Plan(p, ext, nil, PlanOptions{PreferCleaned: true})
	if got := keysOf(tr); got[0] != lf+cleanSuffix || got[1] != lf {
		t.Fatalf("preferred %q", got)
	}
}

func TestPlanNoAudioUntaggedAndOnlyEnglish(t *testing.T) {
	tr := Plan(media.ProbeResult{}, nil, nil, PlanOptions{})
	if tr.Default != silence || len(tr.Audio) != 0 {
		t.Fatalf("no audio %+v", tr)
	}
	tr = Plan(media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Lang: "eng"}, {Index: 2, Lang: "eng", Title: "Commentary", Default: true}}}, nil, nil, PlanOptions{})
	if got := keysOf(tr); !reflect.DeepEqual(got, []string{"orig:en", "en:commentary"}) || tr.Default.Index != 2 {
		t.Fatalf("english %q %+v", got, tr.Default)
	}
	// an untagged Cyrillic title is Russian, an untagged other track the original
	tr = Plan(media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Title: "Дубляж"}, {Index: 2, Lang: "und"}, {Index: 3, Lang: "hun"}}}, nil, nil, PlanOptions{})
	if got := keysOf(tr); !reflect.DeepEqual(got, []string{"ru:дубляж", "orig:", "hu:"}) {
		t.Fatalf("untagged %q", got)
	}
}

// AAC-LC of ≤ 2 channels at 48 kHz is copied; anything else (another
// rate, HE-AAC, 5.1, an unknown profile, a shifted file beside the video)
// is encoded.
func TestPlanCopiesOnlyAACLCStereo48k(t *testing.T) {
	a := func(codec, profile string, ch, rate int) media.AudioStream {
		return media.AudioStream{Codec: codec, Profile: profile, Channels: ch, SampleRate: rate, Lang: "rus"}
	}
	for _, c := range []struct {
		s    media.AudioStream
		copy bool
	}{
		{a("aac", "LC", 2, 48000), true}, {a("aac", "LC", 1, 48000), true}, {a("aac", "LC", 2, 44100), false},
		{a("aac", "HE-AAC", 2, 48000), false}, {a("aac", "LC", 6, 48000), false}, {a("aac", "", 2, 48000), false},
		{a("ac3", "", 2, 48000), false}, {a("eac3", "LC", 2, 48000), false},
	} {
		if got := copyable(c.s); got != c.copy {
			t.Errorf("%+v: copy %v", c.s, got)
		}
		tr := Plan(media.ProbeResult{Audio: []media.AudioStream{c.s}}, nil, nil, PlanOptions{})
		if tr.Audio[0].Source.Copy != c.copy || tr.Default.Copy != c.copy {
			t.Errorf("%+v: plan copy %+v", c.s, tr.Audio[0].Source)
		}
	}
	lc := media.AudioStream{Codec: "aac", Profile: "LC", Channels: 2, SampleRate: 48000, Lang: "ru"}
	for _, c := range []struct {
		e    media.ExternalAudio
		copy bool
	}{
		{media.ExternalAudio{Path: "/m/a.mka", Studio: "LostFilm", Stream: lc}, true},
		{media.ExternalAudio{Path: "/m/a.mka", Studio: "LostFilm", Stream: lc, OffsetMs: 40}, false},
		{media.ExternalAudio{Path: "/m/a.mka", Studio: "LostFilm", Stream: lc, Tempo: 1.04}, false},
	} {
		if tr := Plan(media.ProbeResult{}, []media.ExternalAudio{c.e}, nil, PlanOptions{}); tr.Audio[0].Source.Copy != c.copy {
			t.Errorf("%+v: copy %v", c.e, tr.Audio[0].Source.Copy)
		}
	}
}

func TestVoiceTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Русский — Дубляж AC3 5.1": "Дубляж", "Rus: MVO": "Многоголосый", "AVO - Дмитрий Пучков": "Дмитрий Пучков", "DVO - Евгений Пашин и Катерина Двоеглазова": "Евгений Пашин и Катерина Двоеглазова",
		"Dub": "Дубляж", "AC3 5.1": "", "Original": "", "Commentary (director)": "Commentary director",
		"DD 5.1 640 kbps": "", "6ch": "",
	} {
		if got := voiceTitle(in); got != want {
			t.Errorf("voiceTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
