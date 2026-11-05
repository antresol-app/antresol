package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// flacTrack writes a 2 s FLAC and adds it as a one-track album.
func (e *libEnv) flacTrack() (trackID, fileID int64) {
	e.t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		e.t.Skip("ffmpeg not installed")
	}
	dir := filepath.Join(e.media, "Кино", "Группа крови")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	p := filepath.Join(dir, "01.flac")
	if out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:sample_rate=22050:duration=2", "-c:a", "flac", p).CombinedOutput(); err != nil {
		e.t.Fatalf("ffmpeg: %v %s", err, out)
	}
	st, err := os.Stat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	ctx := context.Background()
	album, err := e.repo.UpsertAlbumTracks(ctx, "Кино", []library.TrackInput{{
		File: library.FileInput{Path: p, Size: st.Size(), Mtime: st.ModTime().Unix()}, AlbumDir: dir,
		Album: "Группа крови", Artist: "Кино", Title: "Группа крови", Track: 1, Codec: "flac", Lossless: true}})
	if err != nil {
		e.t.Fatal(err)
	}
	tracks, err := e.repo.AlbumTracks(ctx, album)
	if err != nil || len(tracks) != 1 {
		e.t.Fatal(err, tracks)
	}
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=2000 WHERE id=?`, tracks[0].FileID); err != nil {
		e.t.Fatal(err)
	}
	return tracks[0].ID, tracks[0].FileID
}

func TestTrackStreamRemoteGetsAAC(t *testing.T) {
	var remote atomic.Bool
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.ClientIsRemote = func(*http.Request) bool { return remote.Load() }
		d.AudioTranscoder = &media.AudioTranscoder{FFmpeg: "ffmpeg", Limit: 4}
	})
	tr, _ := e.flacTrack()
	url := "/api/tracks/" + itoa(tr) + "/stream"

	remote.Store(true)
	w := e.do("GET", url, "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/aac" || w.Header().Get("Accept-Ranges") != "none" ||
		w.Header().Get("X-Content-Duration") != "2" || w.Body.Len() == 0 {
		t.Fatalf("remote: %d %v", w.Code, w.Header())
	}
	// an explicit client limit beats the setting
	if w := e.do("GET", url+"?maxBitRate=0", "", e.admin); w.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("explicit maxBitRate=0: %v", w.Header())
	}

	remote.Store(false)
	w = e.do("GET", url, "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/flac" || w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("home: %d %v", w.Code, w.Header())
	}
	w = e.do("GET", url, "", e.admin, "Range", "bytes=0-9")
	if w.Code != 206 || w.Body.Len() != 10 || w.Body.String()[:4] != "fLaC" {
		t.Fatalf("range: %d %q", w.Code, w.Body)
	}
	// t only moves a transcode
	w = e.do("GET", url+"?maxBitRate=128&t=0.5", "", e.admin)
	if w.Code != 200 || w.Header().Get("X-Content-Duration") != "1.5" {
		t.Fatalf("offset: %d %v", w.Code, w.Header())
	}
	for _, bad := range []string{"?t=x", "?t=-1", "?maxBitRate=abc", "?format=wma"} {
		if w := e.do("GET", url+bad, "", e.admin); w.Code != 400 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	if w := e.do("GET", "/api/tracks/999999/stream", "", e.admin); w.Code != 404 {
		t.Fatalf("missing track %d", w.Code)
	}
	if w := e.do("GET", url, "", nil); w.Code != 401 {
		t.Fatalf("anon %d", w.Code)
	}
}

func TestAudioFileStream(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.ClientIsRemote = func(*http.Request) bool { return false }
		d.AudioTranscoder = &media.AudioTranscoder{FFmpeg: "ffmpeg", Limit: 4}
	})
	_, f := e.flacTrack()
	w := e.do("GET", "/api/audio/"+itoa(f)+"?accept=mp3,aac", "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/aac" || w.Header().Get("X-Vault-Transcode") != "1" {
		t.Fatalf("not accepted: %d %v", w.Code, w.Header())
	}
	w = e.do("GET", "/api/audio/"+itoa(f), "", e.admin, "Range", "bytes=0-3")
	if w.Code != 206 || w.Body.String() != "fLaC" {
		t.Fatalf("direct: %d %q", w.Code, w.Body)
	}
}

func TestStreamContentTypeAudioBook(t *testing.T) {
	e := newLibEnv(t)
	item, _ := e.movie("Брат", "16+", "x")
	want := map[string]string{
		".mp3": "audio/mpeg", ".flac": "audio/flac", ".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac",
		".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg", ".wav": "audio/wav",
		".epub": "application/epub+zip", ".fb2": "application/x-fictionbook+xml", ".zip": "application/zip",
		".pdf": "application/pdf", ".mobi": "application/x-mobipocket-ebook", ".azw3": "application/vnd.amazon.ebook",
		".cbz": "application/vnd.comicbook+zip", ".cbr": "application/vnd.comicbook-rar", ".cb7": "application/x-7z-compressed",
	}
	if len(want) != 18 {
		t.Fatal(len(want))
	}
	for ext, ct := range want {
		p := filepath.Join(e.media, "f"+ext)
		if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := e.db.Exec(`INSERT INTO files(item_id, path, size, mtime) VALUES(?,?,10,0)`, item, p)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		w := e.do("GET", "/api/stream/"+itoa(id), "", e.admin)
		if w.Code != 200 || w.Header().Get("Content-Type") != ct || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %q", ext, w.Code, w.Header().Get("Content-Type"))
		}
		// a book or any file opened directly never runs in the app's origin
		for _, u := range []string{"/api/stream/", "/api/download/"} {
			w := e.do("GET", u+itoa(id), "", e.admin)
			if csp := w.Header().Get("Content-Security-Policy"); csp != "sandbox" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s%s: CSP %q nosniff %q", u, ext, csp, w.Header().Get("X-Content-Type-Options"))
			}
		}
	}
}

func TestRemoteMusicSetting(t *testing.T) {
	e := newSettingsEnv(t)
	var s struct {
		RemoteMusic string `json:"remoteMusic"`
	}
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &s)
	if s.RemoteMusic != "high" {
		t.Fatalf("default %q", s.RemoteMusic)
	}
	w := e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20,"remoteMusic":"low"}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	app, err := LoadAppSettings(context.Background(), e.db, AppSettings{})
	if err != nil || app.RemoteMusic != "low" || app.RemoteMusicKbps() != 128 {
		t.Fatalf("%+v %v", app, err)
	}
	if (AppSettings{}).RemoteMusicKbps() != 256 || (AppSettings{RemoteMusic: "lossless"}).RemoteMusicKbps() != 0 {
		t.Fatal("kbps")
	}
	w = e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20,"remoteMusic":"bad"}`, e.admin)
	var out struct{ Error string }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 400 || out.Error != "Неизвестное качество музыки" {
		t.Fatalf("invalid: %d %q", w.Code, out.Error)
	}
}
