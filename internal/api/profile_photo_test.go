package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

func photoPNG(w, h int) []byte {
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

// uploadPhoto posts data as the multipart field "file", as the web app does
// (header: with X-Vault-Upload).
func (e *env) uploadPhoto(id int64, data []byte, c *http.Cookie, header bool) *httptest.ResponseRecorder {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "photo.jpg")
	fw.Write(data)
	mw.Close()
	r := httptest.NewRequest("POST", "/api/users/"+strconv.FormatInt(id, 10)+"/photo", &b)
	r.RemoteAddr = "192.168.1.20:50000"
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if header {
		r.Header.Set("X-Vault-Upload", "1")
	}
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestProfilePhoto(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var kid auth.User
	if w := e.do("POST", "/api/users", `{"name":"Маша","isKid":true,"maxRating":"6+"}`, admin); w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	} else {
		json.Unmarshal(w.Body.Bytes(), &kid)
	}
	id := strconv.FormatInt(kid.ID, 10)
	if kid.Photo != "" {
		t.Fatalf("a new profile has a photo: %q", kid.Photo)
	}
	if w := e.do("GET", "/api/profiles/"+id+"/photo", "", nil); w.Code != 404 {
		t.Fatalf("photo before upload %d", w.Code)
	}

	// only an administrator sets it, and only with the upload header (CSRF)
	lena := func() *http.Cookie {
		w := e.do("POST", "/api/users", `{"name":"Лена"}`, admin)
		var u auth.User
		json.Unmarshal(w.Body.Bytes(), &u)
		return session(e.do("POST", "/api/login", `{"userId":`+strconv.FormatInt(u.ID, 10)+`}`, nil))
	}()
	if w := e.uploadPhoto(kid.ID, photoPNG(40, 40), lena, true); w.Code != 403 {
		t.Fatalf("not an admin %d", w.Code)
	}
	if w := e.do("DELETE", "/api/users/"+id+"/photo", "", lena); w.Code != 403 {
		t.Fatalf("not an admin delete %d", w.Code)
	}
	if w := e.uploadPhoto(kid.ID, photoPNG(40, 40), admin, false); w.Code != 403 {
		t.Fatalf("no upload header %d", w.Code)
	}
	if w := e.uploadPhoto(kid.ID, []byte("GIF89a\x01\x00\x01\x00"), admin, true); w.Code != 400 {
		t.Fatalf("not a picture %d %s", w.Code, w.Body)
	}
	if w := e.uploadPhoto(kid.ID, append(photoPNG(4, 4), make([]byte, auth.MaxPhotoBytes)...), admin, true); w.Code != 413 {
		t.Fatalf("too big %d", w.Code)
	}
	if w := e.uploadPhoto(9999, photoPNG(40, 40), admin, true); w.Code != 404 {
		t.Fatalf("unknown profile %d", w.Code)
	}

	// a wide picture: the middle square, 512 px
	w := e.uploadPhoto(kid.ID, photoPNG(1000, 700), admin, true)
	var u auth.User
	json.Unmarshal(w.Body.Bytes(), &u)
	if w.Code != 200 || !strings.HasPrefix(u.Photo, "/api/profiles/"+id+"/photo?v=") {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	// the sign-in screen's list names it, with no session
	var list []struct {
		ID    int64
		Name  string
		Photo string
	}
	json.Unmarshal(e.do("GET", "/api/profiles", "", nil).Body.Bytes(), &list)
	if len(list) != 3 || list[0].Photo != "" || list[1].Photo != u.Photo {
		t.Fatalf("profiles: %+v", list)
	}
	// as does /api/users (admin) and the profile's own /api/me
	if w := e.do("GET", "/api/users", "", admin); !strings.Contains(w.Body.String(), `"photo":"`+u.Photo+`"`) {
		t.Fatalf("users: %s", w.Body)
	}
	// the picture: a public JPEG, kept for good under its own version
	w = e.do("GET", u.Photo, "", nil)
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(w.Body.Bytes())); w.Code != 200 || err != nil || cfg.Width != auth.PhotoBox || cfg.Height != auth.PhotoBox {
		t.Fatalf("photo %d %v %+v", w.Code, err, cfg)
	}
	if h := w.Header(); h.Get("Content-Type") != "image/jpeg" || !strings.Contains(h.Get("Cache-Control"), "immutable") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", h)
	}
	if w := e.do("GET", "/api/profiles/"+id+"/photo", "", nil); w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("bare address %d %v", w.Code, w.Header())
	}
	if w := e.do("GET", "/api/profiles/"+id+"/photo?v=1", "", nil); w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("stale version %v", w.Header())
	}

	// a new upload is a new address
	w = e.uploadPhoto(kid.ID, photoPNG(300, 300), admin, true)
	var again auth.User
	json.Unmarshal(w.Body.Bytes(), &again)
	if w.Code != 200 || again.Photo == u.Photo {
		t.Fatalf("second upload %d %q == %q", w.Code, again.Photo, u.Photo)
	}

	// an edit of the profile keeps it (the web app saves the whole form)
	if w := e.do("PATCH", "/api/users/"+id, `{"name":"Маша К.","avatar":"grad-5"}`, admin); !strings.Contains(w.Body.String(), `"photo":"`+again.Photo+`"`) {
		t.Fatalf("patch lost the photo: %s", w.Body)
	}

	// removed: the letter again
	w = e.do("DELETE", "/api/users/"+id+"/photo", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "photo") {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", again.Photo, "", nil); w.Code != 404 {
		t.Fatalf("photo after delete %d", w.Code)
	}
	if w := e.do("DELETE", "/api/users/9999/photo", "", admin); w.Code != 404 {
		t.Fatalf("delete of unknown profile %d", w.Code)
	}

	// a deleted profile takes its photo along (its id may go to a new one)
	e.uploadPhoto(kid.ID, photoPNG(50, 50), admin, true)
	if w := e.do("DELETE", "/api/users/"+id, "", admin); w.Code != 204 {
		t.Fatalf("delete profile %d", w.Code)
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM user_photos`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("photos left %d %v", n, err)
	}
}

// the photo rides along wherever the profile's avatar does (me, family)
func TestProfilePhotoInMe(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var me auth.User
	json.Unmarshal(e.do("GET", "/api/me", "", admin).Body.Bytes(), &me)
	if w := e.uploadPhoto(me.ID, photoPNG(64, 64), admin, true); w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/me", "", admin); !strings.Contains(w.Body.String(), `"photo":"/api/profiles/`+strconv.FormatInt(me.ID, 10)+`/photo?v=`) {
		t.Fatalf("me: %s", w.Body)
	}
}

