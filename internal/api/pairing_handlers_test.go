package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

// newPairEnv is newEnv with the database (pairing needs it) and a fixed
// server id.
func newPairEnv(t *testing.T) (*env, *http.Cookie) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	e := &env{db: d, t: t, store: st, h: NewRouter(Deps{Users: st, Sessions: ss, DB: d, ServerID: "srv0123"})}
	return e, e.setup()
}

const tvStart = `{"device":{"id":"tv-livingroom-1","name":"Гостиная","model":"Android TV","version":"3.1"}}`

type pairStartOut struct {
	Code, Secret, ExpiresAt, URL, ServerID string
	Interval                               int
}

func (e *env) pairStart(body string) pairStartOut {
	e.t.Helper()
	w := e.do("POST", "/api/auth/pair/start", body, nil)
	if w.Code != 200 {
		e.t.Fatalf("start %d %s", w.Code, w.Body)
	}
	var out pairStartOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) pairPoll(secret string) (int, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]string{"secret": secret})
	w := e.do("POST", "/api/auth/pair/poll", string(b), nil)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func codeBody(code string, userID int64) string {
	b, _ := json.Marshal(map[string]any{"code": code, "userId": userID})
	return string(b)
}

func TestPairFlowIssuesDeviceToken(t *testing.T) {
	e, admin := newPairEnv(t)
	st := e.pairStart(tvStart)
	if st.Interval != pairPollInterval || st.ServerID != "srv0123" || st.ExpiresAt == "" ||
		st.URL != "http://example.com/pair?code="+st.Code+"&sid=srv0123" {
		t.Fatalf("start %+v", st)
	}
	if code, out := e.pairPoll(st.Secret); code != 200 || out["status"] != "pending" {
		t.Fatalf("pending %d %v", code, out)
	}
	w := e.do("POST", "/api/auth/pair/lookup", codeBody(st.Code, 0), admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Гостиная"`) || !strings.Contains(w.Body.String(), `"remote":false`) {
		t.Fatalf("lookup %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), st.Secret) {
		t.Fatal("lookup leaks the secret")
	}
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, 0), admin); w.Code != 200 {
		t.Fatalf("approve %d %s", w.Code, w.Body)
	}
	code, out := e.pairPoll(st.Secret)
	tok, _ := out["token"].(string)
	user, _ := out["user"].(map[string]any)
	if code != 200 || out["status"] != "approved" || tok == "" || user["name"] != "Папа" {
		t.Fatalf("approved %d %v", code, out)
	}
	if w := e.do("GET", "/api/me", "", nil, bearer(tok)...); w.Code != 200 {
		t.Fatalf("token %d %s", w.Code, w.Body)
	}
	// the TV shows up in «Устройства» like any signed-in app
	if w := e.do("GET", "/api/me/devices", "", admin); !strings.Contains(w.Body.String(), `"name":"Гостиная","model":"Android TV"`) {
		t.Fatalf("devices %s", w.Body)
	}
	if code, out := e.pairPoll(st.Secret); code != 410 || out["status"] != "expired" {
		t.Fatalf("second pickup %d %v", code, out)
	}
}

func TestPairNeedsSession(t *testing.T) {
	e, _ := newPairEnv(t)
	st := e.pairStart(tvStart)
	for _, p := range []string{"/api/auth/pair/lookup", "/api/auth/pair/approve", "/api/auth/pair/deny"} {
		if w := e.do("POST", p, codeBody(st.Code, 0), nil); w.Code != 401 {
			t.Fatalf("%s without a session: %d", p, w.Code)
		}
	}
}

func TestPairDeny(t *testing.T) {
	e, admin := newPairEnv(t)
	st := e.pairStart(tvStart)
	if w := e.do("POST", "/api/auth/pair/deny", codeBody(st.Code, 0), admin); w.Code != 204 {
		t.Fatalf("deny %d %s", w.Code, w.Body)
	}
	if code, out := e.pairPoll(st.Secret); code != 410 || out["status"] != "denied" || out["error"] == "" {
		t.Fatalf("poll %d %v", code, out)
	}
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, 0), admin); w.Code != 404 {
		t.Fatalf("approve after deny %d", w.Code)
	}
}

