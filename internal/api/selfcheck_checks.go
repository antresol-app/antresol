package api

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/mediacheck"
	"github.com/antresol-app/antresol/internal/netguard"
)

const (
	gib = int64(1) << 30
	mib = int64(1) << 20
)

// The figures the checks judge by.
const (
	// dataLow and dataFull: free bytes of the settings folder's disk (SQLite
	// on a full disk corrupts the database).
	dataLow  = 2 * gib
	dataFull = 256 * mib
	// A media disk is judged by what is free as well as by its percent: 88 %
	// of 3.6 TB is fine, 88 % of 120 GB is not.
	spaceFull       = 5 * gib   // below it: bad
	spaceLow        = 20 * gib  // below it: warn
	spaceBigFull    = 100 * gib // «full» (95 %) is bad only below it
	spaceBigWarning = 200 * gib // «warn» (85 %) is a warning only below it
	// clockSkew: how far the clocks may part before it is said.
	clockSkew = 2 * time.Minute
	// ffmpegMajor and ffmpegMinor: the oldest ffmpeg the server's filters are written for (6.1).
	ffmpegMajor, ffmpegMinor = 6, 1
)

// reqHost is the name the request used for the server, without its port.
func reqHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
}

// hostIsLocal: an address, or a name that only a home network can have.
func hostIsLocal(h string) bool {
	_, err := netip.ParseAddr(h)
	return err == nil || netguard.LANSpelling(h)
}

// connection: how the server sees this request, at home or not and, when it
// cannot tell, why (a proxy it was not told about, a foreign name).
func (s *selfCheck) connection(r *http.Request) *check {
	d := s.network.Diagnose(r)
	switch {
	case d.Home:
		return chk("connection", stateOK, "home")
	case d.Reason == auth.ReasonProxy:
		return chk("connection", stateWarn, "proxy", "peer", d.Peer)
	case d.Reason == auth.ReasonBare:
		return chk("connection", stateWarn, "bare", "peer", d.Peer)
	case d.Reason == auth.ReasonHost:
		return chk("connection", stateWarn, "host", "host", d.Host)
	}
	// from outside: nothing wrong with that, the admin may be away
	return chk("connection", stateOK, "away")
}

// secure: whether the server takes the connection for https. A page opened
// over https that the server takes for plain means the proxy does not say
// so (X-Forwarded-Proto) or is not one the server trusts. Plain http at
// home is fine; through a public name it is open to anyone on the way.
func (s *selfCheck) secure(r *http.Request, pageHTTPS bool) *check {
	switch {
	case s.network.Secure(r):
		return chk("https", stateOK, "secure")
	case pageHTTPS:
		return chk("https", stateWarn, "lost")
	case hostIsLocal(reqHost(r)):
		return chk("https", stateOK, "plainHome")
	}
	return chk("https", stateWarn, "plainPublic", "host", reqHost(r))
}

// address: the public address is set, and is not plain http.
func (s *selfCheck) address() *check {
	var raw string
	if s.public != nil {
		raw = strings.TrimSpace(s.public())
	}
	if raw == "" {
		return chk("address", stateOff, "unset")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return chk("address", stateWarn, "invalid")
	}
	if u.Scheme == "http" && !hostIsLocal(strings.ToLower(u.Hostname())) {
		return chk("address", stateWarn, "plain", "host", u.Hostname())
	}
	return chk("address", stateOK, "set", "host", u.Hostname())
}

// dataStat is disk.Stat (tests replace it).
var dataStat = disk.Stat

// dataFolder: the server's own folder (the database, the keys) is
// writable, closed to others and has room.
func (s *selfCheck) dataFolder() *check {
	if s.dataDir == "" {
		return nil
	}
	if err := mediacheck.WriteTest(s.dataDir); err != nil {
		code := "other"
		switch mediacheck.Classify(err) {
		case mediacheck.WhyDenied:
			code = "denied"
		case mediacheck.WhyReadOnlyFS:
			code = "readonlyFS"
		}
		return chk("data", stateBad, code, "path", s.dataDir)
	}
	used, total, statErr := dataStat(s.dataDir)
	if statErr == nil {
		free := total - used
		switch {
		case free < dataFull:
			return chk("data", stateBad, "full", "free", free)
		case free < dataLow:
			return chk("data", stateWarn, "low", "free", free)
		}
	}
	if st, err := os.Stat(s.dataDir); err == nil && st.Mode().Perm()&0o007 != 0 {
		return chk("data", stateWarn, "open", "path", s.dataDir, "mode", fmt.Sprintf("%04o", st.Mode().Perm()))
	}
	if statErr != nil {
		return chk("data", stateOK, "ok", "path", s.dataDir)
	}
	return chk("data", stateOK, "ok", "path", s.dataDir, "free", total-used)
}

