package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/progress"
)

type filesEnv struct {
	*libEnv
	root string
	svc  *files.Service
}

func newFilesEnv(t *testing.T) *filesEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	repo := library.NewRepo(d)
	mr := t.TempDir()
	for _, p := range []string{"media/movies", "media/series", "torrents", "tmp/vault-stream"} {
		if err := os.MkdirAll(filepath.Join(mr, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(mr, "media/movies/a.mkv"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb := files.NewSandbox(mr, []files.Root{
		{ID: "movies", Title: "Фильмы", Icon: "film", Dir: filepath.Join(mr, "media/movies")},
		{ID: "series", Title: "Сериалы", Icon: "tv", Dir: filepath.Join(mr, "media/series")},
		{ID: "downloads", Title: "Загрузки", Icon: "download", Dir: filepath.Join(mr, "torrents")},
		{ID: "stream-cache", Title: "Кеш просмотра", Icon: "clock", Dir: filepath.Join(mr, "tmp/vault-stream"), NoDrop: true},
		{ID: "disk", Title: "Весь диск", Icon: "disk", Dir: mr, AdminOnly: true},
	})
	svc := files.NewService(files.ServiceConfig{Sandbox: sb, DB: d, Library: repo, DataDir: t.TempDir()})
	e := &libEnv{env: &env{db: d, t: t, store: st}, repo: repo, prog: progress.New(d)}
	e.h = NewRouter(Deps{Users: st, Sessions: ss, Library: repo, Progress: e.prog, Files: svc})
	e.admin = e.setup()
	return &filesEnv{libEnv: e, root: mr, svc: svc}
}

func q(root, path string) string {
	return "root=" + url.QueryEscape(root) + "&path=" + url.QueryEscape(path)
}

func TestKidForbidden(t *testing.T) {
	e := newFilesEnv(t)
	kid := e.kid("")
	for _, p := range []string{"/api/files/roots", "/api/files/list?" + q("movies", ""), "/api/files/stat?" + q("movies", "a.mkv"),
		"/api/files/content?" + q("movies", "a.mkv"), "/api/files/usage", "/api/files/thumb?" + q("movies", "a.mkv")} {
		if w := e.do("GET", p, "", kid); w.Code != 403 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := e.do("GET", "/api/files/roots", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
}

func TestUserReadOnly(t *testing.T) {
	e := newFilesEnv(t)
	u := e.login(`{"name":"Мама"}`)
	if w := e.do("POST", "/api/files/rename", `{"root":"movies","path":"a.mkv","newName":"b.mkv"}`, u); w.Code != 403 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/files/trash", `{"root":"movies","paths":["a.mkv"]}`, u); w.Code != 403 {
		t.Fatalf("trash: %d", w.Code)
	}
	if w := e.do("GET", "/api/files/list?"+q("movies", ""), "", u); w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/files/list?"+q("disk", ""), "", u); w.Code != 403 {
		t.Fatalf("disk: %d", w.Code)
	}
	var roots []struct{ ID, Title string }
	json.Unmarshal(e.do("GET", "/api/files/roots", "", u).Body.Bytes(), &roots)
	for _, r := range roots {
		if r.ID == "disk" {
			t.Fatal("disk root shown to a regular profile")
		}
	}
	var aroots []struct{ ID, Title string }
	json.Unmarshal(e.do("GET", "/api/files/roots", "", e.admin).Body.Bytes(), &aroots)
	if len(aroots) != 5 || aroots[4].ID != "disk" || aroots[4].Title != "Весь диск" {
		t.Fatalf("admin roots %+v", aroots)
	}
}

func TestFilesAdminFlow(t *testing.T) {
	e := newFilesEnv(t)
	w := e.do("GET", "/api/files/list?"+q("movies", ""), "", e.admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), e.root) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/files/list?"+q("movies", "../series"), "", e.admin); w.Code != 400 {
		t.Fatalf("traversal: %d", w.Code)
	}
	if w := e.do("POST", "/api/files/mkdir", `{"root":"movies","path":"","name":"Брат (1997)"}`, e.admin); w.Code != 200 {
		t.Fatalf("mkdir: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/files/rename", `{"root":"movies","path":"a.mkv","newName":"../b.mkv"}`, e.admin); w.Code != 400 {
		t.Fatalf("bad rename: %d", w.Code)
	}
	if w := e.do("POST", "/api/files/move", `{"root":"movies","path":"a.mkv","toRoot":"movies","toPath":"Брат (1997)"}`, e.admin); w.Code != 200 {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/files/trash", `{"root":"movies","paths":["Брат (1997)"]}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("trash: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/files/trash", "", e.admin)
	var items []files.TrashItem
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || len(items) != 1 || strings.Contains(w.Body.String(), e.root) {
		t.Fatalf("trash list: %s", w.Body)
	}
	if w := e.do("POST", "/api/files/trash/restore", `{"id":`+itoa(items[0].ID)+`}`, e.admin); w.Code != 200 {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.root, "media/movies/Брат (1997)/a.mkv")); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/files/trash", `{"root":"movies","paths":["Брат (1997)"]}`, e.admin)
	json.Unmarshal(e.do("GET", "/api/files/trash", "", e.admin).Body.Bytes(), &items)
	if w := e.do("DELETE", "/api/files/trash/"+itoa(items[0].ID), "", e.admin); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/files/usage", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"categories"`) {
		t.Fatalf("usage: %d %s", w.Code, w.Body)
	}
}

func TestContentRange(t *testing.T) {
	e := newFilesEnv(t)
	w := e.do("GET", "/api/files/content?"+q("movies", "a.mkv"), "", e.admin, "Range", "bytes=2-5")
	if w.Code != http.StatusPartialContent || w.Body.String() != "2345" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/x-matroska" {
		t.Fatalf("content type %q", ct)
	}
	w = e.do("GET", "/api/files/content?"+q("movies", "a.mkv")+"&download=1", "", e.admin)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %v", w.Code, w.Header())
	}
	// HTML is never rendered inline: only offered for download.
	os.WriteFile(filepath.Join(e.root, "media/movies/x.html"), []byte("<script>alert(1)</script>"), 0o644)
	w = e.do("GET", "/api/files/content?"+q("movies", "x.html"), "", e.admin)
	if w.Code != http.StatusUnsupportedMediaType || strings.Contains(w.Header().Get("Content-Type"), "html") ||
		strings.Contains(w.Body.String(), "<script>") {
		t.Fatalf("html served inline: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w := e.do("GET", "/api/files/content?"+q("movies", "../../etc"), "", e.admin); w.Code != 400 {
		t.Fatalf("traversal: %d", w.Code)
	}
}

