package discovery

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var hostLAN = []netip.Prefix{netip.MustParsePrefix("192.168.50.10/24")}

func writeAnnouncer(t *testing.T, dir string, a sharedAnnouncer) {
	t.Helper()
	if err := writeShared(dir, announcerFile, a); err != nil {
		t.Fatal(err)
	}
}

// The server on a bridge network: it tells who it is, and shows the name
// and the host's addresses only while the announcer's file is fresh.
func TestShareServerSide(t *testing.T) {
	dir := t.TempDir()
	st := &Status{}
	c := &Config{Info: Info{ID: "0123456789abcdef", Name: "Дом", Version: "1.2.3", Port: 8420, Scheme: "http"},
		ShareDir: dir, Status: st, network: netBridge, host: &hostView{},
		Addrs: func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("172.18.0.5/16")} }}
	now := time.Unix(1_800_000_000, 0)
	var look shareLook
	if _, err := c.shareOnce(now, &look); err != nil {
		t.Fatal(err)
	}
	var s sharedServer
	if !readShared(dir, serverFile, &s, now) || s.ID != c.Info.ID || s.Name != "Дом" || s.Port != 8420 || s.Version != "1.2.3" {
		t.Fatalf("server.json %+v", s)
	}
	// the announcer has its time to show up: not "bridge" yet
	if got := st.Get(); got.State != StateProbing {
		t.Fatalf("no announcer yet: %+v", got)
	}

	writeAnnouncer(t, dir, sharedAnnouncer{State: StateReady, Name: "antresol-2.local", Port: 8500,
		Addrs: []string{"192.168.50.10/24"}, Updated: now.Unix()})
	c.shareOnce(now.Add(5*time.Second), &look)
	if got := st.Get(); got.State != StateReady || got.URL != "http://antresol-2.local:8500" {
		t.Fatalf("announcer ready: %+v", got)
	}
	// the UDP answer carries the host's address and the published port,
	// not the container's
	var a Answer
	json.Unmarshal(c.answer(netip.MustParseAddr("192.168.50.77")), &a)
	if a.Port != 8500 || len(a.URLs) != 1 || a.URLs[0] != "http://192.168.50.10:8500" {
		t.Fatalf("answer %+v", a)
	}

	// the announcer stopped: its file goes stale
	c.shareOnce(now.Add(shareStale+time.Second), &look)
	if got := st.Get(); got.State != StateBridge || got.URL != "" {
		t.Fatalf("stale announcer: %+v", got)
	}
	json.Unmarshal(c.answer(netip.MustParseAddr("192.168.50.77")), &a)
	if a.Port != 8420 || a.URLs[0] != "http://172.18.0.5:8420" {
		t.Fatalf("answer after the announcer: %+v", a)
	}

	// an announcer that is on a VM's network itself: its state, not its addresses
	writeAnnouncer(t, dir, sharedAnnouncer{State: StateVM, Port: 8500, Addrs: []string{"192.168.65.3/24"}, Updated: now.Unix()})
	c.shareOnce(now, &look)
	if got := st.Get(); got.State != StateVM {
		t.Fatalf("vm: %+v", got)
	}
	if addrs, _ := c.host.get(); addrs != nil {
		t.Fatalf("vm addresses used: %v", addrs)
	}
}

// bridgeServer is a server on a bridge network that logs into buf.
func bridgeServer(t *testing.T, dir string, buf *bytes.Buffer) (*Config, *Status) {
	t.Helper()
	st := &Status{}
	log := slog.New(slog.NewTextHandler(buf, nil))
	return &Config{Info: Info{ID: "0123456789abcdef", Name: "Дом", Version: "1.2.3", Port: 8420, Scheme: "http"},
		ShareDir: dir, Status: st, network: netBridge, host: &hostView{}, Log: log,
		Addrs: func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("172.18.0.5/16")} }}, st
}

