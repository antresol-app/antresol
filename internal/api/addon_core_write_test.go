package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

// coreWrite is the addon env with a movies root on disk, the addons "w"
// and "w2" (library.write), "img" (images), and the profile Маша.
type coreWrite struct {
	*addonEnv
	repo                    *library.Repo
	refs                    *addons.RefStore
	jobs                    *library.Jobs
	img                     *metadata.Images
	movies, outside, images string
	masha                   int64
}

func newCoreWrite(t *testing.T) *coreWrite {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &coreWrite{movies: filepath.Join(base, "movies"), outside: filepath.Join(base, "outside"), images: filepath.Join(base, "images")}
	for _, d := range []string{c.movies, c.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(c.outside, "Secret (2000)", "Secret.2000.mkv"))
	if err := os.Symlink(c.outside, filepath.Join(c.movies, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(c.outside, "later"), filepath.Join(c.movies, "dangling")); err != nil {
		t.Fatal(err)
	}
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		c.jobs = &library.Jobs{Scanner: &library.Scanner{Repo: d.Library, Movies: c.movies}}
		c.refs = &addons.RefStore{DB: d.DB}
		d.Jobs, d.AddonRefs = c.jobs, c.refs
		c.img = &metadata.Images{Dir: c.images}
		d.Images = c.img
	})
	c.repo = library.NewRepo(c.db)
	hello := c.fake.installed["hello"]
	c.fake.mu.Lock()
	for id, perms := range map[string][]string{"w": {"library.write"}, "w2": {"library.write"}, "img": {"images"}} {
		in := hello
		in.ID, in.Manifest.ID, in.Manifest.Permissions = id, id, perms
		c.fake.installed[id], c.fake.tokens["tok-"+id] = in, id
	}
	c.fake.mu.Unlock()
	users, err := c.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Name == "Маша" {
			c.masha = u.ID
		}
	}
	return c
}

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// call calls the core API as the addon of tok with a JSON body.
func (c *coreWrite) call(method, tok, path string, body any, wantCode int, out any) string {
	c.t.Helper()
	b, _ := json.Marshal(body)
	w := c.do(method, "/api/addon-core/v1"+path, string(b), nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
	if w.Code != wantCode {
		c.t.Fatalf("%s %s %s: %d %s (want %d)", method, path, b, w.Code, w.Body, wantCode)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, w.Body)
		}
	}
	return w.Body.String()
}

func (c *coreWrite) refOf(path string) (addon, ref string) {
	c.t.Helper()
	err := c.db.QueryRow(`SELECT r.addon_id, r.ref FROM addon_file_refs r JOIN files f ON f.id=r.file_id WHERE f.path=?`, path).Scan(&addon, &ref)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		c.t.Fatal(err)
	}
	return addon, ref
}

