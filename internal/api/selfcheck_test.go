package api

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// scAnswer is GET /api/admin/selfcheck as a test reads it.
type scAnswer struct {
	CheckedAt time.Time `json:"checkedAt"`
	Checks    []check   `json:"checks"`
}

func (a scAnswer) of(id string) *check {
	for i := range a.Checks {
		if a.Checks[i].ID == id {
			return &a.Checks[i]
		}
	}
	return nil
}

// want checks a check's state and code.
func want(t *testing.T, name string, c *check, state, code string) {
	t.Helper()
	if c == nil {
		t.Errorf("%s: no check", name)
		return
	}
	if c.State != state || c.Code != code {
		t.Errorf("%s: got %s/%s %v, want %s/%s", name, c.State, c.Code, c.Params, state, code)
	}
}

func wantParam(t *testing.T, name string, c *check, key string, value any) {
	t.Helper()
	if c == nil || fmt.Sprint(c.Params[key]) != fmt.Sprint(value) {
		t.Errorf("%s: param %s = %v, want %v (%+v)", name, key, c.Params[key], value, c)
	}
}

// scReq is a request as a proxy or a browser would make it.
func scReq(peer, host string, hdr ...string) *http.Request {
	r := httptest.NewRequest("GET", "/api/admin/selfcheck", nil)
	r.RemoteAddr, r.Host = peer, host
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return r
}

func newSC(t *testing.T) *selfCheck {
	t.Helper()
	now := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)
	t.Setenv("TZ", "")
	return &selfCheck{now: func() time.Time { return now }}
}

// useClock puts a clock under the router's checks for the test.
func useClock(t *testing.T) *testClock {
	c := &testClock{t: time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)}
	old := selfCheckNow
	selfCheckNow = c.now
	t.Cleanup(func() { selfCheckNow = old })
	return c
}

// The connection: at home, or the reason the server cannot say so.
func TestSelfCheckConnection(t *testing.T) {
	s := newSC(t)
	s.network = &auth.Network{
		Proxies: []netip.Prefix{netip.MustParsePrefix("172.18.0.1/32")},
		Hosts:   &auth.HostPolicy{Public: func() string { return "https://media.example.org" }},
	}
	want(t, "home", s.connection(scReq("192.168.1.20:1", "antresol.local:8420")), "ok", "home")
	want(t, "through a listed proxy from home", s.connection(scReq("172.18.0.1:1", "media.example.org", "X-Forwarded-For", "192.168.1.20")), "ok", "home")
	want(t, "from far away", s.connection(scReq("172.18.0.1:1", "media.example.org", "X-Forwarded-For", "93.184.216.34")), "ok", "away")

	c := s.connection(scReq("172.19.0.9:1", "media.example.org", "X-Forwarded-For", "192.168.1.20"))
	want(t, "an unlisted proxy", c, "warn", "proxy")
	wantParam(t, "an unlisted proxy", c, "peer", "172.19.0.9")
	c = s.connection(scReq("172.18.0.1:1", "media.example.org"))
	want(t, "a proxy that forwards nothing", c, "warn", "bare")
	wantParam(t, "a proxy that forwards nothing", c, "peer", "172.18.0.1")
	c = s.connection(scReq("192.168.1.20:1", "Other.Example.com:8420"))
	want(t, "a foreign name", c, "warn", "host")
	wantParam(t, "a foreign name", c, "host", "other.example.com")
}

func TestSelfCheckHTTPS(t *testing.T) {
	s := newSC(t)
	s.network = &auth.Network{Proxies: []netip.Prefix{netip.MustParsePrefix("172.18.0.1/32")}}
	want(t, "tls", func() *check {
		r := scReq("192.168.1.20:1", "antresol.local")
		r.TLS = &tls.ConnectionState{}
		return s.secure(r, true)
	}(), "ok", "secure")
	want(t, "a trusted proxy says https", s.secure(scReq("172.18.0.1:1", "media.example.org", "X-Forwarded-Proto", "https"), true), "ok", "secure")
	// the page is https, the server takes it for plain: the proxy's header is not believed
	want(t, "an unlisted proxy", s.secure(scReq("172.19.0.9:1", "media.example.org", "X-Forwarded-Proto", "https"), true), "warn", "lost")
	want(t, "no header", s.secure(scReq("172.18.0.1:1", "media.example.org"), true), "warn", "lost")
	// plain http: fine at home (an address, a home name), not through a public name
	want(t, "an address", s.secure(scReq("192.168.1.20:1", "192.168.1.5:8420"), false), "ok", "plainHome")
	want(t, "a home name", s.secure(scReq("192.168.1.20:1", "antresol.local"), false), "ok", "plainHome")
	c := s.secure(scReq("172.18.0.1:1", "Media.Example.org"), false)
	want(t, "a public name", c, "warn", "plainPublic")
	wantParam(t, "a public name", c, "host", "media.example.org")
}

