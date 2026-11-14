// Package myshows is a small read-only client for the MyShows JSON-RPC API
// (public profile lists, no authentication) and the cache that maps its
// shows to TMDB titles. All clients share one process-wide rate limiter.
package myshows

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
const DefaultBase = "https://api.myshows.me/v2/rpc/"

const (
	userAgent  = "Antresol"
	maxRetries = 3
	maxBody    = 32 << 20
	maxErrBody = 200
	// minGap is the spacing between requests (1 req/s; the official limit
	// is unknown, so stay gentle).
	minGap = time.Second
)

var (
	ErrNotFound    = errors.New("myshows: not found")
	ErrPrivate     = errors.New("myshows: profile is private")
	ErrRateLimited = errors.New("myshows: rate limited")
	// ErrUnavailable: the service did not answer (transport error, HTTP 5xx).
	ErrUnavailable = errors.New("myshows: unavailable")
)

// Unavailable reports an error that says the service is struggling
// (transport, 5xx or rate limit) rather than an answer about the request.
func Unavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, ErrRateLimited)
}

// Entry is one title of a profile's list.
type Entry struct {
	ShowID          int64
	Title           string
	TitleOriginal   string
	Category        string // "anime", "shows", ...
	Year            int
	Status          string // watching | later | finished | cancelled
	Rating          int    // 0..5, 0 = unrated
	WatchedEpisodes int
	TotalEpisodes   int
}

// ShowInfo is the part of a show the mapping needs.
type ShowInfo struct {
	ID            int64
	IMDbID        int64 // numeric part of "tt…"; 0 = none
	Year          int
	Title         string
	TitleOriginal string
}

// Client talks to one MyShows base URL.
type Client struct {
	Base    string
	HTTP    *http.Client
	Backoff time.Duration // base delay after HTTP 429 without Retry-After (default 1s)

	limiter *limiter
}

// New returns a client bound to the process-wide shared limiter. A nil hc
// gets a client with a 30 s timeout; an empty base means DefaultBase.
func New(base string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	if base == "" {
		base = DefaultBase
	}
	return &Client{Base: base, HTTP: hc, limiter: shared}
}

// limiter spaces requests at least minGap apart.
type limiter struct {
	mu    sync.Mutex
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	last  time.Time
}

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

// wait blocks until one more request is allowed, then records it. The lock
// is released while sleeping so waiters honour their own context.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	for {
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		now := l.now()
		d := l.last.Add(minGap).Sub(now)
		if l.last.IsZero() || d <= 0 {
			l.last = now
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

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// mapRPCError turns a JSON-RPC error object into a package error. Live
// shape for an unknown login: HTTP 200, code 404, data "user was not found".
func mapRPCError(method string, e *rpcError) error {
	text := strings.ToLower(e.Message + " " + string(e.Data))
	switch {
	case e.Code == 404 || strings.Contains(text, "not found"):
		return ErrNotFound
	case e.Code == 403 || e.Code == 401 || strings.Contains(text, "private") || strings.Contains(text, "forbidden"):
		return ErrPrivate
	case e.Code == 429:
		return ErrRateLimited
	}
	return fmt.Errorf("myshows %s: rpc error %d: %s", method, e.Code, truncate([]byte(e.Message+" "+string(e.Data))))
}

// call performs one rate-limited JSON-RPC request with 429 retries and
// decodes the result into out.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": 1})
	if err != nil {
		return err
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("myshows %s: bad request", method)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return fmt.Errorf("myshows %s: %w: %v", method, ErrUnavailable, err)
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
		case resp.StatusCode >= 500:
			return fmt.Errorf("myshows %s: %w: status %d: %s", method, ErrUnavailable, resp.StatusCode, truncate(b))
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return fmt.Errorf("myshows %s: status %d: %s", method, resp.StatusCode, truncate(b))
		}
		if rerr != nil {
			return fmt.Errorf("myshows %s: read: %v", method, rerr)
		}
		var env struct {
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(b, &env); err != nil {
			return fmt.Errorf("myshows %s: bad json", method)
		}
		if env.Error != nil {
			return mapRPCError(method, env.Error)
		}
		if out == nil {
			return nil
		}
		if r := bytes.TrimSpace(env.Result); len(r) == 0 || bytes.Equal(r, []byte("null")) {
			// never read as an empty list (that would look like
			// everything removed)
			return fmt.Errorf("myshows %s: empty result", method)
		}
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("myshows %s: bad result", method)
		}
		return nil
	}
}

// ProfileShows returns a public profile's whole list.
func (c *Client) ProfileShows(ctx context.Context, login string) ([]Entry, error) {
	var raw []struct {
		Show struct {
			ID            int64  `json:"id"`
			Title         string `json:"title"`
			TitleOriginal string `json:"titleOriginal"`
			Year          int    `json:"year"`
			Category      string `json:"category"`
		} `json:"show"`
		WatchStatus     string `json:"watchStatus"`
		Rating          int    `json:"rating"`
		TotalEpisodes   int    `json:"totalEpisodes"`
		WatchedEpisodes int    `json:"watchedEpisodes"`
	}
	if err := c.call(ctx, "profile.Shows", map[string]any{"login": login}, &raw); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(raw))
	for _, r := range raw {
		out = append(out, Entry{
			ShowID: r.Show.ID, Title: r.Show.Title, TitleOriginal: r.Show.TitleOriginal,
			Category: r.Show.Category, Year: r.Show.Year, Status: r.WatchStatus, Rating: r.Rating,
			WatchedEpisodes: r.WatchedEpisodes, TotalEpisodes: r.TotalEpisodes,
		})
	}
	return out, nil
}

// Show fetches one show (without episodes).
func (c *Client) Show(ctx context.Context, id int64) (ShowInfo, error) {
	var r struct {
		ID            int64  `json:"id"`
		IMDbID        int64  `json:"imdbId"`
		Year          int    `json:"year"`
		Title         string `json:"title"`
		TitleOriginal string `json:"titleOriginal"`
	}
	if err := c.call(ctx, "shows.GetById", map[string]any{"showId": id, "withEpisodes": false}, &r); err != nil {
		return ShowInfo{}, err
	}
	return ShowInfo{ID: r.ID, IMDbID: r.IMDbID, Year: r.Year, Title: r.Title, TitleOriginal: r.TitleOriginal}, nil
}
