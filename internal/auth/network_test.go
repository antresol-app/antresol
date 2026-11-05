package auth

import (
	"crypto/tls"
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