func TestSelfCheckAddress(t *testing.T) {
	s := newSC(t)
	want(t, "none wired", s.address(), "off", "unset")
	for addr, want_ := range map[string][3]string{
		"":                           {"off", "unset", ""},
		"  ":                         {"off", "unset", ""},
		"https://media.example.org":  {"ok", "set", "media.example.org"},
		"https://media.example.org/": {"ok", "set", "media.example.org"},
		"http://media.example.org":   {"warn", "plain", "media.example.org"},
		"http://192.168.1.5:8420":    {"ok", "set", "192.168.1.5"},
		"http://antresol.local":      {"ok", "set", "antresol.local"},
		"media.example.org":          {"warn", "invalid", ""},
	} {
		s.public = func() string { return addr }
		c := s.address()
		want(t, "address "+addr, c, want_[0], want_[1])
		if want_[2] != "" {
			wantParam(t, "address "+addr, c, "host", want_[2])
		}
	}
}

func TestSelfCheckDataFolder(t *testing.T) {
	s := newSC(t)
	if s.dataFolder() != nil {
		t.Error("no data folder, a check anyway")
	}
	free := int64(50) << 30
	old := dataStat
	t.Cleanup(func() { dataStat = old })
	dataStat = func(string) (int64, int64, error) { return 100<<30 - free, 100 << 30, nil }

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s.dataDir = dir
	c := s.dataFolder()
	want(t, "fine", c, "ok", "ok")
	wantParam(t, "fine", c, "free", free)

	free = 1 << 30
	want(t, "little room", s.dataFolder(), "warn", "low")
	free = 100 << 20
	want(t, "no room", s.dataFolder(), "bad", "full")
	free = 50 << 30
	dataStat = func(string) (int64, int64, error) { return 0, 0, errors.New("statfs") }
	want(t, "size unknown", s.dataFolder(), "ok", "ok")
	dataStat = func(string) (int64, int64, error) { return 100<<30 - free, 100 << 30, nil }

	// open to others
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c = s.dataFolder()
	want(t, "open", c, "warn", "open")
	wantParam(t, "open", c, "mode", "0755")
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	want(t, "group only", s.dataFolder(), "ok", "ok")

	// not writable: no such folder (anything else), then a read-only one
	s.dataDir = dir + "/nowhere"
	want(t, "missing", s.dataFolder(), "bad", "other")
	if os.Geteuid() != 0 {
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
		s.dataDir = dir
		c = s.dataFolder()
		want(t, "read-only", c, "bad", "denied")
		wantParam(t, "read-only", c, "path", dir)
	}
}

