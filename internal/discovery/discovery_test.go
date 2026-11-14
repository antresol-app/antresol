package discovery

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func testConfig() *Config {
	return &Config{
		Info: Info{ID: "0123456789abcdef0123456789abcdef", Name: "Дом", Version: "1.2.3", Port: 8420, Scheme: "http",
			Extra: []string{"https://media.example.org"}},
		Addrs: func() []netip.Prefix {
			return []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24"), netip.MustParsePrefix("10.8.0.1/24")}
		},
	}
}

func TestUDPProbeLoopback(t *testing.T) {
	c := testConfig()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); serveUDP(ctx, conn, c) }()
	defer func() { cancel(); <-done }()

	cl, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	buf := make([]byte, 2048)
	// not a probe: no answer
	cl.Write([]byte("who is there?"))
	cl.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := cl.Read(buf); err == nil {
		t.Fatalf("answered a stranger: %q", buf[:n])
	}
	if _, err := cl.Write([]byte(Probe + "\n")); err != nil {
		t.Fatal(err)
	}
	cl.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := cl.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var a Answer
	if err := json.Unmarshal(buf[:n], &a); err != nil {
		t.Fatalf("%v: %q", err, buf[:n])
	}
	// a loopback peer is on neither subnet: every address, then the extra URL
	want := []string{"http://192.168.1.10:8420", "http://10.8.0.1:8420", "https://media.example.org"}
	if a.Service != "antresol" || a.Protocol != 1 || a.ID != c.Info.ID || a.Name != "Дом" || a.Version != "1.2.3" ||
		a.Port != 8420 || a.Scheme != "http" || strings.Join(a.URLs, " ") != strings.Join(want, " ") {
		t.Fatalf("%+v", a)
	}
}

func TestURLsPreferPeerSubnet(t *testing.T) {
	c := testConfig()
	got := c.urls(netip.MustParseAddr("192.168.1.77"))
	if strings.Join(got, " ") != "http://192.168.1.10:8420 https://media.example.org" {
		t.Fatalf("%v", got)
	}
}

func TestLANClientOnly(t *testing.T) {
	for s, want := range map[string]bool{"192.168.1.5": true, "10.0.0.1": true, "127.0.0.1": true, "169.254.1.1": true,
		"fd00::1": true, "8.8.8.8": false, "2001:db8::1": false} {
		if lanClient(netip.MustParseAddr(s)) != want {
			t.Errorf("%s: want %v", s, want)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2)
	now := time.Unix(1000, 0)
	if !l.allow(now) || !l.allow(now) || l.allow(now) {
		t.Fatal("burst of 2")
	}
	if !l.allow(now.Add(600 * time.Millisecond)) {
		t.Fatal("refill")
	}
}

// --- mDNS ---

func mdnsQuery(id uint16, qs ...question) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b, id)
	binary.BigEndian.PutUint16(b[4:], uint16(len(qs)))
	for _, q := range qs {
		b = appendName(b, q.name)
		b = binary.BigEndian.AppendUint16(b, q.qtype)
		cls := uint16(classIN)
		if q.qu {
			cls |= qu
		}
		b = binary.BigEndian.AppendUint16(b, cls)
	}
	return b
}

type rr struct {
	name  string
	rtype uint16
	class uint16
	ttl   uint32
	data  []byte
	off   int // of data
}

type parsed struct {
	id             uint16
	flags          uint16
	qd             int
	answers, extra []rr
}

func parseResponse(t *testing.T, msg []byte) parsed {
	t.Helper()
	p := parsed{id: binary.BigEndian.Uint16(msg), flags: binary.BigEndian.Uint16(msg[2:]), qd: int(binary.BigEndian.Uint16(msg[4:]))}
	an, ar := int(binary.BigEndian.Uint16(msg[6:])), int(binary.BigEndian.Uint16(msg[10:]))
	off := 12
	for range p.qd {
		_, next, err := readName(msg, off)
		if err != nil {
			t.Fatal(err)
		}
		off = next + 4
	}
	read := func(n int) []rr {
		var out []rr
		for range n {
			name, next, err := readName(msg, off)
			if err != nil {
				t.Fatal(err)
			}
			r := rr{name: name, rtype: binary.BigEndian.Uint16(msg[next:]), class: binary.BigEndian.Uint16(msg[next+2:]),
				ttl: binary.BigEndian.Uint32(msg[next+4:])}
			l := int(binary.BigEndian.Uint16(msg[next+8:]))
			r.off = next + 10
			r.data = msg[r.off : r.off+l]
			off = r.off + l
			out = append(out, r)
		}
		return out
	}
	p.answers, p.extra = read(an), read(ar)
	return p
}

func find(rs []rr, rtype uint16) *rr {
	for i := range rs {
		if rs[i].rtype == rtype {
			return &rs[i]
		}
	}
	return nil
}

