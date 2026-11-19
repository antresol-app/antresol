package goodreads

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/booknames"
	"github.com/antresol-app/antresol/internal/metadata/openlibrary"
	"github.com/antresol-app/antresol/internal/metadata/wikidata"
	"github.com/antresol-app/antresol/internal/version"
)

// recorded replays the Wikidata and Open Library answers in
// testdata/names, keyed by host, path and query; VAULT_RECORD_NAMES=1
// records them from the live services (politely: the clients' limiters
// space the requests).
type recorded struct {
	t      *testing.T
	record bool
}

func recordedName(r *http.Request) string {
	q := r.URL.Query()
	q.Del("format")
	sum := sha1.Sum([]byte(r.URL.Path + "?" + q.Encode()))
	return filepath.Join("testdata", "names", r.URL.Host+"-"+hex.EncodeToString(sum[:6])+".json")
}

func (f recorded) RoundTrip(r *http.Request) (*http.Response, error) {
	name := recordedName(r)
	if f.record {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				f.t.Fatal(err)
			}
			if err := os.WriteFile(name, body, 0o644); err != nil {
				f.t.Fatal(err)
			}
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	body, err := os.ReadFile(name)
	if err != nil {
		f.t.Fatalf("no recorded answer for %s?%s: %v", r.URL.Path, r.URL.RawQuery, err)
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
}

// recordedResolver is a Resolver on the recorded answers, no AI.
func recordedResolver(t *testing.T, e *env) *booknames.Resolver {
	rec := os.Getenv("VAULT_RECORD_NAMES") == "1"
	hc := &http.Client{Transport: recorded{t, rec}}
	wdGap, olGap := time.Duration(0), time.Duration(0)
	if rec {
		wdGap, olGap = time.Second, time.Second
	}
	return &booknames.Resolver{DB: e.svc.Accounts.DB,
		Wikidata: &wikidata.Client{BaseURL: "https://www.wikidata.org", UserAgent: version.UserAgent, HTTP: hc,
			Limiter: &metadata.Limiter{Interval: wdGap}},
		OpenLibrary: &openlibrary.Client{BaseURL: "https://openlibrary.org", UserAgent: version.UserAgent, HTTP: hc,
			Limiter: &metadata.Limiter{Interval: olGap}},
		Now: func() time.Time { return e.now }}
}

// evalBook is a book of the match-rate set: as the library has it, and as
// a Goodreads shelf lists it.
type evalBook struct {
	libTitle, libISBN string
	libAuthors        []string
	gr                Entry
}

// evalSet: well-known books whose Goodreads title is in another language
// than the library's edition — Russian classics listed in English, English
// (and other) books the library has in Russian translation, and two the
// other way round (a Russian Goodreads shelf, an English library book).
// The shelf widget gives no ISBN; two rows come from the CSV export, with
// the ISBN of an English edition.
var evalSet = []evalBook{
	{"Мастер и Маргарита", "", []string{"Булгаков Михаил Афанасьевич"}, Entry{Title: "The Master and Margarita", Author: "Mikhail Bulgakov"}},
	{"Преступление и наказание", "", []string{"Достоевский Ф. М."}, Entry{Title: "Crime and Punishment", Author: "Fyodor Dostoevsky"}},
	{"Война и мир", "", []string{"Лев Толстой"}, Entry{Title: "War and Peace", Author: "Leo Tolstoy"}},
	{"Анна Каренина", "", []string{"Толстой Лев Николаевич"}, Entry{Title: "Anna Karenina", Author: "Leo Tolstoy"}},
	{"Братья Карамазовы", "", []string{"Фёдор Достоевский"}, Entry{Title: "The Brothers Karamazov", Author: "Fyodor Dostoevsky"}},
	{"Мёртвые души", "", []string{"Николай Гоголь"}, Entry{Title: "Dead Souls", Author: "Nikolai Gogol"}},
	{"Евгений Онегин", "", []string{"Пушкин А. С."}, Entry{Title: "Eugene Onegin", Author: "Alexander Pushkin"}},
	{"Отцы и дети", "", []string{"Иван Тургенев"}, Entry{Title: "Fathers and Sons", Author: "Ivan Turgenev"}},
	{"Доктор Живаго", "", []string{"Борис Пастернак"}, Entry{Title: "Doctor Zhivago", Author: "Boris Pasternak"}},
	{"Пикник на обочине", "", []string{"Аркадий Стругацкий", "Борис Стругацкий"}, Entry{Title: "Roadside Picnic", Author: "Arkady Strugatsky"}},
	{"Собачье сердце", "", []string{"Михаил Булгаков"}, Entry{Title: "Heart of a Dog", Author: "Mikhail Bulgakov"}},
	{"Мы", "", []string{"Евгений Замятин"}, Entry{Title: "We", Author: "Yevgeny Zamyatin"}},
	{"1984", "", []string{"Джордж Оруэлл"}, Entry{Title: "1984", Author: "George Orwell"}},
	{"Над пропастью во ржи", "", []string{"Джером Д. Сэлинджер"}, Entry{Title: "The Catcher in the Rye", Author: "J.D. Salinger"}},
	{"Убить пересмешника", "", []string{"Харпер Ли"}, Entry{Title: "To Kill a Mockingbird", Author: "Harper Lee"}},
	{"451 градус по Фаренгейту", "", []string{"Рэй Брэдбери"}, Entry{Title: "Fahrenheit 451", Author: "Ray Bradbury"}},
	{"Великий Гэтсби", "", []string{"Фрэнсис Скотт Фицджеральд"}, Entry{Title: "The Great Gatsby", Author: "F. Scott Fitzgerald"}},
	{"Гарри Поттер и философский камень", "", []string{"Джоан Роулинг"}, Entry{Title: "Harry Potter and the Sorcerer's Stone (Harry Potter, #1)", Author: "J.K. Rowling"}},
	{"Сто лет одиночества", "", []string{"Габриэль Гарсиа Маркес"}, Entry{Title: "One Hundred Years of Solitude", Author: "Gabriel García Márquez"}},
	{"Маленький принц", "", []string{"Антуан де Сент-Экзюпери"}, Entry{Title: "The Little Prince", Author: "Antoine de Saint-Exupéry"}},
	// the other way round
	{"Shantaram", "", []string{"Gregory David Roberts"}, Entry{Title: "Шантарам", Author: "Грегори Дэвид Робертс"}},
	{"Lolita", "", []string{"Vladimir Nabokov"}, Entry{Title: "Лолита", Author: "Владимир Набоков"}},
	// the export: a Penguin edition's ISBN; the library's Russian edition,
	// under a title no catalog has, carries its own (the work's editions on
	// Open Library list both)
	{"Идиот (иллюстрированное издание)", "9785170211807", []string{"Достоевский Фёдор"},
		Entry{Title: "The Idiot", Author: "Fyodor Dostoevsky", ISBN13: "9780140447927"}},
}

// evalTraps must find nothing: a book the library lacks, and another
// book of an author the library has.
var evalTraps = []Entry{
	{Title: "Ulysses", Author: "James Joyce"},
	{Title: "The White Guard", Author: "Mikhail Bulgakov"},
}

// TestMatchRateAcrossLanguages reports how many books of evalSet a profile's
// Goodreads shelf finds in the library: before (the titles as Goodreads
// writes them) and after the books' work names are looked up (recorded
// Wikidata and Open Library answers), with each source alone and both. The
// book found must be the right one, and the traps find nothing.
func TestMatchRateAcrossLanguages(t *testing.T) {
	for _, c := range []struct {
		name   string
		wd, ol bool
	}{{"wikidata", true, false}, {"openlibrary", false, true}, {"both", true, true}} {
		before, after, missed := matchRate(t, c.wd, c.ol)
		t.Logf("%s: before %d/%d, after %d/%d; missed %q", c.name, before, len(evalSet), after, len(evalSet), missed)
		if c.wd && c.ol && after != len(evalSet) {
			t.Errorf("both sources: %d/%d", after, len(evalSet))
		}
	}
}

func matchRate(t *testing.T, wd, ol bool) (before, after int, missed []string) {
	e := newEnv(t)
	ctx := context.Background()
	want := map[string]int64{} // entry key -> library book
	var entries []Entry
	for i, b := range evalSet {
		id := e.book(filepath.Join("/b", string(rune('a'+i))+".epub"), b.libTitle, b.libISBN, b.libAuthors...)
		g := b.gr
		g.Shelf = ShelfToRead
		want[g.key()] = id
		entries = append(entries, g)
	}
	for _, g := range evalTraps {
		g.Shelf = ShelfToRead
		entries = append(entries, g)
	}
	before = e.imp(entries...).InLibrary

	r := recordedResolver(t, e)
	if !wd {
		r.Wikidata = nil
	}
	if !ol {
		r.OpenLibrary = nil
	}
	e.svc.Names = r
	// the background lookups, round after round, until every unmatched
	// book has an answer
	for i := 0; i < 10; i++ {
		e.svc.lookupPending(ctx)
	}
	if err := e.svc.Reconcile(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	sum, err := e.svc.Summary(ctx, e.uid)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := e.prog.Watchlist(ctx, e.uid)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[int64]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	right := map[int64]bool{}
	for _, b := range evalSet {
		id := want[b.gr.key()]
		right[id] = true
		if !wanted[id] {
			missed = append(missed, b.gr.Title)
		}
	}
	for id := range wanted {
		if !right[id] {
			t.Errorf("a wrong book marked: %d", id)
		}
	}
	var trapsMatched int
	if err := e.svc.Accounts.DB.QueryRow(`SELECT COUNT(*) FROM goodreads_entries WHERE matched=1 AND title IN (?,?)`,
		evalTraps[0].Title, evalTraps[1].Title).Scan(&trapsMatched); err != nil || trapsMatched != 0 {
		t.Errorf("traps matched: %d %v", trapsMatched, err)
	}
	return before, sum.InLibrary, missed
}
