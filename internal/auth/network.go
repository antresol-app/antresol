package auth

import (
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// Network decides whether a request comes from the home network. Profiles
// without a password and the first-run setup only work from there.
//
// Trusted by default: loopback, RFC1918, IPv6 ULA (fc00::/7) and IPv6
// link-local; Trusted adds more (VAULT_TRUSTED_NETS). X-Forwarded-For and
// X-Real-IP are believed only when the TCP peer is in Proxies
// (VAULT_TRUSTED_PROXIES, the Nginx Proxy Manager host). A request that
// carries those headers from any other peer is treated as remote: it came
// through a proxy we were not told about, so its real origin is unknown.
// A trusted proxy that sends no forwarding headers is also remote (fail closed).
// A nil *Network uses the defaults with no proxies.
type Network struct {
	Trusted []netip.Prefix
	Proxies []netip.Prefix

	warnOnce sync.Once
}

// ClientIP returns the client address (the proxy's view of it when the peer
// is a trusted proxy). ok is false when the address cannot be worked out or
// came through an unknown proxy.
func (n *Network) ClientIP(r *http.Request) (addr netip.Addr, ok bool) {
	peer, err := parseAddr(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	xff, xri := r.Header.Values("X-Forwarded-For"), strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if !n.isProxy(peer) {
		if len(xff) > 0 || xri != "" || r.Header.Get("Forwarded") != "" {
			if n != nil {
				n.warnOnce.Do(func() {
					log.Printf("auth: request from %s carries X-Forwarded-For/X-Real-IP but VAULT_TRUSTED_PROXIES does not list it: treating such requests as remote", peer)
				})
			}
			return peer, false
		}
		return peer, true
	}
	// Walk X-Forwarded-For from the right: the last hop not run by us is the client.
	var hops []string
	for _, v := range xff {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		h := strings.TrimSpace(hops[i])
		if h == "" {
			continue
		}
		a, err := parseAddr(h)
		if err != nil {
			return netip.Addr{}, false
		}
		if !n.isProxy(a) {
			return a, true
		}
		addr = a
	}
	if addr.IsValid() {
		return addr, true // every hop is one of our proxies
	}
	if xri != "" {
		a, err := parseAddr(xri)
		if err != nil {
			return netip.Addr{}, false
		}
		return a, true
	}
	// A trusted proxy that forwards nothing: the client is unknown, so remote.
	return peer, false
}

// IsTrusted reports whether the request comes from the home network.
func (n *Network) IsTrusted(r *http.Request) bool {
	a, ok := n.ClientIP(r)
	return ok && n.trustedAddr(a)
}

// ClientKey identifies the client for throttling: the address, or its /64
// for IPv6 (one home gets a whole /64).
func (n *Network) ClientKey(r *http.Request) string {
	a, ok := n.ClientIP(r)
	if !ok {
		if p, err := parseAddr(r.RemoteAddr); err == nil {
			a = p
		} else {
			return "unknown"
		}
	}
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}

// Origin is the scheme and host the client used to reach us, for absolute
// URLs in responses (OPDS, Jellyfin): https when the connection is TLS, or
// when a trusted proxy (Proxies) says so in X-Forwarded-Proto; anyone else's
// X-Forwarded-Proto is ignored. A Host with odd characters becomes
// localhost.
func (n *Network) Origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if p := strings.ToLower(r.Header.Get("X-Forwarded-Proto")); (p == "https" || p == "http") && n.peerIsProxy(r) {
		scheme = p
	}
	host := r.Host
	if host == "" || strings.ContainsAny(host, "/\\ \"'<>") {
		host = "localhost"
	}
	return scheme + "://" + host
}

// peerIsProxy: the TCP peer itself is one of Proxies.
func (n *Network) peerIsProxy(r *http.Request) bool {
	a, err := parseAddr(r.RemoteAddr)
	return err == nil && n.isProxy(a)
}

func (n *Network) trustedAddr(a netip.Addr) bool {
	if a.IsLoopback() || a.IsPrivate() || (a.Is6() && a.IsLinkLocalUnicast()) {
		return true
	}
	if n == nil {
		return false
	}
	for _, p := range n.Trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (n *Network) isProxy(a netip.Addr) bool {
	if n == nil {
		return false
	}
	for _, p := range n.Proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseAddr accepts "ip", "ip:port", "[ip]:port" and zones; IPv4-mapped
// IPv6 is unmapped so 10.0.0.1 and ::ffff:10.0.0.1 are the same.
func parseAddr(s string) (netip.Addr, error) {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}, err
	}
	return a.Unmap().WithZone(""), nil
}
