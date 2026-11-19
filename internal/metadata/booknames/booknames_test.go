package booknames

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/openlibrary"
	"github.com/antresol-app/antresol/internal/metadata/wikidata"
)

type fakeWD struct {
	works []wikidata.WorkNames
	err   error
	calls int
}

func (f *fakeWD) WorkNames(context.Context, string, string) ([]wikidata.WorkNames, error) {
	f.calls++
	return f.works, f.err
}

type fakeOL struct {
	byISBN, byTitle []openlibrary.Doc
	err             error
	asked           []string
}

func (f *fakeOL) Works(_ context.Context, title, author, isbn string) ([]openlibrary.Doc, error) {
	if isbn != "" {
		f.asked = append(f.asked, "isbn:"+isbn)
		return f.byISBN, f.err
	}
	f.asked = append(f.asked, title+"/"+author)
	return f.byTitle, f.err
}

type fakeAI struct {
	answer string
	err    error
	calls  int
}

func (f *fakeAI) AskJSON(context.Context, string, string) (string, error) {
	f.calls++
	return f.answer, f.err
}

func newResolver(t *testing.T) (*Resolver, *time.Time) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	return &Resolver{DB: d, Now: func() time.Time { return now }}, &now
}

var master = Book{Title: "The Master and Margarita", Author: "Mikhail Bulgakov", ISBN13: "9780141180144"}

// Both sources' answers are put together and kept; the AI helper is not
// asked when they know the book.
func TestLookupMergesSourcesAndKeepsTheAnswer(t *testing.T) {
	r, now := newResolver(t)
	wd := &fakeWD{works: []wikidata.WorkNames{{ID: "Q1", Titles: []string{"Мастер и Маргарита", "The Master and Margarita"},
		Authors: []string{"Михаил Булгаков"}}}}
	ol := &fakeOL{byISBN: []openlibrary.Doc{{Key: "/works/OL676009W", Title: "Мастер и Маргарита", Authors: []string{"Михаил Булгаков"},
		ISBN: []string{"5170661029", "9780141180144"}}}}
	ai := &fakeAI{}
	r.Wikidata, r.OpenLibrary, r.AI = wd, ol, func(context.Context) Asker { return ai }
	ctx := context.Background()
	if _, ok, _ := r.Cached(ctx, master); ok {
		t.Fatal("cached before any lookup")
	}
	n, err := r.Lookup(ctx, master)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.Titles, []string{"Мастер и Маргарита", "The Master and Margarita"}) ||
		!slices.Equal(n.ISBNs, []string{"9785170661022", "9780141180144"}) || !slices.Equal(n.Works, []string{"/works/OL676009W"}) ||
		!slices.Equal(n.Sources, []string{SourceWikidata, SourceOpenLibrary}) {
		t.Fatalf("%+v", n)
	}
	if ai.calls != 0 || !slices.Equal(ol.asked, []string{"isbn:9780141180144"}) {
		t.Errorf("ai %d, open library %q", ai.calls, ol.asked)
	}
	got, ok, err := r.Cached(ctx, Book{Title: "the master and margarita!", Author: "Mikhail  Bulgakov", ISBN13: master.ISBN13})
	if err != nil || !ok || !slices.Equal(got.Titles, n.Titles) {
		t.Fatalf("cached: %+v %v %v", got, ok, err)
	}
	// half a year on it is due again, the old answer still there
	*now = now.Add(foundFor + time.Hour)
	if got, ok, _ := r.Cached(ctx, master); ok || len(got.Titles) == 0 {
		t.Errorf("after half a year: %+v %v", got, ok)
	}
}

// Open Library by title: only a work of that title by that author (in any
// script) counts.
func TestOpenLibraryByTitleIsStrict(t *testing.T) {
	r, _ := newResolver(t)
	ol := &fakeOL{byTitle: []openlibrary.Doc{
		{Key: "/works/OL1W", Title: "The Master and Margarita", Authors: []string{"Михаил Афанасьевич Булгаков"}, ISBN: []string{"9785170661022"}},
		{Key: "/works/OL2W", Title: "The Master and Margarita", Authors: []string{"Somebody Else"}},
		{Key: "/works/OL3W", Title: "Master and Margarita: A Study", Authors: []string{"Mikhail Bulgakov"}},
	}}
	r.OpenLibrary = ol
	n, err := r.Lookup(context.Background(), Book{Title: master.Title, Author: master.Author})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.Works, []string{"/works/OL1W"}) {
		t.Fatalf("%+v", n)
	}
}

// A source that cannot answer is an error and nothing is kept; one that
// does not know the book (404) is no error.
func TestLookupFailureKeepsNothing(t *testing.T) {
	r, _ := newResolver(t)
	ctx := context.Background()
	r.Wikidata = &fakeWD{err: &metadata.HTTPError{Service: "wikidata", Path: "/w/api.php", Status: 503}}
	if _, err := r.Lookup(ctx, master); err == nil {
		t.Fatal("no error")
	}
	if _, ok, _ := r.Cached(ctx, master); ok {
		t.Fatal("a failure was kept")
	}
	r.Wikidata = &fakeWD{err: metadata.ErrNotFound}
	if n, err := r.Lookup(ctx, master); err != nil || !n.Empty() {
		t.Fatalf("%+v %v", n, err)
	}
	if _, ok, _ := r.Cached(ctx, master); !ok {
		t.Fatal("a miss is kept")
	}
}

// The AI helper is the last resort: asked only when the sources know
// nothing, its titles kept; its failure is asked again the next day, a
// model that does not know the book a month on.
func TestAIOnlyWhenTheSourcesKnowNothing(t *testing.T) {
	r, now := newResolver(t)
	ctx := context.Background()
	wd := &fakeWD{}
	ai := &fakeAI{answer: `{"known": true, "title_ru": "Мастер и Маргарита", "title_en": "The Master and Margarita",
		"author_ru": "Михаил Булгаков", "author_en": "Mikhail Bulgakov"}`}
	r.Wikidata, r.AI = wd, func(context.Context) Asker { return ai }
	n, err := r.Lookup(ctx, master)
	if err != nil || ai.calls != 1 || !slices.Equal(n.Sources, []string{SourceAI}) || !slices.Contains(n.Titles, "Мастер и Маргарита") ||
		!slices.Contains(n.Authors, "Михаил Булгаков") {
		t.Fatalf("%+v %v (ai %d)", n, err, ai.calls)
	}

	other := Book{Title: "Unknown", Author: "Nobody"}
	ai.answer, ai.err = "", errors.New("timeout")
	if n, err := r.Lookup(ctx, other); err != nil || !n.Empty() {
		t.Fatalf("%+v %v", n, err)
	}
	*now = now.Add(aiFailFor + time.Minute)
	if _, ok, _ := r.Cached(ctx, other); ok {
		t.Error("an AI failure is asked again the next day")
	}
	ai.err, ai.answer = nil, `{"known": false}`
	if n, err := r.Lookup(ctx, other); err != nil || !n.Empty() {
		t.Fatalf("%+v %v", n, err)
	}
	*now = now.Add(aiFailFor + time.Minute)
	if _, ok, _ := r.Cached(ctx, other); !ok {
		t.Error("a book nobody knows waits a month")
	}
	// no model configured: no question
	r.AI = func(context.Context) Asker { return nil }
	if n, err := r.Lookup(ctx, Book{Title: "X", Author: "Y"}); err != nil || !n.Empty() {
		t.Fatalf("%+v %v", n, err)
	}
}
