// Package discovery lets the apps find the server on the local network
// without an address typed in: an mDNS / DNS-SD advertisement
// (_antresol._tcp, what Bonjour and Android's NsdManager browse) and a UDP
// responder that answers a broadcast probe with the server's JSON
// (Jellyfin-style; it also works where multicast does not, e.g. a Docker
// bridge network with the UDP port published).
//
// Both answer only clients on private, loopback or link-local addresses:
// discovery is for the home network, and the UDP answer is larger than the
// probe (no reflection to the internet).
package discovery

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServiceType is the DNS-SD service type the server advertises.
const ServiceType = "_antresol._tcp"

// DefaultUDPPort is the UDP port of the probe responder.
const DefaultUDPPort = 8420

// Protocol is the version of the TXT keys and the UDP answer.
const Protocol = 1

// Info is what the server tells about itself.
type Info struct {
	ID      string // the server id of GET /api/health (serverId)
	Name    string // shown in the apps' server list
	Version string
	Port    int    // the HTTP port the apps connect to
	Scheme  string // http or https
	// Extra are more base URLs to offer (VAULT_PUBLIC_URL), after the
	// LAN addresses.
	Extra []string
}

// Config turns the parts on: MDNS, and UDPPort (0: no UDP responder).
type Config struct {
	Info    Info
	MDNS    bool
	UDPPort int
	Log     *slog.Logger
	// Addrs lists the server's own addresses (nil: the interfaces'); tests.
	Addrs func() []netip.Prefix
	// Status (optional) gets the state of the friendly name antresol.local.
	Status *Status
	// ShareDir (optional) is the folder shared with the mDNS announcer
	// (share.go): a server on a bridge network leaves mDNS on the home
	// network to it.
	ShareDir string

	network string     // where the process runs: netHost, netBridge, netVM (Run)
	claim   *hostClaim // the friendly name, while mDNS runs
	host    *hostView  // what the announcer says about the host (share.go; nil: nothing)
}

// The network the process runs on, as Run judges it.
const (
	netHost   = "host"   // the home network: claim antresol.local
	netBridge = "bridge" // a container network multicast does not leave
	netVM     = "vm"     // a VM's NAT'd network (Docker Desktop, WSL 2, Podman machine)
)

// whereRun judges the network once: it does not change while the process runs.
func (c *Config) whereRun() string {
	switch {
	case bridgeNetwork():
		return netBridge
	case vmNetwork(c.prefixes()):
		return netVM
	}
	return netHost
}

func (c *Config) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

// Run serves until ctx ends. A part that cannot open its socket logs it
// and stays off: discovery is a convenience, never a reason not to start.
func Run(ctx context.Context, c Config) {
	if c.Info.Scheme == "" {
		c.Info.Scheme = "http"
	}
	var wg sync.WaitGroup
	if c.MDNS && c.network == "" {
		c.network = c.whereRun()
	}
	if c.ShareDir != "" && c.host == nil {
		c.host = &hostView{}
	}
	if c.UDPPort > 0 {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: c.UDPPort})
		if err != nil {
			c.log().Warn("discovery: UDP responder off", "port", c.UDPPort, "err", err)
		} else {
			c.log().Info("discovery: UDP responder", "port", c.UDPPort)
			wg.Add(1)
			go func() { defer wg.Done(); serveUDP(ctx, conn, &c) }()
		}
	}
	if !c.MDNS {
		c.Status.set(StateOff, "")
	} else {
		c.Status.set(StateProbing, "")
		wg.Add(1)
		go func() { defer wg.Done(); runMDNS(ctx, &c) }()
		if c.ShareDir != "" {
			wg.Add(1)
			go func() { defer wg.Done(); c.share(ctx) }()
		}
	}
	wg.Wait()
}

// lanClient: an address discovery answers (home network only).
func lanClient(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()
}

// virtualIf are interface name prefixes of container and VPN plumbing,
// whose addresses no app on the LAN can reach.
var virtualIf = []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "vxlan", "tun", "utun", "tailscale", "zt", "wg", "lxcbr", "vmnet", "podman"}

// ifaceAddrs are the IPv4 prefixes of the up, non-loopback, non-virtual
// interfaces.
func ifaceAddrs() []netip.Prefix {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtual(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(n.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			if !ip.Is4() || ip.IsLoopback() {
				continue
			}
			ones, _ := n.Mask.Size()
			out = append(out, netip.PrefixFrom(ip, ones))
		}
	}
	return out
}

func virtual(name string) bool {
	for _, p := range virtualIf {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// addrsFor are the server's IPv4 addresses for a client at peer: those on
// the peer's subnet when there are any, else all of them (peer invalid:
// all of them).
func (c *Config) addrsFor(peer netip.Addr) []netip.Addr {
	return pickAddrs(c.prefixes(), peer)
}

// prefixes are the server's own interface prefixes.
func (c *Config) prefixes() []netip.Prefix {
	if c.Addrs != nil {
		return c.Addrs()
	}
	return ifaceAddrs()
}

// pickAddrs are the addresses of all for a client at peer (addrsFor).
func pickAddrs(all []netip.Prefix, peer netip.Addr) []netip.Addr {
	var same, rest []netip.Addr
	for _, p := range all {
		if peer.IsValid() && p.Contains(peer.Unmap()) {
			same = append(same, p.Addr())
		} else {
			rest = append(rest, p.Addr())
		}
	}
	if len(same) > 0 {
		return same
	}
	return rest
}

// lan are the addresses and the HTTP port a client on the home network
// reaches the server at: the host's, as the announcer tells them, for a
// server on a bridge network (its own there are the container's, which no
// app can reach), else its own.
func (c *Config) lan() ([]netip.Prefix, int) {
	if addrs, port := c.host.get(); len(addrs) > 0 && port > 0 {
		return addrs, port
	}
	return c.prefixes(), c.Info.Port
}

// urls are the base URLs of the server for a client at peer, and the port.
func (c *Config) urls(peer netip.Addr) ([]string, int) {
	all, port := c.lan()
	var out []string
	for _, a := range pickAddrs(all, peer) {
		out = append(out, c.Info.Scheme+"://"+net.JoinHostPort(a.String(), strconv.Itoa(port)))
	}
	return append(out, c.Info.Extra...), port
}

// limiter allows n events per second (a token bucket).
type limiter struct {
	mu     sync.Mutex
	n      float64
	tokens float64
	last   time.Time
}

func newLimiter(n float64) *limiter { return &limiter{n: n, tokens: n} }

func (l *limiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() {
		l.tokens = min(l.n, l.tokens+now.Sub(l.last).Seconds()*l.n)
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
