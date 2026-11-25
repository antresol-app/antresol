package addons

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Status of an addon process.
type Status string

const (
	StatusStarting     Status = "starting"
	StatusRunning      Status = "running"
	StatusCrashed      Status = "crashed"
	StatusStopped      Status = "stopped"
	StatusIncompatible Status = "incompatible"
)

const (
	defaultHealth = 15 * time.Second
	defaultGrace  = 10 * time.Second
	defaultReady  = 2 * time.Second // /health polls of a started addon that is not ready yet
	maxBackoff    = 60              // in backoff units (1 s in production)
	resetAfter    = 5 * time.Minute
)

var (
	errIncompatible = userError("Расширение несовместимо с этой версией сервера")
	errStartFailed  = userError("Не удалось запустить расширение")
	errExited       = userError("Расширение завершилось при запуске")
	errNotHealthy   = userError("Расширение не ответило на проверку состояния")
	errStopped      = userError("Расширение остановлено")
	errShutdown     = userError("Сервер останавливается")
)

// Supervisor runs installed addons as child processes, each serving HTTP
// on its own Unix socket, and restarts the ones that crash.
type Supervisor struct {
	Dir     string        // $VAULT_DATA_DIR/addons
	RunDir  string        // sockets; empty: Dir/run, or a short temp dir if that path is too long
	CoreURL string        // e.g. http://127.0.0.1:8420
	Locale  string        // ANTRESOL_LOCALE
	Log     *log.Logger   // nil: log.Default()
	Health  time.Duration // health deadline per start, default 15s
	// Mint returns a fresh addon token; called on every (re)start.
	Mint func(id string) (string, error)
	// AcceptUnsigned lets the addons take a profile header the server did
	// not sign (VAULT_ADDONS_ACCEPT_UNSIGNED=on, an escape hatch). Off: an
	// addon on a current addonkit refuses one; an addon on an older
	// addonkit does not check signatures at all.
	AcceptUnsigned bool

	extraEnv    []string      // test hook: added to the child env
	backoffUnit time.Duration // test hook: restart backoff unit, default 1s
	stopGrace   time.Duration // test hook: SIGTERM→SIGKILL grace, default 10s
	readyPoll   time.Duration // test hook: readiness poll interval, default 2s

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
	seq     atomic.Uint64
	// onStatus hear every status change, in the order added (OnStatus)
	onStatus []func(id string, st Status)
}

// entry is the supervision state of one addon id.
type entry struct {
	id       string
	ring     logRing
	status   Status
	cur      *proc     // the healthy process serving Socket(id)
	in       Installed // what cur runs; restarts use it
	life     *life     // nil once stopped
	backoff  time.Duration
	loop     context.CancelFunc // pending restart loop
	loopDone chan struct{}
}

// life spans Start..Stop; cancelling it aborts starts and restarts.
type life struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // in-flight Start calls
}

type proc struct {
	cmd       *exec.Cmd
	sock      string
	done      chan struct{} // closed when the process has exited
	healthyAt time.Time
	ready     bool // its /health said ready (set before it becomes cur)
}

func (s *Supervisor) logger() *log.Logger {
	if s.Log != nil {
		return s.Log
	}
	return log.Default()
}

func (s *Supervisor) grace() time.Duration {
	if s.stopGrace > 0 {
		return s.stopGrace
	}
	return defaultGrace
}

func (s *Supervisor) pollEvery() time.Duration {
	if s.readyPoll > 0 {
		return s.readyPoll
	}
	return defaultReady
}

// running is the status of a current process: starting until it is ready.
func (p *proc) running() Status {
	if p.ready {
		return StatusRunning
	}
	return StatusStarting
}

func (s *Supervisor) unit() time.Duration {
	if s.backoffUnit > 0 {
		return s.backoffUnit
	}
	return time.Second
}

// Binary is the executable of an installed version.
func (s *Supervisor) Binary(id, version string) string {
	return filepath.Join(s.Dir, id, version, "bin", "addon")
}

