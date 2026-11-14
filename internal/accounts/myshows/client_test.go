package myshows

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }
func (f *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	f.now = f.now.Add(d)
	return nil
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, srv.Client())
	clk := &fakeClock{now: time.Unix(1000, 0)}
	c.limiter = newLimiter(clk.Now, clk.Sleep)
	c.Backoff = time.Millisecond
	return c
}

func TestProfileShowsParses(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("User-Agent") != "Antresol" {
			t.Errorf("method=%s ua=%q", r.Method, r.Header.Get("User-Agent"))
		}
		for _, s := range []string{`"profile.Shows"`, `"login":"tester"`, `"jsonrpc":"2.0"`} {
			if !strings.Contains(string(b), s) {
				t.Errorf("body %s lacks %s", b, s)
			}
		}
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":[
		{"show":{"id":11,"title":"Пример","titleOriginal":"Sample","status":"Ended","year":2020,"category":"anime","image":"x"},
		 "watchStatus":"watching","rating":4,"watchCount":1,"totalEpisodes":12,"watchedEpisodes":5}]}`)
	})
	es, err := c.ProfileShows(context.Background(), "tester")
	if err != nil || len(es) != 1 {
		t.Fatalf("es=%v err=%v", es, err)
	}
	want := Entry{ShowID: 11, Title: "Пример", TitleOriginal: "Sample", Category: "anime", Year: 2020,
		Status: "watching", Rating: 4, WatchedEpisodes: 5, TotalEpisodes: 12}
	if es[0] != want {
		t.Fatalf("entry=%+v", es[0])
	}
}

func TestUnknownLoginMapped(t *testing.T) {
	// Live shape: HTTP 200 with an error object, code 404.
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":404,"message":"Internal error","data":"user was not found"}}`)
	})
	if _, err := c.ProfileShows(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestPrivateMapped(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":403,"message":"Forbidden","data":"profile is private"}}`)
	})
	if _, err := c.ProfileShows(context.Background(), "x"); !errors.Is(err, ErrPrivate) {
		t.Fatalf("err=%v", err)
	}
}

func TestShowParsesIMDb(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"id":5,"title":"T","titleOriginal":"O","year":2001,"imdbId":412142,"kinopoiskId":9}}`)
	})
	s, err := c.Show(context.Background(), 5)
	if err != nil || s.ID != 5 || s.IMDbID != 412142 || s.Year != 2001 || s.TitleOriginal != "O" {
		t.Fatalf("s=%+v err=%v", s, err)
	}
}

func TestLimiterOnePerSecond(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	l := newLimiter(clk.Now, clk.Sleep)
	start := clk.now
	for i := 0; i < 4; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := clk.now.Sub(start); got != 3*time.Second {
		t.Fatalf("4 requests took %v, want 3s", got)
	}
}

func TestRetryAfterOn429(t *testing.T) {
	var n atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":[]}`)
	})
	if _, err := c.ProfileShows(context.Background(), "x"); err != nil || n.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, n.Load())
	}
	// Persistent 429 gives up with ErrRateLimited.
	c2 := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := c2.ProfileShows(context.Background(), "x"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v", err)
	}
}

func TestNullResultIsError(t *testing.T) {
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"result":null}`, `{"jsonrpc":"2.0","id":1}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
		if es, err := c.ProfileShows(context.Background(), "tester"); err == nil {
			t.Fatalf("%s: want an error, got %v", body, es)
		}
		if _, err := c.Show(context.Background(), 1); err == nil {
			t.Fatalf("%s: Show wants an error", body)
		}
	}
}

func TestUnavailableClassified(t *testing.T) {
	code := func(n int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(n)
		}
	}
	for _, c := range []struct {
		status int
		want   bool
	}{{502, true}, {503, true}, {429, true}, {400, false}, {404, false}} {
		_, err := testClient(t, code(c.status)).Show(context.Background(), 1)
		if err == nil || Unavailable(err) != c.want {
			t.Fatalf("%d: err=%v unavailable=%v", c.status, err, Unavailable(err))
		}
	}
	// a JSON-RPC not-found answer is no outage
	nf := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":404,"message":"x","data":"show not found"}}`)
	})
	if _, err := nf.Show(context.Background(), 1); !errors.Is(err, ErrNotFound) || Unavailable(err) {
		t.Fatalf("err=%v", err)
	}
	// transport error
	srv := httptest.NewServer(http.NotFoundHandler())
	c := New(srv.URL, srv.Client())
	clk := &fakeClock{now: time.Unix(1000, 0)}
	c.limiter = newLimiter(clk.Now, clk.Sleep)
	srv.Close()
	if _, err := c.Show(context.Background(), 1); !Unavailable(err) {
		t.Fatalf("transport err=%v", err)
	}
}
