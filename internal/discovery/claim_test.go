package discovery

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// A fake network for the friendly name's claim: every message one host
// sends reaches every host (itself too, as multicast loopback does),
// synchronously; the clock never waits.

type fakeHost struct {
	c    *Config
	st   *Status
	addr netip.Addr
}

type fakeNet struct {
	mu    sync.Mutex
	hosts []*fakeHost
	// rogue answers a message as a host that is not a responder of ours
	// would (nil: no such host)
	rogue func(m message) []byte
	sent  map[string]int // probes seen per name
}

func newFakeNet() *fakeNet { return &fakeNet{sent: map[string]int{}} }

func (n *fakeNet) host(id, ip string) *fakeHost {
	c := testConfig()
	c.Info.ID = id
	a := netip.MustParseAddr(ip)
	c.Addrs = func() []netip.Prefix { return []netip.Prefix{netip.PrefixFrom(a, 24)} }
	h := &fakeHost{c: c, st: &Status{}, addr: a}
	h.st.setPort(c.Info.Port)
	c.Status = h.st
	c.claim = newHostClaim(c.lanAddrs)
	n.mu.Lock()
	n.hosts = append(n.hosts, h)
	n.mu.Unlock()
	return h
}

// deliver hands b, sent by from, to every host and to the rogue; their
// answers go round the same way.
func (n *fakeNet) deliver(from netip.Addr, b []byte) {
	m, err := parseMessage(b)
	if err != nil {
		panic(err)
	}
	n.mu.Lock()
	hosts := append([]*fakeHost(nil), n.hosts...)
	rogue := n.rogue
	if !m.response {
		for _, q := range m.questions {
			if len(m.authority) > 0 {
				n.sent[q.name]++
			}
		}
	}
	n.mu.Unlock()
	for _, h := range hosts {
		h.c.claim.observe(m)
		if out, _ := h.c.respond(b, netip.AddrPortFrom(from, 5353)); out != nil {
			n.deliver(h.addr, out)
		}
	}
	if rogue != nil {
		if out := rogue(m); out != nil {
			n.deliver(netip.MustParseAddr("192.168.50.99"), out)
		}
	}
}

// fakeClock: after fires at once and moves the clock on by d.
type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	waits []time.Duration
}

func (f *fakeClock) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.waits = append(f.waits, d)
	now := f.t
	f.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

func (f *fakeClock) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.t }

func (n *fakeNet) env(h *fakeHost, clk *fakeClock) claimEnv {
	return claimEnv{
		send:   func(b []byte) { n.deliver(h.addr, b) },
		after:  clk.after,
		jitter: func() time.Duration { return 0 },
		now:    clk.now,
		status: h.st,
		log:    func(string, ...any) {},
	}
}

// start runs h's claim until the test ends; the returned channel gets the
// name run returns.
func (n *fakeNet) start(t *testing.T, h *fakeHost) (*fakeClock, <-chan string) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1_000_000, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan string, 1)
	go func() { out <- h.c.claim.run(ctx, n.env(h, clk)) }()
	t.Cleanup(cancel)
	return clk, out
}

