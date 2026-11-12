package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

func pngOf(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// upload posts data as the multipart field "file", as the web app does.
func (e *libEnv) upload(path string, data []byte, c *http.Cookie) *httptest.ResponseRecorder {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "logo.png")
	fw.Write(data)
	mw.Close()
	r := httptest.NewRequest("POST", path, &b)
	r.RemoteAddr = "192.168.1.20:50000"
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-Vault-Upload", "1")
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestChannelsPicksSearchAndFolders(t *testing.T) {
	e, svc := newChannelsEnv(t)
	roots := channels.LibraryRoots(e.media)
	svc.SetRoots(roots)
	key := roots[0].Key
	m1, _ := e.movie("Один", "12+", "a")
	m2, _ := e.movie("Два", "16+", "b")
	e.withDuration()
	kid := e.kid("12+")

	// search: by title, within the editor's limit
	var s struct{ Results []channels.SearchHit }
	w := e.do("GET", "/api/channels/options/search?q="+url.QueryEscape("два"), "", e.admin)
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || len(s.Results) != 1 || s.Results[0].ID != m2 || s.Results[0].Kind != "movie" || s.Results[0].Pick != (channels.Pick{Kind: "title", ID: m2}) {
		t.Fatalf("search %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/options/search?q="+url.QueryEscape("два"), "", kid)
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || len(s.Results) != 0 || !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatalf("kid search %d %s", w.Code, w.Body)
	}

	// folders: the roots, a root's files (limited), no way out of them
	var f channels.Folder
	w = e.do("GET", "/api/channels/options/folders", "", e.admin)
	json.Unmarshal(w.Body.Bytes(), &f)
	if w.Code != 200 || len(f.Folders) != 1 || f.Folders[0].Path != key {
		t.Fatalf("roots %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/options/folders?path="+url.QueryEscape(key), "", e.admin)
	json.Unmarshal(w.Body.Bytes(), &f)
	if w.Code != 200 || len(f.Files) != 2 || f.Files[0].Name != "Два.mkv" || f.Files[0].ID != m2 {
		t.Fatalf("root %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/options/folders?path="+url.QueryEscape(key), "", kid)
	json.Unmarshal(w.Body.Bytes(), &f)
	if len(f.Files) != 1 || f.Files[0].ID != m1 {
		t.Fatalf("kid root %s", w.Body)
	}
	for _, p := range []string{"..", "../..", key + "/../..", key + "/..%2F..", "%2E%2E/etc", key + "/./x"} {
		if w := e.do("GET", "/api/channels/options/folders?path="+p, "", e.admin); w.Code != 400 || strings.Contains(w.Body.String(), e.media) {
			t.Fatalf("traversal %q: %d %s", p, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/channels/options/folders?path=nope", "", e.admin); w.Code != 404 {
		t.Fatalf("unknown root %d", w.Code)
	}

	// a channel of picks in a manual order: the folder, then a title
	body := `{"name":"Сборка","rules":{"order":"ordered","picks":[{"kind":"item","id":` + itoa(m2) + `},{"kind":"folder","path":"` + key + `"},{"kind":"title","id":` + itoa(m1) + `}]}}`
	w = e.do("POST", "/api/channels", body, e.admin)
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	if w.Code != 201 || len(c.Rules.Picks) != 3 || len(c.PickInfo) != 3 || c.PickInfo[0].Title != "Два" || c.PickInfo[1].Title != key || c.PickInfo[1].Missing {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/"+itoa(c.ID)+"/guide?hours=12", "", e.admin)
	var g struct{ Programs []programJSON }
	json.Unmarshal(w.Body.Bytes(), &g)
	want := []int64{m2, m2, m1, m1} // Два; the folder: Два, Один; Один
	if len(g.Programs) < len(want) {
		t.Fatalf("guide %s", w.Body)
	}
	for i, id := range want {
		if g.Programs[i].ItemID != id {
			t.Fatalf("programme %d: %d, want %d (%s)", i, g.Programs[i].ItemID, id, w.Body)
		}
	}
	// round 1 titles come back as picks
	w = e.do("PUT", "/api/channels/"+itoa(c.ID), `{"name":"Сборка","rules":{"titles":[`+itoa(m1)+`]}}`, e.admin)
	json.Unmarshal(w.Body.Bytes(), &c)
	if w.Code != 200 || len(c.Rules.Titles) != 0 || len(c.Rules.Picks) != 1 || c.Rules.Picks[0] != (channels.Pick{Kind: "title", ID: m1}) {
		t.Fatalf("titles %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/channels/"+itoa(c.ID), `{"name":"x","rules":{"picks":[{"kind":"folder","path":"../etc"}]}}`, e.admin); w.Code != 400 {
		t.Fatalf("bad folder pick %d", w.Code)
	}
	// the viewers' list does not describe picks
	w = e.do("GET", "/api/channels", "", e.admin)
	if strings.Contains(w.Body.String(), "pickInfo") {
		t.Fatalf("viewers' list %s", w.Body)
	}
	w = e.do("GET", "/api/channels?manage=1", "", e.admin)
	if !strings.Contains(w.Body.String(), `"pickInfo":[{"kind":"title"`) {
		t.Fatalf("manage list %s", w.Body)
	}
}

func TestChannelsLogoUpload(t *testing.T) {
	e, svc := newChannelsEnv(t)
	m1, _ := e.movie("Один", "", "a")
	e.withDuration()
	w := e.do("POST", "/api/channels", `{"name":"Кино","rules":{"titles":[`+itoa(m1)+`]}}`, e.admin)
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	if c.LogoURL != "" || strings.Contains(w.Body.String(), "logoUrl") {
		t.Fatalf("logo before upload: %s", w.Body)
	}
	id := itoa(c.ID)
	// who may: whoever may edit the channel
	ann := e.login(`{"name":"Аня"}`)
	if w := e.upload("/api/channels/"+id+"/logo", pngOf(20, 20), ann); w.Code != 403 {
		t.Fatalf("ann uploads %d", w.Code)
	}
	if w := e.do("DELETE", "/api/channels/"+id+"/logo", "", ann); w.Code != 403 {
		t.Fatalf("ann removes %d", w.Code)
	}
	// types and size
	if w := e.upload("/api/channels/"+id+"/logo", []byte("GIF89a\x01\x00\x01\x00"), e.admin); w.Code != 400 {
		t.Fatalf("gif %d %s", w.Code, w.Body)
	}
	if w := e.upload("/api/channels/"+id+"/logo", append(pngOf(4, 4), make([]byte, channels.MaxLogoBytes)...), e.admin); w.Code != 413 {
		t.Fatalf("too big %d %s", w.Code, w.Body)
	}
	// without the upload header a multipart POST is refused (CSRF)
	{
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", "l.png")
		fw.Write(pngOf(4, 4))
		mw.Close()
		r := httptest.NewRequest("POST", "/api/channels/"+id+"/logo", &b)
		r.RemoteAddr = "192.168.1.20:50000"
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.AddCookie(e.admin)
		rw := httptest.NewRecorder()
		e.h.ServeHTTP(rw, r)
		if rw.Code != 403 {
			t.Fatalf("no upload header %d", rw.Code)
		}
	}
	w = e.upload("/api/channels/"+id+"/logo", pngOf(1000, 500), e.admin)
	json.Unmarshal(w.Body.Bytes(), &c)
	if w.Code != 200 || !strings.HasPrefix(c.LogoURL, "/api/channels/"+id+"/logo.png?v=") {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	file := filepath.Join(svc.DataDir, "channel-logos", id+".png")
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	w = e.do("GET", c.LogoURL, "", e.admin)
	img, err := png.Decode(w.Body)
	if w.Code != 200 || err != nil || img.Bounds().Dx() != 512 || img.Bounds().Dy() != 256 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("logo %d %v %v %s", w.Code, err, img, w.Header())
	}
	if w := e.do("GET", "/api/channels/"+id+"/logo.png", "", e.admin); w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("unversioned %s", w.Header())
	}
	// list and EPG grid carry it
	for _, p := range []string{"/api/channels", "/api/channels/epg"} {
		if w := e.do("GET", p, "", e.admin); !strings.Contains(w.Body.String(), `"logoUrl":"`+c.LogoURL+`"`) {
			t.Fatalf("%s %s", p, w.Body)
		}
	}
	// IPTV: M3U and XMLTV name the version; the logo route serves the upload
	w = e.do("GET", "/api/channels/iptv", "", e.admin)
	var l iptvLinks
	json.Unmarshal(w.Body.Bytes(), &l)
	base := strings.TrimSuffix(strings.TrimPrefix(l.Playlist, "http://example.com"), "/playlist.m3u")
	v := strings.TrimPrefix(c.LogoURL, "/api/channels/"+id+"/logo.png")
	if w := e.do("GET", base+"/playlist.m3u", "", nil); !strings.Contains(w.Body.String(), `/logo/`+id+`.png`+v+`"`) {
		t.Fatalf("m3u %s", w.Body)
	}
	if w := e.do("GET", base+"/epg.xml", "", nil); !strings.Contains(w.Body.String(), `/logo/`+id+`.png`+v+`"`) {
		t.Fatalf("xmltv %s", w.Body)
	}
	w = e.do("GET", base+"/logo/"+id+".png"+v, "", nil)
	if img, err := png.Decode(w.Body); err != nil || img.Bounds().Dx() != 512 {
		t.Fatalf("iptv logo %v", err)
	}
	// removed: the generated tile again, no logoUrl
	w = e.do("DELETE", "/api/channels/"+id+"/logo", "", e.admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "logoUrl") {
		t.Fatalf("delete logo %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("file kept: %v", err)
	}
	w = e.do("GET", "/api/channels/"+id+"/logo.png", "", e.admin)
	if img, err := png.Decode(w.Body); err != nil || img.Bounds().Dx() != channels.LogoWidth {
		t.Fatalf("tile %v", err)
	}
	// deleting the channel deletes its logo
	e.upload("/api/channels/"+id+"/logo", pngOf(30, 30), e.admin)
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if w := e.do("DELETE", "/api/channels/"+id, "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("file kept after delete: %v", err)
	}
}
