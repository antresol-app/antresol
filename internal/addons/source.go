package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is a GitHub repository.
type Repo struct{ Owner, Name string }

var repoPartRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var errBadRepo = userError("Неверный адрес репозитория GitHub")

// ParseRepoURL accepts https://github.com/o/r[.git][/] and github.com/o/r.
func ParseRepoURL(s string) (Repo, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	rest, ok := strings.CutPrefix(s, "github.com/")
	if !ok {
		return Repo{}, errBadRepo
	}
	rest = strings.TrimSuffix(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		return Repo{}, errBadRepo
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !repoPartRe.MatchString(owner) || !repoPartRe.MatchString(name) || name == "." || name == ".." {
		return Repo{}, errBadRepo
	}
	return Repo{owner, name}, nil
}

// Release is one resolved addon release.
type Release struct {
	Tag      string
	Manifest Manifest
	assets   map[string]string // asset name → remote download URL (validated)
	local    map[string]string // asset name → local path; set only by LocalRelease
}

const (
	defaultAPI = "https://api.github.com"
	// cacheTTL: how old a release an install or a preview may reuse.
	cacheTTL = 10 * time.Minute
	// checkTTL: how old a release an update check may reuse («Проверить
	// снова» asks for a fresh one).
	checkTTL = 6 * time.Hour
	// failTTL: a failed fetch is not repeated sooner: the pages poll the
	// list, and every miss would cost a GitHub request.
	failTTL = 2 * time.Minute
	// fetchTimeout bounds one attempt at a release and its manifest. It is
	// not a request's deadline: on a slow link the manifest's CDN (a
	// redirect from github.com) alone takes seconds, so the fetch runs
	// detached and a page that stops waiting finds the result later.
	fetchTimeout = 90 * time.Second
	fetchTries   = 3
	// stallTimeout: a download that sends no byte for this long is cut and
	// resumed (a whole-download deadline would fail a big archive on a
	// slow but working link).
	stallTimeout  = 60 * time.Second
	downloadTries = 5
	maxManifest   = 1 << 20
	maxDownload   = 512 << 20
)

// Resolver talks to the GitHub releases API. The zero value works.
type Resolver struct {
	API  string // default https://api.github.com
	HTTP *http.Client

	mu      sync.Mutex
	cache   map[Repo]cached
	fails   map[Repo]failed
	flights map[Repo]*flight
	tags    map[Repo]cachedTag
	now     func() time.Time
	wait    time.Duration // the pause before a retry (×try); 0: 2 s
}

type cached struct {
	rel Release
	at  time.Time
}

type failed struct {
	err   error
	until time.Time
	limit bool // GitHub's rate limit: refused until then even for a fresh check
}

// flight is one fetch of a repo's latest release; whoever asks meanwhile
// waits for it instead of starting another.
type flight struct {
	done chan struct{}
	rel  Release
	err  error
}

type cachedTag struct {
	tag, page string
	at        time.Time
}

func (r *Resolver) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// downloadClient has no overall timeout (stallTimeout guards a download).
func (r *Resolver) downloadClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{}
}

func (r *Resolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *Resolver) retryWait() time.Duration {
	if r.wait > 0 {
		return r.wait
	}
	return 2 * time.Second
}

var (
	errNoManifest = userError("В релизе нет antresol-addon.json")
	errRelease    = userError("Не удалось получить релиз с GitHub")
	errDownload   = userError("Не удалось скачать файл расширения")
	errChecksum   = userError("Контрольная сумма файла расширения не совпадает")
	errNoAsset    = userError("В релизе нет нужного файла расширения")
	errTimeout    = userError("GitHub не ответил вовремя")
	errOffline    = userError("Нет связи с GitHub")
	errNoReleases = userError("В репозитории нет опубликованных релизов")
	// ErrPending: the release is still being fetched in the background
	// (the caller stopped waiting); asking again later finds it.
	ErrPending = userError("Проверка ещё идёт: GitHub отвечает медленно")
)

// fetchError is a failed fetch: the user's message, the cause for the log,
// whether another try may help and, for GitHub's rate limit, until when.
type fetchError struct {
	msg   userError
	cause error
	retry bool
	limit time.Time
}

