package discovery

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// A minimal mDNS responder (RFC 6762) for one DNS-SD service (RFC 6763):
//
//	_antresol._tcp.local.                 PTR <instance>._antresol._tcp.local.
//	<instance>._antresol._tcp.local.      SRV 0 0 <port> antresol-<id8>.local.
//	                                      TXT txtvers=1 id= name= version= scheme= port= path=/
//	antresol-<id8>.local.                 A   <the server's LAN addresses>
//	_services._dns-sd._udp.local.         PTR _antresol._tcp.local.
//
// The instance is "<name> <id6>" and the host "antresol-<id8>": both carry
// the server id, so two servers on one network never claim the same name
// and the responder skips probing. It announces itself at start and says
// goodbye (TTL 0) at the end. IPv4 only.

var mdnsGroup = netip.MustParseAddrPort("224.0.0.251:5353")

var errNoInterface = errors.New("discovery: no interface to join the mDNS group on")

const (
	serviceName  = ServiceType + ".local."
	servicesEnum = "_services._dns-sd._udp.local."
	ttlHost      = 120  // SRV and A (RFC 6762 §10)
	ttlOther     = 4500 // PTR and TXT
	ttlLegacy    = 10   // answers to legacy unicast queries
)

type mdnsNames struct {
	instance string // the full instance name, as written
	host     string
}

func (c *Config) names() mdnsNames {
	id := c.Info.ID
	if id == "" {
		id = "000000000"
	}
	label := strings.NewReplacer(".", " ", "\\", " ").Replace(strings.TrimSpace(c.Info.Name))
	if label == "" {
		label = "Antresol"
	}
	suffix := " " + id[:min(6, len(id))]
	for len(label)+len(suffix) > 63 { // a DNS label: 63 bytes, cut on a rune
		r := []rune(label)
		label = strings.TrimSpace(string(r[:len(r)-1]))
	}
	label += suffix
	return mdnsNames{instance: label + "." + serviceName, host: "antresol-" + id[:min(8, len(id))] + ".local."}
}

func (c *Config) txt() []string {
	return []string{
		"txtvers=" + strconv.Itoa(Protocol),
		"id=" + c.Info.ID,
		"name=" + c.Info.Name,
		"version=" + c.Info.Version,
		"scheme=" + c.Info.Scheme,
		"port=" + strconv.Itoa(c.Info.Port),
		"path=/",
	}
}

func capTTL(rs []record, ttl uint32) []record {
	for i := range rs {
		rs[i].ttl = min(rs[i].ttl, ttl)
	}
	return rs
}