// WebDir is the web bundle of an installed version.
func (s *Supervisor) WebDir(id, version string) string {
	return filepath.Join(s.Dir, id, version, "web")
}

// DataDir is the addon's own data directory, kept across versions.
func (s *Supervisor) DataDir(id string) string {
	return filepath.Join(s.Dir, id, "data")
}

// runDir picks the socket directory; Unix socket paths must stay short
// (104 bytes on macOS).
func (s *Supervisor) runDir(id string, n uint64) string {
	if s.RunDir != "" {
		return s.RunDir
	}
	dir := filepath.Join(s.Dir, "run")
	if len(filepath.Join(dir, sockName(id, n))) < 100 {
		return dir
	}
	return filepath.Join(os.TempDir(), "antresol-"+strconv.Itoa(os.Getuid()))
}

func sockName(id string, n uint64) string { return fmt.Sprintf("%s.%d.sock", id, n) }

// ensurePrivateDir creates dir 0700 and refuses one that is not ours.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s: not a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s: owned by uid %d", dir, st.Uid)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// nextBackoff: 1, 2, 4 … 60 units; back to 1 after resetAfter running.
func nextBackoff(prev, ran, unit time.Duration) time.Duration {
	if prev <= 0 || ran >= resetAfter {
		return unit
	}
	return min(prev*2, maxBackoff*unit)
}

// OnStatus adds f to hear every change of an addon's status (install,
// enable, disable, remove and crash all pass through one), in order; the
// listeners are called in the order they were added. f is called with the
// supervisor's lock held: it must return quickly and must not call the
// Supervisor.
func (s *Supervisor) OnStatus(f func(id string, st Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStatus = append(s.onStatus, f)
}

// setStatusLocked sets e's status and tells onStatus when it changed.
func (s *Supervisor) setStatusLocked(e *entry, st Status) {
	if e.status == st {
		return
	}
	e.status = st
	for _, f := range s.onStatus {
		f(e.id, st)
	}
}

func (s *Supervisor) entryLocked(id string) *entry {
	if s.entries == nil {
		s.entries = map[string]*entry{}
	}
	e := s.entries[id]
	if e == nil {
		e = &entry{id: id, status: StatusStopped}
		s.entries[id] = e
	}
	return e
}

// Start launches in.Version and returns once it answers GET /health with
// 200, or with an error. A process already running for in.ID keeps serving
// until the new one is healthy and is stopped then; if the new one fails,
// the old one stays. A failed start is not retried; only a process that
// was healthy and then exited is restarted, with backoff. A healthy
// process whose /health says {"ready": false} is current but stays
// StatusStarting, polled every 2 s (no deadline), until it says ready.
func (s *Supervisor) Start(ctx context.Context, in Installed) error {
	id := in.ID
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errShutdown
	}
	e := s.entryLocked(id)
	if in.Manifest.Contract != ContractVersion {
		if e.cur == nil {
			s.setStatusLocked(e, StatusIncompatible)
		}
		s.mu.Unlock()
		return errIncompatible
	}
	if e.life == nil {
		ctx, cancel := context.WithCancel(context.Background())
		e.life = &life{ctx: ctx, cancel: cancel}
	}
	l := e.life
	l.wg.Add(1)
	defer l.wg.Done()
	if e.cur == nil && e.loop == nil {
		s.setStatusLocked(e, StatusStarting)
	}
	s.mu.Unlock()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(l.ctx, cancel)()
	p, err := s.spawn(sctx, e, in)

	s.mu.Lock()
	if err != nil {
		if e.life == l && e.cur == nil && e.loop == nil {
			s.setStatusLocked(e, StatusCrashed)
		}
		s.mu.Unlock()
		if l.ctx.Err() != nil {
			return errStopped
		}
		return err
	}
	if e.life != l {
		s.mu.Unlock()
		s.terminate(p, time.Now().Add(s.grace()))
		return errStopped
	}
	old := e.cur
	e.cur, e.in = p, in
	s.setStatusLocked(e, p.running())
	loopDone := s.cancelLoopLocked(e)
	s.mu.Unlock()
	go s.watch(e, p)
	if !p.ready {
		go s.pollReady(e, p)
	}
	if loopDone != nil {
		<-loopDone
	}
	if old != nil {
		s.terminate(old, time.Now().Add(s.grace()))
	}
	return nil
}

