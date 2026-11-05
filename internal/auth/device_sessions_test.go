package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateDeviceReplacesSameDevice(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	u, err := s.CreateFirstAdmin(ctx, "Papa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	dev := DeviceInfo{ID: "ipad-0001", Name: "iPad Маши", Model: "iPad14,1", Version: "1.0"}
	first, err := ss.CreateDevice(ctx, u.ID, dev)
	if err != nil {
		t.Fatal(err)
	}
	dev.Name = "iPad"
	second, err := ss.CreateDevice(ctx, u.ID, dev)
	if err != nil || second == first || len(second) != 43 {
		t.Fatalf("second %q %v", second, err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=? AND device_id=?`, u.ID, dev.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows %d %v", n, err)
	}
	if _, _, err := ss.LookupSession(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first token: %v", err)
	}
	got, sess, err := ss.LookupSession(ctx, second)
	if err != nil || got.ID != u.ID || sess.Kind != KindNative || sess.Device != dev || sess.ID == 0 || sess.UserID != u.ID {
		t.Fatalf("%+v %+v %v", got, sess, err)
	}
	// a web session of the same profile is untouched and reported as web
	web, _ := ss.Create(ctx, u.ID)
	if _, ws, err := ss.LookupSession(ctx, web); err != nil || ws.Kind != KindWeb {
		t.Fatalf("web %+v %v", ws, err)
	}
	if _, err := ss.CreateDevice(ctx, u.ID, dev); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(ctx, web); err != nil {
		t.Fatalf("web session removed by device sign-in: %v", err)
	}
}

func TestDevicesAndRevokeDevice(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	k, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true})
	web, _ := ss.Create(ctx, a.ID)
	if _, err := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "mac-00001", Name: "Mac"}); err != nil {
		t.Fatal(err)
	}
	tok, _ := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "phone-0001", Name: "iPhone"})
	kt, _ := ss.CreateDevice(ctx, k.ID, DeviceInfo{ID: "kid-00001", Name: "iPad"})
	list, err := ss.Devices(ctx, a.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("%+v %v", list, err)
	}
	_, ws, _ := ss.LookupSession(ctx, web)
	_, ks, _ := ss.LookupSession(ctx, kt)
	// a browser's session is listed and can be ended too
	if err := ss.RevokeDevice(ctx, a.ID, ws.ID); err != nil {
		t.Fatalf("web rowid: %v", err)
	}
	if _, err := ss.Lookup(ctx, web); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked web session: %v", err)
	}
	if err := ss.RevokeDevice(ctx, a.ID, ks.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's device: %v", err)
	}
	_, ps, _ := ss.LookupSession(ctx, tok)
	if err := ss.RevokeDevice(ctx, a.ID, ps.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token: %v", err)
	}
}

func TestGuardRemotePasswordlessNative(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	s.CreateFirstAdmin(ctx, "Papa", "secret")
	k, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true})
	tok, _ := ss.CreateDevice(ctx, k.ID, DeviceInfo{ID: "kid-00001"})
	g := &Guard{Sessions: ss}
	h := g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := SessionFrom(r.Context())
		if !ok || sess.Kind != KindNative || TokenFrom(r.Context()) != tok {
			t.Errorf("ctx %+v %v", sess, ok)
		}
		w.WriteHeader(204)
	}))
	req := func(peer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := req("127.0.0.1:1"); w.Code != 204 {
		t.Fatalf("home %d", w.Code)
	}
	w := req("203.0.113.7:1")
	if w.Code != 403 || w.Header().Get("X-Vault-Error") != "remote-passwordless" {
		t.Fatalf("remote %d %v", w.Code, w.Header())
	}
}