func (e *fetchError) Error() string { return string(e.msg) }
func (e *fetchError) Unwrap() error { return e.msg }

// NeedsCoreError: the latest release's manifest is one this server cannot
// read (a newer contract, field, permission, hook or platform), while it
// reads as a release of Version for a newer server.
type NeedsCoreError struct{ Version string }

const needsCoreMsg = "Версии %s нужен более новый сервер: обновите сервер"

func (e NeedsCoreError) Error() string { return fmt.Sprintf(needsCoreMsg, e.Version) }
func (e NeedsCoreError) Unwrap() error { return userError(e.Error()) }

// statusError is a non-200 answer.
type statusError struct {
	url        string
	code       int
	remaining  string // X-RateLimit-Remaining
	reset      string // X-RateLimit-Reset (unix seconds)
	retryAfter string
}

func (e *statusError) Error() string { return fmt.Sprintf("%s: %d", e.url, e.code) }

// disallowedHost: an asset or a redirect off GitHub's hosts.
type disallowedHost struct{ host string }

func (e disallowedHost) Error() string { return "a disallowed host " + e.host }

// classify turns a fetch failure into what the user reads and whether a
// retry may help.
func (r *Resolver) classify(err error) *fetchError {
	var fe *fetchError
	if errors.As(err, &fe) {
		return fe
	}
	var dh disallowedHost
	var se *statusError
	var ne net.Error
	switch {
	case errors.As(err, &dh):
		return &fetchError{msg: userError(fmt.Sprintf("GitHub отдал файл с неразрешённого адреса %s", dh.host)), cause: err}
	case errors.As(err, &se):
		if se.code == http.StatusForbidden || se.code == http.StatusTooManyRequests {
			if until, ok := r.limitUntil(se); ok {
				mins := int(until.Sub(r.clock()).Minutes()) + 1
				return &fetchError{msg: userError(fmt.Sprintf("GitHub ограничил число запросов с этого адреса, повторите через %d мин", mins)),
					cause: err, limit: until}
			}
		}
		if se.code == http.StatusNotFound {
			return &fetchError{msg: errNoReleases, cause: err}
		}
		return &fetchError{msg: userError(fmt.Sprintf("GitHub ответил с ошибкой %d", se.code)), cause: err, retry: se.code >= 500}
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return &fetchError{msg: errTimeout, cause: err, retry: true}
	default:
		return &fetchError{msg: errOffline, cause: err, retry: true}
	}
}

// limitUntil reads GitHub's rate limit headers: when the limit is spent,
// the time it resets (at most an hour on).
func (r *Resolver) limitUntil(se *statusError) (time.Time, bool) {
	now := r.clock()
	if s, err := strconv.Atoi(se.retryAfter); err == nil && s > 0 {
		return now.Add(min(time.Duration(s)*time.Second, time.Hour)), true
	}
	if se.remaining != "0" {
		return time.Time{}, false
	}
	until := now.Add(time.Hour)
	if s, err := strconv.ParseInt(se.reset, 10, 64); err == nil {
		if t := time.Unix(s, 0); t.After(now) && t.Before(until) {
			until = t
		}
	}
	return until, true
}

// Latest reads the latest release of repo and its manifest; a release
// fetched less than 10 minutes ago is reused.
func (r *Resolver) Latest(ctx context.Context, repo Repo) (Release, error) {
	return r.latest(ctx, repo, cacheTTL, false)
}

// Check is Latest for an update check: a release fetched in the last hours
// is reused, unless fresh. When ctx ends before the fetch does, it returns
// ErrPending and the fetch goes on.
func (r *Resolver) Check(ctx context.Context, repo Repo, fresh bool) (Release, error) {
	return r.latest(ctx, repo, checkTTL, fresh)
}