// The server and the announcer start together: the announcer learns of the
// server from server.json, claims the name and only then writes its file.
// A server that has not read that file yet is not on a "bridge network
// without an announcer": it says "probing" and logs nothing, for one full
// staleness window; only after it, with no announcer file, it says bridge.
func TestShareStartWaitsOneWindowForTheAnnouncer(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	c, st := bridgeServer(t, dir, &buf)
	now := time.Unix(1_800_000_000, 0)
	var look shareLook
	for _, at := range []time.Duration{0, shareFast, shareEvery, shareStale - time.Second} {
		state, err := c.shareOnce(now.Add(at), &look)
		if err != nil || state != StateProbing || st.Get().State != StateProbing {
			t.Fatalf("at +%v: %q %v, status %+v", at, state, err, st.Get())
		}
		if buf.Len() != 0 {
			t.Fatalf("at +%v: logged %q", at, buf.String())
		}
	}
	state, _ := c.shareOnce(now.Add(shareStale), &look)
	if state != StateBridge || st.Get().State != StateBridge {
		t.Fatalf("after a full window: %q, status %+v", state, st.Get())
	}
	if !strings.Contains(buf.String(), "no mDNS announcer") || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("log after a full window: %q", buf.String())
	}
}

// The announcer file is read at the very first look, so an announcer that
// is already there (the server alone restarted) is believed at once, with
// no "bridge" in between and nothing in the log about a missing announcer.
func TestShareFirstLookReadsTheAnnouncer(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	c, st := bridgeServer(t, dir, &buf)
	now := time.Unix(1_800_000_000, 0)
	writeAnnouncer(t, dir, sharedAnnouncer{State: StateReady, Name: "antresol.local", Port: 8420,
		Addrs: []string{"192.168.50.10/24"}, Updated: now.Unix() - 3})
	var look shareLook
	state, err := c.shareOnce(now, &look)
	if err != nil || state != StateReady {
		t.Fatalf("%q %v", state, err)
	}
	if got := st.Get(); got.State != StateReady || got.URL != "http://antresol.local:8420" {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(buf.String(), "no mDNS announcer") {
		t.Fatalf("logged a missing announcer: %q", buf.String())
	}
}

// The window is for an announcer not seen yet: once it has been seen, its
// file going away is bridge at once (the announcer was stopped; it left),
// and a stale file does not restart the wait.
func TestShareAnnouncerSeenThenGoneIsBridgeAtOnce(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	c, st := bridgeServer(t, dir, &buf)
	now := time.Unix(1_800_000_000, 0)
	var look shareLook
	c.shareOnce(now, &look)
	writeAnnouncer(t, dir, sharedAnnouncer{State: StateReady, Name: "antresol.local", Port: 8420, Updated: now.Unix()})
	c.shareOnce(now.Add(shareFast), &look)
	if st.Get().State != StateReady {
		t.Fatalf("%+v", st.Get())
	}
	os.Remove(filepath.Join(dir, announcerFile))
	if state, _ := c.shareOnce(now.Add(shareFast+shareEvery), &look); state != StateBridge || st.Get().State != StateBridge {
		t.Fatalf("announcer gone: %q, status %+v", state, st.Get())
	}
}

// The two sides look often while the name is being settled and at the
// heartbeat otherwise, so a start does not cost two rounds of shareEvery.
func TestShareWaits(t *testing.T) {
	for state, want := range map[string]time.Duration{
		StateProbing: shareFast, StateReady: shareEvery, StateBridge: shareEvery, StateFailed: shareEvery, StateVM: shareEvery,
	} {
		if got := shareWait(state); got != want {
			t.Errorf("server in %q: %v", state, got)
		}
	}
	an := &announcer{}
	if an.wait() != shareFast {
		t.Error("announcer with no server does not look often")
	}
	an.cur = &sharedServer{}
	for state, want := range map[string]time.Duration{
		StateProbing: shareFast, StateReady: shareEvery, StateFailed: shareEvery, StateVM: shareEvery,
	} {
		an.st.set(state, "")
		if got := an.wait(); got != want {
			t.Errorf("announcer in %q: %v", state, got)
		}
	}
}

// A server that is not on a bridge network claims the name itself: it
// takes away a server.json left from an earlier run, so the announcer
// stays quiet.
func TestShareOffBridgeRemovesServerFile(t *testing.T) {
	dir := t.TempDir()
	writeShared(dir, serverFile, sharedServer{ID: "x", Port: 8420, Updated: time.Now().Unix()})
	c := &Config{ShareDir: dir, network: netHost}
	c.share(context.Background())
	if _, err := os.Stat(filepath.Join(dir, serverFile)); !os.IsNotExist(err) {
		t.Fatalf("server.json kept: %v", err)
	}
}

func TestReadSharedStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_800_000_000, 0)
	var s sharedServer
	if readShared(dir, serverFile, &s, now) {
		t.Fatal("missing file read")
	}
	writeShared(dir, serverFile, sharedServer{ID: "x", Updated: now.Unix()})
	if !readShared(dir, serverFile, &s, now.Add(shareStale-time.Second)) {
		t.Fatal("fresh file not read")
	}
	for _, at := range []time.Time{now.Add(shareStale), now.Add(-shareStale)} {
		if readShared(dir, serverFile, &s, at) {
			t.Errorf("stale at %v", at.Sub(now))
		}
	}
	os.WriteFile(filepath.Join(dir, serverFile), []byte("{"), 0o644)
	if readShared(dir, serverFile, &s, now) {
		t.Fatal("broken file read")
	}
}