// Cross-site form posts (every *.example.com host is "same-site" for the
// SameSite=Lax cookie) must not reach mutating endpoints.
func TestMutationsRejectCrossSite(t *testing.T) {
	e := newFilesEnv(t)
	body := `{"root":"movies","paths":["a.mkv"],"confirm":true,"x":"="}`
	for _, hdr := range [][]string{
		{"Content-Type", "text/plain"},
		{"Content-Type", "application/x-www-form-urlencoded"},
		{"Origin", "https://wiki.example.com"},
		{"Sec-Fetch-Site", "same-site"},
		{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"},
	} {
		if w := e.do("POST", "/api/files/trash", body, e.admin, hdr...); w.Code != 403 {
			t.Errorf("%v: %d %s", hdr, w.Code, w.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(e.root, "media/movies/a.mkv")); err != nil {
		t.Fatal("file trashed by a cross-site request")
	}
	// Bodyless mutations (logout) are checked too.
	if w := e.do("POST", "/api/logout", "", e.admin, "Sec-Fetch-Site", "same-site"); w.Code != 403 {
		t.Errorf("cross-site logout: %d", w.Code)
	}
	// The web client itself: same-origin JSON.
	if w := e.do("POST", "/api/files/mkdir", `{"root":"movies","path":"","name":"ok"}`, e.admin,
		"Sec-Fetch-Site", "same-origin", "Origin", "http://example.com"); w.Code != 200 {
		t.Fatalf("same-origin: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/files/mkdir", `{"root":"movies","path":"","name":"ok2"}`, e.admin,
		"Content-Type", "application/json; charset=utf-8"); w.Code != 200 {
		t.Fatalf("json with charset: %d %s", w.Code, w.Body)
	}
}

// A failed move after some items reached the trash reports them (not a bare error).
func TestTrashPartialResponse(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newFilesEnv(t)
	ro := filepath.Join(e.root, "media/movies/ro")
	if err := os.MkdirAll(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, "b.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(ro, 0o555)
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	w := e.do("POST", "/api/files/trash", `{"root":"movies","paths":["a.mkv","ro/b.mkv"]}`, e.admin)
	var out struct {
		Trashed []files.TrashItem
		Error   string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || len(out.Trashed) != 1 || out.Error == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// ffprobe from stat is bounded like thumbnail rendering.
func TestStatProbeConcurrencyBounded(t *testing.T) {
	e := newFilesEnv(t)
	release := make(chan struct{})
	var mu sync.Mutex
	active, peak := 0, 0
	e.svc.Probe = func(ctx context.Context, p string) (media.ProbeResult, error) {
		mu.Lock()
		active++
		peak = max(peak, active)
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		active--
		mu.Unlock()
		return media.ProbeResult{Container: "matroska"}, nil
	}
	const n = 6
	for i := range n {
		os.WriteFile(filepath.Join(e.root, "media/movies", "v"+itoa(int64(i))+".mkv"), []byte("x"), 0o644)
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { e.do("GET", "/api/files/stat?"+q("movies", "v"+itoa(int64(i))+".mkv"), "", e.admin) })
	}
	// Wait until both slots are taken, give the other requests time to
	// (wrongly) start a third probe, then check the peak is exactly 2.
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		a := active
		mu.Unlock()
		if a == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("only %d probes started", a)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	got := peak
	mu.Unlock()
	close(release)
	wg.Wait()
	if got != 2 {
		t.Fatalf("peak concurrent ffprobe %d, want 2", got)
	}
}

func TestMoveIntoStreamCacheForbidden(t *testing.T) {
	e := newFilesEnv(t)
	w := e.do("POST", "/api/files/move", `{"root":"movies","path":"a.mkv","toRoot":"stream-cache","toPath":""}`, e.admin)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Кеш просмотра") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.root, "media/movies/a.mkv")); err != nil {
		t.Fatal("moved")
	}
}

// every path below the prefix is shared
