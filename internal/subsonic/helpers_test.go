package subsonic

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/audioserve"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/music"
)

const homeAddr, remoteAddr = "192.168.1.20:50000", "203.0.113.7:50000"

type env struct {
	t     *testing.T
	db    *sql.DB
	repo  *library.Repo
	music *music.Store
	users *auth.Store
	srv   *Server
	media string
	admin auth.User
	app   string // the admin's app password
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &env{t: t, db: d, repo: library.NewRepo(d), music: music.New(d), users: auth.NewStore(d), media: t.TempDir()}
	network := &auth.Network{}
	e.srv = &Server{Users: e.users, Library: e.repo, Music: e.music, Network: network, Throttle: auth.NewThrottle(),
		Version: "test", ImageDir: t.TempDir(),
		Audio: audioserve.Deps{Transcoder: &media.AudioTranscoder{FFmpeg: "ffmpeg", Limit: 4},
			IsRemote:   func(r *http.Request) bool { return !network.IsTrusted(r) },
			RemoteKbps: func(context.Context) int { return 256 }}}
	ctx := context.Background()
	if e.admin, err = e.users.CreateFirstAdmin(ctx, "Папа", "secret"); err != nil {
		t.Fatal(err)
	}
	if e.app, err = e.users.SetAppPassword(ctx, e.admin.ID); err != nil {
		t.Fatal(err)
	}
	return e
}

// creds are the admin's token credentials (s=abc123).
func (e *env) creds() url.Values {
	sum := md5.Sum([]byte(e.app + "abc123"))
	return url.Values{"u": {e.admin.Name}, "t": {hex.EncodeToString(sum[:])}, "s": {"abc123"}, "c": {"test"}, "v": {"1.16.1"}}
}

// req sends GET /rest/<method>?<params> from addr ("" = home).
func (e *env) req(method, addr string, params url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/rest/"+method+"?"+params.Encode(), nil)
	r.RemoteAddr = homeAddr
	if addr != "" {
		r.RemoteAddr = addr
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, r)
	return w
}

// call sends method with the admin's credentials, JSON, plus kv pairs.
func (e *env) call(method string, kv ...string) map[string]any {
	e.t.Helper()
	p := e.creds()
	p.Set("f", "json")
	for i := 0; i+1 < len(kv); i += 2 {
		p.Add(kv[i], kv[i+1])
	}
	return e.decode(e.req(method, "", p))
}

func (e *env) decode(w *httptest.ResponseRecorder) map[string]any {
	e.t.Helper()
	if w.Code != 200 {
		e.t.Fatalf("HTTP %d: %s", w.Code, w.Body)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("%v: %s", err, w.Body)
	}
	r, ok := out["subsonic-response"]
	if !ok {
		e.t.Fatalf("no subsonic-response: %s", w.Body)
	}
	return r
}

// mustOK fails unless r is status ok.
func (e *env) mustOK(r map[string]any) map[string]any {
	e.t.Helper()
	if r["status"] != "ok" {
		e.t.Fatalf("status %v: %v", r["status"], r["error"])
	}
	return r
}

// errCode returns the error code of a failed response (-1 when ok).
func errCode(r map[string]any) (int, string) {
	er, ok := r["error"].(map[string]any)
	if !ok || r["status"] != "failed" {
		return -1, ""
	}
	return int(er["code"].(float64)), er["message"].(string)
}

type tTrack struct {
	no    int
	title string
}

// album adds an album of tiny (not real audio) FLAC files, 120 s each.
func (e *env) album(artist, title string, year int, genre string, tracks ...tTrack) (int64, []int64) {
	e.t.Helper()
	dir := filepath.Join(e.media, artist, title)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	var in []library.TrackInput
	for _, t := range tracks {
		p := filepath.Join(dir, itoa(int64(t.no))+" - "+t.title+".flac")
		body := strings.Repeat("a", 1500)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			e.t.Fatal(err)
		}
		in = append(in, library.TrackInput{File: library.FileInput{Path: p, Size: 15_000_000, Mtime: 1}, // ≈ 1000 kbit/s over 120 s
			AlbumDir: dir, Album: title, Artist: artist, Title: t.title, Disc: 1, Track: t.no, Year: year,
			Genre: genre, Codec: "flac", Lossless: true})
	}
	ctx := context.Background()
	id, err := e.repo.UpsertAlbumTracks(ctx, artist, in)
	if err != nil {
		e.t.Fatal(err)
	}
	trs, err := e.repo.AlbumTracks(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	var ids []int64
	for _, tr := range trs {
		ids = append(ids, tr.ID)
		if _, err := e.db.Exec(`UPDATE files SET duration_ms=120000 WHERE id=?`, tr.FileID); err != nil {
			e.t.Fatal(err)
		}
	}
	return id, ids
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// ffmpeg returns the ffmpeg path or skips the test.
func ffmpeg(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	return p
}

// sine writes a FLAC sine of secs seconds to p.
func sine(t *testing.T, p string, secs int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg(t), "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=22050:duration="+itoa(int64(secs)), "-c:a", "flac", p).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
}

func keys(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// poster gives an album a cover from the image cache.
func (e *env) poster(albumID int64) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE items SET poster=? WHERE id=?`, "0123456789abcdef0123456789abcdef01234567.jpg", albumID); err != nil {
		e.t.Fatal(err)
	}
}

// asKid makes the calls run as a new kid profile.
func (e *env) asKid() {
	e.t.Helper()
	ctx := context.Background()
	kid, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Вася", IsKid: true})
	if err != nil {
		e.t.Fatal(err)
	}
	app, err := e.users.SetAppPassword(ctx, kid.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	e.admin, e.app = kid, app
}
