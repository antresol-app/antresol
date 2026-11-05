package openlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

type fakeOL struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	docs   map[string]string // title -> docs JSON array
	works  map[string]string // work path -> JSON
	covers map[string]bool   // cover path -> exists
}

func newFakeOL(t *testing.T) *fakeOL {
	f := &fakeOL{docs: map[string]string{}, works: map[string]string{}, covers: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, r.URL.Path)
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/search.json":
			d, ok := f.docs[r.URL.Query().Get("title")]
			if !ok {
				d = "[]"
			}
			fmt.Fprintf(w, `{"docs":%s}`, d)
		case strings.HasPrefix(r.URL.Path, "/works/"):
			if b, ok := f.works[r.URL.Path]; ok {
				w.Write([]byte(b))
				return
			}
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/covers/"):
			if r.URL.Query().Get("default") != "false" {
				t.Errorf("cover without default=false: %s", r.URL)
			}
			if f.covers[r.URL.Path] {
				w.Write([]byte("JPEG"))
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOL) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, h := range f.hits {
		if strings.HasPrefix(h, prefix) {
			n++
		}
	}
	return n
}

func setup(t *testing.T, base string) (*Enricher, *library.Repo) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	repo := library.NewRepo(d)
	return &Enricher{
		Client: &Client{BaseURL: base, CoversBase: base + "/covers", UserAgent: testUA, Limiter: &metadata.Limiter{}},
		Repo:   repo,
		Images: &metadata.Images{Dir: filepath.Join(t.TempDir(), "images")},
	}, repo
}

