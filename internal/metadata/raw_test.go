package metadata

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

func TestRawPathSafe(t *testing.T) {
	for _, p := range []string{"/movie/550", "/search/movie", "/release-group/6b4f", "/w/api.php", "/works/OL1W.json",
		"/x/a%20b", "/x/%3Fq", "/", "/x/"} {
		if !SafeRawPath(p) {
			t.Errorf("%q refused", p)
		}
	}
	for _, p := range []string{"", "movie", "/../x", "/x/..", "/x/../y", "/%2e%2e/x", "/%2E%2E/x", "/x/%2e%2E", "/.%2e/x",
		"/%2e./x", "/%252e%252e/x", "/./x", "/%2e/x", "//evil.example/x", "/%2F%2Fevil.example/x", "/%2f/evil",
		"/https://evil.example/x", "/x\\y", "/x%5cy", "/x%5C..%5Cy", "/x%zz", "/x//y",
		"/%25252e%25252e/x"} {
		if SafeRawPath(p) {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestRawURLStaysOnBase(t *testing.T) {
	u, err := RawURL("https://api.example.org/3/", "/movie/550", url.Values{"language": {"ru-RU"}})
	if err != nil || u != "https://api.example.org/3/movie/550?language=ru-RU" {
		t.Fatalf("%q %v", u, err)
	}
	if u, err := RawURL("https://api.example.org/3", "/x", nil); err != nil || u != "https://api.example.org/3/x" {
		t.Fatalf("%q %v", u, err)
	}
	for _, p := range []string{"//evil.example/x", "/../x", "/%2e%2e/x", "@evil.example/x", "evil.example", "/x\\y"} {
		if u, err := RawURL("https://api.example.org/3", p, nil); err == nil {
			t.Errorf("%q: %q", p, u)
		}
	}
	if _, err := RawURL("", "/x", nil); err == nil {
		t.Error("no base accepted")
	}
}

func TestGetterRaw(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/busy":
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
		case "/gone":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("nope"))
		case "/big":
			w.Write([]byte(strings.Repeat("x", MaxRaw+1)))
		case "/full":
			w.Write([]byte(strings.Repeat("x", MaxRaw)))
		}
	}))
	defer srv.Close()
	g := Getter{Service: "t", Retry: func(s int) bool { return s == 503 }, RetryDelay: time.Millisecond, MaxRetries: 2}
	st, ct, body, err := g.Raw(context.Background(), srv.URL+"/busy", "/busy")
	if err != nil || st != 200 || ct != "application/json" || string(body) != `{"ok":true}` || calls.Load() != 2 {
		t.Fatalf("%d %q %q %v calls=%d", st, ct, body, err, calls.Load())
	}
	st, ct, body, err = g.Raw(context.Background(), srv.URL+"/gone", "/gone")
	if err != nil || st != 404 || ct != "text/plain" || string(body) != "nope" {
		t.Fatalf("404 passed through: %d %q %q %v", st, ct, body, err)
	}
	if _, _, _, err := g.Raw(context.Background(), srv.URL+"/big", "/big"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the cap: %v", err)
	}
	if _, _, body, err := g.Raw(context.Background(), srv.URL+"/full", "/full"); err != nil || len(body) != MaxRaw {
		t.Fatalf("at the cap: %d %v", len(body), err)
	}
}

func TestGetterRawWithin(t *testing.T) {
	var hits atomic.Int32
	var ua atomic.Value
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect followed off the base host: %s", r.URL)
	}))
	defer evil.Close()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ua.Store(r.Header.Get("User-Agent"))
		switch r.URL.Path {
		case "/base/off":
			http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
		case "/base/on":
			http.Redirect(w, r, srv.URL+"/base/target", http.StatusFound)
		default:
			w.Write([]byte(r.URL.Path))
		}
	}))
	defer srv.Close()
	g := Getter{Service: "t", UserAgent: "Antresol/test", Limiter: &Limiter{}}
	sem := make(chan struct{}, 1)
	base := srv.URL + "/base"
	ctx := context.Background()

	for _, tc := range []struct {
		name, path string
		status     int
		body       string
	}{
		{"plain", "/a/b%20c", 200, "/base/a/b c"},
		{"same-host redirect followed", "/on", 200, "/base/target"},
		{"off-host redirect returned", "/off", http.StatusFound, ""},
	} {
		st, _, body, err := g.RawWithin(ctx, sem, base, tc.path, nil)
		if err != nil || st != tc.status || (tc.body != "" && string(body) != tc.body) {
			t.Errorf("%s: %d %q %v", tc.name, st, body, err)
		}
	}
	if ua.Load() != "Antresol/test" {
		t.Errorf("User-Agent %v", ua.Load())
	}
	n := hits.Load()
	for _, p := range []string{"//evil.example/x", "/%2e%2e/x", "/../x", "x", "/x\\y"} {
		if _, _, _, err := g.RawWithin(ctx, sem, base, p, nil); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if hits.Load() != n {
		t.Error("an unsafe path reached the server")
	}
	// the call waits on sem
	sem <- struct{}{}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, _, _, err := g.RawWithin(short, sem, base, "/x", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("did not wait on sem: %v", err)
	}
}
