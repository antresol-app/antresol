// Package shikimori is a small client for the Shikimori REST API: anonymous
// list reads, OAuth, and bearer-authenticated list writes. All clients share
// one process-wide rate limiter.
package shikimori

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBase is used when no base URL is configured.
const DefaultBase = "https://shikimori.io"

const (
	userAgent  = "Antresol"
	maxRetries = 3
	maxBody    = 32 << 20
	maxErrBody = 200
)

var (
	ErrNotFound     = errors.New("shikimori: not found")
	ErrForbidden    = errors.New("shikimori: forbidden")
	ErrUnauthorized = errors.New("shikimori: unauthorized")
	ErrRateLimited  = errors.New("shikimori: rate limited")
)

// User is a Shikimori account.
type User struct {
	ID       int64  `json:"id"`
	Nickname string `json:"nickname"`
}

// Anime is the title embedded in a list entry (or fetched on its own).
type Anime struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Russian       string `json:"russian"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Episodes      int    `json:"episodes"`
	EpisodesAired int    `json:"episodes_aired"`
	AiredOn       string `json:"aired_on"`
}

// Rate is one entry of a user's anime list.
type Rate struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Score     int       `json:"score"`
	Episodes  int       `json:"episodes"`
	Rewatches int       `json:"rewatches"`
	UpdatedAt time.Time `json:"updated_at"`
	Anime     Anime     `json:"anime"`
}

// RateInput is the writable part of a list entry.
type RateInput struct {
	Status string `json:"status,omitempty"`
	// Pointers: nil is omitted, so a partial update never resets a remote
	// value. Score 0 means "no score" on Shikimori and is sent when set.
	Score    *int `json:"score,omitempty"`
	Episodes *int `json:"episodes,omitempty"`
}

// Client talks to one Shikimori base URL.
type Client struct {
	Base         string
	HTTP         *http.Client
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Backoff      time.Duration // base delay after HTTP 429 without Retry-After (default 1s)

	limiter *limiter
}

// New returns a client bound to the process-wide shared limiter. A nil hc
// gets a client with a 30 s timeout; an empty base means DefaultBase.
func New(base string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	// Copy: never mutate a caller's shared client.
	cp := *hc
	prev := cp.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if m := via[0].Method; m != http.MethodGet && m != http.MethodHead {
			return fmt.Errorf("Shikimori base redirected; set VAULT_SHIKIMORI_URL to %s", req.URL.Host)
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	hc = &cp
	if base == "" {
		base = DefaultBase
	}
	return &Client{Base: base, HTTP: hc, limiter: shared}
}

// limiter enforces at most 5 requests/s and 90/min with a sliding window.
type limiter struct {
	mu    sync.Mutex
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	stamp []time.Time
}

const (
	perSecond = 5
	perMinute = 90
)

var shared = newLimiter(time.Now, func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
})

func newLimiter(now func() time.Time, sleep func(context.Context, time.Duration) error) *limiter {
	return &limiter{now: now, sleep: sleep}
}

// wait blocks until one more request is allowed, then records it.
// The lock is released while sleeping so waiters stay responsive to their
// own context; the window is re-evaluated after every sleep.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	for {
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		now := l.now()
		i := 0
		for i < len(l.stamp) && !l.stamp[i].After(now.Add(-time.Minute)) {
			i++
		}
		l.stamp = l.stamp[i:]
		var d time.Duration
		if len(l.stamp) >= perMinute {
			d = l.stamp[len(l.stamp)-perMinute].Add(time.Minute).Sub(now)
		}
		if len(l.stamp) >= perSecond {
			if s := l.stamp[len(l.stamp)-perSecond].Add(time.Second).Sub(now); s > d {
				d = s
			}
		}
		if d <= 0 {
			l.stamp = append(l.stamp, now)
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		if err := l.sleep(ctx, d); err != nil {
			return err
		}
		l.mu.Lock()
	}
}

func (c *Client) base() string { return strings.TrimRight(c.Base, "/") }

func (c *Client) backoff() time.Duration {
	if c.Backoff > 0 {
		return c.Backoff
	}
	return time.Second
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxErrBody {
		s = s[:maxErrBody] + "..."
	}
	return s
}

// do performs one rate-limited request with 429 retries and decodes a JSON
// body into out (nil to ignore). access, when set, is sent as a bearer token.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, ctype string, body []byte, access string, out any) error {
	u := c.base() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	lim := c.limiter
	if lim == nil {
		lim = shared
	}
	for attempt := 0; ; attempt++ {
		if err := lim.wait(ctx); err != nil {
			return err
		}
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return fmt.Errorf("shikimori %s: bad request", path)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if access != "" {
			req.Header.Set("Authorization", "Bearer "+access)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err // drop the URL (may carry a nickname or code)
			}
			return fmt.Errorf("shikimori %s %s: %v", method, path, err)
		}
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			if attempt >= maxRetries {
				return ErrRateLimited
			}
			d := c.backoff() << attempt
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
				d = min(time.Duration(s)*time.Second, 30*time.Second)
			}
			t := time.NewTimer(d)
			select {
			case <-t.C:
				continue
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			}
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode == http.StatusForbidden:
			return ErrForbidden
		case resp.StatusCode == http.StatusUnauthorized:
			return ErrUnauthorized
		case resp.StatusCode == http.StatusBadRequest && path == "/oauth/token" && bytes.Contains(b, []byte("invalid_grant")):
			return ErrUnauthorized
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return fmt.Errorf("shikimori %s %s: status %d: %s", method, path, resp.StatusCode, truncate(b))
		}
		if rerr != nil {
			return fmt.Errorf("shikimori %s %s: read: %v", method, path, rerr)
		}
		if out == nil || len(bytes.TrimSpace(b)) == 0 {
			return nil
		}
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("shikimori %s %s: bad json", method, path)
		}
		return nil
	}
}

// UserByNick resolves a public nickname. Anonymous.
func (c *Client) UserByNick(ctx context.Context, nick string) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/api/users/"+url.PathEscape(nick), url.Values{"is_nickname": {"1"}}, "", nil, "", &u)
	return u, err
}

// AnimeRates returns a user's whole anime list, with the user's access
// token when given ("" = anonymous: a private list yields ErrForbidden).
func (c *Client) AnimeRates(ctx context.Context, userID int64, access string) ([]Rate, error) {
	var rs []Rate
	err := c.do(ctx, http.MethodGet, "/api/users/"+strconv.FormatInt(userID, 10)+"/anime_rates", url.Values{"limit": {"5000"}}, "", nil, access, &rs)
	return rs, err
}

// Anime fetches one title. Anonymous.
func (c *Client) Anime(ctx context.Context, id int64) (Anime, error) {
	var a Anime
	err := c.do(ctx, http.MethodGet, "/api/animes/"+strconv.FormatInt(id, 10), nil, "", nil, "", &a)
	return a, err
}

// Whoami returns the account behind an access token.
func (c *Client) Whoami(ctx context.Context, access string) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/api/users/whoami", nil, "", nil, access, &u)
	return u, err
}

func (c *Client) writeJSON(ctx context.Context, method, path, access string, payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, nil, "application/json", b, access, out)
}

// WrittenRate is the part of a written list entry the caller keeps.
type WrittenRate struct {
	ID        int64     `json:"id"`
	UpdatedAt time.Time `json:"updated_at"` // zero when the answer lacks it
}

// CreateRate adds a list entry.
func (c *Client) CreateRate(ctx context.Context, access string, userID, animeID int64, r RateInput) (WrittenRate, error) {
	type body struct {
		UserID     int64  `json:"user_id"`
		TargetID   int64  `json:"target_id"`
		TargetType string `json:"target_type"`
		RateInput
	}
	var out WrittenRate
	err := c.writeJSON(ctx, http.MethodPost, "/api/v2/user_rates", access,
		map[string]any{"user_rate": body{userID, animeID, "Anime", r}}, &out)
	if err != nil {
		return WrittenRate{}, err
	}
	if out.ID == 0 {
		return WrittenRate{}, errors.New("shikimori: create rate returned no id")
	}
	return out, nil
}

// UpdateRate changes an existing entry.
func (c *Client) UpdateRate(ctx context.Context, access string, rateID int64, r RateInput) (WrittenRate, error) {
	var out WrittenRate
	err := c.writeJSON(ctx, http.MethodPatch, "/api/v2/user_rates/"+strconv.FormatInt(rateID, 10), access,
		map[string]any{"user_rate": r}, &out)
	if err == nil && out.ID == 0 {
		out.ID = rateID
	}
	return out, err
}

// UserRate reads one entry back.
func (c *Client) UserRate(ctx context.Context, access string, rateID int64) (WrittenRate, error) {
	var out WrittenRate
	err := c.do(ctx, http.MethodGet, "/api/v2/user_rates/"+strconv.FormatInt(rateID, 10), nil, "", nil, access, &out)
	return out, err
}

// DeleteRate removes an entry.
func (c *Client) DeleteRate(ctx context.Context, access string, rateID int64) error {
	return c.do(ctx, http.MethodDelete, "/api/v2/user_rates/"+strconv.FormatInt(rateID, 10), nil, "", nil, access, nil)
}
