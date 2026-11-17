package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const rawKey = "0123456789abcdef0123456789abcdef" // gitleaks:allow (a test value)

func TestRawSendsCoreKeyAndPassesAnswer(t *testing.T) {
	var gotKey, gotPath, gotLang atomic.Value
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(429) // retried
			return
		}
		gotKey.Store(r.URL.Query().Get("api_key"))
		gotPath.Store(r.URL.EscapedPath())
		gotLang.Store(r.URL.Query().Get("language"))
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte(`{"echo":"https://api.example/3/x?api_key=` + rawKey + `"}`))
	}))
	defer srv.Close()
	c := &Client{Key: rawKey, BaseURL: srv.URL + "/3", Backoff: time.Millisecond}
	st, ct, body, err := c.Raw(context.Background(), "/movie/550/a%20b", url.Values{"language": {"en-US"}, "api_key": {"addon"}})
	if err != nil || st != http.StatusTeapot || ct != "application/json;charset=utf-8" {
		t.Fatalf("%d %q %v", st, ct, err)
	}
	if gotKey.Load() != rawKey || gotPath.Load() != "/3/movie/550/a%20b" || gotLang.Load() != "en-US" {
		t.Fatalf("upstream got key=%v path=%v lang=%v", gotKey.Load(), gotPath.Load(), gotLang.Load())
	}
	if strings.Contains(string(body), rawKey) || !strings.Contains(string(body), "api_key=***") {
		t.Fatalf("key in body: %s", body)
	}
}

func TestRawBearerKey(t *testing.T) {
	long := strings.Repeat("b", 60)
	var gotAuth, gotKey atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		gotKey.Store(r.URL.Query().Get("api_key"))
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Client{Key: long, BaseURL: srv.URL}
	if _, _, _, err := c.Raw(context.Background(), "/x", url.Values{"api_key": {"addon"}}); err != nil {
		t.Fatal(err)
	}
	if gotAuth.Load() != "Bearer "+long || gotKey.Load() != "" {
		t.Fatalf("%v %v", gotAuth.Load(), gotKey.Load())
	}
}

func TestRawRefusesEscapes(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c := &Client{Key: rawKey, BaseURL: srv.URL + "/3"}
	for _, p := range []string{"//evil.example/x", "/../x", "/%2e%2e/x", "x", "/x\\y"} {
		if _, _, _, err := c.Raw(context.Background(), p, nil); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("upstream called %d times", hits.Load())
	}
}

func TestRawErrorsDoNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	_, _, _, err := (&Client{Key: rawKey, BaseURL: base}).Raw(context.Background(), "/x", nil)
	if err == nil || strings.Contains(err.Error(), rawKey) || strings.Contains(err.Error(), base) {
		t.Fatalf("%v", err)
	}
}

func TestRawTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 8<<20+1)))
	}))
	defer srv.Close()
	if _, _, _, err := (&Client{BaseURL: srv.URL}).Raw(context.Background(), "/x", nil); err == nil {
		t.Fatal("over 8 MiB accepted")
	}
}

// Addon calls wait on addonSem only: a full addonSem does not hold back the
// core's lookups, and a full sem does not hold back the addons'.
func TestRawOwnSemaphore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	c.init()
	c.sem, c.addonSem = make(chan struct{}, 1), make(chan struct{}, 1)

	c.addonSem <- struct{}{} // an addon call in flight
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.SearchMovie(ctx, "x", 0); err != nil {
		t.Fatalf("core lookup blocked by addons: %v", err)
	}
	short, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if _, _, _, err := c.Raw(short, "/x", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("addon call did not wait on addonSem: %v", err)
	}
	<-c.addonSem

	c.sem <- struct{}{} // a core lookup in flight
	if st, _, _, err := c.Raw(ctx, "/x", nil); err != nil || st != 200 {
		t.Fatalf("addon call blocked by the core: %d %v", st, err)
	}
	fresh := &Client{}
	fresh.init()
	if cap(fresh.addonSem) != 2 {
		t.Fatalf("addonSem size %d", cap(fresh.addonSem))
	}
}

// A redirect off TMDB's host is not followed: the key never leaves it.
func TestRawNoOffHostRedirect(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect followed: %s", r.URL)
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal?"+r.URL.RawQuery, http.StatusFound)
	}))
	defer srv.Close()
	st, _, body, err := (&Client{Key: rawKey, BaseURL: srv.URL}).Raw(context.Background(), "/x", nil)
	if err != nil || st != http.StatusFound || strings.Contains(string(body), rawKey) {
		t.Fatalf("%d %q %v", st, body, err)
	}
}
