// Package openlibrary is a minimal Open Library client (search, works,
// covers) and the book and audiobook enricher built on it.
package openlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// DefaultMinInterval spaces requests when Client.Limiter is nil (Open
// Library asks for moderation; 2 requests per second).
const DefaultMinInterval = 500 * time.Millisecond

const (
	maxRetries   = 3 // after HTTP 503 / 429
	searchFields = "key,title,author_name,first_publish_year,cover_i,isbn,language,subject"
)

// retryDelay is the pause before repeating a 503/429 request. A var for tests.
var retryDelay = 2 * time.Second

// ErrNotFound is returned for HTTP 404.
var ErrNotFound = metadata.ErrNotFound

// HTTPError is an answer other than 200 and 404 (after the retries).
type HTTPError = metadata.HTTPError

// Client talks to Open Library. All its requests share Limiter (nil: one
// is created with DefaultMinInterval).
type Client struct {
	BaseURL    string // https://openlibrary.org
	CoversBase string // https://covers.openlibrary.org ("" = no covers)
	UserAgent  string
	HTTP       *http.Client
	Limiter    *metadata.Limiter

	once sync.Once
	lim  *metadata.Limiter
}

// Doc is one search result (a work).
type Doc struct {
	Key       string // "/works/OL27448W"
	Title     string
	Authors   []string
	Year      int // first publish year, 0 = unknown
	CoverID   int // 0 = none
	ISBN      []string
	Languages []string // MARC codes: "rus", "eng"
	Subjects  []string // "Fiction", "Erotica"… (what a kid profile's search filters by)
}

// Work is the part of a work record the enricher uses.
type Work struct {
	Description string
	Subjects    []string
}

func (c *Client) limiter() *metadata.Limiter {
	c.once.Do(func() {
		c.lim = c.Limiter
		if c.lim == nil {
			c.lim = &metadata.Limiter{Interval: DefaultMinInterval}
		}
	})
	return c.lim
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	u := strings.TrimRight(c.BaseURL, "/") + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	g := metadata.Getter{Service: "openlibrary", HTTP: c.HTTP, Limiter: c.limiter(), UserAgent: c.UserAgent,
		Retry:      func(s int) bool { return s == http.StatusServiceUnavailable || s == http.StatusTooManyRequests },
		RetryDelay: retryDelay, MaxRetries: maxRetries}
	return g.JSON(ctx, u, p, out)
}

func (c *Client) search(ctx context.Context, q url.Values) ([]Doc, error) {
	q.Set("fields", searchFields)
	var raw struct {
		Docs []struct {
			Key       string   `json:"key"`
			Title     string   `json:"title"`
			Authors   []string `json:"author_name"`
			Year      int      `json:"first_publish_year"`
			CoverID   int      `json:"cover_i"`
			ISBN      []string `json:"isbn"`
			Languages []string `json:"language"`
			Subjects  []string `json:"subject"`
		} `json:"docs"`
	}
	if err := c.get(ctx, "/search.json", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Doc, 0, len(raw.Docs))
	for _, d := range raw.Docs {
		out = append(out, Doc(d))
	}
	return out, nil
}

// Search looks a work up by title and author ("" = any author).
func (c *Client) Search(ctx context.Context, title, author string) ([]Doc, error) {
	q := url.Values{"title": {title}, "limit": {"5"}}
	if author = strings.TrimSpace(author); author != "" {
		q.Set("author", author)
	}
	return c.search(ctx, q)
}

// SearchQ runs a free-text search (the user's query).
func (c *Client) SearchQ(ctx context.Context, q string) ([]Doc, error) {
	return c.search(ctx, url.Values{"q": {q}, "limit": {"10"}})
}

var workKey = regexp.MustCompile(`^(?:/works/)?(OL\d+W)$`)

// Work fetches a work record by its key ("/works/OL1W" or "OL1W").
// Description may be a string or a {"type","value"} object upstream.
func (c *Client) Work(ctx context.Context, key string) (Work, error) {
	m := workKey.FindStringSubmatch(strings.TrimSpace(key))
	if m == nil {
		return Work{}, fmt.Errorf("openlibrary: bad work key %q", key)
	}
	var raw struct {
		Description json.RawMessage `json:"description"`
		Subjects    []string        `json:"subjects"`
	}
	if err := c.get(ctx, "/works/"+m[1]+".json", nil, &raw); err != nil {
		return Work{}, err
	}
	w := Work{Subjects: raw.Subjects}
	var s string
	if json.Unmarshal(raw.Description, &s) != nil {
		var obj struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(raw.Description, &obj) == nil {
			s = obj.Value
		}
	}
	w.Description = strings.TrimSpace(s)
	return w, nil
}

// CoverURL is the large cover of a cover id, "" without CoversBase or id.
// default=false makes a missing cover a 404 instead of a placeholder.
func (c *Client) CoverURL(coverID int) string {
	if c.CoversBase == "" || coverID <= 0 {
		return ""
	}
	return strings.TrimRight(c.CoversBase, "/") + "/b/id/" + strconv.Itoa(coverID) + "-L.jpg?default=false"
}
