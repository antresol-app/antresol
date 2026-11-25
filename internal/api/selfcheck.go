package api

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/go-chi/chi/v5"
)

// «Состояние»: what only the server can say about itself, one answer per
// check — its state and a code with a few figures. The words are the web
// app's (the catalogs), so the answer is the same in every language and
// holds nothing to leak: no key, no password, no address of the admin's
// beyond the host of the public address. The page adds what the other
// endpoints already tell it (the folders, the GPU, the addons, the
// network, the last scan, the versions).

// State of a check.
const (
	stateOK      = "ok"      // works
	stateWarn    = "warn"    // works, but worse than it could
	stateBad     = "bad"     // something that was set up does not work
	stateOff     = "off"     // not set up: never counted as a fault
	stateUnknown = "unknown" // could not be found out from here
)

// check is one row of the answer. ID names the check, Code the situation
// within it, Params the figures its sentence uses (strings and numbers).
type check struct {
	ID     string         `json:"id"`
	State  string         `json:"state"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
	// At: when an answer that is asked for on demand (the key of TMDB, the
	// mail) was got; absent for the checks made on every call.
	At *time.Time `json:"at,omitempty"`
}

// checkView is GET /api/admin/selfcheck.
type checkView struct {
	CheckedAt time.Time `json:"checkedAt"`
	Checks    []check   `json:"checks"`
}

const (
	// toolsTTL: how long ffmpeg's answer is kept (a «fresh» call asks again).
	toolsTTL = 10 * time.Minute
	// tmdbGap and mailGap: a test asked again sooner gets the last answer
	// (the mail's login is throttled by providers when it fails often).
	tmdbGap = 5 * time.Second
	mailGap = 20 * time.Second
	// testWait bounds a test of an outside service.
	testWait = 12 * time.Second
)

// selfCheck makes the checks. What a build does not have is nil, and its
// check is left out.
type selfCheck struct {
	network *auth.Network
	// public is the server's public address ("": none).
	public             func() string
	dataDir            string
	ffmpeg, ffprobe    string
	usage              func() (disk.Usage, error)
	tmdb               *tmdb.Client
	sources            func(context.Context) ([]iptv.Source, error)
	settings           func(context.Context) (AppSettings, error)
	anyKindle          func(context.Context) (bool, error)
	now                func() time.Time
	mu                 sync.Mutex
	tools              *check // ffmpeg's last answer
	toolsAt            time.Time
	tmdbTest, mailTest *testRun
}

// testRun is the last answer of an on-demand test.
type testRun struct {
	c   check
	at  time.Time
	sig string // what it was made for: a changed account is another test
}

// selfCheckNow is the clock of the checks (tests replace it).
var selfCheckNow = time.Now

// newSelfCheck takes what the checks need from the router's dependencies.
func newSelfCheck(deps Deps) *selfCheck {
	s := &selfCheck{network: deps.Network, dataDir: deps.DataDir, ffmpeg: deps.FFmpeg, ffprobe: deps.FFprobe,
		usage: deps.DiskUsage, tmdb: deps.TMDBClient, now: func() time.Time { return selfCheckNow() }}
	switch {
	case deps.PublicAddress != nil:
		s.public = deps.PublicAddress.Get
	case deps.PublicURL != "":
		s.public = func() string { return deps.PublicURL }
	}
	if deps.ExtIPTV != nil && deps.ExtIPTV.Store != nil {
		s.sources = deps.ExtIPTV.Store.Sources
	}
	if deps.DB != nil {
		defaults := deps.SettingsDefaults
		s.settings = func(ctx context.Context) (AppSettings, error) { return LoadAppSettings(ctx, deps.DB, defaults) }
		s.anyKindle = func(ctx context.Context) (bool, error) { return anyKindleAddress(ctx, deps.DB) }
	}
	return s
}

// anyKindleAddress: some profile has an address to send books to.
func anyKindleAddress(ctx context.Context, db *sql.DB) (bool, error) {
	var found bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE kindle_email != '')`).Scan(&found)
	return found, err
}

// mountSelfCheck: the admin's «Состояние» checks.
//
//   - GET /api/admin/selfcheck: every check the server can make (checkView);
//     ?fresh=1 asks ffmpeg again instead of the answer kept for ten
//     minutes; ?https=1, ?now= (the page's clock, ms) and ?tz= (its UTC
//     offset, minutes east) are what the server cannot know: whether the
//     page was opened over https, and its time and zone;
//   - POST /api/admin/selfcheck/tmdb: asks TMDB whether the key is
//     accepted; POST /api/admin/selfcheck/mail: signs in to the mailbox
//     (no letter is sent). Both answer one check, and keep it.
func mountSelfCheck(r chi.Router, deps Deps) {
	s := newSelfCheck(deps)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/admin/selfcheck", s.get)
		r.Post("/api/admin/selfcheck/tmdb", s.testTMDB)
		r.Post("/api/admin/selfcheck/mail", s.testMail)
	})
}

func (s *selfCheck) get(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	out := checkView{CheckedAt: s.now().UTC()}
	add := func(c *check) {
		if c != nil {
			out.Checks = append(out.Checks, *c)
		}
	}
	add(s.connection(r))
	add(s.secure(r, q.Get("https") == "1"))
	add(s.address())
	add(s.dataFolder())
	add(s.space())
	add(s.toolsCheck(ctx, q.Get("fresh") == "1"))
	add(s.tmdbCheck())
	add(s.clock(q.Get("now"), q.Get("tz")))
	add(s.tv(ctx))
	add(s.mailCheck(ctx))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *selfCheck) testTMDB(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.tmdb == nil {
		writeJSON(w, http.StatusOK, s.tmdbCheck())
		return
	}
	s.mu.Lock()
	last := s.tmdbTest
	s.mu.Unlock()
	if last != nil && s.now().Sub(last.at) < tmdbGap {
		writeJSON(w, http.StatusOK, last.check())
		return
	}
	writeJSON(w, http.StatusOK, s.keep(&s.tmdbTest, s.askTMDB(r.Context()), "").check())
}

func (s *selfCheck) testMail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	cfg, ok, err := s.mailAccount(ctx)
	if err != nil {
		internalErr(w, r, "selfcheck: mail settings", err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, s.mailCheck(ctx))
		return
	}
	sig := mailSig(cfg)
	s.mu.Lock()
	last := s.mailTest
	s.mu.Unlock()
	if last != nil && last.sig == sig && s.now().Sub(last.at) < mailGap {
		writeJSON(w, http.StatusOK, last.check())
		return
	}
	writeJSON(w, http.StatusOK, s.keep(&s.mailTest, s.askMail(ctx, cfg), sig).check())
}

// keep remembers an on-demand answer, stamped with now.
func (s *selfCheck) keep(slot **testRun, c check, sig string) *testRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	*slot = &testRun{c: c, at: s.now().UTC(), sig: sig}
	return *slot
}

// chk makes a check; params are name, value pairs.
func chk(id, state, code string, params ...any) *check {
	c := &check{ID: id, State: state, Code: code}
	for i := 0; i+1 < len(params); i += 2 {
		if c.Params == nil {
			c.Params = map[string]any{}
		}
		c.Params[params[i].(string)] = params[i+1]
	}
	return c
}