// cancelLoopLocked stops a pending restart loop; wait on the result.
func (s *Supervisor) cancelLoopLocked(e *entry) chan struct{} {
	done := e.loopDone
	if e.loop != nil {
		e.loop()
	}
	e.loop, e.loopDone = nil, nil
	return done
}

// spawn starts one process and waits for it to become healthy. On error
// nothing is left running.
func (s *Supervisor) spawn(ctx context.Context, e *entry, in Installed) (*proc, error) {
	lg := s.logger()
	if s.Mint == nil {
		lg.Printf("addon %s: no token minter", e.id)
		return nil, errStartFailed
	}
	token, err := s.Mint(e.id)
	if err != nil {
		lg.Printf("addon %s: mint token: %v", e.id, err)
		return nil, errStartFailed
	}
	n := s.seq.Add(1)
	dir := s.runDir(e.id, n)
	if err := ensurePrivateDir(dir); err != nil {
		lg.Printf("addon %s: run dir: %v", e.id, err)
		return nil, errStartFailed
	}
	data := s.DataDir(e.id)
	if err := os.MkdirAll(data, 0o700); err != nil {
		lg.Printf("addon %s: data dir: %v", e.id, err)
		return nil, errStartFailed
	}
	sock := filepath.Join(dir, sockName(e.id, n))
	_ = os.Remove(sock) // stale file from an earlier server run

	emit := func(line string) {
		e.ring.add(line)
		lg.Printf("addon %s: %s", e.id, line)
	}
	stdout, stderr := &lineWriter{emit: emit}, &lineWriter{emit: emit}
	cmd := exec.Command(s.Binary(e.id, in.Version))
	cmd.Dir = data
	cmd.Env = s.childEnv(e.id, sock, token)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = time.Second // a grandchild holding the pipes must not block Wait
	if err := cmd.Start(); err != nil {
		lg.Printf("addon %s: start: %v", e.id, err)
		return nil, errStartFailed
	}
	p := &proc{cmd: cmd, sock: sock, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		stdout.flush()
		stderr.flush()
		lg.Printf("addon %s: process %d exited: %v", e.id, cmd.Process.Pid, exitText(err))
		close(p.done)
	}()

	ready, err := s.waitHealthy(ctx, p)
	if err != nil {
		lg.Printf("addon %s %s: %v", e.id, in.Version, err)
		s.terminate(p, time.Now().Add(2*time.Second))
		return nil, err
	}
	p.healthyAt, p.ready = time.Now(), ready
	if ready {
		lg.Printf("addon %s %s: running (pid %d)", e.id, in.Version, cmd.Process.Pid)
	} else {
		lg.Printf("addon %s %s: started, not ready yet (pid %d)", e.id, in.Version, cmd.Process.Pid)
	}
	return p, nil
}

func exitText(err error) string {
	if err == nil {
		return "status 0"
	}
	return err.Error()
}

// EnvPrefix is the prefix of the server variables an addon inherits:
// ANTRESOL_<ID>_, the id upper-cased with '-' → '_' ("my-addon" →
// "ANTRESOL_MY_ADDON_"). Ids hold no '_', so the mapping is one to one.
func EnvPrefix(id string) string {
	return "ANTRESOL_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_"
}

// inheritedEnv: the server variables an addon gets besides its own: what a
// process needs to run, find certificates, reach the network through a
// proxy, keep time and tune the Go runtime. Nothing else of the server's
// environment (a `.env` file's passwords, tokens and keys) reaches it.
var inheritedEnv = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "LD_LIBRARY_PATH": true,
	"TZ": true, "ZONEINFO": true, "LANG": true, "LANGUAGE": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true, "all_proxy": true,
	"GOMAXPROCS": true, "GOMEMLIMIT": true, "GOGC": true, "GODEBUG": true,
	"LIBVA_DRIVER_NAME": true,
}