func TestPairWhoMayApproveForWhom(t *testing.T) {
	e, admin := newPairEnv(t)
	mama := e.addUser(`{"name":"Мама","password":"mama1"}`, admin)
	kid := e.addUser(`{"name":"Маша","password":"masha","isKid":true}`, admin)
	login := func(id int64, pw string) *http.Cookie {
		b, _ := json.Marshal(map[string]any{"userId": id, "password": pw})
		c := session(e.do("POST", "/api/login", string(b), nil))
		if c == nil {
			t.Fatalf("login %d", id)
		}
		return c
	}
	mamaC, kidC := login(mama.ID, "mama1"), login(kid.ID, "masha")
	st := e.pairStart(tvStart)
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, kid.ID), mamaC); w.Code != 403 {
		t.Fatalf("adult for another profile %d", w.Code)
	}
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, mama.ID), kidC); w.Code != 403 {
		t.Fatalf("kid for another profile %d", w.Code)
	}
	// an admin sets up the kid's TV
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, kid.ID), admin); w.Code != 200 {
		t.Fatalf("admin for the kid %d %s", w.Code, w.Body)
	}
	_, out := e.pairPoll(st.Secret)
	if user, _ := out["user"].(map[string]any); user["isKid"] != true {
		t.Fatalf("%v", out)
	}
	// a kid signs a TV in to itself
	st2 := e.pairStart(`{"device":{"id":"tv-kidsroom-01","name":"Детская"}}`)
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st2.Code, 0), kidC); w.Code != 200 {
		t.Fatalf("kid for itself %d %s", w.Code, w.Body)
	}
}

func TestPairRemoteTVPasswordlessRefused(t *testing.T) {
	e, admin := newPairEnv(t)
	kid := e.addUser(`{"name":"Маша"}`, admin) // no password
	e.peer = "8.8.8.8:4000"
	st := e.pairStart(tvStart)
	e.peer = ""
	w := e.do("POST", "/api/auth/pair/lookup", codeBody(st.Code, 0), admin)
	if !strings.Contains(w.Body.String(), `"remote":true`) {
		t.Fatalf("lookup %s", w.Body)
	}
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, kid.ID), admin); w.Code != 403 || w.Header().Get("X-Vault-Error") != "remote-passwordless" {
		t.Fatalf("passwordless for a remote TV %d %s", w.Code, w.Body)
	}
	// with a password it is fine
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, 0), admin); w.Code != 200 {
		t.Fatalf("admin %d %s", w.Code, w.Body)
	}
}

func TestPairCodeGuessesThrottled(t *testing.T) {
	e, admin := newPairEnv(t)
	for i := range 5 {
		if w := e.do("POST", "/api/auth/pair/lookup", codeBody("AAAAAA", 0), admin); w.Code != 404 {
			t.Fatalf("guess %d: %d", i, w.Code)
		}
	}
	st := e.pairStart(tvStart)
	w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, 0), admin)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("after 5 bad codes: %d", w.Code)
	}
}

func TestPairPollUnknownSecretThrottled(t *testing.T) {
	e, _ := newPairEnv(t)
	for range 5 {
		if code, out := e.pairPoll("not-a-secret"); code != 410 || out["status"] != "expired" {
			t.Fatalf("%d %v", code, out)
		}
	}
	if code, _ := e.pairPoll("not-a-secret"); code != 429 {
		t.Fatalf("after 5 unknown secrets %d", code)
	}
}

func TestPairStartChecks(t *testing.T) {
	e, _ := newPairEnv(t)
	if w := e.do("POST", "/api/auth/pair/start", `{"device":{"id":"x"}}`, nil); w.Code != 400 {
		t.Fatalf("short device id %d", w.Code)
	}
	for i := range auth.PairingMaxPerClient {
		e.pairStart(`{"device":{"id":"tv-device-0` + string(rune('a'+i)) + `"}}`)
	}
	w := e.do("POST", "/api/auth/pair/start", `{"device":{"id":"tv-device-0z"}}`, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("over the cap %d %v", w.Code, w.Header())
	}
}

// A managed account has no sign-in: an administrator cannot approve a QR
// sign-in for one, whatever the request names.
func TestPairApproveRefusesAManagedAccount(t *testing.T) {
	e, admin := newPairEnv(t)
	acc, err := e.store.CreateManaged(context.Background(), 1, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	st := e.pairStart(tvStart)
	if w := e.do("POST", "/api/auth/pair/approve", codeBody(st.Code, acc.ID), admin); w.Code != 404 {
		t.Fatalf("approve for a managed account: %d %s", w.Code, w.Body)
	}
	// nothing was decided: the request is still open and signs nobody in
	if code, out := e.pairPoll(st.Secret); code != 200 || out["status"] != "pending" || out["token"] != nil {
		t.Fatalf("poll %d %v", code, out)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, acc.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d sessions of a managed account", n)
	}
}
