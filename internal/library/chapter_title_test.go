package library

import "testing"

func TestDisplayChapterTitle(t *testing.T) {
	for _, c := range []struct {
		in   string
		idx  int
		want string
	}{
		{"Part 2 Chapter 1", 3, "Часть 2 · Глава 1"},
		{"Part 1 - Chapter 02", 1, "Часть 1 · Глава 2"},
		{"Chapter 12", 11, "Глава 12"},
		{"chapter 001", 0, "Глава 1"},
		{"Ch. 7", 6, "Глава 7"},
		{"Part 3", 9, "Часть 3"},
		{"Track 05", 4, "Глава 5"},
		{"05", 4, "Глава 5"},
		{"", 4, "Глава 5"},
		{"  ", 0, "Глава 1"},
		{"Chapter 3: The Storm", 2, "Chapter 3: The Storm"},
		{"Пролог", 0, "Пролог"},
		{"Глава 4", 3, "Глава 4"},
		{"The Hobbit", 0, "The Hobbit"},
	} {
		if got := DisplayChapterTitle("ru", c.in, c.idx); got != c.want {
			t.Errorf("DisplayChapterTitle(%q, %d) = %q, want %q", c.in, c.idx, got, c.want)
		}
	}
}

func TestDisplayChapterTitleEnglish(t *testing.T) {
	for _, c := range []struct {
		in   string
		idx  int
		want string
	}{
		{"Part 2 Chapter 1", 3, "Part 2 · Chapter 1"},
		{"Chapter 12", 11, "Chapter 12"},
		{"Part 3", 9, "Part 3"},
		{"Track 05", 4, "Chapter 5"},
		{"", 0, "Chapter 1"},
		{"Пролог", 0, "Пролог"},
		{"The Hobbit", 0, "The Hobbit"},
	} {
		if got := DisplayChapterTitle("en", c.in, c.idx); got != c.want {
			t.Errorf("DisplayChapterTitle(en, %q, %d) = %q, want %q", c.in, c.idx, got, c.want)
		}
	}
}

// A title that is the name of a file says nothing to a listener: it is
// shown as the chapter's number.
func TestDisplayChapterTitleFileNames(t *testing.T) {
	for _, c := range []struct {
		in   string
		idx  int
		want string
	}{
		{"00_Starik_i_more", 0, "Глава 1"},
		{"Starik_i_more", 1, "Глава 2"},
		{"Chapter_07", 2, "Глава 3"},
		{"01-starik-i-more", 3, "Глава 4"},
		{"01.starik.i.more", 0, "Глава 1"},
		{"Старик и море.mp3", 4, "Глава 5"},
		{"part1.M4B", 0, "Глава 1"},
		{"ernest_hemingway - the_old_man", 5, "Глава 6"},
		// words of their own stay
		{"Self-Reliance", 0, "Self-Reliance"},
		{"20-летний юбилей", 0, "20-летний юбилей"},
		{"Ах_ эти дети", 0, "Ах_ эти дети"},
		{"3.14: история числа", 0, "3.14: история числа"},
		{"Mr. Smith goes to Washington", 0, "Mr. Smith goes to Washington"},
		{"1917-1918", 0, "1917-1918"},
	} {
		if got := DisplayChapterTitle("ru", c.in, c.idx); got != c.want {
			t.Errorf("DisplayChapterTitle(%q, %d) = %q, want %q", c.in, c.idx, got, c.want)
		}
	}
	if got := DisplayChapterTitle("en", "00_Starik_i_more", 0); got != "Chapter 1" {
		t.Errorf("en: %q", got)
	}
}

// RealChapterTitles: whether a book's own chapter titles tell the listener
// anything (else automatic chapters may replace them).
func TestRealChapterTitles(t *testing.T) {
	const book = "Старик и море"
	for _, c := range []struct {
		name   string
		titles []string
		stems  []string
		want   bool
	}{
		{"none", nil, nil, false},
		{"numbers", []string{"1", "02", "003"}, nil, false},
		{"numbered chapters", []string{"Глава 1", "Глава 2", "Часть 2 · Глава 3"}, nil, false},
		{"english generic", []string{"Chapter 1", "Part 2 - Chapter 02", "Track 03", "CD1 Track 4", "Chapter IV"}, nil, false},
		{"russian generic", []string{"Трек 01", "Файл 2", "Диск 1, дорожка 3", "Аудиокнига"}, nil, false},
		{"empty", []string{"", " "}, nil, false},
		{"file names", []string{"00_Starik_i_more", "01_Starik_i_more"}, nil, false},
		{"the book's name numbered", []string{"Старик и море 01", "Старик и море - 02", "Старик и море (часть 3)"}, nil, false},
		{"one name numbered", []string{"Starik i more 01", "Starik i more 02", "Starik i more 03"}, nil, false},
		{"one chapter, the book", []string{"Старик и море"}, []string{"Hemingway"}, false},
		{"one chapter, the file", []string{"Hemingway Starik"}, []string{"Hemingway Starik"}, false},
		{"one chapter, numbered file", []string{"Starik i more"}, []string{"01 - Starik i more"}, false},
		{"one chapter of its own", []string{"Пролог"}, []string{"book"}, true},
		{"real", []string{"Пролог", "Встреча", "Эпилог"}, []string{"01 - Пролог", "02 - Встреча", "03 - Эпилог"}, true},
		{"half real", []string{"Введение", "Глава 1"}, nil, true},
		{"mostly generic", []string{"Предисловие", "Глава 1", "Глава 2", "Глава 3"}, nil, false},
		{"numbered with names", []string{"Глава 1. Буря", "Глава 2. Берег"}, nil, true},
	} {
		if got := RealChapterTitles(book, c.titles, c.stems); got != c.want {
			t.Errorf("%s: RealChapterTitles(%q) = %v, want %v", c.name, c.titles, got, c.want)
		}
	}
}
