package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The mDNS announcer: antresol.local and the apps' search for a server
// on a bridge network.
//
// compose.yml runs the server on the project's own bridge network: the
// helper services of addons are found there by their service names, which
// host networking would break. Multicast does not leave that network, so
// beside the server a second container of the same image,
// `vault mdns-announce`, runs on the host's network and does mDNS alone:
// _antresol._tcp and antresol.local, with the host's addresses and the
// port published for the server. It opens no port but UDP 5353.
//
// The two talk through a shared folder (VAULT_ANNOUNCE_DIR), not the
// network: the announcer is not on the project network, the published
// port may be bound to one address only, and a file needs no secret.
//
//	server.json     the server: id, name, version, port
//	announcer.json  the announcer: the name's state and the name, the
//	                host's addresses and the advertised port
//
// Each side rewrites its file every shareEvery; a file older than
// shareStale is from a container that is gone. The server then says
// "bridge" again (no name), the announcer says goodbye for it. shareStale
// rides over a restart of either side, so the name is not let go.
//
// The same shareStale is the time a starting server gives the announcer to
// show up: the two usually start together, and the announcer learns of the
// server from server.json, claims the name (about a second) and writes
// announcer.json, so until the server has read that file once it says
// "probing", not "bridge". Both sides look every shareFast meanwhile (the
// announcer: while it has no server or is still probing), so the name shows
// within seconds of a start, not after two rounds of shareEvery.

const (
	serverFile    = "server.json"
	announcerFile = "announcer.json"
	shareEvery    = 10 * time.Second
	shareFast     = 2 * time.Second // between looks while the two sides are finding each other
	shareStale    = 45 * time.Second
)

// sharedServer is server.json.
type sharedServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Scheme  string `json:"scheme"`
	Port    int    `json:"port"`    // the HTTP port in the container
	Updated int64  `json:"updated"` // unix seconds
}

// sharedAnnouncer is announcer.json.
type sharedAnnouncer struct {
	State   string   `json:"state"`
	Name    string   `json:"name,omitempty"` // antresol.local (only when ready)
	Port    int      `json:"port"`           // the HTTP port on the host
	Addrs   []string `json:"addrs,omitempty"`
	Updated int64    `json:"updated"`
}

// writeShared replaces dir/file with v's JSON (a whole file or none: the
// other side reads it at any moment).
func writeShared(dir, file string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+file+".*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, file))
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// readShared reads dir/file into v; false when it is missing, unreadable
// or stale (a clock far ahead counts as stale too).
func readShared(dir, file string, v any, now time.Time) bool {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return false
	}
	var stamp struct {
		Updated int64 `json:"updated"`
	}
	if json.Unmarshal(b, &stamp) != nil || json.Unmarshal(b, v) != nil {
		return false
	}
	age := now.Sub(time.Unix(stamp.Updated, 0))
	return age < shareStale && age > -shareStale
}

// hostView is what the announcer tells about the host: its LAN prefixes
// and the published port, for the UDP answers (Config.lan).
type hostView struct {
	mu    sync.Mutex
	addrs []netip.Prefix
	port  int
}