func waitState(t *testing.T, st *Status, want string) LocalName {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ln := st.Get(); ln.State == want {
			return ln
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("state %+v, want %s", st.Get(), want)
	return LocalName{}
}

func TestClaimAloneTakesTheName(t *testing.T) {
	n := newFakeNet()
	a := n.host("aaaaaaaa00", "192.168.50.10")
	n.start(t, a)
	ln := waitState(t, a.st, StateReady)
	if ln.Name != "antresol.local" || ln.URL != "http://antresol.local:8420" {
		t.Fatalf("%+v", ln)
	}
	if n.sent["antresol.local."] != 3 {
		t.Fatalf("%d probes, want 3", n.sent["antresol.local."])
	}
	// once owned, an A query for it is answered with the address on the asker's subnet
	out, _ := a.c.respond(mdnsQuery(0, question{name: "antresol.local.", qtype: typeA}), netip.MustParseAddrPort("192.168.50.77:5353"))
	p := parseResponse(t, out)
	if r := find(p.answers, typeA); r == nil || netip.AddrFrom4([4]byte(r.data)).String() != "192.168.50.10" || r.class != classIN|cacheFlush {
		t.Fatalf("%+v", p)
	}
}

func TestClaimNotAnsweredWhileProbing(t *testing.T) {
	c := testConfig()
	c.claim = newHostClaim(c.lanAddrs)
	c.claim.setState(true, false)
	if out, _ := c.respond(mdnsQuery(0, question{name: "antresol.local.", qtype: typeA}), netip.MustParseAddrPort("192.168.50.77:5353")); out != nil {
		t.Fatal("answered a name not yet ours")
	}
}

func TestClaimSecondServerTakesTheNextName(t *testing.T) {
	n := newFakeNet()
	a := n.host("aaaaaaaa00", "192.168.50.10")
	n.start(t, a)
	waitState(t, a.st, StateReady)

	b := n.host("bbbbbbbb00", "192.168.50.20")
	_, done := n.start(t, b)
	ln := waitState(t, b.st, StateReady)
	if ln.Name != "antresol-2.local" {
		t.Fatalf("second server: %+v", ln)
	}
	// the first keeps its name: the second's announcement is no conflict to it
	if ln := a.st.Get(); ln.Name != "antresol.local" {
		t.Fatalf("first server: %+v", ln)
	}
	if n.sent["antresol.local."] != 3+1 {
		t.Fatalf("antresol.local probed %d times: the second gives way after the first answer", n.sent["antresol.local."])
	}
	select {
	case name := <-done:
		t.Fatalf("run returned %q while owning the name", name)
	default:
	}
}

func TestClaimGivesWayDuringOperation(t *testing.T) {
	n := newFakeNet()
	a := n.host("aaaaaaaa00", "192.168.50.10")
	n.start(t, a)
	waitState(t, a.st, StateReady)

	// a host that came back from sleep holding antresol.local announces it, and defends it
	claim := func() []byte {
		return buildResponse(false, 0, nil, []record{aRecord("antresol.local.", netip.MustParseAddr("192.168.50.99"), ttlHost)}, nil)
	}
	n.mu.Lock()
	n.rogue = func(m message) []byte {
		if !m.response && len(m.questions) > 0 && m.questions[0].name == "antresol.local." {
			return claim()
		}
		return nil
	}
	n.mu.Unlock()
	n.deliver(netip.MustParseAddr("192.168.50.99"), claim())
	deadline := time.Now().Add(3 * time.Second)
	for a.st.Get().Name != "antresol-2.local" {
		if time.Now().After(deadline) {
			t.Fatalf("%+v", a.st.Get())
		}
		time.Sleep(2 * time.Millisecond)
	}
	if out, _ := a.c.respond(mdnsQuery(0, question{name: "antresol.local.", qtype: typeA}), netip.MustParseAddrPort("192.168.50.77:5353")); out != nil {
		t.Fatal("still answers the name it gave up")
	}
}

func TestClaimOwnTrafficIsNoConflict(t *testing.T) {
	c := testConfig()
	h := newHostClaim(c.lanAddrs)
	name := friendlyName(1)
	h.setState(true, false)
	own := func(b []byte) message { m, _ := parseMessage(b); return m }
	if got := h.judge(own(buildProbe(name, h.proposed(name))), name, true); got != noConflict {
		t.Fatalf("own probe: %v", got)
	}
	if got := h.judge(own(buildResponse(false, 0, nil, h.proposed(name), nil)), name, false); got != noConflict {
		t.Fatalf("own announcement: %v", got)
	}
	// a goodbye of another host is not a claim
	bye := buildResponse(false, 0, nil, []record{aRecord(name, netip.MustParseAddr("192.168.50.99"), 0)}, nil)
	if got := h.judge(own(bye), name, false); got != noConflict {
		t.Fatalf("goodbye: %v", got)
	}
	// another name's records are none of its business
	other := buildResponse(false, 0, nil, []record{aRecord("antresol-2.local.", netip.MustParseAddr("192.168.50.99"), ttlHost)}, nil)
	if got := h.judge(own(other), name, false); got != noConflict {
		t.Fatalf("other name: %v", got)
	}
}

func TestClaimSimultaneousProbeTieBreak(t *testing.T) {
	c := testConfig()
	c.Addrs = func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("192.168.50.20/24")} }
	h := newHostClaim(c.lanAddrs)
	name := friendlyName(1)
	probeFrom := func(ip string) message {
		m, err := parseMessage(buildProbe(name, []record{aRecord(name, netip.MustParseAddr(ip), ttlHost)}))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	// 192.168.50.30 is later than ours: we lose and probe again
	if got := h.judge(probeFrom("192.168.50.30"), name, true); got != lostTie {
		t.Fatalf("later probe: %v", got)
	}
	// 192.168.50.10 is earlier: they give way, we go on
	if got := h.judge(probeFrom("192.168.50.10"), name, true); got != noConflict {
		t.Fatalf("earlier probe: %v", got)
	}
	// once the name is ours a probe is answered, not compared
	if got := h.judge(probeFrom("192.168.50.30"), name, false); got != noConflict {
		t.Fatalf("probe while owned: %v", got)
	}
	// more records with the same start: the longer list is later
	ours := []record{aRecord(name, netip.MustParseAddr("192.168.50.20"), 1)}
	theirs := []rrIn{{name: name, rtype: typeA, class: classIN, data: []byte{192, 168, 50, 20}}, {name: name, rtype: typeA, class: classIN, data: []byte{192, 168, 50, 21}}}
	if tieBreak(ours, theirs) >= 0 {
		t.Fatal("the longer list must win")
	}
	if tieBreak(ours, theirs[:1]) != 0 {
		t.Fatal("the same records")
	}
}

