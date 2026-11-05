package release

import (
	"slices"
	"testing"
)

func TestParseAudio(t *testing.T) {
	for _, c := range []struct {
		name string
		want AudioInfo
	}{
		{"Кино - Группа крови - 1988, FLAC (tracks+.cue), lossless", AudioInfo{Format: "FLAC", Lossless: true, Year: 1988}},
		{"Ария - Дискография (1985-2014) [MP3, 320 kbps]", AudioInfo{Format: "MP3", Bitrate: 320, Discography: true}},
		{"(Rock) Pink Floyd - The Wall - 1979 (2011 Remaster), FLAC (image+.cue)", AudioInfo{Format: "FLAC", Lossless: true, Image: true, Year: 1979, Reissue: true}},
		{"Пелевин - Чапаев и Пустота [Клюквин, 2010, 128 kbps, MP3]", AudioInfo{Format: "MP3", Bitrate: 128, Year: 2010}},
		{"Лем - Солярис (2019) M4B", AudioInfo{Format: "M4B", Year: 2019}},
		{"Queen - Greatest Hits Collection [ALAC]", AudioInfo{Format: "ALAC", Lossless: true}},
		{"Кино - Группа крови (Коллекционное издание) - 1988, FLAC", AudioInfo{Format: "FLAC", Lossless: true, Year: 1988, Reissue: true}},
		{"Metallica - Collector's Edition (1991) [FLAC]", AudioInfo{Format: "FLAC", Lossless: true, Year: 1991, Reissue: true}},
		{"Pink Floyd - Box Set (1992) [FLAC]", AudioInfo{Format: "FLAC", Lossless: true, Discography: true, Year: 1992, Reissue: true}},
		{"Ария - Коллекция альбомов [MP3]", AudioInfo{Format: "MP3", Discography: true}},
		{"Artist - Album (1999) [WavPack]", AudioInfo{Format: "WV", Lossless: true, Year: 1999}},
		{"Radiohead - OK Computer (1997) [MP3 V0]", AudioInfo{Format: "MP3", VBR: "V0", Year: 1997}},
		{"Radiohead - OK Computer (1997) [MP3, VBR V2]", AudioInfo{Format: "MP3", VBR: "V2", Year: 1997}},
		{"Radiohead - OK Computer (1997) [MP3, 320]", AudioInfo{Format: "MP3", Bitrate: 320, Year: 1997}},
		{"Radiohead - OK Computer (1997) [MP3@256kbps]", AudioInfo{Format: "MP3", Bitrate: 256, Year: 1997}},
	} {
		if got := ParseAudio(c.name); got != c.want {
			t.Errorf("ParseAudio(%q) = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestAudioLabel(t *testing.T) {
	for name, want := range map[string]string{
		"A - B (1997) [FLAC]":          "FLAC",
		"A - B (1997) [MP3 V0]":        "MP3 V0",
		"A - B (1997) [MP3, 320 kbps]": "MP3 320",
		"A - B (1997) [MP3]":           "MP3",
		"A - B (1997) lossless":        "Lossless",
		"A - B (1997)":                 "",
		"A - 1997 - B 320Kbps":         "MP3 320",
	} {
		if got := ParseAudio(name).Label(); got != want {
			t.Errorf("Label(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestParseBook(t *testing.T) {
	b := ParseBook("Булгаков М. - Мастер и Маргарита [1967, FB2, EPUB, RUS]")
	if !slices.Equal(b.Formats, []string{"FB2", "EPUB"}) || b.Lang != "ru" || b.Collection {
		t.Errorf("single book: %+v", b)
	}
	b = ParseBook("Толстой Л.Н. - Собрание сочинений в 10 томах [1958, DJVU]")
	if !b.Collection || !slices.Equal(b.Formats, []string{"DJVU"}) {
		t.Errorf("collection: %+v", b)
	}
	b = ParseBook("Berserk v01-v41 [ENG] (CBZ, CBR)")
	if !slices.Equal(b.Formats, []string{"CBZ", "CBR"}) || b.Lang != "en" {
		t.Errorf("manga: %+v", b)
	}
	if b := ParseBook("Berserk [JAP] RAW (CBZ)"); b.Lang != "raw" {
		t.Errorf("raw: %+v", b)
	}
	if b := ParseBook("Берсерк (Русский) [CBZ]"); b.Lang != "ru" {
		t.Errorf("русский: %+v", b)
	}
	if b := ParseBook("Мастер и Маргарита (Коллекционное издание) [EPUB]"); b.Collection {
		t.Errorf("collector's edition is a collection: %+v", b)
	}
	if b := ParseBook("Мастер [DOC, RTF, TXT]"); !slices.Equal(b.Formats, []string{"DOC", "RTF", "TXT"}) {
		t.Errorf("office: %+v", b)
	}
	b = ParseBook("Ван Пис / One Piece [JAP] PDF")
	if !slices.Equal(b.Formats, []string{"PDF"}) || b.Lang != "ja" {
		t.Errorf("manga jap: %+v", b)
	}
	if b := ParseBook("Pratchett - Mort (1987) [MOBI, AZW3]"); !slices.Equal(b.Formats, []string{"MOBI", "AZW3"}) || b.Lang != "" {
		t.Errorf("kindle: %+v", b)
	}
}

func TestFoldMarks(t *testing.T) {
	for in, want := range map[string]string{
		"JAŸ-Z":                         "JAY-Z",
		"Måneskin":                      "Maneskin",
		"The Rubáiyát of Dorothy Ashby": "The Rubaiyat of Dorothy Ashby",
		"Løwenzahnhonig, Æon, Straße":   "Lowenzahnhonig, aeon, Strasse",
		"Чёрный кофе, Мой":              "Чёрный кофе, Мой", // Cyrillic letters stay
		"VØNTAK":                        "VoNTAK",
		"plain":                         "plain",
	} {
		if got := FoldMarks(in); got != want {
			t.Errorf("FoldMarks(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFoldLatin(t *testing.T) {
	for in, want := range map[string]string{
		"Группа крови":                      "gruppa krovi",
		"Parsley, Sage, Rosemary & Thyme":   "parsley sage rosemary thyme",
		"Parsley, Sage, Rosemary and Thyme": "parsley sage rosemary thyme",
		"Sgt. Pepper's Lonely Hearts":       "sgt peppers lonely hearts",
		"Ágætis byrjun":                     "agaetis byrjun",
	} {
		if got := FoldLatin(in); got != want {
			t.Errorf("FoldLatin(%q) = %q, want %q", in, got, want)
		}
	}
	if got := UsefulAliases([]string{"live", " The Black Album ", "Compilation", "the black album", ""}); len(got) != 1 || got[0] != "The Black Album" {
		t.Errorf("aliases %q", got)
	}
	for in, want := range map[string]string{"ii": "2", "IV": "", "4": "4", "x": "10", "word": ""} {
		if got, _ := Numeral(in); got != want {
			t.Errorf("Numeral(%q) = %q", in, got)
		}
	}
}