// respond builds the answer to a query from peer; nil when nothing in it
// is ours. legacy: the asker is not an mDNS responder (its source port is
// not 5353) and gets a plain unicast DNS answer.
func (c *Config) respond(msg []byte, peer netip.AddrPort) (out []byte, unicast bool) {
	q, err := parseQuery(msg)
	if err != nil || q.response || q.opcode != 0 {
		return nil, false
	}
	n := c.names()
	inst, host := strings.ToLower(n.instance), strings.ToLower(n.host)
	addrs := c.addrsFor(peer.Addr())
	hostRecs := func() []record {
		var rs []record
		for _, a := range addrs {
			rs = append(rs, aRecord(n.host, a, ttlHost))
		}
		return rs
	}
	srv := func() record { return srvRecord(n.instance, n.host, uint16(c.Info.Port), ttlHost) }
	txt := func() record { return txtRecord(n.instance, c.txt(), ttlOther) }
	var ans, extra []record
	var asked []question
	allQU := true
	have := map[string]bool{}
	add := func(to *[]record, r record) {
		k := r.name + "/" + strconv.Itoa(int(r.rtype)) + "/" + string(r.data)
		if !have[k] {
			have[k] = true
			*to = append(*to, r)
		}
	}
	for _, qq := range q.questions {
		all := qq.qtype == typeANY
		before := len(ans)
		switch qq.name {
		case serviceName:
			if all || qq.qtype == typePTR {
				add(&ans, ptrRecord(serviceName, n.instance, ttlOther))
				add(&extra, srv())
				add(&extra, txt())
				for _, r := range hostRecs() {
					add(&extra, r)
				}
			}
		case servicesEnum:
			if all || qq.qtype == typePTR {
				add(&ans, ptrRecord(servicesEnum, serviceName, ttlOther))
			}
		case inst:
			if all || qq.qtype == typeSRV {
				add(&ans, srv())
				for _, r := range hostRecs() {
					add(&extra, r)
				}
			}
			if all || qq.qtype == typeTXT {
				add(&ans, txt())
			}
		case host:
			if all || qq.qtype == typeA {
				for _, r := range hostRecs() {
					add(&ans, r)
				}
			}
		}
		if len(ans) > before {
			asked = append(asked, qq)
			allQU = allQU && qq.qu
		}
	}
	if len(ans) == 0 {
		return nil, false
	}
	// what is already an answer is not repeated as additional
	var ex []record
	for _, r := range extra {
		dup := false
		for _, a := range ans {
			if a.name == r.name && a.rtype == r.rtype && string(a.data) == string(r.data) {
				dup = true
			}
		}
		if !dup {
			ex = append(ex, r)
		}
	}
	if peer.Port() != mdnsGroup.Port() {
		return buildResponse(true, q.id, asked, capTTL(ans, ttlLegacy), capTTL(ex, ttlLegacy)), true
	}
	return buildResponse(false, 0, nil, ans, ex), allQU
}

// announcement is every record of the service as an unsolicited answer
// (ttl 0: the goodbye).
func (c *Config) announcement(goodbye bool) []byte {
	n := c.names()
	rs := []record{
		ptrRecord(serviceName, n.instance, ttlOther),
		ptrRecord(servicesEnum, serviceName, ttlOther),
		srvRecord(n.instance, n.host, uint16(c.Info.Port), ttlHost),
		txtRecord(n.instance, c.txt(), ttlOther),
	}
	for _, a := range c.addrsFor(netip.Addr{}) {
		rs = append(rs, aRecord(n.host, a, ttlHost))
	}
	if goodbye {
		rs = capTTL(rs, 0)
	}
	return buildResponse(false, 0, nil, rs, nil)
}

func (c *Config) lanAddrs() []netip.Addr { return c.addrsFor(netip.Addr{}) }

// runMDNS advertises the service until ctx ends.
func runMDNS(ctx context.Context, c *Config) {
	ifaces := c.lanAddrs()
	conn, err := listenMDNS(ifaces)
	if err != nil {
		c.log().Warn("discovery: mDNS off", "err", err)
		return
	}
	c.log().Info("discovery: mDNS", "service", ServiceType, "instance", strings.TrimSuffix(c.names().instance, "."+serviceName))
	multicast := func(b []byte) {
		for _, a := range ifaces {
			if err := sendVia(conn, a, b); err != nil {
				c.log().Debug("discovery: mDNS send", "via", a, "err", err)
			}
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveMDNS(ctx, conn, c, multicast)
	}()
	// announce twice, a second apart (RFC 6762 §8.3)
	for i := 0; i < 2; i++ {
		multicast(c.announcement(false))
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	<-ctx.Done()
	multicast(c.announcement(true))
	conn.Close()
	<-done
}

// serveMDNS answers the queries read from conn until it is closed.
func serveMDNS(ctx context.Context, conn *net.UDPConn, c *Config, multicast func([]byte)) {
	lim := newLimiter(50)
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				c.log().Warn("discovery: mDNS read", "err", err)
			}
			return
		}
		if !lanClient(from.Addr()) {
			continue
		}
		out, unicast := c.respond(buf[:n], from)
		if out == nil || !lim.allow(time.Now()) {
			continue
		}
		if unicast {
			conn.WriteToUDPAddrPort(out, from)
		} else {
			multicast(out)
		}
	}
}