// fakeRuns records what the announcer runs; each run holds until stopped
// and reports the name as claimed.
type fakeRuns struct {
	mu   sync.Mutex
	got  []Config
	live int
}

func (f *fakeRuns) run(ctx context.Context, c Config) {
	f.mu.Lock()
	f.got = append(f.got, c)
	f.live++
	f.mu.Unlock()
	c.Status.setPort(c.Info.Port)
	c.Status.set(StateReady, "antresol.local")
	<-ctx.Done()
	f.mu.Lock()
	f.live--
	f.mu.Unlock()
}

func (f *fakeRuns) count() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got), f.live
}

func TestAnnouncerFollowsServerFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_800_000_000, 0)
	f := &fakeRuns{}
	an := &announcer{AnnounceConfig: AnnounceConfig{Dir: dir, Port: 8500}, now: func() time.Time { return now },
		run: f.run, addrs: func() []netip.Prefix { return hostLAN }}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); an.halt() }()

	// no server yet: nothing announced, no announcer.json
	an.step(ctx)
	if n, _ := f.count(); n != 0 {
		t.Fatal("announced without a server")
	}
	if _, err := os.Stat(filepath.Join(dir, announcerFile)); !os.IsNotExist(err) {
		t.Fatal("announcer.json without a server")
	}

	srv := sharedServer{ID: "0123456789abcdef", Name: "Дом", Version: "1.2.3", Scheme: "http", Port: 8420, Updated: now.Unix()}
	writeShared(dir, serverFile, srv)
	an.step(ctx)
	waitFor(t, func() bool { return an.st.Get().State == StateReady })
	an.step(ctx) // the write after the claim
	if n, live := f.count(); n != 1 || live != 1 {
		t.Fatalf("runs %d live %d", n, live)
	}
	if c := f.got[0]; c.Info.Port != 8500 || c.Info.ID != srv.ID || c.Info.Name != "Дом" || !c.MDNS || c.UDPPort != 0 || c.ShareDir != "" {
		t.Fatalf("config %+v", c)
	}
	var a sharedAnnouncer
	if !readShared(dir, announcerFile, &a, now) || a.State != StateReady || a.Name != "antresol.local" || a.Port != 8500 ||
		len(a.Addrs) != 1 || a.Addrs[0] != "192.168.50.10/24" {
		t.Fatalf("announcer.json %+v", a)
	}

	// the server's heartbeat alone: the same run goes on
	now = now.Add(shareEvery)
	srv.Updated = now.Unix()
	writeShared(dir, serverFile, srv)
	an.step(ctx)
	if n, _ := f.count(); n != 1 {
		t.Fatalf("restarted on a heartbeat: %d runs", n)
	}

	// a new name (or version): announced again
	srv.Name = "Дача"
	writeShared(dir, serverFile, srv)
	an.step(ctx)
	// the new run starts on its own goroutine: wait for it to record itself
	waitFor(t, func() bool { n, live := f.count(); return n == 2 && live == 1 })
	if n, live := f.count(); n != 2 || live != 1 || f.got[1].Info.Name != "Дача" {
		t.Fatalf("after a change: runs %d live %d", n, live)
	}

	// the server is gone: withdrawn, and the file goes
	now = now.Add(shareStale + time.Second)
	an.step(ctx)
	if _, live := f.count(); live != 0 {
		t.Fatal("still announcing a gone server")
	}
	if _, err := os.Stat(filepath.Join(dir, announcerFile)); !os.IsNotExist(err) {
		t.Fatal("announcer.json kept for a gone server")
	}
}

