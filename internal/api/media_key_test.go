package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
)

// withMediaKeys gives the router media keys (main.go does the same).
func withMediaKeys(d *Deps, _ *sql.DB) {
	d.MediaKeys = auth.NewMediaKeys(bytes.Repeat([]byte{1}, 32), d.Sessions)
}

func newKeyEnv(t *testing.T) *libEnv { return newLibEnvWith(t, withMediaKeys) }

// mediaKey asks for a media key with the device token tok.
func (e *env) mediaKey(tok string) string {
	e.t.Helper()
	w := e.do("POST", "/api/auth/media-key", "", nil, bearer(tok)...)
	if w.Code != 200 {
		e.t.Fatalf("media-key %d %s", w.Code, w.Body)
	}
	var out struct {
		Key       string
		ExpiresAt string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Key == "" {
		e.t.Fatalf("media-key body %s", w.Body)
	}
	exp, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil || time.Until(exp) < 11*time.Hour || time.Until(exp) > 13*time.Hour {
		e.t.Fatalf("expiresAt %q %v", out.ExpiresAt, err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		e.t.Fatalf("media-key cacheable: %v", w.Header())
	}
	return out.Key
}

func mk(key string) string { return "mk=" + url.QueryEscape(key) }

func TestMediaKeyOnlyMediaRoutes(t *testing.T) {
	e := newKeyEnv(t)
	item, f := e.movie("Брат", "16+", "0123456789abcdefghij")
	tok := e.deviceLogin(1, "secret", "phone-0001")
	key := e.mediaKey(tok)

	w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key), "", nil, "Range", "bytes=0-9")
	if w.Code != 206 || w.Body.String() != "0123456789" {
		t.Fatalf("keyed stream: %d %q", w.Code, w.Body)
	}
	for _, p := range []string{"/api/home", "/api/images/x", "/api/download/" + itoa(f), "/api/subs/" + itoa(f) + "/0.vtt",
		"/api/items/" + itoa(item), "/api/me", "/api/music/tracks/" + itoa(item) + "/lyrics"} {
		if w := e.do("GET", p+"?"+mk(key), "", nil); w.Code != 401 {
			t.Fatalf("GET %s with mk: %d", p, w.Code)
		}
	}
	if w := e.do("PUT", "/api/progress/"+itoa(item)+"?"+mk(key), `{"positionMs":1000,"durationMs":5000}`, nil); w.Code != 401 {
		t.Fatalf("PUT progress with mk: %d", w.Code)
	}
	if w := e.do("PUT", "/api/hls/sess1?"+mk(key), "", nil); w.Code != 401 {
		t.Fatalf("PUT hls with mk: %d", w.Code)
	}
	if w := e.do("POST", "/api/auth/media-key?"+mk(key), "", nil); w.Code != 401 {
		t.Fatalf("a key minting keys: %d", w.Code)
	}
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?mk=v1.1.1.1.x", "", nil); w.Code != 401 {
		t.Fatalf("bad key: %d", w.Code)
	}
	// a cookie or Bearer, when present, is the credential (no fallback to mk)
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key), "", nil, bearer("nope")...); w.Code != 401 {
		t.Fatalf("bad bearer + mk: %d", w.Code)
	}
	// keyed tracks and audio
	res, _ := e.db.Exec(`INSERT INTO items(kind,title) VALUES('album','Группа крови')`)
	album, _ := res.LastInsertId()
	res, _ = e.db.Exec(`INSERT INTO items(kind,parent_id,title) VALUES('track',?,'Кукушка')`, album)
	track, _ := res.LastInsertId()
	p := filepath.Join(e.media, "01.mp3")
	os.WriteFile(p, []byte("ID3abcdefg"), 0o644)
	res, _ = e.db.Exec(`INSERT INTO files(item_id,path,size) VALUES(?,?,10)`, track, p)
	tf, _ := res.LastInsertId()
	e.db.Exec(`INSERT INTO tracks(item_id,file_id) VALUES(?,?)`, track, tf)
	if w := e.do("GET", "/api/tracks/"+itoa(track)+"/stream?format=raw&"+mk(key), "", nil); w.Code != 200 {
		t.Fatalf("keyed track: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/audio/"+itoa(tf)+"?format=raw&"+mk(key), "", nil); w.Code != 200 {
		t.Fatalf("keyed audio: %d %s", w.Code, w.Body)
	}
}

