package release

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

func TestStudiosInTitles(t *testing.T) {
	cases := map[string][]string{
		"Миллиарды / Billions [S03] (2018) WEB-DL 1080p | LostFilm":                   {"LostFilm"},
		"Офис / The Office (2005) WEB-DLRip | MVO (Кураж-Бамбей)":                     {"Кураж-Бамбей"},
		"Рик и Морти / Rick and Morty S07 WEBRip 1080p MVO (Кубик в Кубе, NewStudio)": {"Кубик в Кубе", "NewStudio"},
		"Бойцовский клуб / Fight Club (1999) BDRip Dub":                               nil,
		"Lost S01 1080p RHS":                     {"Red Head Sound"},
		"Something.S01.1080p.WEB-DL.NewStudio.TV": {"NewStudio"},
		// several studios, aliases and spellings; repeated names count once
		"Сериал (2020) WEB-DL 720p | Dub + MVO (кубик³, Lost Film, LostFilm, New Studio)": {"Кубик в Кубе", "LostFilm", "NewStudio"},
		"Сериал S02 1080p | Кураж Бамбей, ColdFilm, Пифагор":                                     {"Кураж-Бамбей", "Coldfilm", "Pifagor"},
		// word boundaries: no studio inside another word
		"Сериал S01 1080p rhsomething amediateka": nil,
	}
	for title, want := range cases {
		if got := Parse(title).Studios; !slices.Equal(got, want) {
			t.Errorf("%q: studios %v, want %v", title, got, want)
		}
	}
}

func TestStudioInsideTheTitleIsNotAStudio(t *testing.T) {
	for _, title := range []string{
		"Кубик в кубе / Cube (1997) BDRip 720p Dub",
		"Lost Film Festival (2010) BDRip 720p",
	} {
		if got := Parse(title).Studios; len(got) != 0 {
			t.Errorf("%q: the film's own title counted as a studio: %v", title, got)
		}
	}
	// the English alt title or a scene name's words before the quality are
	// part of the name, not studios
	for _, title := range []string{
		"Шоу (1 сезон) / TV Shows Story (2020)",
		"Amedia: история (1 сезон) / Amedia Story",
		"Шоу (1 сезон) / TV Shows Story (2020) WEB-DL 1080p",
		"Шоу / Amedia Story (2020) WEB-DL 1080p",
		"Шоу / TV Shows Story S01 1080p",
	} {
		if got := Parse(title).Studios; len(got) != 0 {
			t.Errorf("%q: the name counted as a studio: %v", title, got)
		}
		if tail := Tail(title); NamesStudio(tail, "Story") {
			t.Errorf("%q: Tail %q holds the name", title, tail)
		}
	}
	// Accepted risk: a scene name's episode title after SxxEyy is read like
	// the rest of the name, so «New.Studio.Tour» names NewStudio. Studios
	// before the quality (below) matter more.
	if got := Parse("Show.S02E03.New.Studio.Tour.1080p").Studios; !slices.Equal(got, []string{"NewStudio"}) {
		t.Errorf("episode-title case changed: %v (update this documented risk)", got)
	}
	// studios named before the quality, or without any: after season and
	// episode markers, «Studio.TV» anywhere, a «(Studio)» right after the alt title
	for title, want := range map[string]string{
		"Show.S02E03.NewStudio.TV":                              "NewStudio",
		"Шоу (1 сезон) / Show Story | NewStudio":                "NewStudio",
		"Шоу (1 сезон) / Show Story (Кураж-Бамбей)":             "Кураж-Бамбей",
		"Шоу (1 сезон) / TV Shows Story (2020) | RHS":           "Red Head Sound",
		"Something.S01E02.LostFilm.1080p.WEB-DL":                "LostFilm",
		"Billions.S03E01.Rus.AlexFilm.TV.1080p":                 "AlexFilm",
		"Миллиарды S03 (LostFilm) WEB-DL 1080p":                 "LostFilm",
		"Сериал / Show S01 Jaskier":                             "Jaskier",
		"Сериал / Show (Кубик в Кубе)":                          "Кубик в Кубе",
		"Сериал / Show 2x05 NewStudio":                          "NewStudio",
		"Сериал (Сезон 2) Кураж-Бамбей":                         "Кураж-Бамбей",
		"Сериал / Show / 2024 / ПМ (LostFilm) / WEB-DL (1080p)": "LostFilm",
	} {
		if got := Parse(title).Studios; !slices.Equal(got, []string{want}) {
			t.Errorf("%q: studios %v, want %s", title, got, want)
		}
		if !NamesStudio(Tail(title), want) && want != "Red Head Sound" {
			t.Errorf("%q: Tail %q misses %s", title, Tail(title), want)
		}
	}
	// a parenthesised year or note after a title is no studio
	if got := Parse("Сериал / Show (2020)").Studios; len(got) != 0 {
		t.Errorf("year in parens: %v", got)
	}
	if !NamesStudio(Tail("Шоу (1 сезон) / Show Story | Гоблин"), "Гоблин") {
		t.Error("a typed studio after '|' not in Tail")
	}
	// the title names a studio and the release another: only the release's
	if got := Parse("Кубик в кубе / Cube (1997) BDRip 720p MVO | LostFilm").Studios; !slices.Equal(got, []string{"LostFilm"}) {
		t.Errorf("studios %v", got)
	}
}

