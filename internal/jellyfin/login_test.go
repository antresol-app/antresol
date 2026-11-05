package jellyfin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
)

type fixture struct {
	Steps []struct {
		Method, Path, Header, Auth, Save string
		Body                             any
		Status                           int
	}
}

// replay plays a recorded client sequence and returns the saved bodies.
func (e *env) replay(file string) map[string][]byte {
	e.t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		e.t.Fatal(err)
	}
	var fx fixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		e.t.Fatal(err)
	}
	saved := map[string][]byte{}
	vars := map[string]string{"{{app}}": e.anyaApp}
	sub := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, k, v)
		}
		return s
	}
	for i, st := range fx.Steps {
		hdr := map[string]string{}
		if st.Auth != "" {
			name := st.Header
			if name == "" {
				name = "Authorization"
			}
			hdr[name] = sub(st.Auth)
		}
		var body any
		if st.Body != nil {
			b, _ := json.Marshal(st.Body)
			body = sub(string(b))
		}
		w := e.do(st.Method, sub(st.Path), hdr, body)
		if w.Code != st.Status {
			e.t.Fatalf("step %d %s %s: %d, want %d: %s", i, st.Method, st.Path, w.Code, st.Status, w.Body)
		}
		if st.Save != "" {
			saved[st.Save] = w.Body.Bytes()
		}
		if st.Save == "login" {
			var res struct {
				AccessToken string
				User        struct{ Id string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				e.t.Fatal(err)
			}
			vars["{{token}}"], vars["{{userId}}"] = res.AccessToken, res.User.Id
		}
	}
	return saved
}

type authResult struct {
	AccessToken, ServerId string
	User                  struct {
		Name, Id, ServerId string
		HasPassword        bool
		Policy             struct{ IsAdministrator, EnableRemoteAccess, IsDisabled bool }
		Configuration      struct{ MyMediaExcludes, OrderedViews []string }
	}
	SessionInfo struct{ UserId, UserName, Client, DeviceId, DeviceName, ApplicationVersion string }
}

type viewsResult struct {
	Items []struct {
		Name, Id, ServerId, Type, CollectionType string
		IsFolder                                 bool
		ImageTags                                map[string]string
	}
	TotalRecordCount, StartIndex int
}

func checkViews(t *testing.T, body []byte, serverID string) {
	t.Helper()
	var v viewsResult
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.TotalRecordCount != 2 || v.StartIndex != 0 || len(v.Items) != 2 {
		t.Fatalf("%s", body)
	}
	want := [][3]string{{"Фильмы", "movies", ViewID("movies")}, {"Сериалы", "tvshows", ViewID("tvshows")}}
	for i, it := range v.Items {
		if it.Name != want[i][0] || it.CollectionType != want[i][1] || it.Id != want[i][2] || it.Type != "CollectionFolder" ||
			!it.IsFolder || it.ServerId != serverID || it.ImageTags == nil {
			t.Fatalf("view %d: %+v", i, it)
		}
	}
}

func TestSwiftfinLoginSequence(t *testing.T) {
	e := newEnv(t)
	saved := e.replay("testdata/swiftfin-login.json")
	var res authResult
	if err := json.Unmarshal(saved["login"], &res); err != nil {
		t.Fatal(err)
	}
	if !hex32.MatchString(res.AccessToken) || res.User.Id != UserID(e.anya.ID) || res.User.Name != "Аня" || res.User.Policy.IsAdministrator ||
		!res.User.Policy.EnableRemoteAccess || !hex32.MatchString(res.ServerId) || res.User.Configuration.MyMediaExcludes == nil {
		t.Fatalf("%s", saved["login"])
	}
	if res.SessionInfo.Client != "Swiftfin iOS" || res.SessionInfo.DeviceId != "3F2B-iphone" || res.SessionInfo.UserId != res.User.Id {
		t.Fatalf("session %+v", res.SessionInfo)
	}
	if strings.Contains(string(saved["login"]), e.anyaApp) {
		t.Fatal("the response echoes the app password")
	}
	var me authResult
	if err := json.Unmarshal(saved["me"], &me.User); err != nil || me.User.Id != res.User.Id || me.User.Policy.IsAdministrator {
		t.Fatalf("me %s", saved["me"])
	}
	checkViews(t, saved["views"], res.ServerId)
	if strings.Contains(e.logs.String(), e.anyaApp) || strings.Contains(e.logs.String(), res.AccessToken) {
		t.Fatal("a secret in the log")
	}
}

func TestInfuseLoginLegacyViews(t *testing.T) {
	e := newEnv(t)
	saved := e.replay("testdata/infuse-login.json")
	var res authResult
	if err := json.Unmarshal(saved["login"], &res); err != nil {
		t.Fatal(err)
	}
	if res.User.Id != UserID(e.anya.ID) || res.SessionInfo.Client != "Infuse-Direct" || res.SessionInfo.DeviceName != "Apple TV" {
		t.Fatalf("%s", saved["login"])
	}
	checkViews(t, saved["views"], res.ServerId)
}