func TestSelfCheckSpace(t *testing.T) {
	s := newSC(t)
	if s.space() != nil {
		t.Error("no disk monitor, a check anyway")
	}
	var u disk.Usage
	var err error
	s.usage = func() (disk.Usage, error) { return u, err }
	use := func(total, free int64) disk.Usage {
		used := total - free
		return disk.Usage{Used: used, Total: total, Level: disk.LevelOf(used, total)}
	}
	tb := int64(1) << 40
	for name, c := range map[string]struct {
		u           disk.Usage
		state, code string
	}{
		// 88 % of 3.6 TB leaves 430 GB: the percent alone would say «warn»
		"a big disk, 88 %":      {use(36*tb/10, 43*tb/100), "ok", "ok"},
		"a big disk, 96 %":      {use(36*tb/10, 14*tb/100), "warn", "low"}, // 147 GB: past 95 %, still room
		"a big disk, 99 %":      {use(36*tb/10, 3*tb/100), "bad", "full"},
		"a small disk, 88 %":    {use(120<<30, 14<<30), "warn", "low"},
		"a small disk, 97 %":    {use(120<<30, 3<<30), "bad", "full"},
		"12 GB left of 2 TB":    {use(2*tb, 12<<30), "bad", "full"}, // 99 %, and little room
		"15 GB left of 100 GB":  {use(100<<30, 15<<30), "warn", "low"},
		"a disk with 3 GB left": {use(2*tb, 3<<30), "bad", "full"},
		"plenty":                {use(2*tb, tb), "ok", "ok"},
	} {
		u = c.u
		want(t, name, s.space(), c.state, c.code)
	}
	u = disk.Usage{Level: disk.LevelOK, Offline: "gone"}
	want(t, "offline", s.space(), "unknown", "offline")
	err = errors.New("statfs")
	want(t, "unreadable", s.space(), "unknown", "unreadable")
	err = nil
	u = use(2*tb, tb)
	c := s.space()
	wantParam(t, "figures", c, "free", tb)
	wantParam(t, "figures", c, "percent", 50)
}

func TestSelfCheckFFmpeg(t *testing.T) {
	s := newSC(t)
	var calls []string
	answers := map[string]struct {
		line string
		err  error
	}{}
	old := toolVersion
	t.Cleanup(func() { toolVersion = old })
	toolVersion = func(_ context.Context, bin string) (string, error) {
		calls = append(calls, bin)
		a := answers[bin]
		return a.line, a.err
	}
	set := func(ff, fp string, ffErr, fpErr error) {
		answers["ffmpeg"] = struct {
			line string
			err  error
		}{ff, ffErr}
		answers["ffprobe"] = struct {
			line string
			err  error
		}{fp, fpErr}
	}
	ctx := context.Background()

	set("ffmpeg version 7.1 Copyright (c) 2000-2024", "ffprobe version 7.1", nil, nil)
	c := s.toolsCheck(ctx, true)
	want(t, "7.1", c, "ok", "ok")
	wantParam(t, "7.1", c, "version", "7.1")
	for line, version := range map[string]string{
		"ffmpeg version 6.1.1-3ubuntu5 Copyright":       "6.1",
		"ffmpeg version n6.1.2 Copyright (c)":           "6.1",
		"ffmpeg version 8.0-static https://x Copyright": "8.0",
	} {
		set(line, "ffprobe version 6.1", nil, nil)
		c = s.toolsCheck(ctx, true)
		want(t, line, c, "ok", "ok")
		wantParam(t, line, c, "version", version)
	}
	set("ffmpeg version 6.0.1 Copyright", "ffprobe version 6.0.1", nil, nil)
	c = s.toolsCheck(ctx, true)
	want(t, "6.0", c, "warn", "old")
	wantParam(t, "6.0", c, "version", "6.0")
	set("ffmpeg version 4.4.2 Copyright", "x", nil, nil)
	want(t, "4.4", s.toolsCheck(ctx, true), "warn", "old")
	// a build of git's tree has no number: it is taken as it is
	set("ffmpeg version N-118000-g1234567 Copyright", "x", nil, nil)
	c = s.toolsCheck(ctx, true)
	want(t, "git build", c, "ok", "ok")
	if _, has := c.Params["version"]; has {
		t.Errorf("a version made up: %v", c.Params)
	}

	// missing: either of the two, named
	set("", "", fmt.Errorf("exec: %w", exec.ErrNotFound), nil)
	c = s.toolsCheck(ctx, true)
	want(t, "no ffmpeg", c, "bad", "missing")
	wantParam(t, "no ffmpeg", c, "tool", "ffmpeg")
	set("ffmpeg version 7.1", "", nil, &os.PathError{Op: "fork/exec", Path: "ffprobe", Err: os.ErrNotExist})
	c = s.toolsCheck(ctx, true)
	want(t, "no ffprobe", c, "bad", "missing")
	wantParam(t, "no ffprobe", c, "tool", "ffprobe")
	set("", "", errors.New("exit status 127"), nil)
	want(t, "broken", s.toolsCheck(ctx, true), "bad", "broken")

	// the answer is kept for ten minutes; «fresh» and time end it
	set("ffmpeg version 7.1", "ffprobe version 7.1", nil, nil)
	s.toolsCheck(ctx, true)
	calls = nil
	s.toolsCheck(ctx, false)
	s.toolsCheck(ctx, false)
	if len(calls) != 0 {
		t.Errorf("asked again within the ttl: %v", calls)
	}
	s.toolsCheck(ctx, true)
	if len(calls) != 2 {
		t.Errorf("fresh did not ask again: %v", calls)
	}
	later := s.now().Add(toolsTTL + time.Second)
	s.now = func() time.Time { return later }
	calls = nil
	s.toolsCheck(ctx, false)
	if len(calls) != 2 {
		t.Errorf("an old answer was kept: %v", calls)
	}
	// the configured paths are the ones run
	s.ffmpeg, s.ffprobe = "/opt/ff/ffmpeg", "/opt/ff/ffprobe"
	calls = nil
	s.toolsCheck(ctx, true)
	if strings.Join(calls, ",") != "/opt/ff/ffmpeg,/opt/ff/ffprobe" {
		t.Errorf("ran %v", calls)
	}
}

