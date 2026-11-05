package release

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Info
	}{
		{"brat", "Брат 2 (2000) BDRip 1080p.mkv", Info{Title: "Брат 2", Year: 2000, Resolution: 1080, Source: "BDRip"}},
		{"shogun", "Сёгун.S01E03.WEB-DL.1080p.mkv", Info{Title: "Сёгун", Season: 1, Episode: 3, Source: "WEB-DL", Resolution: 1080}},
		{"dune", "Дюна: Часть вторая / Dune: Part Two (2024) WEB-DL 2160p HDR | Дубляж",
			Info{Title: "Дюна: Часть вторая", AltTitle: "Dune: Part Two", Year: 2024, Resolution: 2160, Source: "WEB-DL", HDR: true, Audio: []string{"Dub"}}},
		{"bear", "The.Bear.S03.1080p.WEB-DL.x265-GRP",
			Info{Title: "The Bear", Season: 3, IsSeasonPack: true, Resolution: 1080, Source: "WEB-DL", Codec: "x265", Group: "GRP"}},
		{"cam", "Оппенгеймер (2023) TS", Info{Title: "Оппенгеймер", Year: 2023, Source: "TS", IsCam: true}},
		{"range", "Show.Name.S02E05-E06.720p", Info{Title: "Show Name", Season: 2, Episode: 5, EpisodeEnd: 6, Resolution: 720}},
		{"prefix", "[Tracker.info] Фильм без года.avi", Info{Title: "Фильм без года"}},
		{"tracker brackets", "Дюна / Dune [2024, США, фантастика, WEB-DL 1080p] Dub + Original",
			Info{Title: "Дюна", AltTitle: "Dune", Year: 2024, Resolution: 1080, Source: "WEB-DL", Audio: []string{"Dub", "Original"}}},
		{"ru season", "Ведьмак (Сезон 2) WEB-DL 720p MVO", Info{Title: "Ведьмак", Season: 2, IsSeasonPack: true, Resolution: 720, Source: "WEB-DL", Audio: []string{"MVO"}}},
		{"ru season suffix", "Чернобыль 1 сезон 1080p Многоголосый Оригинал", Info{Title: "Чернобыль", Season: 1, IsSeasonPack: true, Resolution: 1080, Audio: []string{"MVO", "Original"}}},
		{"ru episode", "Сериал Сезон 1 Серия 3 HDTV", Info{Title: "Сериал", Season: 1, Episode: 3, Source: "HDTV"}},
		{"NxNN", "Friends 1x03 720p", Info{Title: "Friends", Season: 1, Episode: 3, Resolution: 720}},
		{"avo hevc", "Матрица (1999) BDRemux 2160p HEVC HDR10 | AVO (Гаврилов)", Info{Title: "Матрица", Year: 1999, Resolution: 2160, Source: "Remux", Codec: "HEVC", HDR: true, Audio: []string{"AVO"}}},
		{"hyphen title", "Spider-Man.2002.1080p.BluRay.x264-GRP", Info{Title: "Spider-Man", Year: 2002, Resolution: 1080, Source: "BluRay", Codec: "x264", Group: "GRP"}},
		{"year in title", "Blade Runner 2049 (2017) 1080p", Info{Title: "Blade Runner 2049", Year: 2017, Resolution: 1080}},
		{"postfix episode", "Игра престолов 2 сезон 3 серия 720p", Info{Title: "Игра престолов", Season: 2, Episode: 3, Resolution: 720}},
		{"resolution not episode", "Сериал Серия 720p", Info{Title: "Сериал Серия", Resolution: 720}},
		{"bracketed pack", "Сёгун / Shōgun / Сезон: 1 / Серии: 1-10 из 10 (Джонатан ван Тюлекен) [2024, США, драма, WEB-DL 1080p] MVO (NewStudio) + Original",
			Info{Title: "Сёгун", AltTitle: "Shōgun", Year: 2024, Season: 1, Episode: 1, EpisodeEnd: 10, IsSeasonPack: true, Resolution: 1080, Source: "WEB-DL", Audio: []string{"MVO", "Original"}, Studios: []string{"NewStudio"}}},
		{"rick pack", "Рик и Морти (Сезон 7, серии 1-10)", Info{Title: "Рик и Морти", Season: 7, Episode: 1, EpisodeEnd: 10, IsSeasonPack: true}},
		{"partial range", "Сёгун / Shōgun / Сезон: 1 / Серии: 1-4 из 10 [2024] WEB-DL 1080p", Info{Title: "Сёгун", AltTitle: "Shōgun", Year: 2024, Season: 1, Episode: 1, EpisodeEnd: 4, Resolution: 1080, Source: "WEB-DL"}},
		{"piped pack", "Сёгун (1 сезон: 1-10 серии из 10) / Shōgun (2024) WEB-DL 1080p | LostFilm",
			Info{Title: "Сёгун", Year: 2024, Season: 1, Episode: 1, EpisodeEnd: 10, IsSeasonPack: true, Resolution: 1080, Source: "WEB-DL", Studios: []string{"LostFilm"}}},
		{"partial season", "Фоллаут (2 сезон: 1-3 серии из 8) WEB-DL 1080p", Info{Title: "Фоллаут", Season: 2, Episode: 1, EpisodeEnd: 3, Resolution: 1080, Source: "WEB-DL"}},
		{"slashed dub", "Дюна: Часть вторая / Dune: Part Two / 2024 / ДБ / WEB-DLRip (1080p)",
			Info{Title: "Дюна: Часть вторая", AltTitle: "Dune: Part Two", Year: 2024, Resolution: 1080, Source: "WEBRip", Audio: []string{"Dub"}}},
		{"slashed mvo", "Фоллаут (1 сезон: 1-8 серии из 8) / Fallout / 2024 / ПМ, СТ / WEB-DL (1080p)",
			Info{Title: "Фоллаут", Year: 2024, Season: 1, Episode: 1, EpisodeEnd: 8, IsSeasonPack: true, Resolution: 1080, Source: "WEB-DL", Audio: []string{"MVO"}}},
		{"double episode", "Show.S01E01E02.720p", Info{Title: "Show", Season: 1, Episode: 1, EpisodeEnd: 2, Resolution: 720}},
		{"empty", "", Info{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Parse(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Parse(%q)\n got: %+v\nwant: %+v", c.in, got, c.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Сёгун: Часть 1!":  "сегун часть 1",
		"  The   Bear. ":   "the bear",
		"":                 "",
		"Ёлки-палки, 2!!!": "елки палки 2",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"Брат 2 (2000) BDRip 1080p.mkv", "[", "]", "[]", "()", "S01E", "1x", "\xff\xfe", "Сезон", "[2024,", "-", ".mkv",
		"The.Bear.S03.1080p.WEB-DL.x265-GRP", "Дюна / [2024, WEB-DL 1080p] Dub + Original", "S99E999-E9999999999999999999",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_ = Parse(s)
		_ = Normalize(s)
	})
}

func TestAudioNilWhenEmpty(t *testing.T) {
	if a := Parse("Брат 2 (2000) BDRip 1080p.mkv").Audio; a != nil {
		t.Errorf("Audio = %#v, want nil", a)
	}
}

func TestParseSubs(t *testing.T) {
	cases := []struct {
		title string
		want  []string
	}{
		{"Dune Part Two 2024 2160p WEB-DL Dub + Sub Rus, Eng", []string{"ru", "en"}},
		{"Дюна 2024 BDRip 1080p Субтитры: RUS/ENG/UKR", []string{"ru", "en", "uk"}},
		{"Movie.2023.1080p.WEB-DL.Rus.Sub", []string{"ru"}},
		{"Movie 2023 1080p Eng Subs", []string{"en"}},
		{"Movie 2023 1080p Subs", []string{"sub"}},
		{"Movie 2023 1080p Сабы", []string{"sub"}},
		{"Submarine 2010 1080p", nil},
		{"Subway Surfers 2019", nil},
		{"Субботник 2020 WEBRip", nil},
		{"The Substance 2024 2160p", nil},
	}
	for _, c := range cases {
		if got := Parse(c.title).Subs; !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q).Subs = %#v, want %#v", c.title, got, c.want)
		}
	}
}
