package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/playlistimport"
	"github.com/antresol-app/antresol/internal/playlistimport/files"
)

// lockedBuf is a log sink tests read while handlers write.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// importHookEnv is the playlist import with fake addons answering the
// import.* hooks (the hooks wired as main wires them).
type importHookEnv struct {
	*libEnv
	svc   *playlistimport.Service
	fake  *fakeAddons
	hooks *addons.Hooks
	logs  *lockedBuf
}

func newImportHookEnv(t *testing.T) *importHookEnv {
	t.Helper()
	e := &importHookEnv{logs: &lockedBuf{},
		fake: &fakeAddons{installed: map[string]addons.Installed{}, status: map[string]addons.Status{},
			sock: map[string]string{}, tokens: map[string]string{}, webRoot: t.TempDir()}}
	e.hooks = &addons.Hooks{T: e.fake, Log: slog.New(slog.NewTextHandler(e.logs, nil))}
	e.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		e.svc = &playlistimport.Service{Store: &playlistimport.Store{DB: db}, Bus: &events.Bus{}, Matcher: offlineMatcher{},
			Connectors: e.hooks.ImportServices(), Reads: e.hooks.ImportStopper(), Resolver: e.hooks.ImportResolver(),
			Catalog: e.hooks.ImportCatalog()}
		d.PlaylistImport = e.svc
		d.AddonHooks = e.hooks
	})
	return e
}

// addon runs addon id declaring the import.* hooks; serve answers them.
func (e *importHookEnv) addon(id string, serve http.HandlerFunc) {
	e.t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ad")
	if err != nil {
		e.t.Fatal(err)
	}
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		e.t.Fatal(err)
	}
	srv := &http.Server{Handler: serve}
	go srv.Serve(ln)
	e.t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	e.fake.mu.Lock()
	e.fake.installed[id] = addons.Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: id, Version: "1.0.0", Contract: addons.ContractVersion, Permissions: []string{"imports"},
			Hooks: []string{"import.services", "import.stop", "import.resolve", "import.search"}}}
	e.fake.status[id] = addons.StatusRunning
	e.fake.sock[id] = sock
	e.fake.mu.Unlock()
}

