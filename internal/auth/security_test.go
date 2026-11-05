package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestNetworkTrusted(t *testing.T) {
	var none *Network
	cases := []struct {
		peer string
		want bool
	}{
		{"127.0.0.1:1", true},
		{"[::1]:1", true},
		{"10.1.2.3:1", true},
		{"172.20.0.4:1", true},
		{"192.168.1.20:1", true},
		{"[::ffff:192.168.1.20]:1", true},
		{"[fd12:3456::1]:1", true},
		{"[fe80::1%en0]:1", true},
		{"203.0.113.7:1", false},
		{"[2001:db8::1]:1", false},
		{"100.100.1.1:1", false}, // CGNAT/Tailscale only when listed
		{"garbage", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if got := none.IsTrusted(r); got != c.want {
			t.Errorf("%s: %v", c.peer, got)
		}
	}
	n := &Network{Trusted: []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")}}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "100.100.1.1:1"
	if !n.IsTrusted(r) {
		t.Error("VAULT_TRUSTED_NETS not honoured")
	}
}

func TestNetworkForwarded(t *testing.T) {
	n := &Network{Proxies: []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}}
	try := func(peer string, hdr ...string) (string, bool, bool) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Add(hdr[i], hdr[i+1])
		}
		a, ok := n.ClientIP(r)
		return a.String(), ok, n.IsTrusted(r)
	}
	if a, ok, tr := try("172.18.0.3:1", "X-Forwarded-For", "203.0.113.9"); a != "203.0.113.9" || !ok || tr {
		t.Errorf("proxy xff: %s %v %v", a, ok, tr)
	}
	if a, _, tr := try("172.18.0.3:1", "X-Forwarded-For", "192.168.1.5, 203.0.113.9"); a != "203.0.113.9" || tr {
		t.Errorf("right-most hop: %s %v", a, tr)
	}
	if a, _, tr := try("172.18.0.3:1", "X-Forwarded-For", "203.0.113.9, 172.18.0.9"); a != "203.0.113.9" || tr {
		t.Errorf("chained proxies: %s %v", a, tr)
	}
	if a, _, tr := try("172.18.0.3:1", "X-Forwarded-For", "203.0.113.9", "X-Forwarded-For", "192.168.1.5"); a != "192.168.1.5" || !tr {
		t.Errorf("repeated header: %s %v", a, tr)
	}
	if a, _, tr := try("172.18.0.3:1", "X-Real-IP", "192.168.1.5"); a != "192.168.1.5" || !tr {
		t.Errorf("x-real-ip: %s %v", a, tr)
	}
	if _, ok, tr := try("172.18.0.3:1", "X-Forwarded-For", "unknown"); ok || tr {
		t.Error("unparsable hop must not be trusted")
	}
	if _, ok, tr := try("172.18.0.3:1"); ok || tr {
		t.Errorf("proxy without forwarding headers must be remote: %v %v", ok, tr)
	}
	if _, ok, tr := try("172.18.0.3:1", "Forwarded", "for=203.0.113.9"); ok || tr {
		t.Errorf("proxy with only Forwarded must be remote: %v %v", ok, tr)
	}
	// not a proxy: headers ignored, and their presence means remote
	if a, ok, tr := try("192.168.1.9:1", "X-Forwarded-For", "192.168.1.5"); a != "192.168.1.9" || ok || tr {
		t.Errorf("unknown proxy: %s %v %v", a, ok, tr)
	}
	if _, _, tr := try("203.0.113.1:1", "X-Real-IP", "127.0.0.1"); tr {
		t.Error("internet peer spoofing X-Real-IP")
	}
}

func TestNetworkClientKey(t *testing.T) {
	var n *Network
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8:1:2:aaaa::1]:5"
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "[2001:db8:1:2:bbbb::9]:5"
	if n.ClientKey(r) != n.ClientKey(r2) || !strings.HasSuffix(n.ClientKey(r), "/64") {
		t.Fatalf("ipv6 /64: %s %s", n.ClientKey(r), n.ClientKey(r2))
	}
	r.RemoteAddr = "203.0.113.7:1"
	if n.ClientKey(r) != "203.0.113.7" {
		t.Fatal(n.ClientKey(r))
	}
}

func TestThrottleBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	th := NewThrottle()
	th.Now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		th.Fail("ip:a")
		if w := th.Wait("ip:a"); w != 0 {
			t.Fatalf("locked after %d failures: %s", i+1, w)
		}
	}
	th.Fail("ip:a")
	if w := th.Wait("ip:a", "other"); w != 30*time.Second {
		t.Fatalf("5th failure: %s", w)
	}
	now = now.Add(31 * time.Second)
	if w := th.Wait("ip:a"); w != 0 {
		t.Fatalf("lock expired: %s", w)
	}
	th.Fail("ip:a")
	if w := th.Wait("ip:a"); w != time.Minute {
		t.Fatalf("doubling: %s", w)
	}
	for i := 0; i < 20; i++ {
		th.Fail("ip:a")
	}
	if w := th.Wait("ip:a"); w != 15*time.Minute {
		t.Fatalf("cap: %s", w)
	}
	th.Reset("ip:a")
	if th.Wait("ip:a") != 0 {
		t.Fatal("reset")
	}
	// forgotten after an hour of quiet
	for i := 0; i < 4; i++ {
		th.Fail("u")
	}
	now = now.Add(2 * time.Hour)
	th.Fail("u")
	if th.Wait("u") != 0 {
		t.Fatal("old failures should be forgotten")
	}
}

func TestThrottleBounded(t *testing.T) {
	th := NewThrottle()
	for i := 0; i < maxThrottleKeys+500; i++ {
		th.Fail("k" + time.Duration(i).String())
	}
	if len(th.m) > maxThrottleKeys+1 {
		t.Fatalf("%d keys", len(th.m))
	}
}

func TestThrottleBoundedWhenAllLocked(t *testing.T) {
	th := NewThrottle()
	for i := 0; i < maxThrottleKeys+500; i++ {
		k := "k" + time.Duration(i).String()
		for j := 0; j < th.Free; j++ {
			th.Fail(k)
		}
	}
	if len(th.m) > maxThrottleKeys {
		t.Fatalf("%d keys, want <= %d", len(th.m), maxThrottleKeys)
	}
}

func TestAdminPasswordMin6(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	if _, err := s.CreateFirstAdmin(ctx, "Папа", "12345"); !errors.Is(err, ErrAdminPassShort) {
		t.Fatalf("short first admin: %v", err)
	}
	a, err := s.CreateFirstAdmin(ctx, "Папа", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, NewUser{Name: "Внук", Password: "1234"}); err != nil {
		t.Fatalf("non-admin 4 chars: %v", err)
	}
	if _, err := s.CreateUser(ctx, NewUser{Name: "Х", Password: "123"}); !errors.Is(err, ErrPasswordShort) {
		t.Fatalf("3 chars: %v", err)
	}
	short := "abcde"
	if err := s.Update(ctx, a.ID, UserPatch{Password: &short}); !errors.Is(err, ErrAdminPassShort) {
		t.Fatalf("admin update short: %v", err)
	}
	// promoting with a new short password is checked against the new role
	yes := true
	if err := s.Update(ctx, 2, UserPatch{IsAdmin: &yes, Password: &short}); !errors.Is(err, ErrAdminPassShort) {
		t.Fatalf("promote short: %v", err)
	}
	// Cyrillic counts characters, not bytes
	if _, err := s.CreateUser(ctx, NewUser{Name: "Мама", Password: "пароль", IsAdmin: true}); err != nil {
		t.Fatalf("6 cyrillic chars: %v", err)
	}
}

func TestVerifyCapsAt72Bytes(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	p := strings.Repeat("a", 72)
	a, err := s.CreateFirstAdmin(ctx, "Папа", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, a.ID, p); err != nil {
		t.Fatalf("72 bytes: %v", err)
	}
	// bcrypt would ignore the tail; we refuse it instead
	if _, err := s.Verify(ctx, a.ID, p+"tail"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("73+ bytes: %v", err)
	}
}

func TestRevokeUser(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	u, _ := s.CreateUser(ctx, NewUser{Name: "A"})
	t1, _ := ss.Create(ctx, u.ID)
	t2, _ := ss.Create(ctx, u.ID)
	if err := ss.RevokeUser(ctx, u.ID, t2); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(ctx, t1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("t1: %v", err)
	}
	if _, err := ss.Lookup(ctx, t2); err != nil {
		t.Fatalf("kept: %v", err)
	}
}