func (r *Resolver) latest(ctx context.Context, repo Repo, maxAge time.Duration, fresh bool) (Release, error) {
	r.mu.Lock()
	now := r.clock()
	if c, ok := r.cache[repo]; ok && !fresh && now.Sub(c.at) < maxAge {
		r.mu.Unlock()
		return c.rel, nil
	}
	if f, ok := r.fails[repo]; ok && now.Before(f.until) && (!fresh || f.limit) {
		r.mu.Unlock()
		return Release{}, f.err
	}
	fl := r.flights[repo]
	if fl == nil {
		fl = &flight{done: make(chan struct{})}
		if r.flights == nil {
			r.flights = map[Repo]*flight{}
		}
		r.flights[repo] = fl
		go r.run(repo, fl)
	}
	r.mu.Unlock()
	select {
	case <-fl.done:
		return fl.rel, fl.err
	case <-ctx.Done():
		return Release{}, ErrPending
	}
}

// run fetches repo's latest release with retries, detached from whoever
// asked, and keeps the result: a release, or a failure for failTTL.
func (r *Resolver) run(repo Repo, fl *flight) {
	var rel Release
	var fe *fetchError
	for try := 1; ; try++ {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		var err error
		rel, err = r.fetchLatest(ctx, repo)
		cancel()
		if err == nil {
			fe = nil
			break
		}
		fe = r.classify(err)
		if !fe.retry || try == fetchTries {
			log.Printf("addons: release of %s/%s: %v (try %d of %d)", repo.Owner, repo.Name, fe.cause, try, fetchTries)
			break
		}
		time.Sleep(r.retryWait() * time.Duration(try))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.flights, repo)
	if fe == nil {
		if r.cache == nil {
			r.cache = map[Repo]cached{}
		}
		r.cache[repo] = cached{rel, r.clock()}
		delete(r.fails, repo)
		fl.rel = rel
	} else {
		if r.fails == nil {
			r.fails = map[Repo]failed{}
		}
		var err error = fe
		var nc NeedsCoreError
		if errors.As(fe.cause, &nc) {
			err = nc
		}
		f := failed{err: err, until: r.clock().Add(failTTL)}
		if !fe.limit.IsZero() {
			f.until, f.limit = fe.limit, true
		}
		r.fails[repo] = f
		fl.err = err
	}
	close(fl.done)
}