// hookJSON answers hook name with v (others 404).
func hookJSON(answers map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, ok := answers[strings.TrimPrefix(r.URL.Path, "/hooks/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
}

var tilesOfApple = map[string]any{"services": []map[string]any{{"id": "apple", "configured": true, "connect": "redirect"}}}

// GET /api/import/oauth/{service}/callback: the sign-in's registered
// address sends the browser on to the addon that lists the service, query
// and all; nothing of it is kept, logged or passed on as a referrer.
func TestOAuthRedirect(t *testing.T) {
	e := newImportHookEnv(t)
	e.addon("alpha", hookJSON(map[string]any{"import.services": tilesOfApple}))
	std := &lockedBuf{}
	log.SetOutput(std)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const q = "code=c0de-SECRET%2F1&state=st4te-SECRET"
	w := e.do("GET", "/api/import/oauth/apple/callback?"+q, "", e.admin)
	if w.Code != http.StatusFound {
		t.Fatalf("callback %d %s", w.Code, w.Body)
	}
	if loc := w.Header().Get("Location"); loc != "/api/addons/alpha/x/admin/import/oauth/apple/callback?"+q {
		t.Fatalf("Location %q", loc)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers %v", w.Header())
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body %q", w.Body)
	}
	// a refusal (error=…) goes the same way; no query: none added
	if w := e.do("GET", "/api/import/oauth/apple/callback?error=access_denied&state=s", "", e.admin); w.Code != 302 ||
		w.Header().Get("Location") != "/api/addons/alpha/x/admin/import/oauth/apple/callback?error=access_denied&state=s" {
		t.Fatalf("refused %d %v", w.Code, w.Header())
	}
	if w := e.do("GET", "/api/import/oauth/apple/callback", "", e.admin); w.Code != 302 ||
		w.Header().Get("Location") != "/api/addons/alpha/x/admin/import/oauth/apple/callback" {
		t.Fatalf("no query %d %v", w.Code, w.Header())
	}

	// no addon lists the service
	if w := e.do("GET", "/api/import/oauth/file/callback?"+q, "", e.admin); w.Code != 404 || errText(w) != "Сервис не подключён" ||
		w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unlisted %d %s %v", w.Code, w.Body, w.Header())
	}
	// who may: an admin with a session
	if w := e.do("GET", "/api/import/oauth/apple/callback?"+q, "", nil); w.Code != 401 {
		t.Fatalf("no session %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/import/oauth/apple/callback?"+q, "", e.kid("")); w.Code != 403 {
		t.Fatalf("kid %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/import/oauth/apple/callback?"+q, "", e.login(`{"name":"Петя"}`)); w.Code != 403 {
		t.Fatalf("adult %d %s", w.Code, w.Body)
	}
	// the addon down: nobody lists it
	e.fake.mu.Lock()
	e.fake.status["alpha"] = addons.StatusStopped
	e.fake.mu.Unlock()
	if w := e.do("GET", "/api/import/oauth/apple/callback?"+q, "", e.admin); w.Code != 404 {
		t.Fatalf("addon down %d %s", w.Code, w.Body)
	}
	for _, l := range []string{std.String(), e.logs.String()} {
		if strings.Contains(l, "SECRET") {
			t.Fatalf("the query was logged: %s", l)
		}
	}
}

// Without addons the callback is not connected anywhere.
func TestOAuthRedirect_NoAddons(t *testing.T) {
	e := newCoreImportEnv(t)
	if w := e.do("GET", "/api/import/oauth/apple/callback?code=c&state=s", "", e.admin); w.Code != 404 || errText(w) != "Сервис не подключён" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// GET /api/import/services: the addons' tiles; an addon down leaves none,
// so the page shows the file import.
func TestImportServicesThroughHooks(t *testing.T) {
	e := newImportHookEnv(t)
	if w := e.do("GET", "/api/import/services", "", e.admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"catalog":false,"downloads":false,"services":[]}` {
		t.Fatalf("none %d %s", w.Code, w.Body)
	}
	e.addon("alpha", hookJSON(map[string]any{"import.services": tilesOfApple}))
	if w := e.do("GET", "/api/import/services", "", e.admin); w.Code != 200 ||
		strings.TrimSpace(w.Body.String()) != `{"catalog":false,"downloads":false,"services":[{"id":"apple","configured":true,"connect":"redirect","setup":[],"files":[]}]}` {
		t.Fatalf("tiles %d %s", w.Code, w.Body)
	}
}

// «Найти вручную» through import.search: the candidates, or 503 with the
// catalog's text when the addon says its catalog is offline.
func TestImportSearchThroughHook(t *testing.T) {
	e := newImportHookEnv(t)
	// no addon searches: as without a catalog
	if w := e.do("GET", "/api/import/search?q=x", "", e.admin); w.Code != 503 || errText(w) != playlistimport.CatalogOffline {
		t.Fatalf("none %d %s", w.Code, w.Body)
	}
	var mu sync.Mutex
	refusal := ""
	var asked []string
	e.addon("alpha", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		asked = append(asked, string(b))
		text := refusal
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if text != "" {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": text})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"candidates": []map[string]any{{"kind": "music", "extId": "rg-1", "title": "Группа крови"}}})
	})
	w := e.do("GET", "/api/import/search?q=кино", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"extId":"rg-1"`) {
		t.Fatalf("search %d %s", w.Code, w.Body)
	}
	if len(asked) != 1 || asked[0] != `{"kind":"music","q":"кино"}` {
		t.Fatalf("asked %v", asked)
	}
	mu.Lock()
	refusal = playlistimport.CatalogOffline
	mu.Unlock()
	if w := e.do("GET", "/api/import/search?q=кино", "", e.admin); w.Code != 503 || errText(w) != playlistimport.CatalogOffline {
		t.Fatalf("offline %d %s", w.Code, w.Body)
	}
	// in English too
	if w := e.do("GET", "/api/import/search?q=кино", "", e.admin, "Accept-Language", "en"); w.Code != 503 ||
		!strings.Contains(errText(w), "catalog is unavailable") {
		t.Fatalf("offline en %d %s", w.Code, w.Body)
	}
	// another refusal: the admin's 400 with its text
	mu.Lock()
	refusal = "Поиск выключен"
	mu.Unlock()
	if w := e.do("GET", "/api/import/search?q=кино", "", e.admin); w.Code != 400 || errText(w) != "Поиск выключен" {
		t.Fatalf("refused %d %s", w.Code, w.Body)
	}
}

// takeoutZip is a Takeout of one playlist of these video ids (none named).
func takeoutZip(t *testing.T, ids ...string) string {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("Takeout/Music/playlists/Дорога-videos.csv")
	io.WriteString(w, "Video ID,Playlist Video Creation Timestamp\n")
	for _, id := range ids {
		io.WriteString(w, id+",2024-01-01T00:00:00+00:00\n")
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// waitJob waits until job id leaves reading.
func (e *importHookEnv) waitJob(id int64) playlistimport.Job {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err := e.svc.Store.Job(context.Background(), id)
		if err == nil && j.Status != playlistimport.StatusReading {
			return j
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("job %d still reading: %+v %v", id, j, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A Takeout upload with videos known only by id: a reading job of the
// core's background reader at once, named by the addon's import.resolve,
// then the preview with what stayed unknown counted.
func TestTakeout_ResolveThroughHook(t *testing.T) {
	e := newImportHookEnv(t)
	var mu sync.Mutex
	var asked [][]string
	gate := make(chan struct{})
	e.addon("alpha", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hooks/import.resolve" {
			http.NotFound(w, r)
			return
		}
		<-gate
		var req struct{ IDs []string }
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		asked = append(asked, req.IDs)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tracks": []map[string]any{
			{"key": "kinoBlood01", "artist": "Кино", "title": "Группа крови"},
			{"key": "kinoKukush1", "artist": "Кино", "title": "Кукушка"},
			{"key": "notAsked001", "artist": "Кино", "title": "Звезда"}}})
	})
	zip := takeoutZip(t, "kinoBlood01", "kinoKukush1", "privateVid1", "bad/../id")
	w := uploadFiles(e.libEnv, [2]string{"takeout.zip", zip})
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	var j struct {
		ID      int64
		Status  string
		Service string
	}
	json.Unmarshal(w.Body.Bytes(), &j)
	parsed, err := files.Parse("takeout.zip", []byte(zip))
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != playlistimport.StatusReading || j.Service != parsed.Service || j.Service == playlistimport.ServiceFile {
		t.Fatalf("job %s", w.Body)
	}
	// one naming at a time per profile
	if w := uploadFiles(e.libEnv, [2]string{"takeout.zip", zip}); w.Code != 409 || errText(w) != "Импорт уже идёт" {
		t.Fatalf("second upload %d %s", w.Code, w.Body)
	}
	close(gate)
	got := e.waitJob(j.ID)
	if got.Status != playlistimport.StatusPreview || got.Report.Unresolved != 2 {
		t.Fatalf("after naming %+v", got)
	}
	if len(asked) != 1 || strings.Join(asked[0], ",") != "kinoBlood01,kinoKukush1,privateVid1,bad/../id" {
		t.Fatalf("asked %v", asked)
	}
	rows, err := e.db.Query(`SELECT key, artist, title FROM import_tracks WHERE job_id = ?`, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]string{}
	for rows.Next() {
		var k, a, ti string
		if err := rows.Scan(&k, &a, &ti); err != nil {
			t.Fatal(err)
		}
		named[k] = a + " — " + ti
	}
	rows.Close()
	if len(named) != 2 || named["kinoBlood01"] != "Кино — Группа крови" || named["kinoKukush1"] != "Кино — Кукушка" {
		t.Fatalf("tracks %v (the unnamed ones and the one not asked left out)", named)
	}
	if w := e.do("DELETE", "/api/import/jobs/"+itoa(j.ID), "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}

	// the addon down: the read still ends, every video counted unknown
	e.fake.mu.Lock()
	e.fake.status["alpha"] = addons.StatusStopped
	e.fake.mu.Unlock()
	w = uploadFiles(e.libEnv, [2]string{"takeout.zip", takeoutZip(t, "kinoBlood01", "privateVid1")})
	json.Unmarshal(w.Body.Bytes(), &j)
	if w.Code != 201 || j.Status != playlistimport.StatusReading {
		t.Fatalf("down upload %d %s", w.Code, w.Body)
	}
	if got := e.waitJob(j.ID); got.Status != playlistimport.StatusPreview || got.Report.Unresolved != 2 {
		t.Fatalf("down %+v", got)
	}
}

// A cancelled Takeout stops its naming at once.
func TestTakeout_CancelStopsNaming(t *testing.T) {
	e := newImportHookEnv(t)
	stopped := make(chan struct{}, 1)
	e.addon("alpha", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hooks/import.resolve" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		io.ReadAll(r.Body) // (the server watches the connection once the body is read)
		select {
		case <-r.Context().Done(): // the core gave up on the call
			stopped <- struct{}{}
		case <-time.After(10 * time.Second):
		}
	})
	w := uploadFiles(e.libEnv, [2]string{"takeout.zip", takeoutZip(t, "vidKino0001")})
	var j struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &j)
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	time.Sleep(50 * time.Millisecond) // the naming has begun
	if w := e.do("POST", "/api/import/jobs/"+itoa(j.ID)+"/cancel", "", e.admin); w.Code != 204 {
		t.Fatalf("cancel %d %s", w.Code, w.Body)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the naming goes on after the cancel")
	}
	e.svc.WaitReads()
	if got, _ := e.svc.Store.Job(context.Background(), j.ID); got.Status != playlistimport.StatusCancelled {
		t.Fatalf("job %+v", got)
	}
}

// POST /api/import/groups/{id}/retry is the core's: «Повторить» puts an
// album not found back in the queue, without any addon.
func TestImportRetryInCore(t *testing.T) {
	e := newCoreImportEnv(t)
	w := e.do("POST", "/api/import/jobs", `{"service":"file","tracks":[{"key":"a","artist":"Кино","title":"Кукушка","album":"Чёрный альбом"}]}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var j struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &j)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`UPDATE import_jobs SET status = 'done' WHERE id = ?`, j.ID)
	exec(`INSERT INTO import_groups(job_id, release_group, artist, album, year, state, infohash, bytes, updated_at)
		VALUES (?, 'rg-a', 'Кино', 'Чёрный альбом', 1990, 'not_found', '', 0, '2026-10-26T12:00:00Z'),
		       (?, 'rg-b', 'Кино', 'Группа крови', 1988, 'have', '', 0, '2026-10-26T12:00:00Z')`, j.ID, j.ID)
	exec(`UPDATE import_tracks SET state = 'not_found', group_id = (SELECT id FROM import_groups WHERE release_group = 'rg-a') WHERE job_id = ?`, j.ID)
	var ga, gb int64
	e.db.QueryRow(`SELECT id FROM import_groups WHERE release_group = 'rg-a'`).Scan(&ga)
	e.db.QueryRow(`SELECT id FROM import_groups WHERE release_group = 'rg-b'`).Scan(&gb)

	w = e.do("POST", "/api/users", `{"name":"Мама","isAdmin":true,"password":"abcdef"}`, e.admin)
	var mom struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &mom)
	other := session(e.do("POST", "/api/login", `{"userId":`+itoa(mom.ID)+`,"password":"abcdef"}`, nil))
	if other == nil {
		t.Fatalf("second admin %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/import/groups/"+itoa(ga)+"/retry", "", other); w.Code != 404 {
		t.Fatalf("other admin %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/import/groups/"+itoa(ga)+"/retry", "", e.kid("")); w.Code != 403 {
		t.Fatalf("kid %d", w.Code)
	}
	if w := e.do("POST", "/api/import/groups/"+itoa(ga)+"/retry", "", nil); w.Code != 401 {
		t.Fatalf("no session %d", w.Code)
	}
	if w := e.do("POST", "/api/import/groups/x/retry", "", e.admin); w.Code != 400 {
		t.Fatalf("bad id %d", w.Code)
	}
	if w := e.do("POST", "/api/import/groups/999/retry", "", e.admin); w.Code != 404 {
		t.Fatalf("unknown %d", w.Code)
	}
	if w := e.do("POST", "/api/import/groups/"+itoa(gb)+"/retry", "", e.admin); w.Code != 409 || errText(w) != "Не найдено" {
		t.Fatalf("retry a group the library has %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/import/groups/"+itoa(ga)+"/retry?format=any", "", e.admin); w.Code != 204 {
		t.Fatalf("retry %d %s", w.Code, w.Body)
	}
	var state, status string
	var anyFormat bool
	e.db.QueryRow(`SELECT state, any_format FROM import_groups WHERE id = ?`, ga).Scan(&state, &anyFormat)
	e.db.QueryRow(`SELECT status FROM import_jobs WHERE id = ?`, j.ID).Scan(&status)
	if state != "waiting" || !anyFormat || status != "downloading" {
		t.Fatalf("after retry: group %s any %v, job %s", state, anyFormat, status)
	}
}
