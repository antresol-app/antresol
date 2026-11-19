package library

import (
	"context"
	"testing"
)

func TestBooksByISBN(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	hyphen, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: "/b/a/Dune.epub", Size: 1, Mtime: 1}}, Title: "Dune", Authors: []string{"Frank Herbert"}, ISBN: "978-0-441-17271-9"})
	if err != nil {
		t.Fatal(err)
	}
	ten, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: "/b/b/Emma.epub", Size: 1, Mtime: 1}}, Title: "Emma", ISBN: "0-14-143958-0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: "/b/c/Odd.epub", Size: 1, Mtime: 1}}, Title: "Odd", ISBN: "not an isbn"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: "/b/d/None.epub", Size: 1, Mtime: 1}}, Title: "None"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.BooksByISBN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got["9780441172719"]) != 1 || got["9780441172719"][0] != (BookRef{ID: hyphen, Kind: KindBook}) {
		t.Fatalf("hyphenated ISBN-13: %+v", got)
	}
	// the ISBN-10 of the library book is looked up by its ISBN-13
	if refs := got["9780141439587"]; len(refs) != 1 || refs[0].ID != ten {
		t.Fatalf("ISBN-10 as ISBN-13: %+v", got)
	}
}

func TestFindBooksIsStrict(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	add := func(path, title, series string, authors ...string) int64 {
		id, err := r.UpsertBook(ctx, BookInput{Files: []FileInput{{Path: path, Size: 1, Mtime: 1}}, Title: title, Series: series, Authors: authors})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	dune := add("/b/h/Dune.epub", "Dune", "", "Frank Herbert")
	messiah := add("/b/h/Dune Messiah.epub", "Dune Messiah", "Dune", "Frank Herbert")
	bare := add("/b/x/Solaris.epub", "Solaris", "")
	hobbit := add("/b/t/The Hobbit.epub", "The Hobbit", "", "J. R. R. Tolkien")
	master := add("/b/b/Master.epub", "Master", "", "Mikhail Bulgakov")
	russian := add("/b/b/Мастер.fb2", "Мастер и Маргарита", "", "Булгаков Михаил Афанасьевич")
	// the audiobook of a book is one more match
	audio, err := r.UpsertAudiobook(ctx, AudiobookInput{Dir: "/a/Dune", Files: []FileInput{{Path: "/a/Dune/01.mp3", Size: 1, Mtime: 1}}, Title: "Dune", Authors: []string{"Frank Herbert"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(refs []BookRef) []int64 {
		var out []int64
		for _, r := range refs {
			out = append(out, r.ID)
		}
		return out
	}
	for _, c := range []struct {
		author, title string
		want          []int64
	}{
		// the series «Dune» names Dune Messiah too, but only a book called Dune is Dune
		{"Frank Herbert", "Dune", []int64{dune, audio}},
		{"Frank Herbert", "dune!", []int64{dune, audio}},
		{"Frank Herbert", "Dune Messiah", []int64{messiah}},
		// an author of another name
		{"Isaac Asimov", "Dune", nil},
		// a book with no author on file: the title alone
		{"Stanislaw Lem", "Solaris", []int64{bare}},
		{"J.R.R. Tolkien", "The Hobbit", []int64{hobbit}},
		// the author's name in another script
		{"Михаил Булгаков", "Master", []int64{master}},
		{"Mikhail Bulgakov", "Мастер и Маргарита", []int64{russian}},
		{"Leo Tolstoy", "Мастер и Маргарита", nil},
		// the author's name is no title
		{"", "Frank Herbert", nil},
		{"", "", nil},
	} {
		got, err := r.FindBooks(ctx, c.author, c.title)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != len(c.want) || (len(g) > 0 && (g[0] != c.want[0] || (len(g) > 1 && g[1] != c.want[1]))) {
			t.Errorf("FindBooks(%q, %q) = %v, want %v", c.author, c.title, g, c.want)
		}
	}
	if got, _ := r.FindBooks(ctx, "Frank Herbert", "Dune"); len(got) == 2 && got[1].Kind != KindAudiobook {
		t.Errorf("kinds: %+v", got)
	}
}