// An adult profile sets and removes its own photo (/api/me/photo, answered as
// /api/me is, so the web app can keep it in its session); a kid's is the
// parents' (403), and nobody sets another's through it.
func TestOwnPhoto(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	login := func(body string) (*http.Cookie, auth.User) {
		w := e.do("POST", "/api/users", body, admin)
		var u auth.User
		json.Unmarshal(w.Body.Bytes(), &u)
		return session(e.do("POST", "/api/login", `{"userId":`+strconv.FormatInt(u.ID, 10)+`}`, nil)), u
	}
	lena, lenaUser := login(`{"name":"Лена"}`)
	masha, mashaUser := login(`{"name":"Маша","isKid":true,"maxRating":"6+"}`)

	post := func(c *http.Cookie, data []byte, header bool) *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", "photo.jpg")
		fw.Write(data)
		mw.Close()
		r := httptest.NewRequest("POST", "/api/me/photo", &b)
		r.RemoteAddr = "192.168.1.20:50000"
		r.Header.Set("Content-Type", mw.FormDataContentType())
		if header {
			r.Header.Set("X-Vault-Upload", "1")
		}
		r.AddCookie(c)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w
	}

	w := post(lena, photoPNG(300, 200), true)
	var me struct {
		auth.User
		Remote bool `json:"remote"`
		Addons bool `json:"addons"`
	}
	json.Unmarshal(w.Body.Bytes(), &me)
	if w.Code != 200 || me.ID != lenaUser.ID || !strings.HasPrefix(me.Photo, "/api/profiles/"+strconv.FormatInt(lenaUser.ID, 10)+"/photo?v=") {
		t.Fatalf("own upload %d %s", w.Code, w.Body)
	}
	// the answer is /api/me's (its flags are in it), and /api/me agrees
	if !strings.Contains(w.Body.String(), `"remote":`) || !strings.Contains(w.Body.String(), `"addons":`) {
		t.Fatalf("not the /api/me shape: %s", w.Body)
	}
	if g := e.do("GET", "/api/me", "", lena); !strings.Contains(g.Body.String(), `"photo":"`+me.Photo+`"`) {
		t.Fatalf("me: %s", g.Body)
	}
	if g := e.do("GET", me.Photo, "", nil); g.Code != 200 || g.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("photo %d", g.Code)
	}
	// it is its own: the upload header is needed, a kid's is refused, nothing else is reachable
	if w := post(lena, photoPNG(30, 30), false); w.Code != 403 {
		t.Fatalf("no upload header %d", w.Code)
	}
	if w := post(lena, []byte("GIF89a\x01\x00\x01\x00"), true); w.Code != 400 {
		t.Fatalf("not a picture %d", w.Code)
	}
	if w := post(masha, photoPNG(30, 30), true); w.Code != 403 {
		t.Fatalf("a kid's own photo %d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/me/photo", "", masha); w.Code != 403 {
		t.Fatalf("a kid's own removal %d", w.Code)
	}
	if w := e.do("POST", "/api/me/photo", "", nil); w.Code != 401 {
		t.Fatalf("signed out %d", w.Code)
	}
	// the parents set the kid's (admin, by id) and a kid still cannot touch it
	if w := e.uploadPhoto(mashaUser.ID, photoPNG(60, 60), admin, true); w.Code != 200 {
		t.Fatalf("admin sets a kid's %d %s", w.Code, w.Body)
	}
	if w := e.uploadPhoto(lenaUser.ID, photoPNG(60, 60), masha, true); w.Code != 403 {
		t.Fatalf("a kid sets another's %d", w.Code)
	}

	// removed
	w = e.do("DELETE", "/api/me/photo", "", lena)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"photo"`) || !strings.Contains(w.Body.String(), `"remote":`) {
		t.Fatalf("own removal %d %s", w.Code, w.Body)
	}
	if g := e.do("GET", me.Photo, "", nil); g.Code != 404 {
		t.Fatalf("photo after removal %d", g.Code)
	}
}