// AirPlay from the web player: the browser's session gets a key too (the
// receiver fetches the stream without the cookie); it opens the same media
// routes only and ends with the session.
func TestMediaKeyWebSession(t *testing.T) {
	e := newKeyEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	w := e.do("POST", "/api/auth/media-key", "", e.admin)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct{ Key, ExpiresAt string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Key == "" {
		t.Fatalf("body %s", w.Body)
	}
	// one AirPlay sitting, not the app's 12 hours
	if exp, err := time.Parse(time.RFC3339, out.ExpiresAt); err != nil || time.Until(exp) < 3*time.Hour || time.Until(exp) > 5*time.Hour {
		t.Fatalf("expiresAt %q", out.ExpiresAt)
	}
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(out.Key), "", nil, "Range", "bytes=0-3"); w.Code != 206 || w.Body.String() != "0123" {
		t.Fatalf("keyed stream: %d %q", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me?"+mk(out.Key), "", nil); w.Code != 401 {
		t.Fatalf("a web key on /api/me: %d", w.Code)
	}
	if w := e.do("POST", "/api/auth/media-key", "", nil); w.Code != 401 {
		t.Fatalf("anon %d", w.Code)
	}
	// «Устройства» on the phone ends the browser's session, and its key with it
	phone := e.deviceLogin(1, "secret", "phone-0001")
	key2 := e.mediaKey(phone)
	var webID int64
	if err := e.db.QueryRow(`SELECT rowid FROM sessions WHERE token=?`, auth.TokenHash(e.admin.Value)).Scan(&webID); err != nil {
		t.Fatal(err)
	}
	if w := e.do("DELETE", "/api/me/devices/"+itoa(webID), "", nil, bearer(phone)...); w.Code != 204 {
		t.Fatalf("revoke browser %d", w.Code)
	}
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(out.Key), "", nil); w.Code != 401 {
		t.Fatalf("key of a revoked browser: %d", w.Code)
	}
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key2), "", nil); w.Code != 200 {
		t.Fatalf("the phone's key: %d", w.Code)
	}
}

func TestMediaKeyPasswordlessRemote(t *testing.T) {
	e := newKeyEnv(t)
	_, f := e.movie("Простоквашино", "0+", "0123456789")
	masha := e.addUser(`{"name":"Маша"}`, e.admin)
	tok := e.deviceLogin(masha.ID, "", "ipad-00001")
	key := e.mediaKey(tok)
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key), "", nil); w.Code != 200 {
		t.Fatalf("at home: %d %s", w.Code, w.Body)
	}
	// a browser's key of the same profile (AirPlay from the web player)
	web := session(e.do("POST", "/api/login", `{"userId":`+itoa(masha.ID)+`}`, nil))
	if web == nil {
		t.Fatal("no web session for a passwordless profile at home")
	}
	w := e.do("POST", "/api/auth/media-key", "", web)
	var out struct{ Key string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Key == "" {
		t.Fatalf("web key %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(out.Key), "", nil); w.Code != 200 {
		t.Fatalf("web key at home: %d %s", w.Code, w.Body)
	}
	e.peer = internetPeer
	for name, k := range map[string]string{"app": key, "web": out.Key} {
		w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(k), "", nil)
		if w.Code != 403 || w.Header().Get("X-Vault-Error") != "remote-passwordless" ||
			!strings.Contains(w.Body.String(), auth.MsgRemotePasswordless) {
			t.Fatalf("%s key remote: %d %v %s", name, w.Code, w.Header(), w.Body)
		}
	}
}

func TestMediaKeyVisibility(t *testing.T) {
	e := newKeyEnv(t)
	_, adult := e.movie("Брат", "18+", "0123456789")
	_, kids := e.movie("Простоквашино", "0+", "0123456789")
	vasya := e.addUser(`{"name":"Вася","isKid":true,"maxRating":"12+"}`, e.admin)
	key := e.mediaKey(e.deviceLogin(vasya.ID, "", "ipad-00001"))
	if w := e.do("GET", "/api/stream/"+itoa(adult)+"?"+mk(key), "", nil); w.Code != 404 {
		t.Fatalf("18+: %d", w.Code)
	}
	if w := e.do("GET", "/api/stream/"+itoa(kids)+"?"+mk(key), "", nil); w.Code != 200 {
		t.Fatalf("0+: %d", w.Code)
	}
}

func TestMediaKeyDeviceRevoked(t *testing.T) {
	e := newKeyEnv(t)
	_, f := e.movie("Брат", "16+", "0123456789")
	tok := e.deviceLogin(1, "secret", "phone-0001")
	key := e.mediaKey(tok)
	if w := e.do("POST", "/api/logout", "", nil, bearer(tok)...); w.Code != 204 {
		t.Fatalf("logout %d", w.Code)
	}
	if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key), "", nil); w.Code != 401 {
		t.Fatalf("key of a signed-out device: %d", w.Code)
	}
}

