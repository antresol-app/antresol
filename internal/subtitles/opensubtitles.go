package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// UserAgent is the form OpenSubtitles requires: "<AppName> v<version>".
const UserAgent = "Vault v0.1"

var (
	// ErrQuota: the daily download quota is used up (406, or "remaining"
	// reached 0: then Download also returns the last allowed file).
	ErrQuota = errors.New("subtitles: OpenSubtitles download quota exhausted")
	// ErrOffline: OpenSubtitles cannot be reached or is overloaded (network
	// error, 429, 5xx); try again later.
	ErrOffline = errors.New("subtitles: OpenSubtitles unavailable")
	// ErrAuth: the login (VAULT_OPENSUBTITLES_USER / _PASSWORD) or the API
	// key was refused; nothing works until the configuration is fixed.
	ErrAuth = errors.New("subtitles: OpenSubtitles refused the login or the API key")
)

// MinInterval paces requests: OpenSubtitles allows about 5 per second.
const MinInterval = 250 * time.Millisecond

// tokenTTL: login tokens live 24 h; renew a little earlier.
const tokenTTL = 23 * time.Hour

// maxSubtitleBytes bounds a downloaded subtitle file.
const maxSubtitleBytes = 10 << 20

// Client talks to the OpenSubtitles REST API (api.opensubtitles.com).
type Client struct {
	BaseURL            string // https://api.opensubtitles.com/api/v1
	Key                string // Api-Key
	Username, Password string // optional: a login raises the download quota
	UserAgent          string // UserAgent when empty
	HTTP               *http.Client
	// Limiter paces requests (OpenSubtitles allows about 5 per second).
	Limiter *metadata.Limiter

	mu         sync.Mutex
	token      string
	tokenAt    time.Time
	tokenNow   func() time.Time // tests
	quotaUntil time.Time        // downloads paused until (shared by Auto and the API)
}

// QuotaUntil is when downloads count again after ErrQuota (zero: not paused).
func (c *Client) QuotaUntil() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quotaUntil
}

// PauseUntil records the end of a quota pause (only ever extends it).
func (c *Client) PauseUntil(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.quotaUntil) {
		c.quotaUntil = t
	}
}

// QuotaReset is when the quota counts again: OpenSubtitles' reset time
// when it told one (after now), else the next midnight UTC.
func (c *Client) QuotaReset(now time.Time) time.Time {
	if u := c.QuotaUntil(); u.After(now) {
		return u
	}
	return nextMidnight(now)
}

// noteReset keeps the reset time of a quota answer ("reset_time_utc").
func (c *Client) noteReset(raw string) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		c.PauseUntil(t)
	}
}

// Query is one search. Movies go by TMDBID, episodes by ParentTMDBID +
// Season + Episode; MovieHash (Hash) marks the exact release.
type Query struct {
	MovieHash            string
	TMDBID, ParentTMDBID int
	Season, Episode      int
	Languages            []string // lang codes; sent sorted, pt and zh as their regional codes
}

// Identifies says whether q names a video (a hash or a TMDB id).
func (q Query) Identifies() bool { return q.MovieHash != "" || q.TMDBID > 0 || q.ParentTMDBID > 0 }

// Subtitle is one search result. FileID is its first file; Files lists all
// of them (a subtitle for a two-disc release has one file per disc).
type Subtitle struct {
	FileID                     int
	Lang, Release              string
	Downloads                  int
	HashMatch, HearingImpaired bool
	Files                      []int
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

func (c *Client) ua() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return UserAgent
}

func (c *Client) do(ctx context.Context, method, u string, body any, auth bool) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if err := c.Limiter.Wait(ctx); err != nil {
		return nil, err
	}
	req.Header.Set("Api-Key", c.Key)
	req.Header.Set("User-Agent", c.ua())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		tok, err := c.login(ctx)
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: HTTP %d", ErrOffline, resp.StatusCode)
	}
	return resp, nil
}

func statusErr(what string, resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("subtitles: %s: HTTP %d: %s", what, resp.StatusCode, strings.TrimSpace(string(b)))
}

func (c *Client) now() time.Time {
	if c.tokenNow != nil {
		return c.tokenNow()
	}
	return time.Now()
}

