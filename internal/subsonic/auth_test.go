package subsonic

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
)

func TestPingTokenAuth(t *testing.T) {
	e := newEnv(t)
	p := e.creds()
	p.Set("f", "json")
	r := e.mustOK(e.decode(e.req("ping", "", p)))
	if r["version"] != "1.16.1" || r["type"] != "antresol" || r["openSubsonic"] != true || r["serverVersion"] != "test" {
		t.Fatalf("envelope %v", r)
	}
	// the user name is matched ignoring case
	p.Set("u", "папа")
	e.mustOK(e.decode(e.req("ping", "", p)))
}

func TestWrongTokenCode40(t *testing.T) {
	e := newEnv(t)
	p := e.creds()
	p.Set("f", "json")
	p.Set("t", strings.Repeat("0", 32))
	if code, msg := errCode(e.decode(e.req("ping", "", p))); code != 40 || msg != "Неверное имя или пароль" {
		t.Fatalf("code %d %q", code, msg)
	}
	p = e.creds()
	p.Set("f", "json")
	p.Set("u", "Никто")
	if code, _ := errCode(e.decode(e.req("ping", "", p))); code != 40 {
		t.Fatalf("unknown user: code %d", code)
	}
}

func TestNoAppPasswordCode40WithHint(t *testing.T) {
	e := newEnv(t)
	if err := e.users.ClearAppPassword(context.Background(), e.admin.ID); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("secret" + "abc123")) // the login password does not do
	p := url.Values{"u": {"Папа"}, "t": {hex.EncodeToString(sum[:])}, "s": {"abc123"}, "f": {"json"}}
	code, msg := errCode(e.decode(e.req("ping", "", p)))
	if code != 40 || !strings.Contains(msg, "пароль для приложений") {
		t.Fatalf("code %d %q", code, msg)
	}
}

func TestPlainAndEncPassword(t *testing.T) {
	e := newEnv(t)
	for _, pass := range []string{e.app, "enc:" + hex.EncodeToString([]byte(e.app))} {
		p := url.Values{"u": {"Папа"}, "p": {pass}, "f": {"json"}}
		e.mustOK(e.decode(e.req("ping", "", p)))
	}
	// the login password is not an app password (in either form)
	for _, pass := range []string{"secret", "enc:" + hex.EncodeToString([]byte("secret")), "enc:zz"} {
		p := url.Values{"u": {"Папа"}, "p": {pass}, "f": {"json"}}
		if code, _ := errCode(e.decode(e.req("ping", "", p))); code != 40 {
			t.Fatalf("p=%q: code %d", pass, code)
		}
	}
}

func TestPasswordlessProfileEmptyPasswordRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	kid, err := e.users.CreateUser(ctx, auth.NewUser{Name: "Вася", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	p := url.Values{"u": {"Вася"}, "p": {""}, "f": {"json"}}
	if code, _ := errCode(e.decode(e.req("ping", "", p))); code != 40 {
		t.Fatalf("empty p: code %d", code)
	}
	app, err := e.users.SetAppPassword(ctx, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := errCode(e.decode(e.req("ping", "", p))); code != 40 {
		t.Fatalf("empty p with an app password set: code %d", code)
	}
	p.Set("p", app)
	e.mustOK(e.decode(e.req("ping", "", p)))
	// outside the home network a profile without a password cannot log in
	code, msg := errCode(e.decode(e.req("ping", remoteAddr, p)))
	if code != 40 || !strings.Contains(msg, "не из дома") {
		t.Fatalf("remote passwordless: code %d %q", code, msg)
	}
	// a profile with a password can
	e.mustOK(e.decode(e.req("ping", remoteAddr, url.Values{"u": {"Папа"}, "p": {e.app}, "f": {"json"}})))
}

func TestThrottleAfterFailures(t *testing.T) {
	e := newEnv(t)
	bad := url.Values{"u": {"Папа"}, "p": {"wrong"}, "f": {"json"}}
	for i := 0; i < 5; i++ {
		if code, msg := errCode(e.decode(e.req("ping", "", bad))); code != 40 || msg != "Неверное имя или пароль" {
			t.Fatalf("attempt %d: %d %q", i, code, msg)
		}
	}
	good := url.Values{"u": {"Папа"}, "p": {e.app}, "f": {"json"}}
	code, msg := errCode(e.decode(e.req("ping", "", good)))
	if code != 40 || !strings.Contains(msg, "Слишком много попыток") {
		t.Fatalf("after 5 failures: %d %q", code, msg)
	}
	// the web login shares the throttle: same address, same profile
	web := api.NewRouter(api.Deps{Users: e.users, Sessions: auth.NewSessions(e.db), Throttle: e.srv.Throttle})
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"userId":`+itoa(e.admin.ID)+`,"password":"secret"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = homeAddr
	w := httptest.NewRecorder()
	web.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("web login after Subsonic failures: %d %s", w.Code, w.Body)
	}
}

// A client app's successful logins must not wipe the strikes an attacker
// built up guessing the profile's web password (F2): the attacker rotates
// addresses, so only the profile key slows them down.
func TestAppLoginSuccessKeepsWebStrikes(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	e.srv.Throttle.Now = func() time.Time { return now }
	web := api.NewRouter(api.Deps{Users: e.users, Sessions: auth.NewSessions(e.db), Throttle: e.srv.Throttle})
	login := func(addr, pass string) int {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"userId":`+itoa(e.admin.ID)+`,"password":"`+pass+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		web.ServeHTTP(w, r)
		return w.Code
	}
	for i := 1; i <= 5; i++ {
		if code := login("203.0.113."+itoa(int64(i))+":4000", "guess"); code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	now = now.Add(31 * time.Second) // the first lock is over
	good := url.Values{"u": {"Папа"}, "p": {e.app}, "f": {"json"}}
	e.mustOK(e.decode(e.req("ping", "198.51.100.9:5000", good))) // the profile's phone, away from home
	if code := login("203.0.113.6:4000", "guess"); code != http.StatusUnauthorized {
		t.Fatalf("6th guess: %d", code)
	}
	if code := login("203.0.113.7:4000", "secret"); code != http.StatusTooManyRequests {
		t.Fatalf("after 6 guesses and an app login the profile must stay locked: %d", code)
	}
}

func TestViewSuffixAndPOST(t *testing.T) {
	e := newEnv(t)
	album, _ := e.album("Кино", "Группа крови", 1988, "Рок", tTrack{1, "Группа крови"})
	p := e.creds()
	p.Set("f", "json")
	e.mustOK(e.decode(e.req("ping.view", "", p)))
	p.Set("id", itoa(album))
	r := httptest.NewRequest("POST", "/rest/getAlbum", strings.NewReader(p.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = homeAddr
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, r)
	got := e.mustOK(e.decode(w))
	if a := got["album"].(map[string]any); a["name"] != "Группа крови" {
		t.Fatalf("album %v", a)
	}
}

func TestMissingParamCode10(t *testing.T) {
	e := newEnv(t)
	if code, _ := errCode(e.call("getAlbum")); code != 10 {
		t.Fatalf("getAlbum without id: %d", code)
	}
	if code, _ := errCode(e.decode(e.req("ping", "", url.Values{"f": {"json"}}))); code != 10 {
		t.Fatalf("no u: %d", code)
	}
	if code, _ := errCode(e.call("getAlbumList2")); code != 10 {
		t.Fatalf("getAlbumList2 without type: %d", code)
	}
}

func TestNotFoundCode70(t *testing.T) {
	e := newEnv(t)
	for _, m := range []string{"getAlbum", "getSong", "getArtist", "getPlaylist", "getCoverArt"} {
		if code, _ := errCode(e.call(m, "id", "99999")); code != 70 {
			t.Fatalf("%s: code %d", m, code)
		}
	}
}

func TestXMLEnvelopeByDefault(t *testing.T) {
	e := newEnv(t)
	w := e.req("ping", "", e.creds())
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("content-type %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<subsonic-response xmlns="http://subsonic.org/restapi" status="ok" version="1.16.1" type="antresol"`) {
		t.Fatalf("xml %s", body)
	}
}

// Errors answer in the request language (Accept-Language or ?lang=); no
// language → Russian, as music apps send none.
func TestErrorMessageLanguage(t *testing.T) {
	e := newEnv(t)
	call := func(lang string, params url.Values) string {
		r := httptest.NewRequest("GET", "/rest/getAlbum?"+params.Encode(), nil)
		r.RemoteAddr = homeAddr
		if lang != "" {
			r.Header.Set("Accept-Language", lang)
		}
		w := httptest.NewRecorder()
		e.srv.ServeHTTP(w, r)
		_, msg := errCode(e.decode(w))
		return msg
	}
	p := e.creds()
	p.Set("f", "json")
	p.Set("id", "99999")
	if m := call("", p); m != "Альбом не найден" {
		t.Errorf("no language: %q", m)
	}
	if m := call("en", p); m != "Album not found" {
		t.Errorf("en: %q", m)
	}
	p.Del("id")
	if m := call("en-GB,en;q=0.8", p); m != "Missing parameter id" {
		t.Errorf("en missing: %q", m)
	}
	if m := call("", p); m != "Не указан параметр id" {
		t.Errorf("ru missing: %q", m)
	}
	bad := url.Values{"u": {e.admin.Name}, "p": {"wrong"}, "f": {"json"}, "lang": {"en"}}
	if m := call("ru", bad); m != "Incorrect name or password" {
		t.Errorf("?lang=en login: %q", m)
	}
}
