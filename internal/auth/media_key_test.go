package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nativeKey signs a device in for userID and returns its token, session and keys.
func nativeKey(t *testing.T, ss *Sessions, userID int64, device string) (string, Session) {
	t.Helper()
	tok, err := ss.CreateDevice(context.Background(), userID, DeviceInfo{ID: device, Name: device})
	if err != nil {
		t.Fatal(err)
	}
	_, sess, err := ss.LookupSession(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	return tok, sess
}

func newKeys(t *testing.T, ss *Sessions) *MediaKeys {
	t.Helper()
	return NewMediaKeys(bytes.Repeat([]byte{7}, 32), ss)
}

func TestMediaKeyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, sess := nativeKey(t, ss, a.ID, "phone-0001")
	mk := newKeys(t, ss)
	key, exp := mk.Issue(sess, tok, 12*time.Hour)
	if !strings.HasPrefix(key, "v1.") || strings.Count(key, ".") != 4 || strings.Contains(key, tok) {
		t.Fatalf("key %q", key)
	}
	if d := time.Until(exp); d < 11*time.Hour || d > 13*time.Hour {
		t.Fatalf("exp %v", exp)
	}
	u, got, err := mk.Verify(ctx, key)
	if err != nil || u.ID != a.ID || got.ID != sess.ID || got.Kind != KindNative {
		t.Fatalf("%+v %+v %v", u, got, err)
	}
}

func TestMediaKeyTampered(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	k, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true})
	tok, sess := nativeKey(t, ss, a.ID, "phone-0001")
	mk := newKeys(t, ss)
	key, _ := mk.Issue(sess, tok, time.Hour)
	parts := strings.Split(key, ".")
	flip := func(s string) string {
		b := []byte(s)
		if b[len(b)-1] == 'A' {
			b[len(b)-1] = 'B'
		} else {
			b[len(b)-1] = 'A'
		}
		return string(b)
	}
	bad := []string{
		key[:len(key)-1] + string(flip(key[len(key)-1:])),
		strings.Join([]string{parts[0], parts[1], strconv.FormatInt(k.ID, 10), parts[3], parts[4]}, "."), // another profile
		strings.Join([]string{parts[0], parts[1], parts[2], strconv.FormatInt(time.Now().Add(48*time.Hour).Unix(), 10), parts[4]}, "."),
		strings.Join([]string{"v2", parts[1], parts[2], parts[3], parts[4]}, "."),
		"", "v1", "v1.x.y.z.w", key + ".x",
	}
	for _, b := range bad {
		if _, _, err := mk.Verify(ctx, b); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q: %v", b, err)
		}
	}
	// another secret does not accept the key
	if _, _, err := NewMediaKeys(bytes.Repeat([]byte{8}, 32), ss).Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other secret: %v", err)
	}
}

func TestMediaKeyExpired(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, sess := nativeKey(t, ss, a.ID, "phone-0001")
	mk := newKeys(t, ss)
	key, exp := mk.Issue(sess, tok, time.Hour)
	mk.Now = func() time.Time { return exp.Add(time.Second) }
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	mk.Now = func() time.Time { return exp.Add(-time.Second) }
	if _, _, err := mk.Verify(ctx, key); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
}

func TestMediaKeySessionTTL(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, sess := nativeKey(t, ss, a.ID, "phone-0001")
	mk := newKeys(t, ss)
	key, _ := mk.Issue(sess, tok, time.Hour)
	old := time.Now().Add(-sessionTTL - time.Hour).UTC().Format(time.RFC3339)
	if _, err := d.Exec(`UPDATE sessions SET last_seen=? WHERE token=?`, old, TokenHash(tok)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session past TTL: %v", err)
	}
}

// A browser session's key (AirPlay from the web player) works while the
// session lives and ends with it.
func TestMediaKeyWebSession(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	web, _ := ss.Create(ctx, a.ID)
	_, sess, _ := ss.LookupSession(ctx, web)
	mk := newKeys(t, ss)
	key, _ := mk.Issue(sess, web, time.Hour)
	u, got, err := mk.Verify(ctx, key)
	if err != nil || u.ID != a.ID || got.Kind != KindWeb {
		t.Fatalf("web session key: %+v %+v %v", u, got, err)
	}
	if _, err := ss.db.Exec(`DELETE FROM sessions WHERE token=?`, TokenHash(web)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("key of an ended web session: %v", err)
	}
}