// Without VAULT_ANNOUNCE_PORT the server's own port is advertised.
func TestAnnouncerDefaultPort(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	f := &fakeRuns{}
	an := &announcer{AnnounceConfig: AnnounceConfig{Dir: dir}, now: time.Now, run: f.run, addrs: func() []netip.Prefix { return hostLAN }}
	writeShared(dir, serverFile, sharedServer{ID: "0123456789abcdef", Port: 8420, Updated: now.Unix()})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); an.halt() }()
	an.step(ctx)
	waitFor(t, func() bool { n, _ := f.count(); return n == 1 })
	if c := f.got[0]; c.Info.Port != 8420 || c.Info.Scheme != "http" {
		t.Fatalf("%+v", c.Info)
	}
}

// The announcer end to end on a loopback socket: from server.json to the
// records a phone on the home network gets (fake packets in, real
// responder code out).
func TestAnnouncerAnswersOverLoopback(t *testing.T) {
	dir := t.TempDir()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	an := &announcer{AnnounceConfig: AnnounceConfig{Dir: dir, Port: 8500}, now: time.Now,
		addrs: func() []netip.Prefix { return hostLAN },
		run: func(ctx context.Context, c Config) {
			// what runMDNS does on the host's network, the claim already won
			c.claim = newHostClaim(c.lanAddrs)
			c.claim.setState(false, true)
			c.Status.set(StateReady, "antresol.local")
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			close(served)
			serveMDNS(ctx, conn, &c, func([]byte) {})
		}}
	writeShared(dir, serverFile, sharedServer{ID: "0123456789abcdef", Name: "Дом", Version: "1.2.3", Port: 8420, Updated: time.Now().Unix()})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); an.halt() }()
	an.step(ctx)
	<-served

	cl, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ask := func(q question) parsed {
		t.Helper()
		cl.Write(mdnsQuery(9, q))
		cl.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 2048)
		n, err := cl.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		return parseResponse(t, buf[:n])
	}
	p := ask(question{name: "_antresol._tcp.local.", qtype: typePTR})
	srv := find(p.extra, typeSRV)
	if srv == nil || binary.BigEndian.Uint16(srv.data[4:]) != 8500 {
		t.Fatalf("SRV with the published port: %+v", p)
	}
	p = ask(question{name: "antresol.local.", qtype: typeA})
	a := find(p.answers, typeA)
	if a == nil || netip.AddrFrom4([4]byte(a.data)) != hostLAN[0].Addr() {
		t.Fatalf("antresol.local with the host's address: %+v", p)
	}
}

func TestVMHost(t *testing.T) {
	lan := []netip.Prefix{netip.MustParsePrefix("192.168.1.20/24")}
	gv := []netip.Prefix{netip.MustParsePrefix("192.168.127.2/24")}
	for _, c := range []struct {
		release string
		podman  bool
		addrs   []netip.Prefix
		want    bool
	}{
		{"6.10.14-linuxkit", false, lan, true},                   // Docker Desktop
		{"5.15.167.4-microsoft-standard-WSL2", false, lan, true}, // WSL 2
		{"6.11.3-200.fc40.aarch64", true, gv, true},              // Podman machine
		{"6.11.3-200.fc40.aarch64", false, gv, false},            // not Podman: a real LAN on that subnet
		{"6.1.0-26-amd64", true, lan, false},                     // Podman on a Linux host
		{"6.1.0-26-amd64", false, lan, false},
	} {
		if got := vmHost(c.release, c.podman, c.addrs); got != c.want {
			t.Errorf("%q podman=%v %v: %v", c.release, c.podman, c.addrs, got)
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