func TestSelfCheckClock(t *testing.T) {
	s := newSC(t) // 14:00:00 UTC, TZ unset
	ms := func(d time.Duration) string { return fmt.Sprint(s.now().Add(d).UnixMilli()) }
	if s.clock("", "0") != nil || s.clock("soon", "0") != nil {
		t.Error("a check without the page's clock")
	}
	c := s.clock(ms(0), "0")
	want(t, "same", c, "ok", "ok")
	wantParam(t, "same", c, "zone", "UTC")
	want(t, "a minute off", s.clock(ms(-90*time.Second), "0"), "ok", "ok")
	c = s.clock(ms(-5*time.Minute), "0")
	want(t, "server ahead", c, "warn", "ahead")
	wantParam(t, "server ahead", c, "minutes", 5)
	c = s.clock(ms(7*time.Minute+30*time.Second), "0")
	want(t, "server behind", c, "warn", "behind")
	wantParam(t, "server behind", c, "minutes", 7)
	// the server lives in UTC, the page does not
	c = s.clock(ms(0), "60")
	want(t, "utc", c, "warn", "utc")
	wantParam(t, "utc", c, "browser", "+1:00")
	wantParam(t, "utc west", s.clock(ms(0), "-330"), "browser", "-5:30")
	want(t, "no zone given", s.clock(ms(0), ""), "ok", "ok")
	// a time wrong by hours is the clock's fault, said first
	want(t, "skew first", s.clock(ms(-3*time.Hour), "60"), "warn", "ahead")

	// a server in a zone of its own: not UTC, not a fault, whatever the page's
	t.Setenv("TZ", "Europe/Berlin")
	berlin := time.FixedZone("CET", 3600)
	s.now = func() time.Time { return time.Date(2026, 11, 25, 15, 0, 0, 0, berlin) }
	c = s.clock(fmt.Sprint(s.now().UnixMilli()), "0")
	want(t, "berlin", c, "ok", "ok")
	wantParam(t, "berlin", c, "zone", "Europe/Berlin")
	// TZ=UTC set on purpose is the same as none
	t.Setenv("TZ", "Etc/UTC")
	s.now = func() time.Time { return time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC) }
	want(t, "Etc/UTC", s.clock(fmt.Sprint(s.now().UnixMilli()), "120"), "warn", "utc")
}