// playlistHLS serves a fixed playlist for sess1.
type playlistHLS struct {
	*fakeHLS
	body string
}

func (p *playlistHLS) Playlist(id string) (string, error) {
	if _, err := p.fakeHLS.Playlist(id); err != nil {
		return "", err
	}
	f := filepath.Join(p.dir, "index.m3u8")
	return f, os.WriteFile(f, []byte(p.body), 0o644)
}

const fmp4Playlist = "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:EVENT\n" +
	"#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.000000,\nseg00000.m4s\n#EXTINF:6.000000,\nseg00001.m4s\n"

func TestHLSPlaylistKeyed(t *testing.T) {
	ph := &playlistHLS{body: fmp4Playlist}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		withMediaKeys(d, db)
		ph.fakeHLS = &fakeHLS{dir: t.TempDir()}
		d.HLS = ph
	})
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	tok := e.deviceLogin(1, "secret", "phone-0001")
	if w := e.do("POST", "/api/play/"+itoa(f), `{"caps":{"videoCodecs":["h264"]}}`, nil, bearer(tok)...); w.Code != 200 {
		t.Fatalf("play %d %s", w.Code, w.Body)
	}
	key := e.mediaKey(tok)
	esc := url.QueryEscape(key)
	w := e.do("GET", "/api/hls/sess1/index.m3u8?"+mk(key), "", nil)
	if w.Code != 200 {
		t.Fatalf("keyed playlist %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n") ||
		!strings.Contains(body, `#EXT-X-MAP:URI="init.mp4?mk=`+esc+`"`) ||
		!strings.Contains(body, "\nseg00000.m4s?mk="+esc+"\n") || !strings.Contains(body, "\nseg00001.m4s?mk="+esc+"\n") {
		t.Fatalf("keyed playlist:\n%s", body)
	}
	if w.Header().Get("Content-Type") != "application/vnd.apple.mpegurl" || w.Header().Get("Content-Length") != itoa(int64(len(body))) {
		t.Fatalf("headers %v", w.Header())
	}
	// segments accept the key (the fake has none: 404, not 401)
	if w := e.do("GET", "/api/hls/sess1/seg00000.m4s?"+mk(key), "", nil); w.Code != 404 {
		t.Fatalf("keyed segment %d", w.Code)
	}
	// without mk the playlist is byte-identical
	if w := e.do("GET", "/api/hls/sess1/index.m3u8", "", nil, bearer(tok)...); w.Code != 200 || w.Body.String() != fmp4Playlist {
		t.Fatalf("plain playlist %d:\n%s", w.Code, w.Body)
	}
	// another profile's key does not reach the session
	other := e.addUser(`{"name":"Мама","password":"secret2"}`, e.admin)
	okey := e.mediaKey(e.deviceLogin(other.ID, "secret2", "phone-0002"))
	if w := e.do("GET", "/api/hls/sess1/index.m3u8?"+mk(okey), "", nil); w.Code != 404 {
		t.Fatalf("other profile %d", w.Code)
	}
}

// A transcode: the app (keyed) gets the whole film as a VOD playlist from
// the start, so AVPlayer and Picture in Picture treat it as a film; the web
// (no key) keeps ffmpeg's growing EVENT playlist byte for byte.
func TestHLSKeyedTranscodeIsComplete(t *testing.T) {
	const vod = "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n" +
		"#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:6.000000,\nseg00000.m4s\n#EXTINF:6.000000,\nseg00001.m4s\n" +
		"#EXTINF:6.000000,\nseg00002.m4s\n#EXT-X-ENDLIST\n"
	ph := &playlistHLS{body: fmp4Playlist}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		withMediaKeys(d, db)
		ph.fakeHLS = &fakeHLS{dir: t.TempDir(), complete: vod}
		d.HLS = ph
	})
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	tok := e.deviceLogin(1, "secret", "phone-0001")
	w := e.do("POST", "/api/play/"+itoa(f), `{"caps":{"videoCodecs":["h264"]}}`, nil, bearer(tok)...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"hlsComplete":true`) {
		t.Fatalf("play %d %s", w.Code, w.Body)
	}
	if o := ph.opts[len(ph.opts)-1]; o.DurationSec != 5 {
		t.Fatalf("the session was not told the duration: %+v", o)
	}
	key := e.mediaKey(tok)
	esc := url.QueryEscape(key)
	w = e.do("GET", "/api/hls/sess1/index.m3u8?"+mk(key), "", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(body, "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n") ||
		!strings.Contains(body, "#EXT-X-PLAYLIST-TYPE:VOD\n") || !strings.HasSuffix(body, "#EXT-X-ENDLIST\n") ||
		!strings.Contains(body, `#EXT-X-MAP:URI="init.mp4?mk=`+esc+`"`) || !strings.Contains(body, "\nseg00002.m4s?mk="+esc+"\n") {
		t.Fatalf("keyed playlist %d:\n%s", w.Code, body)
	}
	if w := e.do("GET", "/api/hls/sess1/index.m3u8", "", nil, bearer(tok)...); w.Code != 200 || w.Body.String() != fmp4Playlist {
		t.Fatalf("plain playlist %d:\n%s", w.Code, w.Body)
	}
	// a remux keeps the EVENT playlist and says so
	ph.complete = ""
	if w := e.do("POST", "/api/play/"+itoa(f), `{"caps":{"videoCodecs":["h264"]}}`, nil, bearer(tok)...); !strings.Contains(w.Body.String(), `"hlsComplete":false`) {
		t.Fatalf("play %s", w.Body)
	}
	if w := e.do("GET", "/api/hls/sess1/index.m3u8?"+mk(key), "", nil); strings.Contains(w.Body.String(), "ENDLIST") {
		t.Fatalf("event playlist:\n%s", w.Body)
	}
}

