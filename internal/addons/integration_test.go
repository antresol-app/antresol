package addons_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// stage2a is a real server for the end-to-end test: the router over a
// database, a file browser (roots dl and movies) wrapped by the hold
// index, the library scanner over movies, a TMDB pass-through to a fake
// upstream, and the addon manager installing from a local release folder,
// whose processes call back the router over HTTP.
type stage2a struct {
	t                 *testing.T
	h                 http.Handler
	mgr               *addons.Manager
	holds             *addons.Holds
	repo              *library.Repo
	base, dl, movies  string
	admin, adult, kid *http.Cookie

	upMu     sync.Mutex
	upstream []string // the fake TMDB's requests (path?query)
}

const metaKey = "integration-tmdb-key"

// newStage2a builds the server; opts change its Deps before the router is
// made (stage 2b adds playback).
func newStage2a(t *testing.T, opts ...func(*stage2a, *api.Deps)) *stage2a {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &stage2a{t: t, base: base, dl: filepath.Join(base, "dl"), movies: filepath.Join(base, "movies")}
	for _, d := range []string{s.dl, s.movies} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	s.repo = library.NewRepo(database)

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.upMu.Lock()
		s.upstream = append(s.upstream, r.URL.RequestURI())
		s.upMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":550,"title":"Fight Club"}`)
	}))
	t.Cleanup(up.Close)

	store := &addons.Store{DB: database}
	refs := &addons.RefStore{DB: database}
	s.holds = &addons.Holds{Refs: refs, Roots: []string{s.dl, s.movies},
		Installed: func(ctx context.Context) (map[string]string, error) {
			list, err := store.List(ctx)
			out := map[string]string{}
			for _, in := range list {
				out[in.ID] = in.Manifest.Name.RU
			}
			return out, err
		}}
	jobs := &library.Jobs{Scanner: &library.Scanner{Repo: s.repo, Movies: s.movies}}
	fc := files.ServiceConfig{
		Sandbox: files.NewSandbox(base, []files.Root{{ID: "dl", Title: "dl", Dir: s.dl}, {ID: "movies", Title: "movies", Dir: s.movies}}),
		DB:      database, Library: s.repo, DataDir: t.TempDir(),
		LibraryRoots: []string{s.movies},
		Hold:         jobs.Scanner.Hold,
	}
	s.holds.Wrap(&fc)

	run, err := os.MkdirTemp("/tmp", "ad") // a short socket path
	if err != nil {
		t.Fatal(err)
	}
	s.mgr = &addons.Manager{Store: store, Resolver: &addons.Resolver{}, Refs: refs, Holds: s.holds,
		Dev: t.TempDir(), Sup: &addons.Supervisor{Dir: t.TempDir(), RunDir: run, Locale: "ru"}}
	s.mgr.Sup.Mint = s.mgr.Mint
	s.holds.Hooks = &addons.Hooks{T: s.mgr}
	if err := s.holds.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	users := auth.NewStore(database)
	deps := api.Deps{DB: database, Users: users, Sessions: auth.NewSessions(database), Events: &events.Bus{},
		Library: s.repo, Jobs: jobs, Files: files.NewService(fc),
		Addons: s.mgr, AddonRefs: refs, AddonHolds: s.holds}
	deps.Metadata.TMDB = &tmdb.Client{Key: metaKey, BaseURL: up.URL + "/3"}
	for _, o := range opts {
		o(s, &deps)
	}
	s.h = api.NewRouter(deps)
	core := httptest.NewServer(s.h) // the addons' ANTRESOL_CORE_URL
	s.mgr.Sup.CoreURL = core.URL
	t.Cleanup(func() {
		s.mgr.Sup.StopAll()
		core.Close()
		os.RemoveAll(run)
	})

	w := s.do("POST", "/api/setup", `{"name":"Папа","password":"secret-pass"}`, nil)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("setup %d %s", w.Code, w.Body)
	}
	s.admin = session(w)
	s.adult = s.login(`{"name":"Маша"}`)
	s.kid = s.login(`{"name":"Вася","isKid":true,"maxRating":"PG"}`)
	return s
}