// login returns the cached token, logging in when Username is set and the
// token is missing or older than 23 h ("" without Username).
func (c *Client) login(ctx context.Context) (string, error) {
	if c.Username == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Sub(c.tokenAt) < tokenTTL {
		return c.token, nil
	}
	resp, err := c.do(ctx, http.MethodPost, c.BaseURL+"/login", map[string]string{"username": c.Username, "password": c.Password}, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
		return "", fmt.Errorf("%w: %v", ErrAuth, statusErr("login", resp))
	}
	if resp.StatusCode != http.StatusOK {
		return "", statusErr("login", resp)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || out.Token == "" {
		return "", fmt.Errorf("subtitles: login: no token (%v)", err)
	}
	c.token, c.tokenAt = out.Token, c.now()
	return c.token, nil
}

func (c *Client) dropToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// searchQuery builds the query in OpenSubtitles' canonical form (keys
// sorted, commas unescaped): anything else is answered with a redirect.
func searchQuery(q Query) string {
	v := url.Values{}
	if q.MovieHash != "" {
		v.Set("moviehash", strings.ToLower(q.MovieHash))
	}
	if q.TMDBID > 0 {
		v.Set("tmdb_id", strconv.Itoa(q.TMDBID))
	}
	if q.ParentTMDBID > 0 {
		v.Set("parent_tmdb_id", strconv.Itoa(q.ParentTMDBID))
	}
	if q.Season > 0 {
		v.Set("season_number", strconv.Itoa(q.Season))
	}
	if q.Episode > 0 {
		v.Set("episode_number", strconv.Itoa(q.Episode))
	}
	if len(q.Languages) > 0 {
		v.Set("languages", strings.Join(osLanguages(q.Languages), ","))
	}
	return strings.ReplaceAll(v.Encode(), "%2C", ",")
}

// Search runs GET /subtitles (the first page of results).
func (c *Client) Search(ctx context.Context, q Query) ([]Subtitle, error) {
	resp, err := c.do(ctx, http.MethodGet, c.BaseURL+"/subtitles?"+searchQuery(q), nil, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %v", ErrAuth, statusErr("search", resp))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr("search", resp)
	}
	var out struct {
		Data []struct {
			Attributes struct {
				Language        string `json:"language"`
				DownloadCount   int    `json:"download_count"`
				HearingImpaired bool   `json:"hearing_impaired"`
				MoviehashMatch  bool   `json:"moviehash_match"`
				Release         string `json:"release"`
				Files           []struct {
					FileID   int `json:"file_id"`
					CDNumber int `json:"cd_number"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("subtitles: search: %w", err)
	}
	var subs []Subtitle
	for _, d := range out.Data {
		a := d.Attributes
		s := Subtitle{Lang: baseLang(a.Language), Release: a.Release, Downloads: a.DownloadCount,
			HashMatch: a.MoviehashMatch, HearingImpaired: a.HearingImpaired}
		files := a.Files
		sort.SliceStable(files, func(i, j int) bool { return files[i].CDNumber < files[j].CDNumber }) // disc order
		for _, f := range files {
			if f.FileID > 0 {
				s.Files = append(s.Files, f.FileID)
			}
		}
		if len(s.Files) == 0 {
			continue
		}
		s.FileID = s.Files[0]
		subs = append(subs, s)
	}
	return subs, nil
}

// Ranked returns the subtitles in lang, best first: a hash match, then not
// for the hearing impaired, then the most downloaded.
func Ranked(subs []Subtitle, lang string) []Subtitle {
	var c []Subtitle
	for _, s := range subs {
		if s.Lang == lang {
			c = append(c, s)
		}
	}
	sort.SliceStable(c, func(i, j int) bool {
		a, b := c[i], c[j]
		if a.HashMatch != b.HashMatch {
			return a.HashMatch
		}
		if a.HearingImpaired != b.HearingImpaired {
			return !a.HearingImpaired
		}
		return a.Downloads > b.Downloads
	})
	return c
}

// Best is the first of Ranked.
func Best(subs []Subtitle, lang string) (Subtitle, bool) {
	c := Ranked(subs, lang)
	if len(c) == 0 {
		return Subtitle{}, false
	}
	return c[0], true
}

// Download fetches one subtitle file as SRT: POST /download gives a
// temporary link, which is then read. With Username the request carries a
// login token (cached for 23 h; a 401 logs in again once).
func (c *Client) Download(ctx context.Context, fileID int) ([]byte, error) {
	var resp *http.Response
	for try := 0; ; try++ {
		var err error
		resp, err = c.do(ctx, http.MethodPost, c.BaseURL+"/download", map[string]any{"file_id": fileID, "sub_format": "srt"}, true)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusUnauthorized {
			break
		}
		err = statusErr("download", resp)
		resp.Body.Close()
		c.dropToken() // expired early
		if try > 0 || c.Username == "" {
			return nil, fmt.Errorf("%w: %v", ErrAuth, err)
		}
	}
	defer resp.Body.Close()
	var out struct {
		Link      string `json:"link"`
		Remaining *int   `json:"remaining"`
		Reset     string `json:"reset_time_utc"`
	}
	switch {
	case resp.StatusCode == http.StatusNotAcceptable:
		json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
		c.noteReset(out.Reset)
		return nil, ErrQuota
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %v", ErrAuth, statusErr("download", resp))
	case resp.StatusCode != http.StatusOK:
		return nil, statusErr("download", resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("subtitles: download: %w", err)
	}
	last := out.Remaining != nil && *out.Remaining <= 0
	if last {
		c.noteReset(out.Reset)
	}
	if out.Link == "" {
		if last {
			return nil, ErrQuota
		}
		return nil, errors.New("subtitles: download: no link")
	}
	b, err := c.fetch(ctx, out.Link)
	if err != nil {
		return nil, err
	}
	if last {
		return b, ErrQuota // the last allowed file of the day
	}
	return b, nil
}

func (c *Client) fetch(ctx context.Context, link string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.Scheme != "https" && !(u.Scheme == "http" && strings.HasPrefix(c.BaseURL, "http://")) {
		return nil, fmt.Errorf("subtitles: download: unexpected link %q", link)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	if err := c.Limiter.Wait(ctx); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua())
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr("download file", resp)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	if len(b) > maxSubtitleBytes {
		return nil, errors.New("subtitles: download: file too large")
	}
	return b, nil
}

// osVariants are the OpenSubtitles codes of a language it splits by region.
var osVariants = map[string][]string{"pt": {"pt-pt", "pt-br"}, "zh": {"zh-cn", "zh-tw"}}

// osLanguages are the codes to search for langs (lower case, sorted as
// OpenSubtitles wants them).
func osLanguages(langs []string) []string {
	var out []string
	for _, l := range langs {
		l = strings.ToLower(l)
		if v, ok := osVariants[l]; ok {
			out = append(out, v...)
		} else {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// baseLang is the language of an OpenSubtitles code without its region
// ("pt-BR" → "pt").
func baseLang(code string) string {
	c, _, _ := strings.Cut(strings.ToLower(code), "-")
	return c
}