func book(t *testing.T, repo *library.Repo, path string, b library.BookInput) int64 {
	t.Helper()
	b.Files = []library.FileInput{{Path: path, Size: 1, Mtime: 1}}
	id, err := repo.UpsertBook(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func item(t *testing.T, repo *library.Repo, id int64) (library.Item, map[string]any) {
	t.Helper()
	it, err := repo.Item(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
		t.Fatal(err)
	}
	return it, m
}

func TestEnrichBookDescription(t *testing.T) {
	f := newFakeOL(t)
	f.docs["Хоббит"] = `[{"key":"/works/OL1W","title":"Хоббит","author_name":["Джон Толкин"],"first_publish_year":1937,"cover_i":11}]`
	f.docs["Сильмариллион"] = `[{"key":"/works/OL2W","title":"Сильмариллион","author_name":["Tolkien, J.R.R.","Толкин, Джон"],"first_publish_year":1977}]`
	f.works["/works/OL1W.json"] = `{"description":"Строкой."}`
	f.works["/works/OL2W.json"] = `{"description":{"type":"/type/text","value":"Объектом."}}`
	f.covers["/covers/b/id/11-L.jpg"] = true
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	a := book(t, repo, "/b/hobbit.epub", library.BookInput{Title: "Хоббит", Authors: []string{"Джон Рональд Руэл Толкин"}, Lang: "ru"})
	b := book(t, repo, "/b/silm.epub", library.BookInput{Title: "Сильмариллион", Authors: []string{"Дж. Р. Р. Толкин"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	ia, ma := item(t, repo, a)
	if ia.Overview != "Строкой." || ia.Year != 1937 || !strings.HasSuffix(ia.Poster, ".jpg") || ma["ol_key"] != "/works/OL1W" || ma["lang"] != "ru" {
		t.Fatalf("%+v %v", ia, ma)
	}
	if _, err := time.Parse(time.RFC3339, ma["ol_at"].(string)); err != nil {
		t.Fatal(err)
	}
	ib, mb := item(t, repo, b)
	if ib.Overview != "Объектом." || ib.Year != 1977 || ib.Poster != "" || mb["ol_key"] != "/works/OL2W" {
		t.Fatalf("%+v %v", ib, mb)
	}
	before := len(f.hits)
	if err := e.EnrichPending(ctx); err != nil || len(f.hits) != before {
		t.Fatalf("second run: %v %v", err, f.hits[before:])
	}
}

func TestEnrichBookKeepsOwnFields(t *testing.T) {
	f := newFakeOL(t)
	f.docs["Хоббит"] = `[{"key":"/works/OL1W","title":"Хоббит","author_name":["Джон Толкин"],"first_publish_year":1937,"cover_i":11}]`
	f.works["/works/OL1W.json"] = `{"description":"Из Open Library."}`
	f.covers["/covers/b/id/11-L.jpg"] = true
	e, repo := setup(t, f.srv.URL)
	id := book(t, repo, "/b/hobbit.epub", library.BookInput{Title: "Хоббит", Authors: []string{"Толкин"},
		Description: "Своё.", Year: 2015, Cover: "file:/b/hobbit.jpg"})
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	it, m := item(t, repo, id)
	if it.Overview != "Своё." || it.Year != 2015 || it.Poster != "file:/b/hobbit.jpg" || m["ol_key"] != "/works/OL1W" {
		t.Fatalf("%+v %v", it, m)
	}
	if f.count("/works/") != 0 || f.count("/covers/") != 0 {
		t.Fatalf("needless requests %v", f.hits)
	}
}

func TestEnrichBookRejectsOtherAuthor(t *testing.T) {
	f := newFakeOL(t)
	f.docs["Хоббит"] = `[{"key":"/works/OL1W","title":"Хоббит","author_name":["Иван Иванов"]},
	  {"key":"/works/OL5W","title":"Хоббит, или Туда и обратно","author_name":["Толкин"]}]`
	e, repo := setup(t, f.srv.URL)
	id := book(t, repo, "/b/hobbit.epub", library.BookInput{Title: "Хоббит", Authors: []string{"Толкин"}})
	noAuthor := book(t, repo, "/c/x.epub", library.BookInput{Title: "Хоббит"})
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, x := range []int64{id, noAuthor} {
		it, m := item(t, repo, x)
		if m["ol_miss"] == nil || m["ol_at"] != nil || m["ol_key"] != nil || it.Overview != "" {
			t.Fatalf("%+v %v", it, m)
		}
	}
	if n := f.count("/search.json"); n != 1 {
		t.Fatalf("searches %d (a book without authors is not searched)", n)
	}
}

func TestEnrichAudiobook(t *testing.T) {
	f := newFakeOL(t)
	f.docs["Пикник на обочине"] = `[{"key":"/works/OL7W","title":"Пикник на обочине","author_name":["Аркадий Стругацкий","Борис Стругацкий"],"first_publish_year":1972}]`
	f.works["/works/OL7W.json"] = `{"description":"Зона."}`
	e, repo := setup(t, f.srv.URL)
	id, err := repo.UpsertAudiobook(context.Background(), library.AudiobookInput{Dir: "/a/picnic",
		Files: []library.FileInput{{Path: "/a/picnic/01.mp3", Size: 1, Mtime: 1}}, Title: "Пикник на обочине",
		Authors: []string{"Стругацкие А. и Б."}, Narrator: "Чтец"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	it, m := item(t, repo, id)
	// "Стругацкие" is not "Стругацкий": no match is the safe outcome
	if m["ol_miss"] == nil {
		t.Fatalf("%+v %v", it, m)
	}
	id2, err := repo.UpsertAudiobook(context.Background(), library.AudiobookInput{Dir: "/a/picnic2",
		Files: []library.FileInput{{Path: "/a/picnic2/01.mp3", Size: 1, Mtime: 1}}, Title: "Пикник на обочине",
		Authors: []string{"Борис Стругацкий"}, Narrator: "Чтец"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	it2, m2 := item(t, repo, id2)
	if it2.Overview != "Зона." || it2.Year != 1972 || m2["ol_key"] != "/works/OL7W" || m2["narrator"] != "Чтец" {
		t.Fatalf("%+v %v", it2, m2)
	}
}

func TestEnrichBooksOffline(t *testing.T) {
	f := newFakeOL(t)
	e, repo := setup(t, f.srv.URL)
	f.srv.Close()
	id := book(t, repo, "/b/hobbit.epub", library.BookInput{Title: "Хоббит", Authors: []string{"Толкин"}})
	before, _ := item(t, repo, id)
	if err := e.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, m := item(t, repo, id)
	if after.MetaJSON != before.MetaJSON || m["ol_miss"] != nil {
		t.Fatalf("%v", m)
	}
}

func TestEnrichBookMissRetry7d(t *testing.T) {
	f := newFakeOL(t)
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	book(t, repo, "/b/x.epub", library.BookInput{Title: "Нет такой", Authors: []string{"Никто"}})
	if err := e.EnrichPending(ctx); err != nil || f.count("/search.json") != 1 {
		t.Fatal(err, f.hits)
	}
	now = now.Add(6 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/search.json") != 1 {
		t.Fatal(err, f.hits)
	}
	now = now.Add(2 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/search.json") != 2 {
		t.Fatal(err, f.hits)
	}
}

func TestSurnameMatch(t *testing.T) {
	for _, c := range []struct {
		ours, theirs []string
		want         bool
	}{
		{[]string{"Лев Николаевич Толстой"}, []string{"Толстой, Лев"}, true},
		{[]string{"Толстой Лев"}, []string{"Лев Толстой"}, true},
		{[]string{"Л. Толстой"}, []string{"Lev Tolstoy", "Лев Толстой"}, true},
		{[]string{"Лев Толстой"}, []string{"Лев Гумилёв"}, false},
		{[]string{"Гумилев"}, []string{"Лев Гумилёв"}, true},
		{nil, []string{"X"}, false},
		{[]string{"А."}, []string{"А."}, false},
		{[]string{"Толстой Лев Николаевич"}, []string{"Лев Толстой"}, true},
		// a given name is not a surname: "Лев" from "Лев Николаевич Толстой"
		{[]string{"Лев Николаевич Толстой"}, []string{"Николай Лев"}, false},
		// two words may be either order ("Толстой Лев"): both count; the
		// exact title check keeps this safe
		{[]string{"Борис Акунин"}, []string{"Григорий Борис"}, true},
		// NFD (macOS file names): "й" as "и" + U+0306
		{[]string{"Толсто\u0438\u0306"}, []string{"Лев Толстой"}, true},
	} {
		if got := surnameMatch(c.ours, c.theirs); got != c.want {
			t.Errorf("surnameMatch(%q, %q) = %v", c.ours, c.theirs, got)
		}
	}
}

func TestEnrichBookBadAnswers(t *testing.T) {
	f := newFakeOL(t)
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	f.docs["Хоббит"] = `[{"key":"/works/OL1W","title":"Хоббит","author_name":["Джон Толкин"],"first_publish_year":1937}]`
	f.works["/works/OL1W.json"] = `not json`
	good := book(t, repo, "/a/good.epub", library.BookInput{Title: "Сильмариллион", Authors: []string{"Толкин"}})
	f.docs["Сильмариллион"] = `[{"key":"/works/OL2W","title":"Сильмариллион","author_name":["Толкин"],"first_publish_year":1977}]`
	f.works["/works/OL2W.json"] = `{"description":"Да."}`
	bad := book(t, repo, "/b/hobbit.epub", library.BookInput{Title: "Хоббит", Authors: []string{"Толкин"}})
	// newest first: the broken work comes before the good book, which is still enriched
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if it, m := item(t, repo, good); it.Overview != "Да." || m["ol_at"] == nil {
		t.Fatalf("good: %+v", it)
	}
	_, m := item(t, repo, bad)
	if m["ol_err"] != now.Format(time.RFC3339) || m["ol_at"] != nil || m["ol_miss"] != nil {
		t.Fatalf("bad: %v", m)
	}
	n := f.count("/search.json")
	now = now.Add(23 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/search.json") != n {
		t.Fatalf("retried within 24 h: %v", f.hits)
	}
	f.works["/works/OL1W.json"] = `{"description":"Туда и обратно."}`
	now = now.Add(2 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if it, m := item(t, repo, bad); it.Overview != "Туда и обратно." || m["ol_err"] != nil || m["ol_at"] == nil {
		t.Fatalf("after 24 h: %+v", it)
	}
}

func TestEnrichBookCoverRetriedAfterBadAnswer(t *testing.T) {
	f := newFakeOL(t)
	e, repo := setup(t, f.srv.URL)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	ctx := context.Background()
	f.docs["Хоббит"] = `[{"key":"/works/OL1W","title":"Хоббит","author_name":["Толкин"],"cover_i":11}]`
	f.works["/works/OL1W.json"] = `{}`
	id := book(t, repo, "/b/hobbit.epub", library.BookInput{Title: "Хоббит", Authors: []string{"Толкин"}})
	if err := e.EnrichPending(ctx); err != nil { // the cover answers 404 for now
		t.Fatal(err)
	}
	it, m := item(t, repo, id)
	if it.Poster != "" || m["ol_cover_miss"] == nil || m["ol_at"] == nil {
		t.Fatalf("%+v", it)
	}
	f.covers["/covers/b/id/11-L.jpg"] = true
	now = now.Add(6 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/covers/") != 1 {
		t.Fatalf("fresh cover miss retried: %v", f.hits)
	}
	now = now.Add(3 * 24 * time.Hour)
	if err := e.EnrichPending(ctx); err != nil || f.count("/covers/") != 2 || f.count("/search.json") != 1 {
		t.Fatalf("stale cover miss: %v", f.hits)
	}
	if it, m := item(t, repo, id); it.Poster == "" || m["ol_cover_miss"] != nil {
		t.Fatalf("%+v", it)
	}
}
