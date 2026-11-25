// Package netguard decides which addresses the server may dial on a third
// party's word: a stream of an external IPTV source, a guide address a
// playlist names, a playlist the admin typed. The server fetches only what
// the address the admin typed for the source allows:
//
//   - a source on the internet: public addresses only;
//   - a source on the LAN (its address is an IP of the LAN or loopback,
//     localhost, or a name that cannot be on the internet: no dot, .local,
//     .lan, ...): public addresses and the LAN's own (LANSpelling).
//
// Never, for any source: link-local (the cloud metadata address among
// them), unspecified, multicast, reserved, documentation and transition
// ranges (a NAT64 or 6to4 address is judged by the IPv4 address it carries).
// The rules are addonkit's: the addons that fetch on a third party's word
// use the same ones. The decision never rests on the entry's own host, and
// the address is checked when the socket is opened (every connection, every
// redirect hop, whatever a name answers by then): use Control as a
// net.Dialer's ControlContext, and no proxy between (a proxy is dialled,
// not the target).
package netguard

import (
	"context"
	"net/netip"
	"strings"
	"syscall"

	"github.com/antresol-app/antresol/addonkit"
)

// ErrDial: Control refused the address.
var ErrDial = addonkit.ErrBlocked

// Class is what an address is to the guard. The rules are addonkit's, the
// ones the addons' own fetchers use (ClassifyAddr).
type Class = addonkit.AddrClass

const (
	Public = addonkit.AddrPublic
	// LAN: loopback, private, shared (CGNAT) and unique-local addresses.
	LAN = addonkit.AddrLAN
	// Denied: nothing dials it.
	Denied = addonkit.AddrDenied
)

// Classify is the class of ip. An IPv4-mapped IPv6 address counts as its
// IPv4 one; a NAT64 or 6to4 address is judged by the IPv4 address it
// carries.
func Classify(ip netip.Addr) Class { return addonkit.ClassifyAddr(ip) }

// LANSpelling: the address's host is the LAN's own by how it is written,
// with no lookup: a name on the internet can answer anything tomorrow, so
// it never counts.
func LANSpelling(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return Classify(ip) == LAN
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

// Control checks the address a socket is about to connect to (a
// net.Dialer's ControlContext): public ones, and the LAN's when lan is
// set. It answers ErrDial for the rest.
func Control(lan bool) func(ctx context.Context, network, address string, c syscall.RawConn) error {
	check := addonkit.DialControl(lan)
	return func(_ context.Context, network, address string, c syscall.RawConn) error {
		return check(network, address, c)
	}
}
