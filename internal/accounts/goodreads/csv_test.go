package goodreads

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// an export as Goodreads writes it: ISBNs as ="…", dates as 2023/05/14, a
// review with a comma, a quote and a line break in it, and every other
// column the importer ignores
const exportCSV = `Book Id,Title,Author,Author l-f,Additional Authors,ISBN,ISBN13,My Rating,Average Rating,Publisher,Binding,Number of Pages,Year Published,Original Publication Year,Date Read,Date Added,Bookshelves,Bookshelves with positions,Exclusive Shelf,My Review,Spoiler,Private Notes,Read Count,Owned Copies
1,"Dune (Dune, #1)",Frank Herbert,"Herbert, Frank",,"=""0441172717""","=""9780441172719""",5,4.27,Ace,Paperback,604,1990,1965,2023/05/14,2023/01/02,favorites,favorites (#1),read,"A ""classic"", yes,
really.",,,1,0
2,Emma,Jane Austen,"Austen, Jane",,"=""0141439580""","=""""",0,3.98,Penguin,Paperback,474,2003,1815,,2024/02/10,,,to-read,,,,0,0
3,Solaris,Stanislaw Lem,"Lem, Stanislaw",Joanna Kilmartin,"=""""","=""""",0,4.1,,,204,1970,1961,,2024/03/01,,,currently-reading,,,,0,0
4,Some Custom Shelf Book,Someone,"Someone",,"=""""","=""""",0,3,,,100,2000,2000,,2024/03/01,dnf,dnf (#1),abandoned,,,,0,0
`

func TestParseCSV(t *testing.T) {
	got, skipped, err := ParseCSV(strings.NewReader("\xef\xbb\xbf" + exportCSV))
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(got) != 3 {
		t.Fatalf("skipped=%d got=%+v", skipped, got)
	}
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	want := []Entry{
		{Title: "Dune (Dune, #1)", Author: "Frank Herbert", ISBN13: "9780441172719", Shelf: ShelfRead, ReadAt: d(2023, 5, 14), AddedAt: d(2023, 1, 2)},
		// no ISBN-13 on the row: the ISBN-10 is used, converted
		{Title: "Emma", Author: "Jane Austen", ISBN13: "9780141439587", Shelf: ShelfToRead, AddedAt: d(2024, 2, 10)},
		{Title: "Solaris", Author: "Stanislaw Lem", Shelf: ShelfReading, AddedAt: d(2024, 3, 1)},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestParseCSVRejectsOtherFiles(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"no shelf":     "Title,Author\nDune,Herbert\n",
		"no title":     "Author,Exclusive Shelf\nHerbert,read\n",
		"not csv":      "<html><body>sign in</body></html>",
		"a playlist":   "name,artist,album\nx,y,z\n",
		"only header?": "",
	} {
		if _, _, err := ParseCSV(strings.NewReader(in)); !errors.Is(err, ErrNotExport) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a header and no books is an export of an empty library
	got, skipped, err := ParseCSV(strings.NewReader("Title,Author,Exclusive Shelf\n"))
	if err != nil || len(got) != 0 || skipped != 0 {
		t.Errorf("empty library: %v %v %v", got, skipped, err)
	}
}

func TestParseCSVTolerance(t *testing.T) {
	// headers in another case and order, a ragged row, a blank line, a date
	// a spreadsheet rewrote
	in := "exclusive shelf,TITLE,author,Date Read\nread,Dune,Frank Herbert,05/14/2023\n\nread,Cut\nto-read,Emma,Jane Austen,\n"
	got, _, err := ParseCSV(strings.NewReader(in))
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if !got[0].ReadAt.Equal(time.Date(2023, 5, 14, 0, 0, 0, 0, time.UTC)) || got[1].Title != "Emma" {
		t.Errorf("%+v", got)
	}
}