func TestRewritePlaylistKeepsComments(t *testing.T) {
	in := "#EXTM3U\r\n#EXT-X-START:TIME-OFFSET=0\r\n#EXT-X-MAP:URI=\"init.mp4\"\r\n# a comment with URI=\"x\" stays as is\r\n\r\n#EXTINF:6.0,\r\nseg00000.ts?x=1\r\nseg00001.ts\r\n#EXT-X-ENDLIST\r\n"
	got := rewritePlaylist(in, "v1.2.3.4.a+b/c=")
	esc := url.QueryEscape("v1.2.3.4.a+b/c=")
	want := "#EXTM3U\r\n#EXT-X-START:TIME-OFFSET=0\r\n#EXT-X-MAP:URI=\"init.mp4?mk=" + esc + "\"\r\n" +
		"# a comment with URI=\"x\" stays as is\r\n\r\n#EXTINF:6.0,\r\n" +
		"seg00000.ts?x=1&mk=" + esc + "\r\nseg00001.ts?mk=" + esc + "\r\n#EXT-X-ENDLIST\r\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
	if got := rewritePlaylist("#EXTM3U\n", "k"); got != "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n" {
		t.Fatalf("%q", got)
	}
	if got := rewritePlaylist("#EXTM3U", "k"); got != "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES" {
		t.Fatalf("no trailing newline: %q", got)
	}
}

// A media key reaches /api/stream only for video and audio (what AVPlayer
// and AirPlay fetch); books and comics need the Bearer token.
func TestMediaKeyStreamVideoAndAudioOnly(t *testing.T) {
	e := newKeyEnv(t)
	_, movie := e.movie("Брат", "16+", "0123456789")
	tok := e.deviceLogin(1, "secret", "phone-0001")
	key := e.mediaKey(tok)
	file := func(kind, name string) int64 {
		res, err := e.db.Exec(`INSERT INTO items(kind,title) VALUES(?,?)`, kind, name)
		if err != nil {
			t.Fatal(err)
		}
		it, _ := res.LastInsertId()
		p := filepath.Join(e.media, name)
		os.WriteFile(p, []byte("0123456789"), 0o644)
		res, err = e.db.Exec(`INSERT INTO files(item_id,path,size) VALUES(?,?,10)`, it, p)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	allowed := map[string]int64{"movie": movie, "audiobook": file("audiobook", "part1.m4b"), "track": file("track", "01.mp3")}
	for kind, f := range allowed {
		if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key), "", nil); w.Code != 200 {
			t.Errorf("keyed %s: %d %s", kind, w.Code, w.Body)
		}
	}
	for kind, f := range map[string]int64{"book": file("book", "kniga.epub"), "comic_volume": file("comic_volume", "tom1.cbz")} {
		if w := e.do("GET", "/api/stream/"+itoa(f)+"?"+mk(key), "", nil); w.Code != 401 {
			t.Errorf("keyed %s: %d %s", kind, w.Code, w.Body)
		}
		if w := e.do("GET", "/api/stream/"+itoa(f), "", nil, bearer(tok)...); w.Code != 200 {
			t.Errorf("bearer %s: %d %s", kind, w.Code, w.Body)
		}
	}
}