// childEnv: the contract's variables, the addon's own ANTRESOL_<ID>_* and
// the server's inheritedEnv (and LC_*); the contract's variables are never
// taken from the server env.
func (s *Supervisor) childEnv(id, sock, token string) []string {
	acceptUnsigned := "off"
	if s.AcceptUnsigned {
		acceptUnsigned = "on"
	}
	contract := []string{
		"ANTRESOL_ADDON_ID=" + id,
		"ANTRESOL_ADDON_DATA_DIR=" + s.DataDir(id),
		"ANTRESOL_ADDON_SOCKET=" + sock,
		"ANTRESOL_CORE_URL=" + s.CoreURL,
		"ANTRESOL_ADDON_TOKEN=" + token,
		"ANTRESOL_ADDON_USER_KEY=" + UserKey(sock),
		"ANTRESOL_ADDON_ACCEPT_UNSIGNED=" + acceptUnsigned,
		"ANTRESOL_CONTRACT=" + strconv.Itoa(ContractVersion),
		"ANTRESOL_LOCALE=" + s.Locale,
	}
	reserved := map[string]bool{}
	for _, kv := range contract {
		k, _, _ := strings.Cut(kv, "=")
		reserved[k] = true
	}
	own := EnvPrefix(id)
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if reserved[k] {
			continue
		}
		if strings.HasPrefix(k, own) || inheritedEnv[k] || strings.HasPrefix(k, "LC_") {
			env = append(env, kv)
		}
	}
	env = append(env, contract...)
	return append(env, s.extraEnv...)
}

// unixClient talks HTTP to one Unix socket.
func unixClient(sock string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

// healthReady reads a 200 /health body: {"ready": false} is "up, not
// ready yet"; anything else (not JSON, no "ready", not a bool) is ready.
func healthReady(body []byte) bool {
	var h struct {
		Ready *bool `json:"ready"`
	}
	if json.Unmarshal(body, &h) != nil || h.Ready == nil {
		return true
	}
	return *h.Ready
}

// checkHealth asks GET /health once: ok is a 200 answer.
func checkHealth(ctx context.Context, client *http.Client) (ok, ready bool) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://addon/health", nil)
	resp, err := client.Do(req)
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return true, healthReady(b)
}

// waitHealthy waits for the first 200 /health; ready is what it said.
func (s *Supervisor) waitHealthy(ctx context.Context, p *proc) (ready bool, err error) {
	d := s.Health
	if d <= 0 {
		d = defaultHealth
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	client := unixClient(p.sock, time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if ok, ready := checkHealth(ctx, client); ok {
			return ready, nil
		}
		select {
		case <-p.done:
			return false, errExited
		case <-ctx.Done():
			return false, errNotHealthy
		case <-tick.C:
		}
	}
}

// pollReady asks /health of a current, not-ready process every pollEvery
// until it says ready (then it is running), exits, or stops being current.
// A failed poll is just tried again: an exit is watch's to handle.
func (s *Supervisor) pollReady(e *entry, p *proc) {
	client := unixClient(p.sock, time.Second)
	tick := time.NewTicker(s.pollEvery())
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-tick.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ok, ready := checkHealth(ctx, client)
		cancel()
		s.mu.Lock()
		if e.cur != p {
			s.mu.Unlock()
			return
		}
		if ok && ready {
			p.ready = true
			s.setStatusLocked(e, StatusRunning)
			s.mu.Unlock()
			s.logger().Printf("addon %s: ready (pid %d)", e.id, p.cmd.Process.Pid)
			return
		}
		s.mu.Unlock()
	}
}

// terminate sends SIGTERM to the process group, SIGKILL at deadline, and
// removes the socket.
func (s *Supervisor) terminate(p *proc, deadline time.Time) {
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-p.done:
	case <-t.C:
		s.logger().Printf("addon: process %d ignored SIGTERM, killing", pgid)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-p.done
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL) // leftovers of the group, if any
	_ = os.Remove(p.sock)
}

