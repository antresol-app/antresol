package goodreads

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata/booknames"
)

// fakeNames plays booknames.Resolver: answers by title, kept once looked up.
type fakeNames struct {
	answers map[string]booknames.Names
	kept    map[string]booknames.Names
	err     error
	asked   []string
}

func (f *fakeNames) Cached(_ context.Context, b booknames.Book) (booknames.Names, bool, error) {
	n, ok := f.kept[b.Title]
	return n, ok, nil
}

func (f *fakeNames) Lookup(_ context.Context, b booknames.Book) (booknames.Names, error) {
	f.asked = append(f.asked, b.Title)
	if f.err != nil {
		return booknames.Names{}, f.err
	}
	if f.kept == nil {
		f.kept = map[string]booknames.Names{}
	}
	f.kept[b.Title] = f.answers[b.Title]
	return f.answers[b.Title], nil
}

// Books imported before their names were known are looked up in the
// background and matched then; a translated title needs the author to
// agree, in any script.
func TestUnmatchedBooksFoundByTheirWorkNames(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	master := e.book("/b/m.fb2", "Мастер и Маргарита", "", "Булгаков М. А.")
	bare := e.book("/b/w.fb2", "Белая гвардия", "") // no author on file
	other := e.book("/b/d.fb2", "Собачье сердце", "", "Иван Иванов")
	byISBN := e.book("/b/i.fb2", "Идиот (подарочное издание)", "9785170211807", "Достоевский")
	e.imp(
		Entry{Title: "The Master and Margarita", Author: "Mikhail Bulgakov", Shelf: ShelfToRead},
		Entry{Title: "The White Guard", Author: "Mikhail Bulgakov", Shelf: ShelfToRead},
		Entry{Title: "Heart of a Dog", Author: "Mikhail Bulgakov", Shelf: ShelfToRead},
		Entry{Title: "The Idiot", Author: "Fyodor Dostoevsky", ISBN13: "9780140447927", Shelf: ShelfToRead},
	)
	names := &fakeNames{answers: map[string]booknames.Names{
		// the library's author is written in Cyrillic, Goodreads' in Latin:
		// the work's names need not list him
		"The Master and Margarita": {Titles: []string{"Мастер и Маргарита"}},
		"The White Guard":          {Titles: []string{"Белая гвардия"}, Authors: []string{"Михаил Булгаков"}},
		"Heart of a Dog":           {Titles: []string{"Собачье сердце"}, Authors: []string{"Михаил Булгаков"}},
		"The Idiot":                {ISBNs: []string{"9785170211807"}},
	}}
	e.svc.Names = names
	if !e.svc.lookupPending(ctx) || len(names.asked) != 4 {
		t.Fatalf("asked %q", names.asked)
	}
	if e.svc.lookupPending(ctx); len(names.asked) != 4 {
		t.Fatalf("asked again: %q", names.asked)
	}
	if err := e.svc.Reconcile(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]bool{master: true, byISBN: true, bare: false, other: false} {
		if _, _, wanted := e.state(id); wanted != want {
			t.Errorf("book %d wanted=%v", id, wanted)
		}
	}
}

// A source that fails pauses the lookups; they resume later.
func TestLookupFailurePauses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.imp(Entry{Title: "A", Author: "X", Shelf: ShelfRead}, Entry{Title: "B", Author: "X", Shelf: ShelfRead})
	names := &fakeNames{err: errors.New("offline")}
	e.svc.Names = names
	e.svc.lookupPending(ctx)
	e.svc.lookupPending(ctx)
	if len(names.asked) != 1 {
		t.Fatalf("asked %q", names.asked)
	}
	names.err = nil
	e.now = e.now.Add(retryEvery + time.Second)
	e.svc.lookupPending(ctx)
	if len(names.asked) != 3 {
		t.Fatalf("asked %q", names.asked)
	}
}