// fetchLatest is one attempt: the release from the API, then its manifest.
func (r *Resolver) fetchLatest(ctx context.Context, repo Repo) (Release, error) {
	api := r.api()
	u := fmt.Sprintf("%s/repos/%s/%s/releases/latest", api, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	body, err := r.get(ctx, u, 4<<20, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	var gh struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &gh); err != nil {
		return Release{}, &fetchError{msg: errRelease, cause: fmt.Errorf("release JSON: %w", err)}
	}
	rel := Release{Tag: gh.Tag, assets: map[string]string{}}
	for _, a := range gh.Assets {
		if r.allowedURL(a.URL, api) {
			rel.assets[a.Name] = a.URL
		} else if a.Name == ManifestName {
			host := a.URL
			if p, err := url.Parse(a.URL); err == nil {
				host = p.Scheme + "://" + p.Host
			}
			return Release{}, disallowedHost{host}
		}
	}
	mu, ok := rel.assets[ManifestName]
	if !ok {
		return Release{}, &fetchError{msg: errNoManifest, cause: fmt.Errorf("release %s has no %s", gh.Tag, ManifestName)}
	}
	mb, err := r.get(ctx, mu, maxManifest, "")
	if err != nil {
		return Release{}, fmt.Errorf("manifest: %w", err)
	}
	if rel.Manifest, err = ParseManifest(mb); err != nil {
		// a manifest of a newer contract reads as a release all the same
		if m, terr := parseManifest(mb, true); terr == nil {
			nc := NeedsCoreError{m.Version}
			return Release{}, &fetchError{msg: userError(nc.Error()), cause: nc}
		}
		ue, _ := err.(userError)
		return Release{}, &fetchError{msg: ue, cause: fmt.Errorf("manifest of %s: %w", gh.Tag, err)}
	}
	return rel, nil
}

// LatestTag reads only the tag and the page of repo's latest release (the
// core's own repository has no addon manifest), cached like Latest: the
// anonymous GitHub API allows 60 requests an hour.
func (r *Resolver) LatestTag(ctx context.Context, repo Repo) (tag, page string, err error) {
	r.mu.Lock()
	if c, ok := r.tags[repo]; ok && r.clock().Sub(c.at) < cacheTTL {
		r.mu.Unlock()
		return c.tag, c.page, nil
	}
	r.mu.Unlock()
	u := fmt.Sprintf("%s/repos/%s/%s/releases/latest", r.api(), url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	body, err := r.get(ctx, u, 4<<20, "application/vnd.github+json")
	if err != nil {
		log.Printf("addons: latest release of %s/%s: %v", repo.Owner, repo.Name, err)
		return "", "", errRelease
	}
	var gh struct {
		Tag  string `json:"tag_name"`
		Page string `json:"html_url"`
	}
	if json.Unmarshal(body, &gh) != nil || gh.Tag == "" {
		return "", "", errRelease
	}
	// the page is shown as a link: only GitHub's own
	if p, err := url.Parse(gh.Page); err != nil || p.Scheme != "https" || p.Host != "github.com" {
		gh.Page = ""
	}
	r.mu.Lock()
	if r.tags == nil {
		r.tags = map[Repo]cachedTag{}
	}
	r.tags[repo] = cachedTag{gh.Tag, gh.Page, r.clock()}
	r.mu.Unlock()
	return gh.Tag, gh.Page, nil
}

// VersionLess reports a < b for two release versions, a leading "v" aside
// ("v0.8.0" is 0.8.0); false when either is not semver (a "dev" build).
func VersionLess(a, b string) bool {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	return semverRe.MatchString(a) && semverRe.MatchString(b) && semverLess(a, b)
}

func (r *Resolver) api() string {
	if api := strings.TrimSuffix(r.API, "/"); api != "" {
		return api
	}
	return defaultAPI
}

// assetHosts are where GitHub serves release assets from (https only).
var assetHosts = map[string]bool{"github.com": true, "objects.githubusercontent.com": true,
	"release-assets.githubusercontent.com": true}

// allowedURL reports whether a remote URL may be fetched (an asset, or
// where a redirect leads): https on GitHub's asset hosts, or the host of
// the resolver's own API endpoint.
func (r *Resolver) allowedURL(raw, api string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if a, err := url.Parse(api); err == nil && a.Host == u.Host && a.Scheme == u.Scheme &&
		(u.Scheme == "https" || u.Scheme == "http") {
		return true
	}
	return u.Scheme == "https" && assetHosts[u.Host]
}

func (r *Resolver) get(ctx context.Context, u string, limit int64, accept string) ([]byte, error) {
	resp, err := r.open(ctx, r.client(), u, accept, 0)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, &fetchError{msg: errDownload, cause: fmt.Errorf("%s: over %d bytes", u, limit)}
	}
	return b, nil
}

// open GETs u (from byte `from` on, when it is not 0: 206 or 200 answer).
func (r *Resolver) open(ctx context.Context, client *http.Client, u, accept string, from int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if from > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", from))
	}
	// every redirect is checked like an asset URL
	c := *client
	api := r.api()
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after %d redirects", len(via))
		}
		if !r.allowedURL(req.URL.String(), api) {
			return disallowedHost{req.URL.Scheme + "://" + req.URL.Host}
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && (from == 0 || resp.StatusCode != http.StatusPartialContent) {
		resp.Body.Close()
		return nil, &statusError{url: req.URL.Redacted(), code: resp.StatusCode, remaining: resp.Header.Get("X-RateLimit-Remaining"),
			reset: resp.Header.Get("X-RateLimit-Reset"), retryAfter: resp.Header.Get("Retry-After")}
	}
	return resp, nil
}