func (c *coreWrite) owns(user int64, path string) bool {
	c.t.Helper()
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM item_owners o JOIN files f ON f.item_id=o.item_id WHERE f.path=? AND o.user_id=?`,
		path, user).Scan(&n); err != nil {
		c.t.Fatal(err)
	}
	return n > 0
}

func (c *coreWrite) inLibrary(path string) bool {
	_, err := c.repo.FileByPath(context.Background(), path)
	if err != nil && !errors.Is(err, library.ErrNotFound) {
		c.t.Fatal(err)
	}
	return err == nil
}

// importBody is a POST /library/import body.
type importBody struct {
	Paths       []string `json:"paths"`
	Ref         string   `json:"ref,omitempty"`
	OwnerUserID int64    `json:"ownerUserId,omitempty"`
}

func TestAddonCoreWriteImport(t *testing.T) {
	c := newCoreWrite(t)
	dune := filepath.Join(c.movies, "Dune (2021)", "Dune.2021.mkv")
	arrival := filepath.Join(c.movies, "Arrival (2016)", "Arrival.2016.mkv")
	later := filepath.Join(c.movies, "Later (2027)", "Later.2027.mkv")
	write(t, dune)
	write(t, arrival)
	ch, cancel := c.bus.Subscribe()
	defer cancel()

	var res addonkit.ImportResult
	c.call("POST", "tok-w", "/library/import", importBody{Paths: []string{dune, arrival, later}, Ref: "r1", OwnerUserID: c.masha}, 200, &res)
	if len(res.Files) != 2 || res.Files[0].Path != dune || res.Files[1].Path != arrival ||
		res.Files[0].FileID == 0 || res.Files[0].ItemID == 0 || res.Files[1].ItemID == 0 {
		t.Fatalf("files %+v", res.Files)
	}
	if !reflect.DeepEqual(res.Missing, []string{later}) || len(res.Taken) != 0 {
		t.Fatalf("missing %v taken %v", res.Missing, res.Taken)
	}
	for _, p := range []string{dune, arrival} {
		if a, r := c.refOf(p); a != "w" || r != "r1" {
			t.Fatalf("%s ref %q %q", p, a, r)
		}
		if !c.owns(c.masha, p) {
			t.Fatalf("%s not claimed", p)
		}
	}
	if !c.jobs.Status().Queued {
		t.Fatal("enrichers not triggered")
	}
	select {
	case ev := <-ch:
		if ev.Type != events.LibraryUpdated {
			t.Fatalf("event %s", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no library.updated")
	}

	// a folder imports the files below it; another addon's file is taken
	// and keeps its ref; a non-media file on disk is missing
	heat := filepath.Join(c.movies, "Heat (1995)", "Heat.1995.mkv")
	notes := filepath.Join(c.movies, "Heat (1995)", "notes.txt")
	write(t, heat)
	write(t, notes)
	if b, err := c.refs.Bind(context.Background(), "w2", "x", []string{dune}); err != nil || len(b.Taken) != 1 {
		t.Fatalf("pre-bind %+v %v", b, err) // dune is w's already
	}
	c.call("POST", "tok-w2", "/library/import", importBody{Paths: []string{filepath.Dir(heat), notes, dune}, Ref: "x"}, 200, &res)
	if len(res.Files) != 2 || res.Files[0].Path != heat || res.Files[1].Path != dune {
		t.Fatalf("second import files %+v", res.Files)
	}
	if !reflect.DeepEqual(res.Taken, []string{dune}) || !reflect.DeepEqual(res.Missing, []string{notes}) {
		t.Fatalf("second import taken %v missing %v", res.Taken, res.Missing)
	}
	if a, r := c.refOf(dune); a != "w" || r != "r1" {
		t.Fatalf("dune re-bound: %q %q", a, r)
	}
	if a, r := c.refOf(heat); a != "w2" || r != "x" {
		t.Fatalf("heat ref %q %q", a, r)
	}

	// an owner without a ref is not given another addon's files; a path
	// given twice and an empty folder are missing once each
	empty := filepath.Join(c.movies, "Empty (2020)")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	later = filepath.Join(c.movies, "Later (2027)", "Later.mkv")
	c.call("POST", "tok-w", "/library/import", importBody{Paths: []string{heat, arrival, later, later, empty, empty}, OwnerUserID: c.masha}, 200, &res)
	if !reflect.DeepEqual(res.Taken, []string{heat}) || !reflect.DeepEqual(res.Missing, []string{later, empty}) {
		t.Fatalf("owner-only import taken %v missing %v", res.Taken, res.Missing)
	}
	if c.owns(c.masha, heat) {
		t.Fatal("w claimed w2's file for the owner")
	}
	if a, r := c.refOf(heat); a != "w2" || r != "x" {
		t.Fatalf("heat ref after owner-only import %q %q", a, r)
	}
}

func TestAddonCoreWriteImportRefused(t *testing.T) {
	c := newCoreWrite(t)
	if err := os.Symlink(c.movies, filepath.Join(c.movies, "self")); err != nil {
		t.Fatal(err)
	}
	// on disk, not yet in the library: a refused import must not scan it in
	waiting := filepath.Join(c.movies, "Heat (1995)", "Heat.1995.mkv")
	write(t, waiting)
	many := make([]string, addonMaxPaths+1)
	for i := range many {
		many[i] = waiting
	}
	for name, b := range map[string]importBody{
		"outside":             {Paths: []string{filepath.Join(c.outside, "Secret (2000)", "Secret.2000.mkv")}},
		"dot-dot":             {Paths: []string{c.movies + "/../outside/Secret (2000)/Secret.2000.mkv"}},
		"relative":            {Paths: []string{"movies/Heat (1995)/Heat.1995.mkv"}},
		"symlink escape":      {Paths: []string{filepath.Join(c.movies, "escape", "Secret (2000)", "Secret.2000.mkv")}},
		"new under escape":    {Paths: []string{filepath.Join(c.movies, "escape", "New (2026)", "New.mkv")}},
		"dangling symlink":    {Paths: []string{filepath.Join(c.movies, "dangling")}},
		"under dangling":      {Paths: []string{filepath.Join(c.movies, "dangling", "x.mkv")}},
		"one bad of two":      {Paths: []string{waiting, c.outside}},
		"none":                {Paths: nil},
		"too many":            {Paths: many},
		"bad ref":             {Paths: []string{waiting}, Ref: "ключ"},
		"unknown owner":       {Paths: []string{waiting}, OwnerUserID: 9999},
		"negative owner":      {Paths: []string{waiting}, OwnerUserID: -1},
		"the filesystem root": {Paths: []string{"/"}},
		"a library root":      {Paths: []string{c.movies}},
		"a root via symlink":  {Paths: []string{filepath.Join(c.movies, "self")}},
	} {
		w := c.do("POST", "/api/addon-core/v1/library/import", mustJSON(t, b), nil, "Authorization", "Bearer tok-w")
		if w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
		if name == "outside" && !strings.Contains(w.Body.String(), "Путь вне медиатеки: "+b.Paths[0]) {
			t.Errorf("outside message %s", w.Body)
		}
	}
	if c.inLibrary(waiting) || c.jobs.Status().Queued {
		t.Fatal("a refused import scanned")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// scanned: the files at paths, written and scanned into the library.
func (c *coreWrite) scanned(paths ...string) {
	c.t.Helper()
	for _, p := range paths {
		write(c.t, p)
	}
	if _, err := c.jobs.Scanner.ScanAll(context.Background()); err != nil {
		c.t.Fatal(err)
	}
	for _, p := range paths {
		if !c.inLibrary(p) {
			c.t.Fatalf("%s not scanned", p)
		}
	}
}

func TestAddonCoreWriteRefs(t *testing.T) {
	c := newCoreWrite(t)
	a := filepath.Join(c.movies, "A (2001)", "A.2001.mkv")
	b := filepath.Join(c.movies, "B (2002)", "B.2002.mkv")
	o := filepath.Join(c.movies, "O (2003)", "O.2003.mkv")
	c.scanned(a, b, o)
	if _, err := c.refs.Bind(context.Background(), "w2", "r1", []string{o}); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(c.movies, "Later (2027)", "Later.mkv")
	paths := func(p ...string) map[string][]string { return map[string][]string{"paths": append([]string{}, p...)} }

	var bound addonkit.Bound
	c.call("PUT", "tok-w", "/library/refs/r1", paths(a, later, o), 200, &bound)
	if bound.Bound != 1 || !reflect.DeepEqual(bound.Missing, []string{later}) || !reflect.DeepEqual(bound.Taken, []string{o}) {
		t.Fatalf("put %+v", bound)
	}
	// PUT replaces the set: a leaves r1, b joins it; w2's file is untouched
	c.call("PUT", "tok-w", "/library/refs/r1", paths(b), 200, &bound)
	if ad, _ := c.refOf(a); ad != "" {
		t.Fatal("a kept r1 after a PUT without it")
	}
	if ad, r := c.refOf(b); ad != "w" || r != "r1" {
		t.Fatalf("b %q %q", ad, r)
	}
	if ad, r := c.refOf(o); ad != "w2" || r != "r1" {
		t.Fatalf("w2's file %q %q", ad, r)
	}
	// a ref with '/' goes percent-encoded
	c.call("PUT", "tok-w", "/library/refs/s%2F1", paths(a), 200, &bound)
	if ad, r := c.refOf(a); ad != "w" || r != "s/1" {
		t.Fatalf("encoded ref %q %q", ad, r)
	}
	for name, body := range map[string]map[string][]string{
		"outside":          paths(filepath.Join(c.outside, "Secret (2000)", "Secret.2000.mkv")),
		"symlink escape":   paths(filepath.Join(c.movies, "escape", "Secret (2000)", "Secret.2000.mkv")),
		"dangling symlink": paths(filepath.Join(c.movies, "dangling")),
		"one bad":          paths(a, "relative.mkv"),
	} {
		c.call("PUT", "tok-w", "/library/refs/r1", body, 400, nil)
		if ad, r := c.refOf(b); ad != "w" || r != "r1" {
			t.Fatalf("%s: a refused PUT changed the set: %q %q", name, ad, r)
		}
	}
	c.call("PUT", "tok-w", "/library/refs/"+strings.Repeat("x", 129), paths(a), 400, nil)
	// a root itself is refused; no paths key is 400, [] clears
	c.call("PUT", "tok-w", "/library/refs/r1", paths(c.movies), 400, nil)
	c.call("PUT", "tok-w", "/library/refs/r1", map[string]any{}, 400, nil)
	c.call("PUT", "tok-w", "/library/refs/r1", map[string]any{"paths": nil}, 400, nil)
	if ad, _ := c.refOf(b); ad != "w" {
		t.Fatal("a refused PUT cleared the set")
	}
	c.call("PUT", "tok-w", "/library/refs/tmp", paths(a), 200, nil)
	c.call("PUT", "tok-w", "/library/refs/tmp", paths(), 200, &bound)
	if ad, _ := c.refOf(a); ad != "" || bound.Bound != 0 {
		t.Fatalf("PUT [] did not clear: %q %+v", ad, bound)
	}
	c.call("PUT", "tok-w", "/library/refs/s%2F1", paths(a), 200, nil)

	c.call("DELETE", "tok-w", "/library/refs/r1", nil, 204, nil)
	if ad, _ := c.refOf(b); ad != "" {
		t.Fatal("DELETE left b's ref")
	}
	if ad, _ := c.refOf(o); ad != "w2" {
		t.Fatal("DELETE removed another addon's ref")
	}

	pairs := func(p ...addonkit.RefPath) map[string][]addonkit.RefPath {
		return map[string][]addonkit.RefPath{"refs": p}
	}
	c.call("POST", "tok-w", "/library/refs", pairs(addonkit.RefPath{Ref: "x", Path: b}, addonkit.RefPath{Ref: "y", Path: later},
		addonkit.RefPath{Ref: "z", Path: o}), 200, &bound)
	if bound.Bound != 1 || !reflect.DeepEqual(bound.Missing, []string{later}) || !reflect.DeepEqual(bound.Taken, []string{o}) {
		t.Fatalf("batch %+v", bound)
	}
	if ad, r := c.refOf(b); ad != "w" || r != "x" {
		t.Fatalf("batch b %q %q", ad, r)
	}
	c.call("POST", "tok-w", "/library/refs", pairs(addonkit.RefPath{Ref: "", Path: a}), 400, nil)
	c.call("POST", "tok-w", "/library/refs", pairs(addonkit.RefPath{Ref: "q", Path: a}, addonkit.RefPath{Ref: "q", Path: c.outside}), 400, nil)
	c.call("POST", "tok-w", "/library/refs", pairs(addonkit.RefPath{Ref: "q", Path: c.movies}), 400, nil)
	if _, r := c.refOf(a); r != "s/1" {
		t.Fatalf("a refused batch bound a: %q", r)
	}
}

func TestAddonCoreWriteOwners(t *testing.T) {
	c := newCoreWrite(t)
	a := filepath.Join(c.movies, "A (2001)", "A.2001.mkv")
	b := filepath.Join(c.movies, "B (2002)", "B.2002.mkv")
	c.scanned(a, b)
	if _, err := c.refs.Bind(context.Background(), "w", "r1", []string{a}); err != nil {
		t.Fatal(err)
	}
	type body struct {
		Ref    string   `json:"ref,omitempty"`
		Paths  []string `json:"paths,omitempty"`
		UserID int64    `json:"userId"`
	}
	c.call("POST", "tok-w2", "/library/owners", body{Ref: "r1", UserID: c.masha}, 204, nil)
	if c.owns(c.masha, a) {
		t.Fatal("another addon's ref claimed a's file")
	}
	c.call("POST", "tok-w", "/library/owners", body{Ref: "r1", UserID: c.masha}, 204, nil)
	if !c.owns(c.masha, a) || c.owns(c.masha, b) {
		t.Fatal("claim by ref")
	}
	c.call("POST", "tok-w", "/library/owners", body{Paths: []string{b, filepath.Join(c.movies, "Later", "x.mkv")}, UserID: c.masha}, 204, nil)
	if !c.owns(c.masha, b) {
		t.Fatal("claim by paths")
	}
	for name, bd := range map[string]body{
		"both":         {Ref: "r1", Paths: []string{a}, UserID: c.masha},
		"neither":      {UserID: c.masha},
		"no user":      {Ref: "r1"},
		"unknown user": {Ref: "r1", UserID: 9999},
		"bad ref":      {Ref: "ключ", UserID: c.masha},
		"outside":      {Paths: []string{c.outside}, UserID: c.masha},
		"a root":       {Paths: []string{c.movies}, UserID: c.masha},
	} {
		if w := c.do("POST", "/api/addon-core/v1/library/owners", mustJSON(t, bd), nil, "Authorization", "Bearer tok-w"); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestAddonCoreWriteImages(t *testing.T) {
	c := newCoreWrite(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("\x89PNG fake"))
	}))
	defer srv.Close()
	c.img.HTTP = srv.Client()
	var out map[string]string
	c.call("POST", "tok-img", "/images", map[string]string{"url": srv.URL + "/p.png"}, 200, &out)
	if out["name"] != metadata.Name(srv.URL+"/p.png") {
		t.Fatalf("name %v", out)
	}
	if _, err := os.Stat(filepath.Join(c.images, out["name"])); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"http://example.com/p.png", "ftp://example.com/p.png", "/p.png", "", "https:///p.png", "https://u:p@example.com/p.png"} {
		c.call("POST", "tok-img", "/images", map[string]string{"url": u}, 400, nil)
	}
}

// An image's redirects stay on https.
func TestAddonCoreWriteImageRedirects(t *testing.T) {
	c := newCoreWrite(t)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("\x89PNG plain"))
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-http":
			http.Redirect(w, r, plain.URL+"/p.png", http.StatusFound)
		case "/to-https":
			http.Redirect(w, r, "/p.png", http.StatusFound)
		default:
			w.Write([]byte("\x89PNG fake"))
		}
	}))
	defer srv.Close()
	c.img.HTTP = srv.Client()
	c.call("POST", "tok-img", "/images", map[string]string{"url": srv.URL + "/to-http"}, 502, nil)
	if _, ok := c.img.Cached(srv.URL + "/to-http"); ok {
		t.Fatal("an image redirected to http was cached")
	}
	var out map[string]string
	c.call("POST", "tok-img", "/images", map[string]string{"url": srv.URL + "/to-https"}, 200, &out)
	if out["name"] != metadata.Name(srv.URL+"/to-https") {
		t.Fatalf("name %v", out)
	}
	if c.img.HTTP.CheckRedirect != nil {
		t.Fatal("the shared client was changed")
	}
}

func TestAddonCoreWritePermissions(t *testing.T) {
	c := newCoreWrite(t)
	routes := [][2]string{{"POST", "/library/import"}, {"PUT", "/library/refs/r1"}, {"DELETE", "/library/refs/r1"},
		{"POST", "/library/refs"}, {"POST", "/library/owners"}}
	for _, tok := range []string{"tok-lib", "tok-img"} { // library.read, images
		for _, rt := range routes {
			w := c.do(rt[0], "/api/addon-core/v1"+rt[1], `{"paths":["x"]}`, nil, "Authorization", "Bearer "+tok)
			if w.Code != 403 || !strings.Contains(w.Body.String(), msgAddonPermission) {
				t.Errorf("%s %s with %s: %d %s", rt[0], rt[1], tok, w.Code, w.Body)
			}
		}
	}
	for _, tok := range []string{"tok-w", "tok-lib"} {
		if w := c.do("POST", "/api/addon-core/v1/images", `{"url":"http://x"}`, nil, "Authorization", "Bearer "+tok); w.Code != 403 {
			t.Errorf("images with %s: %d %s", tok, w.Code, w.Body)
		}
	}
}

// Two imports at once both succeed, each finding its own file (importMu
// keeps them apart; run with -race).
func TestAddonCoreWriteImportConcurrent(t *testing.T) {
	c := newCoreWrite(t)
	a := filepath.Join(c.movies, "A (2001)", "A.2001.mkv")
	b := filepath.Join(c.movies, "B (2002)", "B.2002.mkv")
	write(t, a)
	write(t, b)
	codes := make(chan string, 2)
	for _, p := range []string{a, b} {
		body := mustJSON(t, importBody{Paths: []string{p}, Ref: "r"}) // t.Fatal stays on the test goroutine
		go func() {
			w := c.do("POST", "/api/addon-core/v1/library/import", body, nil,
				"Authorization", "Bearer tok-w")
			var res addonkit.ImportResult
			json.Unmarshal(w.Body.Bytes(), &res)
			if w.Code != 200 || len(res.Files) != 1 || res.Files[0].Path != p {
				codes <- p + ": " + w.Body.String()
				return
			}
			codes <- ""
		}()
	}
	for range 2 {
		if s := <-codes; s != "" {
			t.Error(s)
		}
	}
}