func TestLoginPasswordField(t *testing.T) {
	e := newEnv(t)
	w := e.do("POST", "/Users/AuthenticateByName", swiftfinHdr(""), map[string]string{"Username": "Аня", "Password": e.anyaApp})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// keys in any case, the name in any case
	w = e.do("POST", "/users/authenticatebyname", swiftfinHdr(""), `{"username":"аня","pw":"`+e.anyaApp+`"}`)
	if w.Code != 200 {
		t.Fatalf("lowercase keys: %d %s", w.Code, w.Body)
	}
}

func TestMediaFolders(t *testing.T) {
	e := newEnv(t)
	tok := e.login("Аня", e.anyaApp)
	w := e.do("GET", "/Library/MediaFolders", swiftfinHdr(tok), nil)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	var p publicInfo
	decode(t, e.do("GET", "/System/Info/Public", nil, nil), &p)
	checkViews(t, w.Body.Bytes(), p.Id)
	if w := e.do("GET", "/Library/MediaFolders", nil, nil); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}

func TestLoginWebPasswordRejected(t *testing.T) {
	e := newEnv(t)
	w := e.do("POST", "/Users/AuthenticateByName", swiftfinHdr(""), map[string]string{"Username": "Аня", "Pw": anyaPassword})
	if w.Code != 401 || w.Body.Len() != 0 {
		t.Fatalf("login password must not work: %d %q", w.Code, w.Body)
	}
	// no app password at all («Миша»), unknown profile, empty password
	for _, in := range []map[string]string{{"Username": "Миша", "Pw": "misha-pw"}, {"Username": "Никто", "Pw": "x"}, {"Username": "Аня"}} {
		if w := e.do("POST", "/Users/AuthenticateByName", nil, in); w.Code != 401 || w.Body.Len() != 0 {
			t.Fatalf("%v: %d %q", in, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/Users/AuthenticateByName", nil, "{nope"); w.Code != 400 || w.Body.Len() != 0 {
		t.Fatalf("bad json: %d %q", w.Code, w.Body)
	}
}

func TestJellyfinStrikesShareWebThrottle(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		if w := e.remote("POST", "/Users/AuthenticateByName", nil, map[string]string{"Username": "Аня", "Pw": "wrong"}); w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	w := e.remote("POST", "/Users/AuthenticateByName", nil, map[string]string{"Username": "Аня", "Pw": e.anyaApp})
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("locked: %d %v", w.Code, w.Header())
	}
	if s, _ := strconv.Atoi(w.Header().Get("Retry-After")); s <= 0 {
		t.Fatalf("Retry-After %q", w.Header().Get("Retry-After"))
	}
	web := api.NewRouter(api.Deps{Users: e.users, Sessions: auth.NewSessions(e.db), Network: e.network, Throttle: e.throttle})
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"userId":`+strconv.FormatInt(e.anya.ID, 10)+`,"password":"`+anyaPassword+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = remoteAddr
	rw := httptest.NewRecorder()
	web.ServeHTTP(rw, r)
	if rw.Code != 429 {
		t.Fatalf("web login after Jellyfin strikes: %d %s", rw.Code, rw.Body)
	}
}

func TestPasswordlessRemoteRefused(t *testing.T) {
	e := newEnv(t)
	in := map[string]string{"Username": "Гость", "Pw": e.guestApp}
	if w := e.remote("POST", "/Users/AuthenticateByName", nil, in); w.Code != 401 || w.Body.Len() != 0 {
		t.Fatalf("remote: %d %q", w.Code, w.Body)
	}
	w := e.do("POST", "/Users/AuthenticateByName", nil, in)
	if w.Code != 200 {
		t.Fatalf("home: %d %s", w.Code, w.Body)
	}
	var res authResult
	decode(t, w, &res)
	h := map[string]string{"X-Emby-Token": res.AccessToken}
	if w := e.do("GET", "/Users/Me", h, nil); w.Code != 200 {
		t.Fatalf("token at home: %d", w.Code)
	}
	// the token got at home does not work from outside
	if w := e.remote("GET", "/Users/Me", h, nil); w.Code != 401 || w.Body.Len() != 0 {
		t.Fatalf("token outside home: %d %q", w.Code, w.Body)
	}
	// a profile with a login password uses its token anywhere
	if w := e.remote("GET", "/Users/Me", map[string]string{"X-Emby-Token": e.login("Аня", e.anyaApp)}, nil); w.Code != 200 {
		t.Fatalf("Аня outside home: %d", w.Code)
	}
}