// watch restarts p if it exits while still the current process.
func (s *Supervisor) watch(e *entry, p *proc) {
	<-p.done
	// What the addon started dies with it, as in terminate. The leader is
	// reaped by now, so its pid could in principle be reused; the group id
	// stays taken while any member lives, and a reused pid becoming a new
	// group leader in this short window is the accepted residual risk.
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.cur != p {
		return // replaced or stopped
	}
	_ = os.Remove(p.sock)
	e.cur = nil
	s.setStatusLocked(e, StatusCrashed)
	e.backoff = nextBackoff(e.backoff, time.Since(p.healthyAt), s.unit())
	ctx, cancel := context.WithCancel(e.life.ctx)
	done := make(chan struct{})
	e.loop, e.loopDone = cancel, done
	s.logger().Printf("addon %s: crashed, restarting in %v", e.id, e.backoff)
	go s.restartLoop(ctx, e, e.in, done)
}

func (s *Supervisor) restartLoop(ctx context.Context, e *entry, in Installed, done chan struct{}) {
	defer close(done)
	for {
		s.mu.Lock()
		d := e.backoff
		s.mu.Unlock()
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		p, err := s.spawn(ctx, e, in)
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			if p != nil {
				s.terminate(p, time.Now().Add(s.grace()))
			}
			return
		}
		if err != nil {
			e.backoff = nextBackoff(e.backoff, 0, s.unit())
			s.logger().Printf("addon %s: restart failed, next try in %v", e.id, e.backoff)
			s.mu.Unlock()
			continue
		}
		e.cur = p
		s.setStatusLocked(e, p.running())
		e.loop, e.loopDone = nil, nil
		s.mu.Unlock()
		go s.watch(e, p)
		if !p.ready {
			go s.pollReady(e, p)
		}
		return
	}
}

// Stop stops the addon: SIGTERM, then SIGKILL after 10 s. It also cancels
// starts and restarts in progress. Stopping an unknown id is a no-op.
func (s *Supervisor) Stop(id string) error {
	s.stop(id, time.Now().Add(s.grace()))
	return nil
}

func (s *Supervisor) stop(id string, deadline time.Time) {
	s.mu.Lock()
	e := s.entries[id]
	if e == nil {
		s.mu.Unlock()
		return
	}
	l := e.life
	e.life = nil
	if l != nil {
		l.cancel()
	}
	p := e.cur
	e.cur = nil
	s.setStatusLocked(e, StatusStopped)
	e.backoff = 0
	loopDone := s.cancelLoopLocked(e)
	s.mu.Unlock()
	if loopDone != nil {
		<-loopDone
	}
	if l != nil {
		l.wg.Wait()
	}
	if p != nil {
		s.terminate(p, deadline)
	}
}

// StopAll stops every addon in parallel within one 10 s budget and refuses
// later starts. Called on server shutdown.
func (s *Supervisor) StopAll() {
	deadline := time.Now().Add(s.grace())
	s.mu.Lock()
	s.closed = true
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() { s.stop(id, deadline) })
	}
	wg.Wait()
}

// Status of the addon; unknown ids are stopped.
func (s *Supervisor) Status(id string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id]; e != nil {
		return e.status
	}
	return StatusStopped
}

// Socket is the Unix socket of the healthy process serving id, or "".
func (s *Supervisor) Socket(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id]; e != nil && e.cur != nil {
		return e.cur.sock
	}
	return ""
}

// Serving is the addon's status and, only while it is running, the socket
// of its process (else ""), read together: the proxy never gets the socket
// of an addon that is still starting or has just stopped.
func (s *Supervisor) Serving(id string) (Status, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	if e == nil {
		return StatusStopped, ""
	}
	if e.status != StatusRunning || e.cur == nil {
		return e.status, ""
	}
	return e.status, e.cur.sock
}

// LogTail returns the newest n output lines of the addon (n <= 0: all
// kept, up to 500).
func (s *Supervisor) LogTail(id string, n int) []string {
	s.mu.Lock()
	e := s.entries[id]
	s.mu.Unlock()
	if e == nil {
		return nil
	}
	return e.ring.tail(n)
}