// space: what is left on the media disk, by the percent and by the bytes.
func (s *selfCheck) space() *check {
	if s.usage == nil {
		return nil
	}
	u, err := s.usage()
	switch {
	case err != nil:
		return chk("space", stateUnknown, "unreadable")
	case u.Offline != "":
		return chk("space", stateUnknown, "offline")
	}
	free := u.Total - u.Used
	pct := 0
	if u.Total > 0 {
		pct = int(u.Used * 100 / u.Total)
	}
	switch {
	case free < spaceFull || u.Level == disk.LevelFull && free < spaceBigFull:
		return chk("space", stateBad, "full", "free", free, "percent", pct)
	case free < spaceLow || u.Level != disk.LevelOK && free < spaceBigWarning:
		return chk("space", stateWarn, "low", "free", free, "percent", pct)
	}
	return chk("space", stateOK, "ok", "free", free, "percent", pct)
}

// toolVersion runs `bin -version` and returns its first line (tests replace it).
var toolVersion = func(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return line, nil
}

var ffmpegVersionRE = regexp.MustCompile(`version n?(\d+)\.(\d+)`)

// toolsCheck: ffmpeg and ffprobe are there and not older than 6.1. The
// answer is kept for toolsTTL, as running them costs a process each.
func (s *selfCheck) toolsCheck(ctx context.Context, fresh bool) *check {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !fresh && s.tools != nil && s.now().Sub(s.toolsAt) < toolsTTL {
		return s.tools
	}
	s.tools, s.toolsAt = s.askTools(ctx), s.now()
	return s.tools
}