func TestClaimLostTieProbesTheSameNameAgain(t *testing.T) {
	n := newFakeNet()
	a := n.host("aaaaaaaa00", "192.168.50.10")
	// a host with later data probes antresol.local at the same moment, once
	var once sync.Once
	n.rogue = func(m message) []byte {
		var out []byte
		if !m.response && len(m.authority) > 0 && m.questions[0].name == "antresol.local." {
			once.Do(func() {
				out = buildProbe("antresol.local.", []record{aRecord("antresol.local.", netip.MustParseAddr("192.168.50.200"), ttlHost)})
			})
		}
		return out
	}
	clk, _ := n.start(t, a)
	ln := waitState(t, a.st, StateReady)
	if ln.Name != "antresol.local" { // the other prober went away: the name is still free
		t.Fatalf("%+v", ln)
	}
	clk.mu.Lock()
	defer clk.mu.Unlock()
	saw := false
	for _, d := range clk.waits {
		saw = saw || d == time.Second
	}
	if !saw || n.sent["antresol.local."] != 1+3+1 { // ours before and after the second, and the other's
		t.Fatalf("waits %v, probes %d", clk.waits, n.sent["antresol.local."])
	}
}

func TestClaimSlowsDownAndGivesUp(t *testing.T) {
	n := newFakeNet()
	a := n.host("aaaaaaaa00", "192.168.50.10")
	// a broken host that claims every name it is asked about
	n.rogue = func(m message) []byte {
		if m.response || len(m.questions) == 0 {
			return nil
		}
		return buildResponse(false, 0, nil, []record{aRecord(m.questions[0].name, netip.MustParseAddr("192.168.50.99"), ttlHost)}, nil)
	}
	clk, _ := n.start(t, a)
	waitState(t, a.st, StateFailed)
	clk.mu.Lock()
	defer clk.mu.Unlock()
	slow := 0
	for _, d := range clk.waits {
		if d == 5*time.Second {
			slow++
		}
	}
	if slow == 0 {
		t.Fatalf("no 5 s wait after 15 conflicts: %v", clk.waits)
	}
	if n.sent[friendlyName(maxFriendly)] != 1 || n.sent[friendlyName(maxFriendly+1)] != 0 {
		t.Fatalf("probes %v", n.sent)
	}
}

func TestStatusURL(t *testing.T) {
	var nilStatus *Status
	if nilStatus.Get().State != StateOff {
		t.Fatal("nil status")
	}
	s := &Status{}
	s.setPort(80)
	s.set(StateReady, "antresol-2.local")
	if ln := s.Get(); ln.URL != "http://antresol-2.local" {
		t.Fatalf("%+v", ln)
	}
	s.set(StateBridge, "")
	if ln := s.Get(); ln.Name != "" || ln.URL != "" || ln.State != StateBridge {
		t.Fatalf("%+v", ln)
	}
}

func TestNATOnly(t *testing.T) {
	lo := link{name: "lo", up: true, loopback: true}
	for _, tc := range []struct {
		name  string
		links []link
		want  bool
	}{
		{"docker bridge", []link{lo, {name: "eth0", kind: "veth", up: true}}, true},
		{"rootless podman", []link{lo, {name: "tap0", kind: "tun", up: true}}, true},
		{"host network", []link{lo, {name: "enp3s0", up: true}, {name: "docker0", kind: "bridge", up: true}}, false},
		{"host network in a system-container guest", []link{lo, {name: "eth0", kind: "veth", up: true}, {name: "docker0", kind: "bridge"}}, false},
		{"host network, no docker0", []link{lo, {name: "eth0", up: true}}, false},
		{"macvlan", []link{lo, {name: "eth0", kind: "macvlan", up: true}}, false},
		{"nothing up", []link{lo, {name: "eth0", kind: "veth"}}, false},
	} {
		if got := natOnly(tc.links); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

func TestParseMessageSections(t *testing.T) {
	name := friendlyName(2)
	p := buildProbe(name, []record{aRecord(name, netip.MustParseAddr("192.168.50.10"), ttlHost)})
	m, err := parseMessage(p)
	if err != nil || m.response || len(m.questions) != 1 || !m.questions[0].qu || m.questions[0].qtype != typeANY ||
		len(m.authority) != 1 || m.authority[0].class != classIN || m.authority[0].name != "antresol-2.local." {
		t.Fatalf("%+v %v", m, err)
	}
	// a cut record section keeps the questions
	m, err = parseMessage(p[:len(p)-2])
	if err != nil || len(m.questions) != 1 || len(m.authority) != 0 {
		t.Fatalf("%+v %v", m, err)
	}
}
