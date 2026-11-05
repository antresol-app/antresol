// Package lyrics finds song words: LRCLIB (synced LRC or plain), cached in
// memory, and parses LRC into timed lines; Auto fetches them for the
// library's tracks in the background and keeps them (track_lyrics).
package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

// Line is one timed lyric line; T is seconds from the track start.
type Line struct {
	T    float64 `json:"t"`
	Text string  `json:"text"`
}

var (
	tagRe    = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	offsetRe = regexp.MustCompile(`(?m)^\s*\[offset:\s*([+-]?\d+)\s*\]\s*$`)
	// Synced matches text with at least one [mm:ss(.xx)] line.
	Synced = library.LRCSynced
)

// Parse reads LRC text into lines sorted by time; nil when untimed.
func Parse(text string) []Line {
	shift := 0.0
	if m := offsetRe.FindStringSubmatch(text); m != nil {
		ms, _ := strconv.ParseFloat(m[1], 64)
		shift = ms / 1000
	}
	var out []Line
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		rest := strings.TrimSpace(raw)
		var times []float64
		for {
			m := tagRe.FindStringSubmatch(rest)
			if m == nil {
				break
			}
			min, _ := strconv.ParseFloat(m[1], 64)
			sec, _ := strconv.ParseFloat(m[2], 64)
			frac := 0.0
			if m[3] != "" {
				f, _ := strconv.ParseFloat(m[3], 64)
				d := 1.0
				for range m[3] {
					d *= 10
				}
				frac = f / d
			}
			times = append(times, min*60+sec+frac)
			rest = rest[len(m[0]):]
		}
		line := strings.TrimSpace(rest)
		for _, t := range times {
			if t -= shift; t < 0 {
				t = 0
			}
			out = append(out, Line{T: t, Text: line})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// Result is what LRCLIB knows about a track.
type Result struct {
	Synced string // LRC text, "" when none
	Plain  string
}

// Query identifies a track.
type Query struct {
	Artist, Title, Album string
	DurationSec          int
}

// Client talks to LRCLIB. A nil Client finds nothing.
type Client struct {
	Base string // https://lrclib.net
	HTTP *http.Client
	UA   string
	// Limit spaces the requests (the background queue: polite to the
	// public service); nil never waits.
	Limit *metadata.Limiter

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	r  *Result
	at time.Time
}

const (
	hitTTL  = 30 * 24 * time.Hour
	missTTL = 24 * time.Hour
	maxKeep = 4096
)

// New returns a client for the public LRCLIB.
func New(ua string) *Client { return NewAt("https://lrclib.net", ua) }

// NewAt returns a client for the LRCLIB at base (an own instance or the
// public one).
func NewAt(base, ua string) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: 8 * time.Second}, UA: ua}
}

type rec struct {
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, v any) (bool, error) {
	ok, err := c.getOnce(ctx, path, q, v)
	if errors.Is(err, errBusy) {
		// LRCLIB answers 5xx under load now and then; one retry after a beat
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
		ok, err = c.getOnce(ctx, path, q, v)
	}
	return ok, err
}

var errBusy = errors.New("lrclib busy")

func (c *Client) getOnce(ctx context.Context, path string, q url.Values, v any) (bool, error) {
	if err := c.Limit.Wait(ctx); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path+"?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 500 {
		return false, fmt.Errorf("%w: %s", errBusy, resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("lrclib: %s", resp.Status)
	}
	return true, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func (r rec) result() *Result {
	if r.Instrumental {
		return nil
	}
	if strings.TrimSpace(r.SyncedLyrics) == "" && strings.TrimSpace(r.PlainLyrics) == "" {
		return nil
	}
	return &Result{Synced: strings.TrimSpace(r.SyncedLyrics), Plain: strings.TrimSpace(r.PlainLyrics)}
}

// Find returns lyrics for q, or nil when LRCLIB has none. Errors (network)
// are not cached.
func (c *Client) Find(ctx context.Context, q Query) (*Result, error) {
	if c == nil || strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	key := strings.ToLower(fmt.Sprintf("%s|%s|%s|%d", q.Artist, q.Title, q.Album, q.DurationSec))
	c.mu.Lock()
	if e, ok := c.cache[key]; ok {
		ttl := hitTTL
		if e.r == nil {
			ttl = missTTL
		}
		if time.Since(e.at) < ttl {
			c.mu.Unlock()
			return e.r, nil
		}
	}
	c.mu.Unlock()

	r, err := c.fetch(ctx, q)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.cache == nil || len(c.cache) >= maxKeep {
		c.cache = map[string]cached{}
	}
	c.cache[key] = cached{r, time.Now()}
	c.mu.Unlock()
	return r, nil
}

func (c *Client) fetch(ctx context.Context, q Query) (*Result, error) {
	p := url.Values{"artist_name": {q.Artist}, "track_name": {q.Title}}
	if q.Album != "" {
		p.Set("album_name", q.Album)
	}
	if q.DurationSec > 0 {
		p.Set("duration", strconv.Itoa(q.DurationSec))
	}
	var one rec
	ok, err := c.getJSON(ctx, "/api/get", p, &one)
	if err != nil {
		return nil, err
	}
	if ok {
		if r := one.result(); r != nil {
			return r, nil
		}
	}
	// fallback: search, duration within 5 s, synced preferred
	sp := url.Values{"track_name": {q.Title}}
	if q.Artist != "" {
		sp.Set("artist_name", q.Artist)
	}
	var list []rec
	if _, err := c.getJSON(ctx, "/api/search", sp, &list); err != nil {
		return nil, err
	}
	// «Stevie Ray Vaughan And Double Trouble» is filed under the lead artist
	if len(list) == 0 && q.Artist != "" {
		if lead := leadArtist(q.Artist); lead != q.Artist {
			sp.Set("artist_name", lead)
			if _, err := c.getJSON(ctx, "/api/search", sp, &list); err != nil {
				return nil, err
			}
		}
	}
	var best *Result
	bestScore := -1.0
	for _, r := range list {
		res := r.result()
		if res == nil {
			continue
		}
		d := 0.0
		if q.DurationSec > 0 && r.Duration > 0 {
			d = r.Duration - float64(q.DurationSec)
			if d < 0 {
				d = -d
			}
			if d > 5 {
				continue
			}
		}
		score := 10 - d
		if res.Synced != "" {
			score += 20
		}
		if score > bestScore {
			best, bestScore = res, score
		}
	}
	return best, nil
}

// leadArtist trims band suffixes and features: «A And B», «A & B», «A feat. B».
func leadArtist(a string) string {
	low := strings.ToLower(a)
	cut := len(a)
	for _, sep := range []string{" and ", " & ", " feat. ", " feat ", " ft. ", " featuring ", ", ", " with "} {
		if i := strings.Index(low, sep); i > 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(a[:cut])
}
