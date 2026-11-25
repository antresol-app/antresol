package addonkit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testKey  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // gitleaks:allow (a test value)
	testUser = `{"id":2,"uid":"u2","name":"Mama","isAdmin":true,"lang":"ru"}`
)

func userReq(header, sig string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/admin/x", nil)
	if header != "" {
		r.Header.Set(UserHeader, header)
	}
	if sig != "" {
		r.Header.Set(UserSigHeader, sig)
	}
	return r
}

// Without a key (a server that does not sign, as before) the header is
// taken as it is, whatever a signature says.
func TestUserFromWithoutKeyTakesTheHeader(t *testing.T) {
	t.Setenv(UserKeyEnv, "")
	if u, ok := UserFrom(userReq(testUser, "")); !ok || u.ID != 2 {
		t.Fatalf("unsigned: %+v %v", u, ok)
	}
	if _, ok := UserFrom(userReq(testUser, "1.deadbeef")); !ok {
		t.Fatal("with no key a signature is not looked at")
	}
}

func TestUserFromChecksTheSignature(t *testing.T) {
	t.Setenv(UserKeyEnv, testKey)
	t.Setenv(AcceptUnsignedEnv, "")
	now := time.Now()
	good := SignUser(testKey, testUser, now)
	if u, ok := UserFrom(userReq(testUser, good)); !ok || u.ID != 2 || !u.IsAdmin {
		t.Fatalf("signed: %+v %v", u, ok)
	}
	forged := strings.Replace(testUser, `"isAdmin":true`, `"isAdmin":true,"id":1`, 1)
	for name, req := range map[string]*http.Request{
		"another header":    userReq(forged, good),
		"another key":       userReq(testUser, SignUser(strings.ToUpper(testKey), testUser, now)),
		"stale":             userReq(testUser, SignUser(testKey, testUser, now.Add(-10*time.Minute))),
		"from the future":   userReq(testUser, SignUser(testKey, testUser, now.Add(10*time.Minute))),
		"no dot":            userReq(testUser, "deadbeef"),
		"no time":           userReq(testUser, ".deadbeef"),
		"time not a number": userReq(testUser, "x."+strings.SplitN(good, ".", 2)[1]),
		"time moved":        userReq(testUser, "1."+strings.SplitN(good, ".", 2)[1]),
	} {
		if u, ok := UserFrom(req); ok {
			t.Errorf("%s: accepted %+v", name, u)
		}
	}
}

// With a key set an unsigned header is refused, whatever the flag leaves
// unsaid; "on" is the admin's escape hatch. A wrong signature never passes.
func TestUserFromUnsignedFlag(t *testing.T) {
	t.Setenv(UserKeyEnv, testKey)
	for _, v := range []string{"", "off", "1", "true"} {
		t.Setenv(AcceptUnsignedEnv, v)
		if _, ok := UserFrom(userReq(testUser, "")); ok {
			t.Errorf("%s=%q: an unsigned header is accepted", AcceptUnsignedEnv, v)
		}
	}
	t.Setenv(AcceptUnsignedEnv, "on")
	if _, ok := UserFrom(userReq(testUser, "")); !ok {
		t.Error("on: an unsigned header is refused")
	}
	if _, ok := UserFrom(userReq(testUser, "1.deadbeef")); ok {
		t.Error("on: a wrong signature is accepted")
	}
	t.Setenv(AcceptUnsignedEnv, "")
	if _, ok := UserFrom(userReq(testUser, SignUser(testKey, testUser, time.Now()))); !ok {
		t.Error("a signed header is refused")
	}
	if _, ok := UserFrom(userReq("", "")); ok {
		t.Error("no header at all is a user")
	}
}

// The key is the variable's text as it is (its ASCII characters), and the
// signed text is "<seconds>.<header value>": what a check written in
// another language does.
func TestSignUserFormat(t *testing.T) {
	now := time.Unix(1700000000, 0)
	m := hmac.New(sha256.New, []byte(testKey))
	m.Write([]byte("1700000000." + testUser))
	if got, want := SignUser(testKey, testUser, now), "1700000000."+hex.EncodeToString(m.Sum(nil)); got != want {
		t.Fatalf("SignUser = %q, want %q", got, want)
	}
}

func TestRequireAdminWantsTheSignature(t *testing.T) {
	t.Setenv(UserKeyEnv, testKey)
	t.Setenv(AcceptUnsignedEnv, "")
	h := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for sig, want := range map[string]int{"": http.StatusForbidden, "1.dead": http.StatusForbidden, SignUser(testKey, testUser, time.Now()): http.StatusNoContent} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, userReq(testUser, sig))
		if rec.Code != want {
			t.Errorf("sig %q: %d, want %d", sig, rec.Code, want)
		}
	}
}