func TestMDNSBrowseAnswer(t *testing.T) {
	c := testConfig()
	msg := mdnsQuery(0, question{name: "_antresol._tcp.local.", qtype: typePTR})
	out, unicast := c.respond(msg, netip.MustParseAddrPort("192.168.1.50:5353"))
	if out == nil || unicast {
		t.Fatalf("out %v unicast %v", out, unicast)
	}
	p := parseResponse(t, out)
	if p.id != 0 || p.flags != 0x8400 || p.qd != 0 {
		t.Fatalf("header %+v", p)
	}
	ptr := find(p.answers, typePTR)
	if ptr == nil {
		t.Fatal("no PTR")
	}
	inst, _, _ := readName(out, ptr.off)
	if inst != strings.ToLower("Дом 012345._antresol._tcp.local.") {
		t.Fatalf("instance %q", inst)
	}
	srv := find(p.extra, typeSRV)
	if srv == nil || binary.BigEndian.Uint16(srv.data[4:]) != 8420 || srv.class != classIN|cacheFlush || srv.ttl != ttlHost {
		t.Fatalf("srv %+v", srv)
	}
	host, _, _ := readName(out, srv.off+6)
	if host != "antresol-01234567.local." {
		t.Fatalf("host %q", host)
	}
	txt := find(p.extra, typeTXT)
	if txt == nil || !strings.Contains(string(txt.data), "id="+c.Info.ID) || !strings.Contains(string(txt.data), "name=Дом") ||
		!strings.Contains(string(txt.data), "\x09txtvers=1") || !strings.Contains(string(txt.data), "port=8420") {
		t.Fatalf("txt %q", txt.data)
	}
	// only the address on the asker's subnet
	a := find(p.extra, typeA)
	if a == nil || netip.AddrFrom4([4]byte(a.data)).String() != "192.168.1.10" {
		t.Fatalf("a %+v", a)
	}
	if len(p.extra) != 3 {
		t.Fatalf("%d extra records", len(p.extra))
	}
}

func TestMDNSLegacyUnicast(t *testing.T) {
	c := testConfig()
	msg := mdnsQuery(0x1234, question{name: "_ANTRESOL._tcp.local.", qtype: typePTR})
	out, unicast := c.respond(msg, netip.MustParseAddrPort("192.168.1.50:40000"))
	if !unicast {
		t.Fatal("legacy query answered by multicast")
	}
	p := parseResponse(t, out)
	if p.id != 0x1234 || p.qd != 1 {
		t.Fatalf("%+v", p)
	}
	for _, r := range append(p.answers, p.extra...) {
		if r.ttl > ttlLegacy || r.class != classIN {
			t.Fatalf("legacy record %+v", r)
		}
	}
}

func TestMDNSOtherQuestions(t *testing.T) {
	c := testConfig()
	peer := netip.MustParseAddrPort("192.168.1.50:5353")
	if out, _ := c.respond(mdnsQuery(0, question{name: "_googlecast._tcp.local.", qtype: typePTR}), peer); out != nil {
		t.Fatal("answered another service")
	}
	out, _ := c.respond(mdnsQuery(0, question{name: "_services._dns-sd._udp.local.", qtype: typePTR}), peer)
	if p := parseResponse(t, out); len(p.answers) != 1 {
		t.Fatalf("%+v", p)
	} else if n, _, _ := readName(out, p.answers[0].off); n != "_antresol._tcp.local." {
		t.Fatal(n)
	}
	out, unicast := c.respond(mdnsQuery(0, question{name: "antresol-01234567.local.", qtype: typeA, qu: true}), peer)
	if p := parseResponse(t, out); !unicast || len(p.answers) != 1 {
		t.Fatalf("A %+v %v", p, unicast)
	}
	// responses (our own announcements, looped back) are not queries
	ann := c.announcement(false)
	if out, _ := c.respond(ann, peer); out != nil {
		t.Fatal("answered a response")
	}
	if p := parseResponse(t, c.announcement(true)); p.answers[0].ttl != 0 {
		t.Fatal("goodbye with a ttl")
	}
}

func TestReadNameCompressed(t *testing.T) {
	// "local." at 12, then "_antresol._tcp" + pointer to it
	msg := make([]byte, 12)
	msg = appendName(msg, "local.")
	start := len(msg)
	msg = append(msg, 9)
	msg = append(msg, "_antresol"...)
	msg = append(msg, 4)
	msg = append(msg, "_tcp"...)
	msg = append(msg, 0xc0, 12)
	name, next, err := readName(msg, start)
	if err != nil || name != "_antresol._tcp.local." || next != len(msg) {
		t.Fatalf("%q %d %v", name, next, err)
	}
	// a pointer loop is refused
	loop := append(make([]byte, 12), 0xc0, 12)
	if _, _, err := readName(loop, 12); err == nil {
		t.Fatal("loop")
	}
}

func TestServeMDNSLoopback(t *testing.T) {
	c := testConfig()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveMDNS(ctx, conn, c, func([]byte) { t.Error("multicast for a legacy query") })
	}()
	defer func() { cancel(); conn.Close(); <-done }()
	cl, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	cl.Write(mdnsQuery(7, question{name: "_antresol._tcp.local.", qtype: typePTR}))
	cl.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, err := cl.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if p := parseResponse(t, buf[:n]); p.id != 7 || find(p.answers, typePTR) == nil {
		t.Fatalf("%+v", p)
	}
}

func TestInstanceNameFitsALabel(t *testing.T) {
	c := testConfig()
	c.Info.Name = strings.Repeat("Медиатека.", 10)
	n := c.names()
	label := strings.TrimSuffix(n.instance, "."+serviceName)
	if len(label) > 63 || strings.Contains(label, ".") || !strings.HasSuffix(label, " 012345") {
		t.Fatalf("%q (%d)", label, len(label))
	}
}
