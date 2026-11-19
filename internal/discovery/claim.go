package discovery

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"time"
)

// The friendly host name, antresol.local, that a browser on the home
// network opens (http://antresol.local:8420). Unlike antresol-<id8>.local
// it is the same for every server, so it is claimed as RFC 6762 asks of a
// shared name: probed first (§8.1), with a simultaneous probe settled by
// comparing the proposed records (§8.2), and given up when another host
// turns out to hold it, at start or later (§9). A server that loses takes
// the next name: antresol-2.local, antresol-3.local and so on.

// FriendlyBase is the first label of the friendly host name.
const FriendlyBase = "antresol"

// maxFriendly is the last fallback number tried (antresol-20.local): past
// it the network is broken in a way another name will not fix.
const maxFriendly = 20

// friendlyName is the n-th candidate: antresol.local., antresol-2.local., …
func friendlyName(n int) string {
	if n <= 1 {
		return FriendlyBase + ".local."
	}
	return FriendlyBase + "-" + strconv.Itoa(n) + ".local."
}

// The state of the local name, as Status reports it.
const (
	StateOff     = "off"     // mDNS is turned off
	StateProbing = "probing" // checking that the name is free
	StateReady   = "ready"   // the name is ours and answered
	StateBridge  = "bridge"  // a container network multicast does not leave (Docker bridge)
	StateFailed  = "failed"  // no mDNS socket, or no free name
)

// Status is what the responder tells about the local name, for the web
// UI («О сервере»). Safe for concurrent use; the zero value is "off".
type Status struct {
	mu    sync.Mutex
	state string
	name  string // e.g. antresol.local, without the trailing dot
	port  int
}

// LocalName is a snapshot of Status.
type LocalName struct {
	State string `json:"state"`
	Name  string `json:"name,omitempty"` // antresol.local (only when ready)
	URL   string `json:"url,omitempty"`  // http://antresol.local:8420 (only when ready)
}

// setPort is the HTTP port the URL carries.
func (s *Status) setPort(port int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.port = port
	s.mu.Unlock()
}

func (s *Status) set(state, name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.state, s.name = state, name
	s.mu.Unlock()
}

// Get is the current state.
func (s *Status) Get() LocalName {
	if s == nil {
		return LocalName{State: StateOff}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ln := LocalName{State: s.state}
	if ln.State == "" {
		ln.State = StateOff
	}
	if ln.State == StateReady && s.name != "" {
		ln.Name = s.name
		ln.URL = "http://" + s.name
		if s.port != 80 && s.port > 0 {
			ln.URL += ":" + strconv.Itoa(s.port)
		}
	}
	return ln
}

// conflict is what a message seen on the network means for the name.
type conflict int

const (
	noConflict conflict = iota
	taken               // another host answers with the name: take the next one
	lostTie             // a simultaneous probe with later data: probe the same name again in a second
)

// hostClaim holds the friendly name while it is probed and owned. The
// read loop reports what it sees (observe); the claim loop (run) probes,
// announces and starts over on a conflict.
type hostClaim struct {
	own func() []netip.Addr // every address of this server (not only one subnet's)

	mu      sync.Mutex
	n       int  // the candidate's number (friendlyName)
	owned   bool // probing done: the name is answered
	probing bool
	events  chan conflict // what observe saw, for run (buffer 1: the first one counts)
}

func newHostClaim(own func() []netip.Addr) *hostClaim {
	return &hostClaim{own: own, n: 1, events: make(chan conflict, 1)}
}

// name is the current candidate (or the owned name); "" before run.
func (h *hostClaim) name() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return friendlyName(h.n)
}

// answered is the name A queries are answered for, "" while it is not ours.
func (h *hostClaim) answered() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.owned {
		return ""
	}
	return friendlyName(h.n)
}

// proposed are the records this server claims the name with: an A record
// per address, sorted as §8.2 compares them.
func (h *hostClaim) proposed(name string) []record {
	var rs []record
	for _, a := range h.own() {
		rs = append(rs, aRecord(name, a, ttlHost))
	}
	slices.SortFunc(rs, func(a, b record) int { return bytes.Compare(a.data, b.data) })
	return rs
}

// ours: an A record whose address is one of this server's own (our own
// announcement or probe, looped back, is not a conflict).
func (h *hostClaim) ours(r rrIn) bool {
	if r.rtype != typeA || len(r.data) != 4 {
		return false
	}
	a := netip.AddrFrom4([4]byte(r.data))
	return slices.Contains(h.own(), a)
}

// observe looks at a message read from the network for what it says
// about the name, and hands a conflict to the claim loop.
func (h *hostClaim) observe(m message) conflict {
	h.mu.Lock()
	name, probing, owned := friendlyName(h.n), h.probing, h.owned
	h.mu.Unlock()
	if !probing && !owned {
		return noConflict
	}
	c := h.judge(m, name, probing)
	if c != noConflict {
		select {
		case h.events <- c:
		default:
		}
	}
	return c
}

// judge is observe without the state: what m means for name.
func (h *hostClaim) judge(m message, name string, probing bool) conflict {
	if m.opcode != 0 {
		return noConflict
	}
	if m.response {
		// §8.1 / §9: any record of the name with data that is not ours,
		// from a host that means it (a goodbye is not a claim)
		for _, r := range m.records() {
			if r.name == name && r.ttl > 0 && !h.ours(r) {
				return taken
			}
		}
		return noConflict
	}
	if !probing {
		return noConflict // a probe for the name we own is answered by respond
	}
	// a probe of another host for the same name at the same time (§8.2)
	asks := false
	for _, q := range m.questions {
		asks = asks || q.name == name
	}
	if !asks {
		return noConflict
	}
	var theirs []rrIn
	for _, r := range m.authority {
		if r.name == name {
			theirs = append(theirs, r)
		}
	}
	if len(theirs) == 0 {
		return noConflict // a plain question, not a probe
	}
	if tieBreak(h.proposed(name), theirs) < 0 {
		return lostTie
	}
	return noConflict // ours is later (they give way) or the same (our own probe)
}