func TestMediaKeyRevokedWithDevice(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, sess := nativeKey(t, ss, a.ID, "phone-0001")
	mk := newKeys(t, ss)
	key, _ := mk.Issue(sess, tok, 12*time.Hour)
	if err := ss.RevokeDevice(ctx, a.ID, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestMediaKeyRowidReuse(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	k, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true})
	if _, err := ss.Create(ctx, a.ID); err != nil { // an older row below the kid's
		t.Fatal(err)
	}
	tok, sess := nativeKey(t, ss, k.ID, "ipad-00001")
	var maxID int64
	if err := d.QueryRow(`SELECT MAX(rowid) FROM sessions`).Scan(&maxID); err != nil || maxID != sess.ID {
		t.Fatalf("kid row %d is not the newest (%d) %v", sess.ID, maxID, err)
	}
	mk := newKeys(t, ss)
	key, _ := mk.Issue(sess, tok, 12*time.Hour)
	if err := ss.RevokeDevice(ctx, k.ID, sess.ID); err != nil {
		t.Fatal(err)
	}
	web, _ := ss.Create(ctx, a.ID)
	_, ws, err := ss.LookupSession(ctx, web)
	if err != nil || ws.ID != sess.ID {
		t.Fatalf("admin web session got rowid %d, want reused %d (%v)", ws.ID, sess.ID, err)
	}
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reused rowid: %v", err)
	}
	// even a native admin row on the reused rowid does not take the kid's key
	if err := ss.Revoke(ctx, web); err != nil {
		t.Fatal(err)
	}
	if _, an := nativeKey(t, ss, a.ID, "mac-000001"); an.ID != sess.ID {
		t.Fatalf("admin native rowid %d, want %d", an.ID, sess.ID)
	}
	if _, _, err := mk.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reused rowid (native): %v", err)
	}
}

func TestMediaKeySecretPersists(t *testing.T) {
	ctx := context.Background()
	_, _, d := newStoresDB(t)
	a, err := LoadOrCreateSecret(ctx, d, "media_key_secret")
	if err != nil || len(a) != 32 {
		t.Fatalf("%x %v", a, err)
	}
	b, err := LoadOrCreateSecret(ctx, d, "media_key_secret")
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("%x != %x %v", a, b, err)
	}
	if c, _ := LoadOrCreateSecret(ctx, d, "other_secret"); bytes.Equal(a, c) {
		t.Fatal("another key shares the secret")
	}
}

func TestGuardMediaKeyRoutes(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, sess := nativeKey(t, ss, a.ID, "phone-0001")
	mk := newKeys(t, ss)
	key, _ := mk.Issue(sess, tok, time.Hour)
	g := &Guard{Sessions: ss, Keys: mk}
	h := g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := ViaMediaKey(r.Context())
		u, _ := UserFrom(r.Context())
		if !ok || got != key || u.ID != a.ID || TokenFrom(r.Context()) != "" {
			t.Errorf("ctx: %q %v %d", got, ok, u.ID)
		}
		w.WriteHeader(204)
	}))
	q := "?mk=" + url.QueryEscape(key)
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/stream/1", 204}, {"HEAD", "/api/stream/1", 204}, {"GET", "/api/hls/s/index.m3u8", 204},
		{"GET", "/api/hls/s/seg00001.m4s", 204}, {"GET", "/api/tracks/5/stream", 204}, {"GET", "/api/audio/7", 204},
		{"POST", "/api/stream/1", 401}, {"PUT", "/api/hls/s", 401}, {"DELETE", "/api/hls/s", 401},
		{"GET", "/api/tracks/5/lyrics", 401}, {"GET", "/api/download/1", 401}, {"GET", "/api/subs/1/0.vtt", 401},
		{"GET", "/api/images/x", 401}, {"GET", "/api/torrent-stream/ih/0", 204}, {"POST", "/api/torrent-stream/ih/0", 401},
		{"GET", "/api/streamx", 401},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path+q, nil))
		if w.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, w.Code, c.want)
		}
	}
	// no Keys: mk is not a credential
	w := httptest.NewRecorder()
	(&Guard{Sessions: ss}).Require(h).ServeHTTP(w, httptest.NewRequest("GET", "/api/stream/1"+q, nil))
	if w.Code != 401 {
		t.Fatalf("guard without keys: %d", w.Code)
	}
}