func (s *selfCheck) askTools(ctx context.Context) *check {
	line, err := toolVersion(ctx, orDefault(s.ffmpeg, "ffmpeg"))
	if err != nil {
		return toolMissing("ffmpeg", err)
	}
	if _, err := toolVersion(ctx, orDefault(s.ffprobe, "ffprobe")); err != nil {
		return toolMissing("ffprobe", err)
	}
	m := ffmpegVersionRE.FindStringSubmatch(line)
	if m == nil {
		return chk("ffmpeg", stateOK, "ok") // a build of the git tree: no number to judge by
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	v := m[1] + "." + m[2]
	if major < ffmpegMajor || major == ffmpegMajor && minor < ffmpegMinor {
		return chk("ffmpeg", stateWarn, "old", "version", v)
	}
	return chk("ffmpeg", stateOK, "ok", "version", v)
}

func toolMissing(tool string, err error) *check {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return chk("ffmpeg", stateBad, "missing", "tool", tool)
	}
	return chk("ffmpeg", stateBad, "broken", "tool", tool)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// tmdbCheck: TMDB's key is set (and, once the admin asked, accepted).
func (s *selfCheck) tmdbCheck() *check {
	if s.tmdb == nil {
		return chk("tmdb", stateWarn, "unset")
	}
	s.mu.Lock()
	last := s.tmdbTest
	s.mu.Unlock()
	if last != nil {
		return last.check()
	}
	return chk("tmdb", stateOK, "set")
}

// askTMDB asks TMDB for its configuration: the lightest call that needs the key.
func (s *selfCheck) askTMDB(ctx context.Context) check {
	ctx, cancel := context.WithTimeout(ctx, testWait)
	defer cancel()
	status, _, _, err := s.tmdb.Raw(ctx, "/configuration", nil)
	switch {
	case err != nil:
		return *chk("tmdb", stateUnknown, "unreachable")
	case status >= 200 && status < 300:
		return *chk("tmdb", stateOK, "accepted")
	case status == http.StatusUnauthorized:
		return *chk("tmdb", stateBad, "rejected")
	}
	return *chk("tmdb", stateWarn, "failed", "status", status)
}

// clock: the page's clock and zone against the server's. The server in UTC
// while the admin is not is the usual mistake (the TV guide shifts).
func (s *selfCheck) clock(nowMs, tz string) *check {
	ms, err := strconv.ParseInt(nowMs, 10, 64)
	if err != nil {
		return nil
	}
	off, offErr := strconv.Atoi(tz)
	now := s.now()
	skew := now.Sub(time.UnixMilli(ms)) // positive: the server is ahead
	if skew >= clockSkew {
		return chk("clock", stateWarn, "ahead", "minutes", int(skew/time.Minute))
	}
	if skew <= -clockSkew {
		return chk("clock", stateWarn, "behind", "minutes", int(-skew/time.Minute))
	}
	abbr, serverOff := now.Zone()
	zone := strings.TrimPrefix(os.Getenv("TZ"), ":")
	if zone == "" {
		zone = abbr
	}
	if offErr == nil && serverOff == 0 && isUTC(zone) && off != 0 {
		return chk("clock", stateWarn, "utc", "browser", utcOffset(off))
	}
	return chk("clock", stateOK, "ok", "zone", zone)
}

func isUTC(zone string) bool {
	switch strings.ToUpper(zone) {
	case "UTC", "ETC/UTC", "Z", "GMT", "ETC/GMT", "UCT", "ETC/UCT":
		return true
	}
	return false
}

// utcOffset is minutes east as «+1:00».
func utcOffset(min int) string {
	sign := "+"
	if min < 0 {
		sign, min = "-", -min
	}
	return fmt.Sprintf("%s%d:%02d", sign, min/60, min%60)
}

// tv: the external IPTV sources refresh. Left out when there are none (or no
// such feature in the build).
func (s *selfCheck) tv(ctx context.Context) *check {
	if s.sources == nil {
		return nil
	}
	srcs, err := s.sources(ctx)
	switch {
	case err != nil:
		return chk("tv", stateUnknown, "unreadable")
	case len(srcs) == 0:
		return nil
	}
	var failing, pending, stale []iptv.Source
	for _, src := range srcs {
		switch {
		case src.LastErr != "":
			failing = append(failing, src)
		case src.PendingDrop > 0:
			pending = append(pending, src)
		case src.RefreshHours > 0 && !src.LastOK.IsZero() && s.now().Sub(src.LastOK) > 3*time.Duration(src.RefreshHours)*time.Hour:
			stale = append(stale, src)
		}
	}
	switch {
	case len(failing) > 0:
		// the reason is the fetcher's own words (they never carry an address with a key)
		return chk("tv", stateWarn, "failing", "name", failing[0].Name, "error", i18n.Localize(ctx, failing[0].LastErr), "count", len(failing))
	case len(pending) > 0:
		return chk("tv", stateWarn, "pending", "name", pending[0].Name, "count", len(pending))
	case len(stale) > 0:
		return chk("tv", stateWarn, "stale", "name", stale[0].Name, "count", len(stale))
	}
	return chk("tv", stateOK, "ok", "count", len(srcs))
}

// mailAccount is the mailbox of the settings; ok: enough is set to try it.
func (s *selfCheck) mailAccount(ctx context.Context) (cfg mailer.Config, ok bool, err error) {
	if s.settings == nil {
		return cfg, false, nil
	}
	st, err := s.settings(ctx)
	if err != nil {
		return cfg, false, err
	}
	m := st.Mail.normalized()
	return m.MailConfig(), m.Configured(), nil
}

// mailSig tells one account from another, the password included; it never
// leaves the server.
func mailSig(c mailer.Config) string {
	h := sha256.Sum256([]byte(strings.Join([]string{c.Host, strconv.Itoa(c.Port), c.Username, c.Password, c.From}, "\x00")))
	return hex.EncodeToString(h[:])
}

// mailCheck: the mailbox for «Отправить на Kindle» and letters is set up
// (and, once the admin asked, accepts the login). Nothing set and nobody
// needing it is not a fault.
func (s *selfCheck) mailCheck(ctx context.Context) *check {
	cfg, ok, err := s.mailAccount(ctx)
	switch {
	case s.settings == nil:
		return nil
	case err != nil:
		return chk("mail", stateUnknown, "unreadable")
	case !ok:
		if s.anyKindle != nil {
			if has, err := s.anyKindle(ctx); err == nil && has {
				return chk("mail", stateWarn, "unsetKindle")
			}
		}
		return chk("mail", stateOff, "unset")
	}
	s.mu.Lock()
	last := s.mailTest
	s.mu.Unlock()
	if last != nil && last.sig == mailSig(cfg) {
		return last.check()
	}
	return chk("mail", stateOK, "set", "host", cfg.Host)
}

// mailProbe signs in to the mailbox (tests replace it).
var mailProbe = mailer.Check

// askMail signs in once and names what went wrong, never in the server's words.
func (s *selfCheck) askMail(ctx context.Context, cfg mailer.Config) check {
	ctx, cancel := context.WithTimeout(ctx, testWait)
	defer cancel()
	err := mailProbe(ctx, cfg)
	var cert *tls.CertificateVerificationError
	var neterr net.Error
	switch {
	case err == nil:
		return *chk("mail", stateOK, "works", "host", cfg.Host)
	case errors.Is(err, mailer.ErrAuth):
		return *chk("mail", stateBad, "auth", "host", cfg.Host)
	case errors.Is(err, mailer.ErrNoTLS):
		return *chk("mail", stateBad, "noTLS", "host", cfg.Host)
	case errors.As(err, &cert):
		return *chk("mail", stateBad, "tls", "host", cfg.Host)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &neterr):
		return *chk("mail", stateBad, "unreachable", "host", cfg.Host, "port", cfg.Port)
	}
	return *chk("mail", stateWarn, "failed", "host", cfg.Host)
}

// check is the run's answer with the time it was got.
func (r *testRun) check() *check {
	c := r.c
	at := r.at
	c.At = &at
	return &c
}
