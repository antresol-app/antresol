package library

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/release"
)

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		q, title string
		ok       bool
		score    int
	}{
		{"Джентельмены", "Джентльмены", true, 1},       // an extra letter
		{"Джентельмены", "Джентльмены удачи", true, 1}, // one word of the title
		{"Гари Потер", "Гарри Поттер и узник Азкабана", true, 2},
		{"Interstelar", "Interstellar", true, 1},
		{"Gentelmen", "The Gentlemen", true, 1}, // swapped neighbours
		{"Джентльм", "Джентльмены", true, 0},    // still typing
		{"Джентельм", "Джентльмены", true, 2},
		{"Ёжик", "ежик в тумане", true, 0},
		{"кот", "пот", false, 0}, // short words: exact only
		{"Мадагаскар", "Мадрид", false, 0},
		{"Гари Потер", "Гарри", false, 0}, // every word must match
	}
	for _, c := range cases {
		q := strings.Fields(release.Normalize(c.q))
		s, ok := fuzzyScore(q, release.Normalize(c.title))
		if ok != c.ok || (ok && s != c.score) {
			t.Errorf("fuzzyScore(%q, %q) = %d %v, want %d %v", c.q, c.title, s, ok, c.score, c.ok)
		}
	}
}

func TestSearchTypoTolerant(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	mustMovie := func(title, orig string, path string) int64 {
		id, err := r.UpsertMovie(ctx, MovieInput{Title: title, OriginalTitle: orig, File: FileInput{Path: path}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	gent := mustMovie("The Gentlemen", "The Gentlemen", "/m/g.mkv")
	if err := r.AddTitles(ctx, gent, []string{"Джентльмены"}); err != nil {
		t.Fatal(err)
	}
	mustMovie("Джентльмены удачи", "", "/m/u.mkv")
	mustMovie("Мадагаскар", "", "/m/m.mkv")

	got, err := r.Search(ctx, "Джентельмены", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != gent && got[1].ID != gent {
		t.Fatalf("misspelt Russian title: %v", titles(got))
	}
	if got, _ := r.Search(ctx, "Gentelmen", 10); len(got) != 1 || got[0].ID != gent {
		t.Fatalf("misspelt English title: %v", titles(got))
	}
	// an exact hit keeps the exact answer: no typo neighbours mixed in
	if got, _ := r.Search(ctx, "Джентльмены удачи", 10); len(got) != 1 {
		t.Fatalf("exact title: %v", titles(got))
	}
	if got, _ := r.Search(ctx, "Мадрид", 10); len(got) != 0 {
		t.Fatalf("unrelated word: %v", titles(got))
	}
}

// The typo pass reads the titles once and compares in Go: a library of
// thousands of titles still answers fast.
func TestSearchTypoTolerantSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	r := newRepo(t)
	ctx := context.Background()
	words := []string{"Ночной", "Дозор", "Поттер", "Гарри", "Джентльмены", "Москва", "Слезам", "Небо", "Звёзды", "Interstellar", "Gentlemen", "Planet", "Earth"}
	for i := range 3000 {
		title := fmt.Sprintf("%s %s %d", words[i%len(words)], words[(i*7+3)%len(words)], i)
		if _, err := r.UpsertMovie(ctx, MovieInput{Title: title, File: FileInput{Path: fmt.Sprintf("/m/%d.mkv", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	got, err := r.Search(ctx, "Джентельмены", 50)
	if err != nil || len(got) == 0 {
		t.Fatalf("%v %v", titles(got), err)
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("typo pass over 3000 titles took %v", d)
	}
}
