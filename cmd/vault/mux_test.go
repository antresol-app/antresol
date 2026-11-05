package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/jellyfin"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/opds"
	"github.com/antresol-app/antresol/internal/subsonic"
	"github.com/antresol-app/antresol/internal/webui"
)

// TestRestPOSTFormNotCSRFBlocked: music apps POST plain forms to /rest;
// buildMux keeps them outside the web router's csrf guard, which still
// covers /api.
func TestRestPOSTFormNotCSRFBlocked(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	users, throttle := auth.NewStore(d), auth.NewThrottle()
	u, err := users.CreateFirstAdmin(context.Background(), "Папа", "secret")
	if err != nil {
		t.Fatal(err)
	}
	app, err := users.SetAppPassword(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	web := api.NewRouter(api.Deps{Users: users, Sessions: auth.NewSessions(d), Throttle: throttle})
	mux := buildMux(web, &subsonic.Server{Users: users, Throttle: throttle}, nil, nil)

	post := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.RemoteAddr = "192.168.1.20:50000"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	w := post("/rest/ping", url.Values{"u": {"Папа"}, "p": {app}, "f": {"json"}}.Encode())
	if w.Code != 200 || strings.Contains(w.Body.String(), "Запрос с другого сайта отклонён") {
		t.Fatalf("/rest/ping: %d %s", w.Code, w.Body)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out["subsonic-response"]["status"] != "ok" {
		t.Fatalf("ping %s", w.Body)
	}
	w = post("/api/login", url.Values{"userId": {"1"}, "password": {"secret"}}.Encode())
	if w.Code != 403 || !strings.Contains(w.Body.String(), "Запрос с другого сайта отклонён") {
		t.Fatalf("/api/login form must stay blocked: %d %s", w.Code, w.Body)
	}
	// the SPA and the API are still behind the mux
	r := httptest.NewRequest("GET", "/api/health", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("/api/health %d", w.Code)
	}
}

// TestOPDSNotSPA: /opds is served by the OPDS handler (Atom), not the
// SPA's index.html, and outside the API router.
func TestOPDSNotSPA(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	users, throttle := auth.NewStore(d), auth.NewThrottle()
	u, err := users.CreateFirstAdmin(context.Background(), "Папа", "secret")
	if err != nil {
		t.Fatal(err)
	}
	app, err := users.SetAppPassword(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	network := &auth.Network{}
	web := api.NewRouter(api.Deps{Users: users, Sessions: auth.NewSessions(d), Throttle: throttle, Network: network})
	mux := buildMux(web, &subsonic.Server{Users: users, Throttle: throttle},
		&opds.Handler{Library: library.NewRepo(d), Users: users, Network: network, Throttle: throttle}, nil)
	for _, path := range []string{"/opds", "/opds/new"} {
		r := httptest.NewRequest("GET", path, nil)
		r.SetBasicAuth("Папа", app)
		r.RemoteAddr = "192.168.1.20:50000"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if ct := w.Header().Get("Content-Type"); w.Code != 200 || !strings.HasPrefix(ct, "application/atom+xml") {
			t.Fatalf("%s: %d %q %.200s", path, w.Code, ct, w.Body)
		}
	}
	r := httptest.NewRequest("GET", "/opds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no credentials: %d", w.Code)
	}
}

func jellyfinMux(t *testing.T) (*http.ServeMux, *jellyfin.Server, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	users, throttle, network := auth.NewStore(d), auth.NewThrottle(), &auth.Network{}
	u, err := users.CreateFirstAdmin(context.Background(), "Аня", "secret")
	if err != nil {
		t.Fatal(err)
	}
	app, err := users.SetAppPassword(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	js := newJellyfin(d, users, network, throttle)
	web := api.NewRouter(api.Deps{Users: users, Sessions: auth.NewSessions(d), Throttle: throttle, Network: network})
	return buildMux(web, &subsonic.Server{Users: users, Throttle: throttle}, nil, js), js, app
}

func newJellyfin(d *sql.DB, users *auth.Store, network *auth.Network, throttle *auth.Throttle) *jellyfin.Server {
	return &jellyfin.Server{Users: users, Tokens: auth.NewAppTokens(d), Network: network, Throttle: throttle, DB: d, Version: "12.1.0"}
}

func serve(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.RemoteAddr = "192.168.1.20:50000"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestJellyfinOutsideCSRF: the Jellyfin login is not behind the web
// router's csrf guard (no Content-Type, a cross-site fetch header).
func TestJellyfinOutsideCSRF(t *testing.T) {
	mux, _, app := jellyfinMux(t)
	w := serve(mux, "POST", "/Users/AuthenticateByName", `{"Username":"Аня","Pw":"`+app+`"}`,
		map[string]string{"Sec-Fetch-Site": "cross-site", "Authorization": `MediaBrowser Client="Infuse", DeviceId="x"`})
	if w.Code != 200 || strings.Contains(w.Body.String(), "Запрос с другого сайта") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// TestJellyfinNotShadowingSPA: web app routes still reach the SPA, Jellyfin
// paths reach Jellyfin, and no first segment of a web route is a Jellyfin
// one.
func TestJellyfinNotShadowingSPA(t *testing.T) {
	mux, _, _ := jellyfinMux(t)
	for _, p := range []string{"/search", "/item/1", "/music", "/books", "/manage/migrate", "/welcome", "/profiles", "/movies", "/series"} {
		w := serve(mux, "GET", p, "", nil)
		if w.Header().Get("Content-Security-Policy") == "" || w.Code == 401 {
			t.Fatalf("%s did not reach the SPA: %d %v", p, w.Code, w.Header())
		}
		if b, err := fs.ReadFile(webui.FS, "index.html"); err != nil || w.Code != 200 || w.Body.String() != string(b) {
			t.Fatalf("%s: not index.html (%d)", p, w.Code)
		}
	}
	if w := serve(mux, "GET", "/Search/Hints?searchTerm=x", "", nil); w.Code != 401 || w.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("/Search/Hints: %d", w.Code)
	}
	if w := serve(mux, "GET", "/System/Info/Public", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"ProductName":"Jellyfin Server"`) {
		t.Fatalf("/System/Info/Public: %d %s", w.Code, w.Body)
	}
	if w := serve(mux, "GET", "/api/health", "", nil); w.Code != 200 {
		t.Fatalf("/api/health: %d", w.Code)
	}
	src, err := os.ReadFile("../../web/src/app/routes.tsx")
	if err != nil {
		t.Fatal(err)
	}
	segs := 0
	for _, m := range regexp.MustCompile(`path:\s*'/?([^'/:*]+)`).FindAllStringSubmatch(string(src), -1) {
		segs++
		if jellyfin.IsJellyfinPath("/" + m[1]) {
			t.Errorf("web route segment %q is a Jellyfin path", m[1])
		}
	}
	if segs < 10 {
		t.Fatalf("only %d route segments found in routes.tsx", segs)
	}
}

// TestJellyfinListenerOnlyJellyfin: the :8096 listener serves the Jellyfin
// server alone: the web API is not there.
func TestJellyfinListenerOnlyJellyfin(t *testing.T) {
	_, js, _ := jellyfinMux(t)
	if w := serve(js, "GET", "/System/Info/Public", "", nil); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if w := serve(js, "GET", "/api/health", "", nil); w.Code != 404 || w.Body.Len() != 0 {
		t.Fatalf("/api/health on the Jellyfin listener: %d %s", w.Code, w.Body)
	}
}
