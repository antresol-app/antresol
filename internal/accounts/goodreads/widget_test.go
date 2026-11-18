package goodreads

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type book struct{ title, author, review string }

// widgetJS is a shelf widget as Goodreads serves it: the list's HTML written
// as one JavaScript string (quotes and slashes escaped) for the page to put
// in place. The markup follows the real widget.
func widgetJS(id string, books ...book) string {
	var b strings.Builder
	b.WriteString(`  <div class="gr_custom_container_` + id + `">
    <h2 class="gr_custom_header_` + id + `">
    <a style="text-decoration: none;" rel="nofollow" href="https://www.goodreads.com/review/list/1-name?shelf=read">Name &#39;s books</a>
    </h2>
`)
	for i, bk := range books {
		b.WriteString(`      <div class="gr_custom_each_container_` + id + `">
          <div class="gr_custom_book_container_` + id + `">
            <a title="` + bk.title + `" rel="nofollow" href="https://www.goodreads.com/review/show/` + string(rune('1'+i)) + `"><img alt="` + bk.title + `" border="0" src="https://img.example/books/1l/22921276._SY75_.jpg" /></a>
          </div>
          <div class="gr_custom_rating_` + id + `"><span class=" staticStars notranslate" title="really liked it"></span></div>
          <div class="gr_custom_title_` + id + `">
            <a rel="nofollow" href="https://www.goodreads.com/review/show/` + string(rune('1'+i)) + `">` + bk.title + `</a>
          </div>
          <div class="gr_custom_author_` + id + `">
            by <a rel="nofollow" href="https://www.goodreads.com/author/show/1.Name">` + bk.author + `</a>
          </div>
          <div class="gr_custom_review_` + id + `">
            ` + bk.review + `
          </div>
      </div>
`)
	}
	b.WriteString(`  <br style="clear: both"/>
  <center><a rel="nofollow" href="https://www.goodreads.com/"><img alt="goodreads.com" /></a></center>
  </div>
`)
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `'`, `\'`, "/", `\/`, "\n", `\n`).Replace(b.String())
	return "  var widget_code = '" + esc + "'\n  var widget_div = document.getElementById('gr_custom_widget_" + id + "')\n  if (widget_div) {\n    widget_div.innerHTML = widget_code\n  }\n"
}

func TestParseWidget(t *testing.T) {
	js := widgetJS("x1",
		book{"Dune (Dune, #1)", "Frank Herbert", "Great &amp; long. <br/>A \"classic\"..."},
		book{"Gödel, Escher, Bach: An Eternal Golden Braid", "Douglas R. Hofstadter", ""},
		book{"Ender&#39;s Game &amp; Speaker", "Orson Scott Card", "x"},
	)
	got, err := parseWidget(js, ShelfRead)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Title: "Dune (Dune, #1)", Author: "Frank Herbert", Shelf: ShelfRead},
		{Title: "Gödel, Escher, Bach: An Eternal Golden Braid", Author: "Douglas R. Hofstadter", Shelf: ShelfRead},
		{Title: "Ender's Game & Speaker", Author: "Orson Scott Card", Shelf: ShelfRead},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseWidgetShapes(t *testing.T) {
	// a shelf with no books
	if got, err := parseWidget(widgetJS("x2"), ShelfToRead); err != nil || len(got) != 0 {
		t.Errorf("empty shelf: %v %v", got, err)
	}
	// a user with no public widget
	notCorrect := `  var widget_code = '  This <a rel=\"nofollow\" href=\"https:\/\/www.goodreads.com\/\">Goodreads<\/a>\n   widget is not correct.  '` + "\n"
	if _, err := parseWidget(notCorrect, ShelfRead); !errors.Is(err, ErrNotFound) {
		t.Errorf("not correct: %v", err)
	}
	// Goodreads changed the markup: books are there, none can be read
	changed := strings.NewReplacer("gr_custom_title_", "gr_book_name_").Replace(widgetJS("x3", book{"A", "B", "c"}))
	if _, err := parseWidget(changed, ShelfRead); !errors.Is(err, ErrFormat) {
		t.Errorf("changed markup: %v", err)
	}
	// not the widget at all (a sign-in page, a captcha)
	if _, err := parseWidget("<html>please sign in</html>", ShelfRead); !errors.Is(err, ErrFormat) {
		t.Errorf("another page: %v", err)
	}
}

func TestWidgetShelf(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query().Get("widget_id"))
		mu.Unlock()
		if r.URL.Path != "/review/custom_widget/777" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("shelf") == "boom" {
			http.Error(w, "x", http.StatusBadGateway)
			return
		}
		if r.URL.Query().Get("shelf") == "slow" {
			http.Error(w, "x", http.StatusTooManyRequests)
			return
		}
		if r.URL.Query().Get("num_books") != "100" || r.Header.Get("User-Agent") != "Antresol" {
			t.Errorf("request: %v %v", r.URL.RawQuery, r.Header)
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(widgetJS(r.URL.Query().Get("widget_id"), book{"Emma", "Jane Austen", "x"})))
	}))
	defer srv.Close()
	wd := NewWidget(srv.URL+"/", nil)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		got, err := wd.Shelf(ctx, 777, ShelfToRead)
		if err != nil || len(got) != 1 || got[0].Title != "Emma" || got[0].Shelf != ShelfToRead {
			t.Fatalf("shelf: %+v %v", got, err)
		}
	}
	// Goodreads caches a widget by its id: a read must not reuse one
	if len(seen) != 2 || seen[0] == "" || seen[0] == seen[1] {
		t.Errorf("widget ids: %q", seen)
	}
	if _, err := wd.Shelf(ctx, 1, ShelfRead); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	for _, shelf := range []string{"boom", "slow"} {
		if _, err := wd.Shelf(ctx, 777, shelf); !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: %v", shelf, err)
		}
	}
	srv.Close()
	if _, err := wd.Shelf(ctx, 777, ShelfRead); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed: %v", err)
	}
	if _, err := NewWidget("off", nil).Shelf(ctx, 777, ShelfRead); !errors.Is(err, ErrUnavailable) {
		t.Errorf("off: %v", err)
	}
}