func TestSelfCheckTV(t *testing.T) {
	s := newSC(t)
	if s.tv(context.Background()) != nil {
		t.Error("no feature, a check anyway")
	}
	var srcs []iptv.Source
	var err error
	s.sources = func(context.Context) ([]iptv.Source, error) { return srcs, err }
	ctx := context.Background()
	if s.tv(ctx) != nil {
		t.Error("no sources, a check anyway")
	}
	day := 24 * time.Hour
	ok := iptv.Source{Name: "Альфа", RefreshHours: 24, LastOK: s.now().Add(-6 * time.Hour), LastTry: s.now().Add(-6 * time.Hour)}
	srcs = []iptv.Source{ok, {Name: "Бета"}}
	c := s.tv(ctx)
	want(t, "fine (a source never tried is not a fault)", c, "ok", "ok")
	wantParam(t, "fine", c, "count", 2)

	srcs = []iptv.Source{ok, {Name: "Бета", LastErr: "HTTP 403", LastTry: s.now()}, {Name: "Гамма", LastErr: "плейлист пуст"}}
	c = s.tv(ctx)
	want(t, "failing", c, "warn", "failing")
	wantParam(t, "failing", c, "name", "Бета")
	wantParam(t, "failing", c, "error", "HTTP 403")
	wantParam(t, "failing", c, "count", 2)

	srcs = []iptv.Source{ok, {Name: "Дельта", PendingDrop: 40}}
	c = s.tv(ctx)
	want(t, "waiting for a decision", c, "warn", "pending")
	wantParam(t, "waiting for a decision", c, "name", "Дельта")

	// refreshed every day, last refreshed four days ago (three missed)
	srcs = []iptv.Source{{Name: "Эпсилон", RefreshHours: 24, LastOK: s.now().Add(-4 * day)}}
	want(t, "stale", s.tv(ctx), "warn", "stale")
	srcs = []iptv.Source{{Name: "Эпсилон", RefreshHours: 24, LastOK: s.now().Add(-2 * day)}}
	want(t, "a day late", s.tv(ctx), "ok", "ok")
	srcs = []iptv.Source{{Name: "Зета", RefreshHours: 0, LastOK: s.now().Add(-40 * day)}}
	want(t, "refreshing off", s.tv(ctx), "ok", "ok")

	err = errors.New("db")
	want(t, "unreadable", s.tv(ctx), "unknown", "unreadable")
}