// Fetch downloads the asset of p, verifies its sha256 and unpacks the
// tar.gz into dst. On any failure dst does not exist afterwards.
func (r *Resolver) Fetch(ctx context.Context, rel Release, p Platform, dst string) error {
	u, remote := rel.assets[p.Asset]
	lp, isLocal := rel.local[p.Asset]
	if !remote && !isLocal {
		return errNoAsset
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return errDownload
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dl-*")
	if err != nil {
		return errDownload
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()

	if isLocal {
		src, err := os.Open(lp)
		if err != nil {
			return errDownload
		}
		_, err = io.Copy(tmp, io.LimitReader(src, maxDownload+1))
		src.Close()
		if err != nil {
			return errDownload
		}
	} else if err := r.download(ctx, u, tmp); err != nil {
		log.Printf("addons: download %s: %v", p.Asset, err)
		var fe *fetchError
		if errors.As(err, &fe) && fe.msg != errDownload {
			return userError(fmt.Sprintf("Не удалось скачать файл расширения: %s", fe.msg))
		}
		return errDownload
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return errDownload
	}
	h := sha256.New()
	n, err := io.Copy(h, tmp)
	if err != nil || n > maxDownload {
		return errDownload
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), p.SHA256) {
		return errChecksum
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return errDownload
	}
	return Unpack(tmp, dst)
}

// download writes u to f. A download that stalls (no byte for
// stallTimeout) or breaks is resumed where it stopped (a Range request; a
// server that answers 200 instead starts it over), up to downloadTries
// times; only a stall ends an attempt, never the archive's size over a
// slow link.
func (r *Resolver) download(ctx context.Context, u string, f *os.File) error {
	var n int64
	var last error
	for try := 1; try <= downloadTries; try++ {
		if try > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.retryWait() * time.Duration(try-1)):
			}
		}
		got, done, err := r.downloadOnce(ctx, u, f, n)
		n = got
		if done {
			return nil
		}
		last = err
		fe := r.classify(err)
		if !fe.retry || ctx.Err() != nil {
			return fe
		}
		log.Printf("addons: download %s: %v at %d bytes, try %d of %d", u, err, n, try, downloadTries)
	}
	return r.classify(last)
}

// downloadOnce is one attempt from byte `from`: it returns the bytes f
// holds afterwards and whether the download is complete.
func (r *Resolver) downloadOnce(ctx context.Context, u string, f *os.File, from int64) (int64, bool, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stalled := errors.New("no data for " + stallTimeout.String())
	watch := time.AfterFunc(stallTimeout, func() { cancel(stalled) })
	defer watch.Stop()
	resp, err := r.open(ctx, r.downloadClient(), u, "application/octet-stream", from)
	if err != nil {
		if c := context.Cause(ctx); c == stalled {
			err = &fetchError{msg: errTimeout, cause: c, retry: true}
		}
		return from, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && from > 0 { // no ranges: from the start
		if err := f.Truncate(0); err != nil {
			return 0, false, &fetchError{msg: errDownload, cause: err}
		}
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return from, false, &fetchError{msg: errDownload, cause: err}
	}
	n := from
	buf := make([]byte, 64<<10)
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 {
			watch.Reset(stallTimeout)
			if n+int64(k) > maxDownload {
				return n, false, &fetchError{msg: errDownload, cause: fmt.Errorf("over %d bytes", maxDownload)}
			}
			if _, err := f.Write(buf[:k]); err != nil {
				return n, false, &fetchError{msg: errDownload, cause: err}
			}
			n += int64(k)
		}
		if rerr == io.EOF {
			return n, true, nil
		}
		if rerr != nil {
			if c := context.Cause(ctx); c == stalled {
				rerr = &fetchError{msg: errTimeout, cause: c, retry: true}
			}
			return n, false, rerr
		}
	}
}

// LocalRelease builds a release from a directory holding antresol-addon.json
// and its assets (development aid).
func LocalRelease(dir string) (Release, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Release{}, errNoManifest
	}
	mb, err := os.ReadFile(filepath.Join(abs, ManifestName))
	if err != nil {
		return Release{}, errNoManifest
	}
	m, err := ParseManifest(mb)
	if err != nil {
		return Release{}, err
	}
	rel := Release{Tag: "local", Manifest: m, local: map[string]string{}}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return Release{}, errNoManifest
	}
	for _, e := range ents {
		if e.Type().IsRegular() {
			rel.local[e.Name()] = filepath.Join(abs, e.Name())
		}
	}
	return rel, nil
}
