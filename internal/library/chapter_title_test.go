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
