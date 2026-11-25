package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// TestNetworkOriginProxyOnly: X-Forwarded-Proto counts only from a
// trusted proxy; a bad Host falls back to localhost.
func TestNetworkOriginProxyOnly(t *testing.T) {
	n := &Network{Proxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}}
	req := func(peer, host, proto string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr, r.Host = peer, host
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return n.Origin(r)
	}
	if got := req("10.0.0.2:4000", "vault.example.org", "https"); got != "https://vault.example.org" {
		t.Fatalf("from proxy: %q", got)
	}
	if got := req("[::ffff:10.0.0.2]:4000", "vault.example.org", "HTTPS"); got != "https://vault.example.org" {
		t.Fatalf("mapped proxy address: %q", got)
	}
	if got := req("192.168.1.5:4000", "vault.example.org", "https"); got != "http://vault.example.org" {
		t.Fatalf("not a proxy: %q", got)
	}
	if got := req("10.0.0.2:4000", "vault.example.org", "gopher"); got != "http://vault.example.org" {
		t.Fatalf("odd proto: %q", got)
	}
	if got := req("192.168.1.5:4000", `evil"<host>`, ""); got != "http://localhost" {
		t.Fatalf("bad host: %q", got)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.TLS = "h:8096", &tls.ConnectionState{}
	if got := n.Origin(r); got != "https://h:8096" {
		t.Fatalf("tls: %q", got)
	}
	var none *Network
	r = httptest.NewRequest("GET", "/", nil)
	r.Host = "h"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := none.Origin(r); got != "http://h" {
		t.Fatalf("nil network: %q", got)
	}
}

func TestHostPolicyAllows(t *testing.T) {
	p := &HostPolicy{Extra: []string{"media.lan.example", "*.family.example"}, Public: func() string { return "https://Media.Example.org:8443/" }}
	for host, want := range map[string]bool{
		// the address itself, whatever the port: no name can be pointed at it
		"192.168.1.5": true, "192.168.1.5:8420": true, "[fd00::5]:8420": true, "[::1]": true, "8.8.8.8": true,
		// names only a home network has
		"antresol": true, "antresol:8420": true, "localhost": true, "nas.local": true, "tv.lan:8420": true, "x.home.arpa": true,
		// the public address's name, VAULT_TRUSTED_HOSTS
		"media.example.org": true, "MEDIA.EXAMPLE.ORG:443": true, "media.example.org.": true,
		"media.lan.example": true, "a.family.example": true, "a.b.family.example": true,
		"": true, // no Host: not a browser
		// a page's own name pointed at the server
		"evil.example.net": false, "evil.example.net:8420": false, "media.example.org.evil.net": false,
		"example.org": false, "family.example": false, "xfamily.example": false, "lan.example.com": false,
	} {
		if got := p.Allows(host); got != want {
			t.Errorf("Allows(%q) = %v, want %v", host, got, want)
		}
	}
	var none *HostPolicy
	if !none.Allows("192.168.1.5") || none.Allows("evil.example.net") {
		t.Error("a nil policy allows addresses and home names only")
	}
}

// IsTrusted asks the name too when Hosts is set, and only then.
func TestIsTrustedChecksTheHost(t *testing.T) {
	req := func(peer, host string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr, r.Host = peer, host
		return r
	}
	plain := &Network{}
	n := &Network{Hosts: &HostPolicy{Public: func() string { return "https://media.example.org" }}}
	for _, c := range []struct {
		peer, host string
		plain, n   bool
	}{
		{"192.168.1.20:5000", "192.168.1.5:8420", true, true},
		{"192.168.1.20:5000", "antresol.local", true, true},
		{"192.168.1.20:5000", "media.example.org", true, true},
		{"192.168.1.20:5000", "evil.example.net", true, false},
		{"8.8.8.8:5000", "192.168.1.5:8420", false, false},
	} {
		if got := plain.IsTrusted(req(c.peer, c.host)); got != c.plain {
			t.Errorf("no policy, %s -> %s: %v", c.peer, c.host, got)
		}
		if got := n.IsTrusted(req(c.peer, c.host)); got != c.n {
			t.Errorf("policy, %s -> %s: %v", c.peer, c.host, got)
		}
	}
	var none *Network
	if !none.IsTrusted(req("192.168.1.20:5000", "evil.example.net")) {
		t.Error("a nil network has no name check")
	}
}

// X-Forwarded-Proto makes the session cookie Secure only from a proxy of ours.
func TestNetworkSecure(t *testing.T) {
	n := &Network{Proxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}}
	req := func(peer, proto string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return r
	}
	for _, c := range []struct {
		peer, proto string
		want        bool
	}{{"10.0.0.2:1", "https", true}, {"10.0.0.2:1", "HTTPS", true}, {"10.0.0.2:1", "http", false}, {"10.0.0.2:1", "", false},
		{"192.168.1.5:1", "https", false}, {"8.8.8.8:1", "https", false}} {
		if got := n.Secure(req(c.peer, c.proto)); got != c.want {
			t.Errorf("%s %q: %v", c.peer, c.proto, got)
		}
	}
	r := req("192.168.1.5:1", "")
	r.TLS = &tls.ConnectionState{}
	if !n.Secure(r) {
		t.Error("a TLS connection is secure")
	}
	var none *Network
	if none.Secure(req("10.0.0.2:1", "https")) {
		t.Error("a nil network has no proxy")
	}
}

// The web login and AppLogin throttle under the same keys.
func TestLoginKeys(t *testing.T) {
	n := &Network{}
	req := func(peer string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = peer
		return r
	}
	keys, home := LoginKeys(n, req("192.168.1.20:5000"), 7)
	if !home || len(keys) != 2 || keys[0] != "ip:192.168.1.20" || keys[1] != "user:7:home" {
		t.Errorf("home: %v %v", keys, home)
	}
	keys, home = LoginKeys(n, req("8.8.8.8:5000"), 7)
	if home || len(keys) != 2 || keys[1] != "user:7:remote" {
		t.Errorf("remote: %v %v", keys, home)
	}
	if keys, _ = LoginKeys(n, req("8.8.8.8:5000"), 0); len(keys) != 1 || keys[0] != "ip:8.8.8.8" {
		t.Errorf("no profile: %v", keys)
	}
}
