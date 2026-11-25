package addonkit

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// The address guard: what a client that fetches an address someone else
// named (a stream or playlist a source lists, a tracker, a redirect) may
// dial. The server's own fetchers and the addons that fetch on a third
// party's word share these rules, so that none has a copy that drifts.
//
// The check belongs on the socket (DialControl as a net.Dialer's Control):
// it sees the IP actually dialled, after DNS and on every redirect hop and
// every re-dial, so a name that resolves into the home network is refused
// as well. Keep a proxy out of the way (an environment proxy is dialled,
// not the target): NewGuardedTransport has none.

// ErrBlocked: the guard refused an address.
var ErrBlocked = errors.New("address not allowed")

// AddrClass is what an address is to the guard.
type AddrClass int

const (
	// AddrPublic is an address on the internet.
	AddrPublic AddrClass = iota
	// AddrLAN: loopback, private, shared (CGNAT) and unique-local
	// addresses. Reachable only for what an admin pointed at on purpose.
	AddrLAN
	// AddrDenied: nothing dials it (link-local and the cloud metadata
	// address among them, unspecified, multicast, reserved,
	// documentation and transition ranges).
	AddrDenied
)

func prefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

var (
	lanNets = prefixes(
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC 1918
		"100.64.0.0/10", // RFC 6598 shared address space (CGNAT, Tailscale)
		"127.0.0.0/8",
		"fc00::/7", // unique local
		"::1/128",
	)
	deniedNets = prefixes(
		// IPv4
		"0.0.0.0/8",       // "this network"
		"169.254.0.0/16",  // link-local, cloud metadata
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"192.88.99.0/24",  // 6to4 relay anycast
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, broadcast
		// IPv6
		"::/96",           // unspecified, IPv4-compatible
		"::ffff:0:0:0/96", // IPv4-translated (SIIT)
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard-only
		"2001::/23",       // IETF protocol assignments (Teredo, ORCHID, ...)
		"2001:db8::/32",   // documentation
		"3fff::/20",       // documentation
		"5f00::/16",       // segment routing SIDs
		"fe80::/10",       // link-local
		"fec0::/10",       // site-local (deprecated)
		"ff00::/8",        // multicast
	)
	globalV6  = netip.MustParsePrefix("2000::/3")
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
)

// ClassifyAddr is the class of ip. An IPv4-mapped IPv6 address counts as
// its IPv4 one. A NAT64 (64:ff9b::/96) or 6to4 (2002::/16) address carries
// an IPv4 address that decides it: public if that one is, denied (never
// LAN) if it is not.
func ClassifyAddr(ip netip.Addr) AddrClass {
	ip = ip.Unmap().WithZone("")
	if !ip.IsValid() {
		return AddrDenied
	}
	if ip.IsLoopback() {
		return AddrLAN
	}
	if v4, ok := embeddedV4(ip); ok {
		if ClassifyAddr(v4) == AddrPublic {
			return AddrPublic
		}
		return AddrDenied
	}
	for _, p := range deniedNets {
		if p.Contains(ip) {
			return AddrDenied
		}
	}
	for _, p := range lanNets {
		if p.Contains(ip) {
			return AddrLAN
		}
	}
	// only IANA's global unicast block is public IPv6
	if !ip.IsGlobalUnicast() || (ip.Is6() && !globalV6.Contains(ip)) {
		return AddrDenied
	}
	return AddrPublic
}

// embeddedV4 is the IPv4 address a NAT64 or 6to4 address carries.
func embeddedV4(ip netip.Addr) (netip.Addr, bool) {
	b := ip.As16()
	switch {
	case !ip.Is6():
	case nat64.Contains(ip):
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case sixToFour.Contains(ip):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	}
	return netip.Addr{}, false
}

// BlockedAddr reports whether s (an IP, no port) is not an address on the
// internet: the home network and everything AddrDenied covers. Text that
// is not an IP is blocked.
func BlockedAddr(s string) bool {
	ip, err := netip.ParseAddr(s)
	return err != nil || ClassifyAddr(ip) != AddrPublic
}

// DialControl is a net.Dialer's Control: it refuses (ErrBlocked) every
// address but the public ones, and the LAN's own when allowPrivate is set
// (what an admin pointed at on purpose). Link-local, unspecified,
// multicast and the other denied ranges are refused either way.
func DialControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return ErrBlocked
		}
		switch ClassifyAddr(ap.Addr()) {
		case AddrPublic:
			return nil
		case AddrLAN:
			if allowPrivate {
				return nil
			}
		}
		return ErrBlocked
	}
}

// NewGuardedTransport is a transport whose dialer is DialControl
// (allowPrivate): it never connects to an address the guard refuses, and
// has no proxy (a proxy would connect on its own behalf). HTTP/2 stays on.
// The client that uses it follows the redirects it chooses to; every hop
// is checked at the socket all the same.
func NewGuardedTransport(allowPrivate bool) *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: DialControl(allowPrivate)}
	return &http.Transport{
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true, // a custom dialer would turn it off
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