func TestSelfCheckTMDB(t *testing.T) {
	var asked atomic.Int32
	var status atomic.Int32
	status.Store(200)
	var gotKey atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		gotKey.Store(r.URL.Query().Get("api_key") + r.Header.Get("Authorization"))
		if r.URL.Path != "/3/configuration" {
			t.Errorf("asked %s", r.URL.Path)
		}
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"images":{}}`)
	}))
	defer ts.Close()

	clock := useClock(t)
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		d.TMDBClient = &tmdb.Client{Key: "k3y-of-tmdb", BaseURL: ts.URL + "/3"}
	})
	get := func() *check { return getSelfCheck(t, e, "").of("tmdb") }
	post := func() check {
		w := e.do("POST", "/api/admin/selfcheck/tmdb", "", e.admin)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var c check
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), "k3y-of-tmdb") {
			t.Errorf("the key is in the answer: %s", w.Body)
		}
		return c
	}
	// a key is set: no call is made until asked
	want(t, "set", get(), "ok", "set")
	if asked.Load() != 0 {
		t.Fatal("TMDB asked on a plain check")
	}
	c := post()
	want(t, "accepted", &c, "ok", "accepted")
	if c.At == nil {
		t.Error("no time on a test")
	}
	if k, _ := gotKey.Load().(string); !strings.Contains(k, "k3y-of-tmdb") {
		t.Errorf("the key was not sent: %q", k)
	}
	// kept: the page's next check says so, and a second press within the gap asks nobody
	g := get()
	want(t, "kept", g, "ok", "accepted")
	if g.At == nil {
		t.Error("kept answer without its time")
	}
	post()
	if asked.Load() != 1 {
		t.Errorf("TMDB asked %d times, want 1", asked.Load())
	}

	// a refused key, an ailing TMDB, an unreachable one (each after the gap)
	for _, c := range []struct {
		code         int
		state, code2 string
	}{{401, "bad", "rejected"}, {404, "warn", "failed"}, {200, "ok", "accepted"}} {
		clock.add(time.Minute)
		status.Store(int32(c.code))
		got := post()
		if got.State != c.state || got.Code != c.code2 {
			t.Errorf("status %d: %+v", c.code, got)
		}
	}
	clock.add(time.Minute)
	ts.Close()
	gone := post()
	want(t, "unreachable", &gone, "unknown", "unreachable")
	if strings.Contains(fmt.Sprint(gone.Params), "k3y") {
		t.Error("the key in the params")
	}
}

func TestSelfCheckTMDBWithoutAKey(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	want(t, "no key", getSelfCheck(t, e, "").of("tmdb"), "warn", "unset")
	w := e.do("POST", "/api/admin/selfcheck/tmdb", "", e.admin)
	var c check
	json.Unmarshal(w.Body.Bytes(), &c)
	want(t, "no key, tested", &c, "warn", "unset")
}

// the page's own facts reach the server by the query
func getSelfCheck(t *testing.T, e *libEnv, query string, hdr ...string) scAnswer {
	t.Helper()
	path := "/api/admin/selfcheck"
	if query != "" {
		path += "?" + query
	}
	w := e.do("GET", path, "", e.admin, hdr...)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var a scAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSelfCheckMail(t *testing.T) {
	var probes atomic.Int32
	var outcome atomic.Pointer[error]
	setOutcome := func(err error) { outcome.Store(&err) }
	setOutcome(nil)
	var seen mailer.Config
	old := mailProbe
	t.Cleanup(func() { mailProbe = old })
	mailProbe = func(_ context.Context, c mailer.Config) error {
		probes.Add(1)
		seen = c
		return *outcome.Load()
	}
	clock := useClock(t)
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	get := func() *check { return getSelfCheck(t, e, "").of("mail") }
	post := func() (check, string) {
		w := e.do("POST", "/api/admin/selfcheck/mail", "", e.admin)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var c check
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c, w.Body.String()
	}

	// nothing set, nobody needs it: not a fault; asking does not dial
	want(t, "unset", get(), "off", "unset")
	c, _ := post()
	want(t, "unset, tested", &c, "off", "unset")
	if probes.Load() != 0 {
		t.Fatal("the mailbox was dialled with no account")
	}
	// a profile with a Kindle address and no mailbox
	if _, err := e.db.Exec(`UPDATE users SET kindle_email='me@kindle.com' WHERE is_admin=1`); err != nil {
		t.Fatal(err)
	}
	want(t, "a Kindle address, no mailbox", get(), "warn", "unsetKindle")

	e.configureMail() // password «abcd efgh»
	g := get()
	want(t, "set", g, "ok", "set")
	wantParam(t, "set", g, "host", "smtp.example.com")
	if probes.Load() != 0 {
		t.Fatal("the mailbox was dialled on a plain check")
	}

	c, body := post()
	want(t, "works", &c, "ok", "works")
	if c.At == nil || probes.Load() != 1 {
		t.Errorf("at %v, probes %d", c.At, probes.Load())
	}
	if seen.Host != "smtp.example.com" || seen.Username != "me@example.com" || seen.Password != "abcd efgh" {
		t.Errorf("probed %+v", seen)
	}
	if strings.Contains(body, "abcd") || strings.Contains(body, "me@example.com") {
		t.Errorf("the account is in the answer: %s", body)
	}
	want(t, "kept", get(), "ok", "works")
	// pressed again at once: the last answer, no second login
	post()
	if probes.Load() != 1 {
		t.Errorf("logged in %d times", probes.Load())
	}

	// a changed account is another test: the old answer is not its answer
	e.do("PUT", "/api/settings", `{"quality":1080,"voice":"original","voices":["original"],"streamCacheGB":10,"mail":{"host":"smtp.example.com","port":587,"username":"me@example.com","password":"another","from":"me@example.com"}}`, e.admin)
	want(t, "a new password", get(), "ok", "set")

	// what went wrong, said without the server's words
	for name, c := range map[string]struct {
		err         error
		state, code string
	}{
		"login":       {fmt.Errorf("%w: 535 5.7.8 Username and Password not accepted", mailer.ErrAuth), "bad", "auth"},
		"no TLS":      {mailer.ErrNoTLS, "bad", "noTLS"},
		"certificate": {&tls.CertificateVerificationError{Err: errors.New("x509: certificate is not valid")}, "bad", "tls"},
		"refused":     {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, "bad", "unreachable"},
		"timeout":     {context.DeadlineExceeded, "bad", "unreachable"},
		"other":       {errors.New("421 try later"), "warn", "failed"},
	} {
		clock.add(time.Minute)
		setOutcome(c.err)
		got, body := post()
		want(t, name, &got, c.state, c.code)
		if strings.Contains(body, "Username and Password") || strings.Contains(body, "try later") || strings.Contains(body, "another") {
			t.Errorf("%s: the server's words or the password in the answer: %s", name, body)
		}
		wantParam(t, name, &got, "host", "smtp.example.com")
	}
}

// The route: admins only, the answer holds no secret and says what is wrong
// with the setup (a proxy nobody listed, a missing ffmpeg).
func TestSelfCheckRoute(t *testing.T) {
	old := toolVersion
	t.Cleanup(func() { toolVersion = old })
	toolVersion = func(_ context.Context, bin string) (string, error) {
		return "", &os.PathError{Op: "fork/exec", Path: bin, Err: os.ErrNotExist}
	}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.DB = db
		if err := os.Chmod(d.DataDir, 0o700); err != nil {
			t.Fatal(err)
		}
		d.Network = &auth.Network{Proxies: []netip.Prefix{netip.MustParsePrefix("172.18.0.1/32")}}
		d.PublicURL = "https://media.example.org"
		d.TMDBClient = &tmdb.Client{Key: "k3y-of-tmdb", BaseURL: "http://127.0.0.1:1/3"}
		d.ExtIPTV = &iptv.Service{Store: &iptv.Store{DB: db}}
		d.DiskUsage = func() (disk.Usage, error) {
			return disk.Usage{Used: 50 << 30, Total: 100 << 30, Level: disk.LevelOK}, nil
		}
	})
	e.configureMail()
	if _, err := (&iptv.Store{DB: e.db}).SaveSource(context.Background(), iptv.Source{Name: "Источник", LastErr: "HTTP 403", URLEnc: []byte("https://user:s3cr3t-token@tv.example.org/list.m3u")}); err != nil {
		t.Fatal(err)
	}

	if w := e.do("GET", "/api/admin/selfcheck", "", e.login(`{"name":"Мама"}`)); w.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d", w.Code)
	}
	if w := e.do("GET", "/api/admin/selfcheck", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", w.Code)
	}
	for _, p := range []string{"tmdb", "mail"} {
		if w := e.do("POST", "/api/admin/selfcheck/"+p, "", e.login(`{"name":"Папа`+p+`"}`)); w.Code != http.StatusForbidden {
			t.Errorf("non-admin %s: %d", p, w.Code)
		}
		if w := e.do("POST", "/api/admin/selfcheck/"+p, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: %d", p, w.Code)
		}
	}

	w := e.do("GET", "/api/admin/selfcheck?https=1&now="+fmt.Sprint(time.Now().UnixMilli())+"&tz=0", "", e.admin,
		"X-Forwarded-For", "192.168.1.20", "X-Forwarded-Proto", "https")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	var a scAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	// the test request comes from 192.168.1.20 and says it was forwarded: a proxy nobody listed
	want(t, "connection", a.of("connection"), "warn", "proxy")
	want(t, "https", a.of("https"), "warn", "lost")
	want(t, "address", a.of("address"), "ok", "set")
	want(t, "data", a.of("data"), "ok", "ok")
	want(t, "space", a.of("space"), "ok", "ok")
	want(t, "ffmpeg", a.of("ffmpeg"), "bad", "missing")
	want(t, "tmdb", a.of("tmdb"), "ok", "set")
	want(t, "clock", a.of("clock"), "ok", "ok")
	want(t, "tv", a.of("tv"), "warn", "failing")
	want(t, "mail", a.of("mail"), "ok", "set")
	if a.CheckedAt.IsZero() {
		t.Error("no time of the check")
	}
	seen := map[string]bool{}
	for _, c := range a.Checks {
		if seen[c.ID] {
			t.Errorf("%s twice", c.ID)
		}
		seen[c.ID] = true
		if c.At != nil {
			t.Errorf("%s: a time on a check nobody asked for", c.ID)
		}
	}
	// no secret anywhere in it: the TMDB key, the mail password and its login, a source's address
	for _, secret := range []string{"k3y-of-tmdb", "abcd", "efgh", "me@example.com", "s3cr3t-token", "tv.example.org", "user:"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("%q is in the answer: %s", secret, w.Body)
		}
	}

	// without the page's facts the page-dependent checks say what the server alone can
	a = getSelfCheck(t, e, "")
	if a.of("clock") != nil {
		t.Error("a clock check without the page's clock")
	}
	want(t, "https, plain page", a.of("https"), "warn", "plainPublic") // the test request names example.com
}