func (h *hostView) get() ([]netip.Prefix, int) {
	if h == nil {
		return nil, 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.addrs, h.port
}

func (h *hostView) set(addrs []netip.Prefix, port int) {
	h.mu.Lock()
	h.addrs, h.port = addrs, port
	h.mu.Unlock()
}

// shareLook is what the server's side remembers between its looks.
type shareLook struct {
	last  string    // the state and name logged last (a change is logged)
	since time.Time // the first look: the announcer has shareStale from then to show up
	seen  bool      // announcer.json has been fresh at least once
}

// share is the server's side, until ctx ends. Only a server on a bridge
// network has an announcer; any other removes a server.json left from an
// earlier run, so an announcer does not claim the name it claims itself.
func (c *Config) share(ctx context.Context) {
	if c.network != netBridge {
		if err := os.Remove(filepath.Join(c.ShareDir, serverFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			c.log().Warn("discovery: announcer folder", "dir", c.ShareDir, "err", err)
		}
		return
	}
	var look shareLook
	var lastErr string
	for {
		state, err := c.shareOnce(time.Now(), &look)
		if e := errString(err); e != lastErr { // once until it changes or is fixed
			lastErr = e
			if err != nil {
				c.log().Warn("discovery: announcer folder", "dir", c.ShareDir, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(shareWait(state)):
		}
	}
}

// shareWait is the time to the next look at a state: short while the name
// is still being settled (the other side is waiting for the news), else
// the heartbeat.
func shareWait(state string) time.Duration {
	if state == StateProbing {
		return shareFast
	}
	return shareEvery
}

// shareOnce writes server.json and takes the announcer's word for the
// name; it returns the state it set. look is what was seen before; the
// error is that of the write.
func (c *Config) shareOnce(now time.Time, look *shareLook) (string, error) {
	werr := writeShared(c.ShareDir, serverFile, sharedServer{ID: c.Info.ID, Name: c.Info.Name, Version: c.Info.Version,
		Scheme: c.Info.Scheme, Port: c.Info.Port, Updated: now.Unix()})
	if look.since.IsZero() {
		look.since = now
	}
	var a sharedAnnouncer
	state, name := StateBridge, ""
	waiting := false // no announcer yet, and its time to show up is not over
	if readShared(c.ShareDir, announcerFile, &a, now) {
		look.seen = true
		switch a.State {
		case StateReady:
			if a.Name != "" && a.Port > 0 {
				state, name = StateReady, a.Name
			}
		case StateProbing, StateFailed, StateVM:
			state = a.State
		}
		// the addresses of an announcer that is not on the home network
		// itself (bridge, vm) are no better than the server's own
		if state != StateBridge && state != StateVM {
			var addrs []netip.Prefix
			for _, s := range a.Addrs {
				if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() {
					addrs = append(addrs, p)
				}
			}
			c.host.set(addrs, a.Port)
		} else {
			c.host.set(nil, 0)
		}
	} else {
		c.host.set(nil, 0)
		if !look.seen && now.Sub(look.since) < shareStale {
			state, waiting = StateProbing, true
		}
	}
	if state == StateReady {
		c.Status.setPort(a.Port)
	} else {
		c.Status.setPort(c.Info.Port)
	}
	c.Status.set(state, name)
	if key := state + " " + name; key != look.last && !waiting {
		look.last = key
		switch state {
		case StateReady:
			c.log().Info("discovery: the mDNS announcer holds the local name", "name", name, "port", a.Port)
		case StateBridge:
			c.log().Info("discovery: no mDNS announcer: " + FriendlyBase + ".local and the apps' search do not reach the home network")
		default:
			c.log().Info("discovery: the mDNS announcer", "state", state)
		}
	}
	return state, werr
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// AnnounceConfig is `vault mdns-announce`.
type AnnounceConfig struct {
	Dir string // the shared folder (VAULT_ANNOUNCE_DIR)
	// Port is the HTTP port published for the server on the host
	// (VAULT_ANNOUNCE_PORT); 0: the server's own, for ports "N:N".
	Port int
	Log  *slog.Logger
}

// announcer runs Run for the server server.json describes.
type announcer struct {
	AnnounceConfig
	now   func() time.Time
	run   func(context.Context, Config) // Run; tests: a fake network
	addrs func() []netip.Prefix
	check func(context.Context, sharedServer) // checkPort

	st   Status
	cur  *sharedServer // what runs, nil: nothing
	stop context.CancelFunc
	done chan struct{}
}

func (an *announcer) log() *slog.Logger {
	if an.Log != nil {
		return an.Log
	}
	return slog.Default()
}

// Announce is the announcer, until ctx ends: it advertises the server of
// server.json while that file is fresh, and writes announcer.json.
func Announce(ctx context.Context, a AnnounceConfig) {
	an := &announcer{AnnounceConfig: a, now: time.Now, run: Run, addrs: ifaceAddrs}
	an.check = an.checkPort
	an.log().Info("discovery: mDNS announcer", "dir", a.Dir, "port", a.Port)
	for {
		an.step(ctx)
		select {
		case <-ctx.Done():
			an.halt()
			os.Remove(filepath.Join(an.Dir, announcerFile))
			return
		case <-time.After(an.wait()):
		}
	}
}

// wait is the time to the next look at server.json: short while there is
// no server yet (it is probably starting) or the name is still being
// probed (the server waits to read the result), else the heartbeat.
func (an *announcer) wait() time.Duration {
	if an.cur == nil || an.st.Get().State == StateProbing {
		return shareFast
	}
	return shareEvery
}

// step is one look at server.json, and the write of announcer.json.
func (an *announcer) step(ctx context.Context) {
	var s sharedServer
	fresh := readShared(an.Dir, serverFile, &s, an.now())
	if fresh {
		s.Updated = 0
		if an.Port > 0 {
			s.Port = an.Port
		}
		if s.Scheme == "" {
			s.Scheme = "http"
		}
		fresh = s.ID != "" && s.Port > 0
	}
	switch {
	case !fresh && an.cur != nil:
		an.log().Info("discovery: the server is gone, its mDNS records withdrawn")
		an.halt()
	case fresh && (an.cur == nil || *an.cur != s):
		if an.cur != nil {
			an.log().Info("discovery: the server changed, announcing it again")
		}
		an.halt()
		an.start(ctx, s)
	}
	if an.cur == nil {
		os.Remove(filepath.Join(an.Dir, announcerFile))
		return
	}
	ln := an.st.Get()
	out := sharedAnnouncer{State: ln.State, Name: ln.Name, Port: an.cur.Port, Updated: an.now().Unix()}
	for _, p := range an.addrs() {
		out.Addrs = append(out.Addrs, p.String())
	}
	if err := writeShared(an.Dir, announcerFile, out); err != nil {
		an.log().Warn("discovery: announcer folder", "dir", an.Dir, "err", err)
	}
}

func (an *announcer) start(ctx context.Context, s sharedServer) {
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	c := Config{Info: Info{ID: s.ID, Name: s.Name, Version: s.Version, Port: s.Port, Scheme: s.Scheme},
		MDNS: true, Log: an.Log, Status: &an.st, Addrs: an.addrs}
	an.st.set(StateProbing, "") // what announcer.json says right after this step, before Run gets going
	go func() { defer close(done); an.run(rctx, c) }()
	if an.check != nil {
		go an.check(rctx, s)
	}
	an.cur, an.stop, an.done = &s, cancel, done
}

// halt stops what runs (its goodbye included) and waits for it.
func (an *announcer) halt() {
	if an.cur == nil {
		return
	}
	an.stop()
	<-an.done
	an.cur, an.stop, an.done = nil, nil, nil
	an.st.set(StateOff, "")
}

// checkPort warns when no server with this id answers on the advertised
// port of the host: the user changed the published port and not the
// announcer's (HTTP_PORT in compose.yml), and antresol.local would lead
// to the wrong port. It tries for a minute: the server writes server.json
// before it listens.
func (an *announcer) checkPort(ctx context.Context, s sharedServer) {
	cl := &http.Client{Timeout: 3 * time.Second}
	hosts := []string{"127.0.0.1"}
	for _, p := range an.addrs() {
		hosts = append(hosts, p.Addr().String())
	}
	for try := 0; try < 6; try++ {
		for _, h := range hosts {
			if healthID(ctx, cl, s.Scheme+"://"+net.JoinHostPort(h, strconv.Itoa(s.Port))+"/api/health") == s.ID {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
	an.log().Warn("discovery: the server does not answer on the advertised port of this host: set VAULT_ANNOUNCE_PORT "+
		"to the port published for it (HTTP_PORT in compose.yml)", "port", s.Port)
}

// healthID is the serverId GET /api/health at url gives ("" on failure).
func healthID(ctx context.Context, cl *http.Client, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := cl.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var h struct {
		ServerID string `json:"serverId"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&h) != nil {
		return ""
	}
	return h.ServerID
}
