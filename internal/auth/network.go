package auth

import (
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/internal/netguard"
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
// Hosts, when set, also asks the name the request used for the server: a
// web page can point its own name at the server's address (DNS rebinding)
// and then looks like a home visitor to it.
// A nil *Network uses the defaults with no proxies and no name check.
type Network struct {
	Trusted []netip.Prefix
	Proxies []netip.Prefix
	Hosts   *HostPolicy

	warnOnce     sync.Once
	warnHostOnce sync.Once
}

// HostPolicy says which names may reach the server and still count as the
// home network: an IP address, localhost, a name no one on the internet can
// have (no dot, .local, .lan, .home.arpa, ...: netguard.LANSpelling), the
// host of the public address and the names in Extra
// (VAULT_TRUSTED_HOSTS). Any other name, such as a page's own domain that
// answers with the server's address, is not home whoever's address it is.
type HostPolicy struct {
	// Extra: names, lower case, or "*.example.org" for the names under one.
	Extra []string
	// Public is the server's public address ("https://media.example.org";
	// nil or "": none). Read on each request: an admin may change it.
	Public func() string
}

// Allows reports whether a Host header names the server as a home visitor
// would. An empty one does (a browser always sends it).
func (p *HostPolicy) Allows(hostport string) bool {
	h := strings.ToLower(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	if h == "" {
		return true
	}
	if _, err := netip.ParseAddr(h); err == nil || netguard.LANSpelling(h) {
		return true
	}
	if p == nil {
		return false
	}
	for _, e := range p.Extra {
		if e == h || strings.HasPrefix(e, "*.") && strings.HasSuffix(h, e[1:]) {
			return true
		}
	}
	if p.Public != nil {
		if u, err := url.Parse(p.Public()); err == nil && u.Hostname() != "" {
			return strings.EqualFold(u.Hostname(), h)
		}
	}
	return false
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

// IsTrusted reports whether the request comes from the home network: from
// a home address, by a name that is the server's own (Hosts).
func (n *Network) IsTrusted(r *http.Request) bool {
	a, ok := n.ClientIP(r)
	if !ok || !n.trustedAddr(a) {
		return false
	}
	if n != nil && n.Hosts != nil && !n.Hosts.Allows(r.Host) {
		n.warnHostOnce.Do(func() {
			log.Printf("auth: a request from a home address asked for the server as %q: treating it as remote (a web page can point its own name at the server). If that is the server's own name, set VAULT_PUBLIC_URL to it, or add it to VAULT_TRUSTED_HOSTS", r.Host)
		})
		return false
	}
	return true
}

// Why a request is not from the home network (Diagnosis.Reason).
const (
	// ReasonProxy: it carries X-Forwarded-For or X-Real-IP, but its peer is
	// not in Proxies: a proxy the server was not told about.
	ReasonProxy = "proxy"
	// ReasonBare: its peer is a listed proxy that forwards no client
	// address the server can read.
	ReasonBare = "bare"
	// ReasonHost: a home address that asked for the server by a name that
	// is not its own (HostPolicy).
	ReasonHost = "host"
	// ReasonAway: an address that is not on the home network.
	ReasonAway = "away"
)

// Diagnosis is IsTrusted with its reason, for «Состояние».
type Diagnosis struct {
	Home bool
	// Reason: one of the Reason constants when not Home.
	Reason string
	// Peer is the TCP peer's address (a proxy's, when there is one); Host
	// the name the request used, without its port.
	Peer, Host string
}

// Diagnose says what IsTrusted says and why: Home equals IsTrusted(r).
func (n *Network) Diagnose(r *http.Request) Diagnosis {
	d := Diagnosis{Host: strings.ToLower(strings.TrimSuffix(strings.Trim(hostOnly(r.Host), "[]"), "."))}
	peer, err := parseAddr(r.RemoteAddr)
	if err == nil {
		d.Peer = peer.String()
	}
	a, ok := n.ClientIP(r)
	switch {
	case err != nil:
		d.Reason = ReasonAway
	case !ok && n.isProxy(peer):
		d.Reason = ReasonBare
	case !ok:
		d.Reason = ReasonProxy
	case !n.trustedAddr(a):
		d.Reason = ReasonAway
	case n != nil && n.Hosts != nil && !n.Hosts.Allows(r.Host):
		d.Reason = ReasonHost
	default:
		d.Home = true
	}
	return d
}

// hostOnly is a Host header without its port.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
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

// Secure reports whether the browser reached us over HTTPS: the connection
// is TLS, or a trusted proxy (Proxies) says so in X-Forwarded-Proto. Anyone
// else's X-Forwarded-Proto is ignored.
func (n *Network) Secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") && n.peerIsProxy(r)
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
