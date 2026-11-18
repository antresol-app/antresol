package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	defaultAPI  = "https://api.github.com"
	cacheTTL    = 10 * time.Minute
	maxManifest = 1 << 20
	maxDownload = 512 << 20
)

// Resolver talks to the GitHub releases API. The zero value works.
type Resolver struct {
	API  string // default https://api.github.com
	HTTP *http.Client

	mu    sync.Mutex
	cache map[Repo]cached
	tags  map[Repo]cachedTag
	now   func() time.Time
}

type cached struct {
	rel Release
	at  time.Time
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

func (r *Resolver) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

var (
	errNoManifest = userError("В релизе нет antresol-addon.json")
	errRelease    = userError("Не удалось получить релиз с GitHub")
	errDownload   = userError("Не удалось скачать файл расширения")
	errChecksum   = userError("Контрольная сумма файла расширения не совпадает")
	errNoAsset    = userError("В релизе нет нужного файла расширения")
)

// Latest reads the latest release of repo and its manifest. Results are
// cached in memory for 10 minutes per repo.
func (r *Resolver) Latest(ctx context.Context, repo Repo) (Release, error) {
	r.mu.Lock()
	if c, ok := r.cache[repo]; ok && r.clock().Sub(c.at) < cacheTTL {
		r.mu.Unlock()
		return c.rel, nil
	}
	r.mu.Unlock()

	api := r.api()
	u := fmt.Sprintf("%s/repos/%s/%s/releases/latest", api, url.PathEscape(repo.Owner), url.PathEscape(repo.Name))
	body, err := r.get(ctx, u, 4<<20, "application/vnd.github+json")
	if err != nil {
		log.Printf("addons: release of %s/%s: %v", repo.Owner, repo.Name, err)
		return Release{}, errRelease
	}
	var gh struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if json.Unmarshal(body, &gh) != nil {
		return Release{}, errRelease
	}
	rel := Release{Tag: gh.Tag, assets: map[string]string{}}
	for _, a := range gh.Assets {
		if r.allowedURL(a.URL, api) {
			rel.assets[a.Name] = a.URL
		} else if a.Name == ManifestName {
			return Release{}, errRelease
		}
	}
	mu, ok := rel.assets[ManifestName]
	if !ok {
		return Release{}, errNoManifest
	}
	mb, err := r.get(ctx, mu, maxManifest, "")
	if err != nil {
		log.Printf("addons: manifest of %s/%s: %v", repo.Owner, repo.Name, err)
		return Release{}, errDownload
	}
	if rel.Manifest, err = ParseManifest(mb); err != nil {
		return Release{}, err
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[Repo]cached{}
	}
	r.cache[repo] = cached{rel, r.clock()}
	r.mu.Unlock()
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
	rc, err := r.open(ctx, u, accept)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errDownload
	}
	return b, nil
}

func (r *Resolver) open(ctx context.Context, u, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	// every redirect is checked like an asset URL
	c := *r.client()
	api := r.api()
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after %d redirects", len(via))
		}
		if !r.allowedURL(req.URL.String(), api) {
			return fmt.Errorf("redirect to a disallowed host %s", req.URL.Host)
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", req.URL.Redacted(), resp.Status)
	}
	return resp.Body, nil
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

	var rc io.ReadCloser
	if isLocal {
		rc, err = os.Open(lp)
	} else {
		rc, err = r.open(ctx, u, "application/octet-stream")
	}
	if err != nil {
		log.Printf("addons: download %s: %v", p.Asset, err)
		return errDownload
	}
	defer rc.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(rc, maxDownload+1))
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