// tieBreak compares the proposed records of two probes as §8.2 does:
// both sorted by class, type and rdata, compared pair by pair; the first
// difference decides, and a list that runs out first is the earlier one.
// < 0: theirs is later and wins; 0: the same records; > 0: ours wins.
func tieBreak(ours []record, theirs []rrIn) int {
	type key struct {
		class, rtype uint16
		data         []byte
	}
	cmp := func(a, b key) int {
		if a.class != b.class {
			return int(a.class) - int(b.class)
		}
		if a.rtype != b.rtype {
			return int(a.rtype) - int(b.rtype)
		}
		return bytes.Compare(a.data, b.data)
	}
	var o, t []key
	for _, r := range ours {
		o = append(o, key{classIN, r.rtype, r.data})
	}
	for _, r := range theirs {
		t = append(t, key{r.class, r.rtype, r.data})
	}
	slices.SortFunc(o, cmp)
	slices.SortFunc(t, cmp)
	for i := range min(len(o), len(t)) {
		if c := cmp(o[i], t[i]); c != 0 {
			return c
		}
	}
	return len(o) - len(t)
}

// claimEnv is what the claim loop does to the outside world; tests give
// a fake network and a clock that does not wait.
type claimEnv struct {
	send   func([]byte) // multicast on every interface
	after  func(time.Duration) <-chan time.Time
	jitter func() time.Duration // the random wait before the first probe (0–250 ms)
	now    func() time.Time
	status *Status
	log    func(msg string, args ...any)
}

func realClaimEnv(send func([]byte), st *Status, log func(string, ...any)) claimEnv {
	return claimEnv{send: send, after: time.After, now: time.Now, status: st, log: log,
		jitter: func() time.Duration { return time.Duration(rand.Int64N(int64(250 * time.Millisecond))) }}
}

// wait sleeps d; it returns early with a conflict observe saw (one seen
// while sleeping wins over the timer).
func (h *hostClaim) wait(ctx context.Context, env claimEnv, d time.Duration) (conflict, bool) {
	select {
	case <-ctx.Done():
		return noConflict, false
	case c := <-h.events:
		return c, true
	case <-env.after(d):
	}
	select {
	case c := <-h.events:
		return c, true
	default:
		return noConflict, true
	}
}

func (h *hostClaim) setState(probing, owned bool) {
	h.mu.Lock()
	h.probing, h.owned = probing, owned
	h.mu.Unlock()
}

// run claims a name and keeps it until ctx ends; it returns the name it
// owned last ("" when none), for the goodbye.
func (h *hostClaim) run(ctx context.Context, env claimEnv) string {
	var conflicts []time.Time // §8.1: 15 conflicts in 10 s slow the probing down to one try per 5 s
	for ctx.Err() == nil {
		name := h.name()
		h.setState(true, false)
		env.status.set(StateProbing, "")
		// drop what was seen for the previous candidate
		select {
		case <-h.events:
		default:
		}
		recent := conflicts[:0]
		for _, t := range conflicts {
			if env.now().Sub(t) < 10*time.Second {
				recent = append(recent, t)
			}
		}
		conflicts = recent
		first := env.jitter()
		if len(conflicts) >= 15 {
			first = 5 * time.Second
		}
		c, ok := h.wait(ctx, env, first)
		for i := 0; ok && c == noConflict && i < 3; i++ { // three probes, 250 ms apart
			env.send(buildProbe(name, h.proposed(name)))
			c, ok = h.wait(ctx, env, 250*time.Millisecond)
		}
		if !ok {
			break
		}
		switch c {
		case taken:
			conflicts = append(conflicts, env.now())
			h.mu.Lock()
			h.n++
			n := h.n
			h.mu.Unlock()
			if n > maxFriendly {
				h.setState(false, false)
				env.status.set(StateFailed, "")
				env.log("discovery: no free local name", "last", trimDot(name))
				<-ctx.Done()
				return ""
			}
			env.log("discovery: local name taken, trying the next", "name", trimDot(name), "next", trimDot(friendlyName(n)))
			continue
		case lostTie:
			conflicts = append(conflicts, env.now())
			if _, ok := h.wait(ctx, env, time.Second); !ok {
				return ""
			}
			continue
		}
		// the name is ours: announce it twice, a second apart (§8.3)
		h.setState(false, true)
		env.status.set(StateReady, trimDot(name))
		env.log("discovery: local name", "name", trimDot(name))
		ann := buildResponse(false, 0, nil, h.proposed(name), nil)
		env.send(ann)
		c, ok = h.wait(ctx, env, time.Second)
		if ok && c == noConflict {
			env.send(ann)
			c, ok = h.waitConflict(ctx)
		}
		if !ok {
			return name
		}
		// §9: someone else claims it; probe it again (if they hold it,
		// they answer the probe and the next name is taken)
		env.log("discovery: local name claimed by another host, checking again", "name", trimDot(name))
	}
	if n := h.answered(); n != "" {
		return n
	}
	return ""
}

// waitConflict blocks until a conflict is seen or ctx ends.
func (h *hostClaim) waitConflict(ctx context.Context) (conflict, bool) {
	select {
	case <-ctx.Done():
		return noConflict, false
	case c := <-h.events:
		return c, true
	}
}

func trimDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}
