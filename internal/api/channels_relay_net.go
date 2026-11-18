package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// Where the relay may dial. A stream of an external source is a third
// party's word, so the server fetches only what the address the admin typed
// for the source allows:
//
//   - a source on the internet: public addresses only;
//   - a source on the LAN (its playlist address is an IP of the LAN or
//     loopback, localhost, or a name that cannot be on the internet: no
//     dot, .local, .lan, ...): public addresses and the LAN's own.
//
// Never, for any source: link-local (the cloud metadata address among
// them), unspecified, multicast, reserved, documentation and transition
// ranges. The decision never rests on the entry's own host, and the address
// is checked when the socket is opened (every connection, every redirect
// hop, whatever a name answers by then).

type addrClass int

const (
	addrPublic addrClass = iota
	// addrLAN: loopback, private, shared (CGNAT) and unique-local addresses.
	addrLAN
	// addrDenied: no relay dials it.
	addrDenied
)

func prefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

var (
	relayLANNets = prefixes(
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC 1918
		"100.64.0.0/10", // RFC 6598 shared address space
		"127.0.0.0/8",
		"fc00::/7", // unique local
		"::1/128",
	)
	relayDeniedNets = prefixes(
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
		"64:ff9b::/96",    // NAT64: any IPv4, private ones too
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard-only
		"2001::/23",       // IETF protocol assignments (Teredo, ORCHID, ...)
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4: embeds any IPv4
		"3fff::/20",       // documentation
		"5f00::/16",       // segment routing SIDs
		"fe80::/10",       // link-local
		"fec0::/10",       // site-local (deprecated)
		"ff00::/8",        // multicast
	)
)

var relayGlobalV6 = netip.MustParsePrefix("2000::/3")

// classify is the class of ip. An IPv4-mapped IPv6 address counts as its
// IPv4 one.
func classify(ip netip.Addr) addrClass {
	ip = ip.Unmap().WithZone("")
	if !ip.IsValid() {
		return addrDenied
	}
	if ip.IsLoopback() {
		return addrLAN
	}
	for _, p := range relayDeniedNets {
		if p.Contains(ip) {
			return addrDenied
		}
	}
	for _, p := range relayLANNets {
		if p.Contains(ip) {
			return addrLAN
		}
	}
	// only IANA's global unicast block is public IPv6
	if !ip.IsGlobalUnicast() || (ip.Is6() && !relayGlobalV6.Contains(ip)) {
		return addrDenied
	}
	return addrPublic
}

// lanSpelling: the playlist address's host is the LAN's own by how it is
// written, with no lookup: a name on the internet can answer anything
// tomorrow, so it never counts.
func lanSpelling(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return classify(ip) == addrLAN
	}
	if !strings.Contains(host, ".") { // a docker service, a machine name, localhost
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".lan", ".home", ".home.arpa", ".internal", ".intranet", ".localdomain", ".corp", ".private"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// relayControl checks the address a socket is about to connect to.
func relayControl(lan bool) func(ctx context.Context, network, address string, c syscall.RawConn) error {
	return func(_ context.Context, _, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return errRelayDial
		}
		switch classify(ap.Addr()) {
		case addrPublic:
			return nil
		case addrLAN:
			if lan {
				return nil
			}
		}
		return errRelayDial
	}
}

// relayClient: lan false dials public addresses only; true public ones and
// the LAN's. Each has its own connection pool, so a connection opened for
// one is never reused by the other.
func relayClient(lan bool) *http.Client {
	d := &net.Dialer{Timeout: relayConnect, KeepAlive: 30 * time.Second, ControlContext: relayControl(lan)}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   relayConnect,
		ResponseHeaderTimeout: relayHeaders,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("relay: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errRelayRef
		}
		return nil
	}}
}
