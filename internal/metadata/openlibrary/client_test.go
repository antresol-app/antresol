package openlibrary

import (
	"context"
	"errors"
	"github.com/antresol-app/antresol/internal/version"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

var testUA = version.UserAgent

const searchFixture = `{"numFound":2,"start":0,"docs":[
 {"key":"/works/OL27448W","title":"The Lord of the Rings","author_name":["J.R.R. Tolkien"],
  "first_publish_year":1954,"cover_i":14625765,"isbn":["9780618640157","0618640150"],"language":["eng","rus"]},
 {"key":"/works/OL1W","title":"Без обложки","author_name":["Толстой, Лев"]}
]}`

func TestOpenLibrarySearchParses(t *testing.T) {
	var q map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.json" {
			t.Errorf("path %s", r.URL.Path)
		}
		q = map[string]string{}
		for k := range r.URL.Query() {
			q[k] = r.URL.Query().Get(k)
		}
		w.Write([]byte(searchFixture))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	docs, err := c.Search(context.Background(), "The Lord of the Rings", "Tolkien")
	if err != nil {
		t.Fatal(err)
	}
	if q["title"] != "The Lord of the Rings" || q["author"] != "Tolkien" || q["limit"] != "5" ||
		q["fields"] != "key,title,author_name,first_publish_year,cover_i,isbn,language,subject" {
		t.Fatalf("query %v", q)
	}
	if len(docs) != 2 {
		t.Fatalf("%+v", docs)
	}
	d := docs[0]
	if d.Key != "/works/OL27448W" || d.Title != "The Lord of the Rings" || strings.Join(d.Authors, ";") != "J.R.R. Tolkien" ||
		d.Year != 1954 || d.CoverID != 14625765 || len(d.ISBN) != 2 || strings.Join(d.Languages, ",") != "eng,rus" {
		t.Fatalf("%+v", d)
	}
	if docs[1].CoverID != 0 || docs[1].Year != 0 {
		t.Fatalf("%+v", docs[1])
	}

	if _, err := c.SearchQ(context.Background(), "tolkien"); err != nil {
		t.Fatal(err)
	}
	if q["q"] != "tolkien" || q["limit"] != "10" || q["fields"] == "" || q["title"] != "" {
		t.Fatalf("SearchQ query %v", q)
	}
}

func TestWorkDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works/OL1W.json":
			w.Write([]byte(`{"description":"  Plain text.  ","subjects":["Fantasy","Hobbits"]}`))
		case "/works/OL2W.json":
			w.Write([]byte(`{"description":{"type":"/type/text","value":"Object text."}}`))
		case "/works/OL3W.json":
			w.Write([]byte(`{"title":"No description"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	ctx := context.Background()
	w, err := c.Work(ctx, "/works/OL1W")
	if err != nil || w.Description != "Plain text." || strings.Join(w.Subjects, ",") != "Fantasy,Hobbits" {
		t.Fatalf("%+v %v", w, err)
	}
	if w, err := c.Work(ctx, "OL2W"); err != nil || w.Description != "Object text." {
		t.Fatalf("%+v %v", w, err)
	}
	if w, err := c.Work(ctx, "/works/OL3W"); err != nil || w.Description != "" {
		t.Fatalf("%+v %v", w, err)
	}
	if _, err := c.Work(ctx, "/works/OL9W"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := c.Work(ctx, "/works/../x"); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestUserAgentSent(t *testing.T) {
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		w.Write([]byte(`{"docs":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, UserAgent: testUA, Limiter: &metadata.Limiter{}}
	if _, err := c.Search(context.Background(), "x", "y"); err != nil {
		t.Fatal(err)
	}
	if ua.Load() != testUA {
		t.Fatalf("User-Agent %q", ua.Load())
	}
}

func TestCoverURLNoDefault(t *testing.T) {
	c := &Client{CoversBase: "https://covers.openlibrary.org"}
	u := c.CoverURL(1)
	if u != "https://covers.openlibrary.org/b/id/1-L.jpg?default=false" || !strings.HasSuffix(u, "-L.jpg?default=false") {
		t.Fatal(u)
	}
	if (&Client{}).CoverURL(1) != "" || c.CoverURL(0) != "" {
		t.Fatal("no base or id must give no URL")
	}
}

func TestDefaultInterval(t *testing.T) {
	if (&Client{}).limiter().Interval != 500*time.Millisecond {
		t.Fatal("open library default interval")
	}
}