func TestVHSRipSource(t *testing.T) {
	if got := Parse("Фильм (1985) VHSRip MVO").Source; got != "VHSRip" {
		t.Fatalf("source %q", got)
	}
}

func TestStudios(t *testing.T) {
	if got := Studios("MVO (Jaskier, LostFilm) + Jaskier"); !slices.Equal(got, []string{"Jaskier", "LostFilm"}) {
		t.Errorf("%v", got)
	}
	if got := Studios(""); got != nil {
		t.Errorf("%v", got)
	}
}

func TestCanonicalStudio(t *testing.T) {
	for in, want := range map[string]string{
		"lostfilm": "LostFilm", "Кубик в кубе": "Кубик в Кубе", "KUBIK3": "Кубик в Кубе", "rhs": "Red Head Sound",
		"Лостфильм": "LostFilm", " Lost Film ": "LostFilm", "Пифагор": "Pifagor", "Кураж Бамбей": "Кураж-Бамбей",
		"New-Studio": "NewStudio", "яськьер": "Jaskier",
	} {
		if got, ok := CanonicalStudio(in); !ok || got != want {
			t.Errorf("%q → %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"Рога и копыта", "", "LostFilm Кубик в Кубе"} {
		if _, ok := CanonicalStudio(in); ok {
			t.Errorf("%q recognised", in)
		}
	}
}

func TestKnownStudios(t *testing.T) {
	want := []string{"LostFilm", "Кубик в Кубе", "NewStudio", "AlexFilm", "Jaskier", "TVShows",
		"Кураж-Бамбей", "Red Head Sound", "BaibaKo", "Pifagor", "Novamedia", "SDI Media", "Amedia", "Coldfilm", "IdeaFilm"}
	var core []string
	for _, s := range coreStudios {
		core = append(core, s.name)
	}
	if !slices.Equal(core, want) {
		t.Fatalf("%v", core)
	}
	// a build may add studios, never drop or reorder these
	if got := slices.DeleteFunc(KnownStudios(), func(s string) bool { return !slices.Contains(want, s) }); !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	for _, s := range want {
		if c, ok := CanonicalStudio(s); !ok || c != s {
			t.Errorf("%q is not its own canonical name (%q)", s, c)
		}
	}
}

// The web settings suggest a copy of KnownStudios.
func TestWebKnownStudiosMatch(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "components", "StudioList.tsx"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("no web sources")
	}
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)export const STUDIOS[^=]*= \[(.*?)\n\]`).FindSubmatch(src)
	if block == nil {
		t.Fatal("STUDIOS not found in StudioList.tsx")
	}
	quoted := regexp.MustCompile(`'([^']*)'`)
	var web []studio
	for _, e := range regexp.MustCompile(`\[\s*'([^']*)',\s*\[([^\]]*)\]\s*\]`).FindAllSubmatch(block[1], -1) {
		s := studio{name: string(e[1])}
		for _, a := range quoted.FindAllSubmatch(e[2], -1) {
			s.aliases = append(s.aliases, string(a[1]))
		}
		web = append(web, s)
	}
	if len(web) != len(coreStudios) {
		t.Fatalf("web STUDIOS has %d studios, release %d", len(web), len(coreStudios))
	}
	for i, s := range coreStudios {
		if web[i].name != s.name || !slices.Equal(web[i].aliases, s.aliases) {
			t.Errorf("web STUDIOS[%d] = %q %q, release %q %q", i, web[i].name, web[i].aliases, s.name, s.aliases)
		}
	}
}

func TestStudioKeyAndNamesStudio(t *testing.T) {
	if StudioKey(" lost film ") != "LostFilm" || StudioKey("Гоблин") != StudioKey("гоблин") || StudioKey("Ёжик") != StudioKey("ежик") {
		t.Error("StudioKey")
	}
	cases := []struct {
		s, name string
		want    bool
	}{
		{"S01 1080p | Гоблин", "Гоблин", true},
		{"S01 1080p Гоблинский", "Гоблин", false},
		{"MVO (Ёжик в тумане)", "ежик в тумане", true},
		{"", "Гоблин", false},
		{"anything", "  ", false},
	}
	for _, c := range cases {
		if got := NamesStudio(c.s, c.name); got != c.want {
			t.Errorf("NamesStudio(%q, %q) = %v", c.s, c.name, got)
		}
	}
	if got := Tail("Кубик в кубе / Cube (1997) BDRip | Гоблин"); !NamesStudio(got, "Гоблин") || NamesStudio(got, "Кубик") {
		t.Errorf("Tail %q", got)
	}
}

func TestMergeStudios(t *testing.T) {
	got := MergeStudios([]string{" lostfilm ", "LostFilm", ""}, []string{"Лостфильм", "Кубик в кубе", " Гоблин ", "гоблин"})
	want := []string{"LostFilm", "Кубик в Кубе", "Гоблин"}
	if !slices.Equal(got, want) {
		t.Errorf("MergeStudios = %q, want %q", got, want)
	}
}