func session(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			return c
		}
	}
	return nil
}

func (s *stage2a) login(body string) *http.Cookie {
	s.t.Helper()
	w := s.do("POST", "/api/users", body, s.admin)
	if w.Code != 201 {
		s.t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	var u auth.User
	json.Unmarshal(w.Body.Bytes(), &u)
	c := session(s.do("POST", "/api/login", fmt.Sprintf(`{"userId":%d}`, u.ID), nil))
	if c == nil {
		s.t.Fatal("login")
	}
	return c
}

// do is a request of the app (from the home network) to the router.
func (s *stage2a) do(method, path, body string, c *http.Cookie, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "10.0.0.20:50000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if c != nil {
		r.AddCookie(c)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

// sock asks the running addon id directly on its socket.
func (s *stage2a) sock(id, method, path, body string, out any) int {
	s.t.Helper()
	st, sock := s.mgr.Serving(id)
	if sock == "" {
		s.t.Fatalf("%s not serving: %s", id, st)
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	req, _ := http.NewRequest(method, "http://addon"+path, strings.NewReader(body))
	resp, err := c.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			s.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
	return resp.StatusCode
}

type hookCall struct {
	Name string          `json:"name"`
	Body json.RawMessage `json:"body"`
}

// calls are the fake's hook calls after the first from.
func (s *stage2a) calls(from int) []hookCall {
	s.t.Helper()
	var all []hookCall
	s.sock("fake", "GET", "/calls", "", &all)
	return all[from:]
}

func names(cs []hookCall) string {
	n := make([]string, len(cs))
	for i, c := range cs {
		n[i] = c.Name
	}
	return strings.Join(n, " ")
}

// move moves dl/<from> into dl/<to> as the admin.
func (s *stage2a) move(from, to string, confirm bool) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"root": "dl", "path": from, "toRoot": "dl", "toPath": to, "confirm": confirm})
	return s.do("POST", "/api/files/move", string(b), s.admin)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func put(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStage2a_EndToEnd runs the fake addon in holder mode against the real
// core: it is installed from a local release, sets itself up through the
// core API (info, a TMDB lookup, an import with a ref, its holds), and the
// file browser, item delete, the metadata pass-through and the access
// rules then work over what it set up.
func TestStage2a_EndToEnd(t *testing.T) {
	bin := addons.FakeAddonBin(t)
	t.Cleanup(func() { // runs after the server's own cleanup has stopped every addon
		if exec.Command("pgrep", "-f", bin).Run() == nil {
			t.Error("a fake addon process outlived the test")
		}
	})
	s := newStage2a(t)
	ctx := context.Background()

	// the addon's data: four folders it holds in dl
	pack := filepath.Join(s.dl, "Pack")
	packMkv, packSample, packNfo := filepath.Join(pack, "Fight.Club.1999.mkv"), filepath.Join(pack, "sample.mkv"), filepath.Join(pack, "info.nfo")
	for _, p := range []string{packMkv, packSample, packNfo} {
		put(t, p)
	}
	held := map[string]string{} // key → folder in dl
	for _, k := range []string{"refused", "stopped", "follows", "down"} {
		dir := filepath.Join(s.dl, "Held-"+k)
		put(t, filepath.Join(dir, "a.mkv"))
		held[k] = dir
	}
	if err := os.MkdirAll(filepath.Join(s.dl, "Dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	libMkv := filepath.Join(s.movies, "Fight Club (1999)", "Fight.Club.1999.mkv")
	// the title is in the library already (its file not on disk yet)
	movie, err := s.repo.UpsertMovie(ctx, library.MovieInput{Title: "Бойцовский клуб", Year: 1999, TmdbID: 550,
		File: library.FileInput{Path: libMkv}})
	if err != nil {
		t.Fatal(err)
	}
	hold := func(key, root string, fs ...addonkit.HoldFile) addonkit.Hold {
		return addonkit.Hold{Key: key, Root: root, Active: true, Files: fs,
			Label:       addonkit.Text{RU: "Используется", EN: "In use"},
			MoveWarning: addonkit.Text{RU: "«{name}» нужен расширению", EN: "{name} is used by the addon"}}
	}
	holdSet := []addonkit.Hold{hold("r1", pack,
		addonkit.HoldFile{Path: packMkv, Size: 1, Selected: true},
		addonkit.HoldFile{Path: packSample, Size: 1})} // listed, not selected; info.nfo unlisted
	for k, dir := range held {
		holdSet = append(holdSet, hold(k, dir, addonkit.HoldFile{Path: filepath.Join(dir, "a.mkv"), Size: 1, Selected: true}))
	}
	script, _ := json.Marshal(map[string]any{
		"tmdb":   map[string]any{"kind": "movie", "id": 550},
		"links":  []map[string]string{{"from": packMkv, "to": libMkv}},
		"import": map[string]any{"paths": []string{libMkv}, "ref": "r1"},
		"holds":  holdSet,
	})
	// the addon's own variable reaches it (ANTRESOL_FAKE_*)
	t.Setenv("ANTRESOL_FAKE_HOLDER", string(script))
	addons.WriteLocalAddon(t, s.mgr.Dev, addons.Manifest{ID: "fake", Name: addons.Text{RU: "Держатель", EN: "Holder"},
		Version: "1.0.0", Access: "adults",
		Permissions: []string{"library.read", "library.write", "files.holds", "metadata"},
		Hooks:       []string{"files.plan", "files.pause", "files.resume", "files.stop", "files.detach", "files.attach"},
	}, "holder")

	// install through the admin route, from the local release
	if w := s.do("POST", "/api/addons", `{"url":"local:fake","version":"1.0.0"}`, s.admin); w.Code != 200 {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	var st struct {
		Info      addonkit.Info
		Item      addonkit.Item
		ItemFound bool
		Import    addonkit.ImportResult
		Errors    []string
		Done      bool
	}
	end := time.Now().Add(30 * time.Second)
	for {
		if s.mgr.Status("fake") == addons.StatusRunning {
			s.sock("fake", "GET", "/state", "", &st)
			if st.Done {
				break
			}
		}
		if time.Now().After(end) {
			t.Fatalf("holder setup: status %s, state %+v", s.mgr.Status("fake"), st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(st.Errors) != 0 {
		t.Fatalf("holder setup errors: %v", st.Errors)
	}
	if st.Info.ID != "fake" || st.Info.Contract != addons.ContractVersion || st.Info.LibraryRoots.Movies != s.movies {
		t.Fatalf("info %+v", st.Info)
	}
	if !st.ItemFound || st.Item.ID != movie || st.Item.TMDBID != 550 {
		t.Fatalf("tmdb lookup %+v found=%v", st.Item, st.ItemFound)
	}
	if len(st.Import.Files) != 1 || st.Import.Files[0].Path != libMkv || st.Import.Files[0].ItemID != movie {
		t.Fatalf("import %+v", st.Import)
	}
	item := st.Import.Files[0].ItemID

	// the addon's token, as its process got it
	var env []string
	s.sock("fake", "GET", "/env", "", &env)
	token := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "ANTRESOL_ADDON_TOKEN="); ok {
			token = v
		}
	}
	coreGet := func(path string) *httptest.ResponseRecorder {
		return s.do("GET", "/api/addon-core/v1"+path, "", nil, "Authorization", "Bearer "+token)
	}

	// refs: the imported file carries the hold's key
	var refFiles []map[string]any
	if w := coreGet("/library/refs/r1"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &refFiles) != nil ||
		len(refFiles) != 1 || refFiles[0]["path"] != libMkv {
		t.Fatalf("refs r1: %d %s", w.Code, w.Body)
	}

	// the metadata pass-through: the server's key goes upstream, never back
	w := coreGet("/metadata/tmdb/movie/550?language=ru&api_key=addon-own")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Fight Club") || strings.Contains(w.Body.String(), metaKey) {
		t.Fatalf("metadata: %d %s", w.Code, w.Body)
	}
	s.upMu.Lock()
	seen := strings.Join(s.upstream, "\n")
	s.upMu.Unlock()
	if !strings.Contains(seen, "/3/movie/550?") || !strings.Contains(seen, "api_key="+metaKey) || strings.Contains(seen, "addon-own") {
		t.Fatalf("upstream saw %q", seen)
	}

	// access "adults": admin and adult get the bundle list and the proxy,
	// x/admin/… only the admin; the kid nothing
	for _, c := range []struct {
		who                       string
		cookie                    *http.Cookie
		web, whoami, admin, files int
	}{
		{"admin", s.admin, 200, 200, 200, 200},
		{"adult", s.adult, 200, 200, 403, 200},
		{"kid", s.kid, 403, 403, 403, 403},
	} {
		got := [4]int{s.do("GET", "/api/addons/web", "", c.cookie).Code,
			s.do("GET", "/api/addons/fake/x/whoami", "", c.cookie).Code,
			s.do("GET", "/api/addons/fake/x/admin/ping", "", c.cookie).Code,
			s.do("GET", "/api/files/list?root=dl&path=", "", c.cookie).Code}
		if got != [4]int{c.web, c.whoami, c.admin, c.files} {
			t.Errorf("%s: web, whoami, admin, files = %v", c.who, got)
		}
	}
	if w := s.do("GET", "/api/addons/fake/x/whoami", "", s.adult); !strings.Contains(w.Body.String(), `"name":"Маша"`) ||
		!strings.Contains(w.Body.String(), `"isAdmin":false`) {
		t.Fatalf("whoami as adult: %s", w.Body)
	}
	if w := s.do("POST", "/api/files/move", `{"root":"dl","path":"Pack","toRoot":"dl","toPath":"Dest"}`, s.adult); w.Code != 403 {
		t.Fatalf("adult move: %d", w.Code)
	}

	// an admin's listing shows the holds
	w = s.do("GET", "/api/files/list?root=dl&path=", "", s.admin)
	var list struct {
		Entries []struct {
			Name string
			Hold *files.HoldRef
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("list: %v %s", err, w.Body)
	}
	for _, e := range list.Entries {
		want := e.Name != "Dest"
		if (e.Hold != nil) != want || want && (e.Hold.Addon != "Держатель" || e.Hold.Label != "Используется") {
			t.Fatalf("listing: %s %+v", e.Name, e.Hold)
		}
	}

	// without confirm: the addon's warning to confirm, and no hook call
	n := len(s.calls(0))
	w = s.move("Pack", "Dest", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"needsConfirm":true`) || !strings.Contains(w.Body.String(), "«Pack» нужен расширению") || !exists(packMkv) {
		t.Fatalf("move without confirm: %d %s", w.Code, w.Body)
	}
	if got := s.calls(n); len(got) != 0 {
		t.Fatalf("calls before confirming: %s", names(got))
	}

	// files.plan refused: the whole move is refused with the addon's text,
	// nothing is paused or moved
	if c := s.sock("fake", "POST", "/script", `{"refuse":{"files.plan":"Файлы ещё проверяются"}}`, nil); c != 204 {
		t.Fatalf("script: %d", c)
	}
	w = s.move("Held-refused", "Dest", true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Файлы ещё проверяются") {
		t.Fatalf("refused move: %d %s", w.Code, w.Body)
	}
	got := s.calls(n)
	if names(got) != "files.plan files.resume" || !exists(filepath.Join(held["refused"], "a.mkv")) {
		t.Fatalf("refused move: calls %s, moved %v", names(got), !exists(held["refused"]))
	}

	// confirmed and stopped: one files.plan, one files.pause, one files.stop
	s.sock("fake", "POST", "/script", `{}`, nil)
	n += len(got)
	if w := s.move("Held-stopped", "Dest", true); w.Code != 200 {
		t.Fatalf("stopped move: %d %s", w.Code, w.Body)
	}
	got = s.calls(n)
	if names(got) != "files.plan files.pause files.stop" || !exists(filepath.Join(s.dl, "Dest", "Held-stopped", "a.mkv")) {
		t.Fatalf("stopped move: calls %s", names(got))
	}
	for _, c := range got {
		if !strings.Contains(string(c.Body), `"stopped"`) || strings.Contains(string(c.Body), `"follows"`) {
			t.Fatalf("%s body %s", c.Name, c.Body)
		}
	}

	// it follows: detached before, attached (renamed) after; not stopped
	s.sock("fake", "POST", "/script", `{"follow":true}`, nil)
	n += len(got)
	if w := s.move("Held-follows", "Dest", true); w.Code != 200 {
		t.Fatalf("following move: %d %s", w.Code, w.Body)
	}
	got = s.calls(n)
	if ns := names(got); strings.Contains(ns, "files.stop") || !strings.Contains(ns, "files.plan") ||
		strings.Count(ns, "files.detach") != 1 || strings.Count(ns, "files.attach") != 1 ||
		strings.Index(ns, "files.detach") > strings.Index(ns, "files.attach") {
		t.Fatalf("following move: calls %s", ns)
	}
	for _, c := range got {
		if c.Name == "files.attach" && !strings.Contains(string(c.Body), `"renamed":true`) {
			t.Fatalf("attach body %s", c.Body)
		}
	}
	if !exists(filepath.Join(s.dl, "Dest", "Held-follows", "a.mkv")) {
		t.Fatal("the following move did not happen")
	}
	if _, x, _, ok := s.holds.Lookup(filepath.Join(held["follows"], "a.mkv")); !ok || x.Key != "follows" {
		t.Fatalf("the addon's set is the addon's to push again: %+v %v", x, ok)
	}

	// deleting the library item: its file and the hold's selected file go to
	// the trash (the hold found by the file's ref); the hold's root, its
	// unselected and its unlisted files stay
	s.sock("fake", "POST", "/script", `{}`, nil)
	n += len(got)
	if w := s.do("POST", fmt.Sprintf("/api/items/%d/delete", item), "{}", s.admin); w.Code != 200 {
		t.Fatalf("delete item: %d %s", w.Code, w.Body)
	}
	if exists(libMkv) || exists(packMkv) {
		t.Fatalf("not trashed: library %v, held %v", exists(libMkv), exists(packMkv))
	}
	if !exists(packSample) || !exists(packNfo) || !exists(pack) {
		t.Fatalf("trashed more than listed: sample %v nfo %v root %v", exists(packSample), exists(packNfo), exists(pack))
	}
	trash := filepath.Join(s.base, files.TrashDirName)
	var inTrash []string
	filepath.WalkDir(trash, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".mkv") {
			inTrash = append(inTrash, filepath.Base(p))
		}
		return nil
	})
	if len(inTrash) != 2 || inTrash[0] != "Fight.Club.1999.mkv" || inTrash[1] != "Fight.Club.1999.mkv" {
		t.Fatalf("trash holds %v", inTrash)
	}
	if got = s.calls(n); !strings.Contains(names(got), "files.plan") || !strings.Contains(names(got), "files.stop") {
		t.Fatalf("item delete: calls %s", names(got))
	}

	// the addon stopped: its holds stay, and a held folder cannot move
	if w := s.do("POST", "/api/addons/fake/disable", "", s.admin); w.Code != 200 && w.Code != 204 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	w = s.move("Held-down", "Dest", true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "«Держатель» не ответило") || !exists(filepath.Join(held["down"], "a.mkv")) || exists(filepath.Join(s.dl, "Dest", "Held-down")) {
		t.Fatalf("move while the addon is down: %d %s", w.Code, w.Body)
	}
	if _, _, _, ok := s.holds.Lookup(filepath.Join(held["down"], "a.mkv")); !ok {
		t.Fatal("holds dropped when the addon stopped")
	}
}
