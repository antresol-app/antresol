package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

const metaKey = "fedcba9876543210fedcba9876543210" // gitleaks:allow (a test value)

// upstreamReq is the last request an upstream metadata server got, and
// how many it got.
type upstreamReq struct {
	path, query, auth, cookie string
	apiKeys                   []string
	userAgent                 string
	n                         int
}

type seen struct {
	mu sync.Mutex
	r  upstreamReq
}

func (s *seen) get() upstreamReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r
}

// coreMeta is the addon env with a TMDB and a MusicBrainz upstream, the
// addon "meta" (metadata) and the core's key.
type coreMeta struct {
	*addonEnv
	tm, mb *seen
	tmURL  string
}

func newCoreMeta(t *testing.T, withMB bool) *coreMeta {
	t.Helper()
	c := &coreMeta{tm: &seen{}, mb: &seen{}}
	upstream := func(s *seen) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			s.r = upstreamReq{path: r.URL.EscapedPath(), query: r.URL.RawQuery, auth: r.Header.Get("Authorization"),
				cookie: r.Header.Get("Cookie"), apiKeys: r.URL.Query()["api_key"], userAgent: r.Header.Get("User-Agent"), n: s.r.n + 1}
			s.mu.Unlock()
			if strings.HasSuffix(r.URL.Path, "/missing") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"status_message":"not found, api_key=` + metaKey + `"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json;charset=utf-8")
			w.Header().Set("X-Upstream", metaKey)
			w.Header().Set("Set-Cookie", "upstream=1")
			w.Write([]byte(`{"id":550,"title":"Бойцовский клуб"}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	tmSrv, mbSrv := upstream(c.tm), upstream(c.mb)
	c.tmURL = tmSrv.URL
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		d.Metadata.TMDB = &tmdb.Client{Key: metaKey, BaseURL: tmSrv.URL + "/3"}
		if withMB {
			d.Metadata.MusicBrainz = &musicbrainz.Client{BaseURL: mbSrv.URL + "/ws/2", UserAgent: "Antresol/test", Limiter: &metadata.Limiter{}}
		}
	})
	addMetaAddon(c.addonEnv)
	return c
}

// addMetaAddon installs the addon "meta" (metadata), token tok-meta.
func addMetaAddon(e *addonEnv) {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	in := e.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "meta", "meta", []string{"metadata"}
	e.fake.installed["meta"], e.fake.tokens["tok-meta"] = in, "meta"
}

func (c *coreMeta) get(method, tok, path string, hdr ...string) *httptest.ResponseRecorder {
	return c.do(method, "/api/addon-core/v1/metadata/"+path, "", nil, append([]string{"Authorization", "Bearer " + tok}, hdr...)...)
}

func noKey(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), metaKey) {
		t.Fatalf("key in the body: %s", w.Body)
	}
	for k, vs := range w.Header() {
		for _, v := range vs {
			if strings.Contains(v, metaKey) {
				t.Fatalf("key in header %s: %s", k, v)
			}
		}
	}
}

func TestAddonCoreMetadataPassThrough(t *testing.T) {
	c := newCoreMeta(t, true)
	w := c.get("GET", "tok-meta", "tmdb/movie/550?language=en-US&api_key=addon-own&API_KEY=x", "Cookie", "vault_session=s3cret")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json;charset=utf-8" || w.Body.String() != `{"id":550,"title":"Бойцовский клуб"}` {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	noKey(t, w)
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Upstream") != "" {
		t.Fatalf("upstream headers passed: %v", w.Header())
	}
	got := c.tm.get()
	if got.path != "/3/movie/550" || len(got.apiKeys) != 1 || got.apiKeys[0] != metaKey {
		t.Fatalf("upstream: %+v", got)
	}
	if got.auth != "" || got.cookie != "" || strings.Contains(got.query, "addon-own") || strings.Contains(got.query, "API_KEY") ||
		!strings.Contains(got.query, "language=en-US") {
		t.Fatalf("addon's credentials forwarded: %+v", got)
	}

	// an upstream status other than 200 is passed, the key redacted
	w = c.get("GET", "tok-meta", "tmdb/movie/missing")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "api_key=***") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	noKey(t, w)

	// HEAD: the headers, no body
	w = c.get("HEAD", "tok-meta", "tmdb/movie/550")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Type") == "" {
		t.Fatalf("HEAD: %d %q", w.Code, w.Body)
	}

	// MusicBrainz: the core's User-Agent, the query as given
	w = c.get("GET", "tok-meta", "musicbrainz/release-group/6b4f?fmt=json&inc=genres")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if mb := c.mb.get(); mb.path != "/ws/2/release-group/6b4f" || mb.userAgent != "Antresol/test" || mb.query != "fmt=json&inc=genres" || mb.auth != "" {
		t.Fatalf("musicbrainz upstream: %+v", mb)
	}
}

func TestAddonCoreMetadataBearerKeyNotFromAddon(t *testing.T) {
	long := strings.Repeat("v4token", 9)
	var gotAuth string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	e := newAddonEnv(t, func(d *Deps) { d.Metadata.TMDB = &tmdb.Client{Key: long, BaseURL: srv.URL} })
	addMetaAddon(e)
	w := e.do("GET", "/api/addon-core/v1/metadata/tmdb/movie/1", "", nil, "Authorization", "Bearer tok-meta")
	if w.Code != 200 || strings.Contains(w.Body.String(), long) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer "+long {
		t.Fatalf("upstream Authorization %q", gotAuth)
	}
}

func TestAddonCoreMetadataRefusesEscapes(t *testing.T) {
	c := newCoreMeta(t, true)
	for _, p := range []string{"tmdb/../x", "tmdb/movie/../../x", "tmdb/%2e%2e/x", "tmdb/%2E%2E/x", "tmdb/movie/%2e%2E",
		"tmdb/.%2e/x", "tmdb/%252e%252e/x", "tmdb//evil.example/x", "tmdb/%2F%2Fevil.example/x", "tmdb/https://evil.example/x",
		"tmdb/x%5c..%5cy", "tmdb/x\\y", "tmdb/", "tmdb", "musicbrainz//evil.example/x"} {
		w := c.get("GET", "tok-meta", p)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d %s", p, w.Code, w.Body)
		}
		noKey(t, w)
	}
	if n := c.tm.get().n + c.mb.get().n; n != 0 {
		t.Fatalf("upstream called %d times", n)
	}
}

func TestAddonCoreMetadataRoutes(t *testing.T) {
	c := newCoreMeta(t, false)
	// POST and others: 405
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if w := c.get(m, "tok-meta", "tmdb/movie/550"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
	// a source that is off, or unknown: 404
	for _, p := range []string{"musicbrainz/release-group/1", "openlibrary/works/OL1W.json", "wikidata/w/api.php", "imdb/x"} {
		if w := c.get("GET", "tok-meta", p); w.Code != http.StatusNotFound {
			t.Errorf("%q: %d %s", p, w.Code, w.Body)
		}
	}
	if w := c.get("GET", "tok-meta", "musicbrainz/release-group/1"); !strings.Contains(w.Body.String(), "Источник выключен") {
		t.Errorf("off source: %s", w.Body)
	}
	// the source is checked before the path: with no path an off source is
	// 404 and an on one 400, and the provider is not asked (the SDK's probe)
	for _, m := range []string{"GET", "HEAD"} {
		if w := c.get(m, "tok-meta", "musicbrainz"); w.Code != http.StatusNotFound {
			t.Errorf("%s off source, no path: %d", m, w.Code)
		}
		if w := c.get(m, "tok-meta", "tmdb"); w.Code != http.StatusBadRequest {
			t.Errorf("%s on source, no path: %d", m, w.Code)
		}
	}
	// no token: 401; no permission: 403 before anything else
	if w := c.do("GET", "/api/addon-core/v1/metadata/tmdb/movie/550", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}
	for _, p := range []string{"tmdb/movie/550", "tmdb/%2e%2e/x", "imdb/x"} {
		w := c.get("GET", "tok-hello", p)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), msgAddonPermission) {
			t.Errorf("no permission %q: %d %s", p, w.Code, w.Body)
		}
	}
	if c.tm.get().n != 0 {
		t.Fatal("upstream called without permission")
	}
}

func TestAddonCoreMetadataUpstreamDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	e := newAddonEnv(t, func(d *Deps) { d.Metadata.TMDB = &tmdb.Client{Key: metaKey, BaseURL: base} })
	addMetaAddon(e)
	w := e.do("GET", "/api/addon-core/v1/metadata/tmdb/movie/550", "", nil, "Authorization", "Bearer tok-meta")
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), metaKey) ||
		strings.Contains(w.Body.String(), strings.TrimPrefix(base, "http://")) || strings.Contains(w.Body.String(), "/movie/") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAddonCoreMetadataTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", metadata.MaxRaw+1)))
	}))
	defer srv.Close()
	e := newAddonEnv(t, func(d *Deps) { d.Metadata.TMDB = &tmdb.Client{BaseURL: srv.URL} })
	addMetaAddon(e)
	w := e.do("GET", "/api/addon-core/v1/metadata/tmdb/x", "", nil, "Authorization", "Bearer tok-meta")
	if w.Code != http.StatusBadGateway || w.Body.Len() > 1024 {
		t.Fatalf("%d (%d bytes)", w.Code, w.Body.Len())
	}
}
