package goodreads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is used when no base URL is configured.
const DefaultBase = "https://www.goodreads.com"

const (
	userAgent = "Antresol"
	maxBody   = 8 << 20
	// WidgetBooks is how many books of a shelf the widget gives (its own
	// ceiling: asking for more returns the same hundred).
	WidgetBooks = 100
)

var (
	// ErrNotFound: Goodreads has no public shelf widget for the user id
	// (no such user, or the profile is not public).
	ErrNotFound = errors.New("goodreads: profile not found")
	// ErrUnavailable: Goodreads did not answer (transport, 5xx, rate limit).
	ErrUnavailable = errors.New("goodreads: unavailable")
	// ErrFormat: the widget answered but not in the shape this reader knows
	// (Goodreads changed or retired it, as it did the RSS feeds).
	ErrFormat = errors.New("goodreads: unexpected widget")
)

// Widget reads the public shelf widget of a profile: the embeddable list
// (/review/custom_widget/<user id>) a public profile offers other sites. It
// is no API and gives less than the export: title and author, no ISBN, no
// dates, the hundred newest books of a shelf.
type Widget struct {
	Base string
	HTTP *http.Client
}

// NewWidget returns a reader; an empty base means DefaultBase, "off" no
// reads at all (every one is ErrUnavailable: a test server never reaches the
// internet) and a nil client one with a 30 s timeout.
func NewWidget(base string, hc *http.Client) *Widget {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	if base == "" {
		base = DefaultBase
	}
	return &Widget{Base: strings.TrimRight(base, "/"), HTTP: hc}
}

// Shelf reads one shelf (read, currently-reading or to-read) of a user.
func (w *Widget) Shelf(ctx context.Context, userID int64, shelf string) ([]Entry, error) {
	if w.Base == "off" {
		return nil, fmt.Errorf("%w: switched off", ErrUnavailable)
	}
	// Goodreads caches a widget's list by its id and ignores the other
	// parameters of a later request with the same id: every read is a new one
	var id [8]byte
	rand.Read(id[:])
	q := url.Values{"shelf": {shelf}, "num_books": {strconv.Itoa(WidgetBooks)}, "widget_id": {"a" + hex.EncodeToString(id[:])}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		w.Base+"/review/custom_widget/"+strconv.FormatInt(userID, 10)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := w.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: status %d", ErrFormat, resp.StatusCode)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	case len(body) > maxBody:
		return nil, fmt.Errorf("%w: answer too large", ErrFormat)
	}
	return parseWidget(string(body), shelf)
}

var (
	reTitle  = regexp.MustCompile(`(?s)class="gr_custom_title_[^"]*">\s*<a [^>]*>(.*?)</a>`)
	reAuthor = regexp.MustCompile(`(?s)class="gr_custom_author_[^"]*">\s*by\s*<a [^>]*>(.*?)</a>`)
	reTags   = regexp.MustCompile(`<[^>]*>`)
	// the widget script writes its HTML as a JavaScript string
	jsEscapes = strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\/`, `/`, `\n`, "\n", `\r`, "", `\t`, " ", `\\`, `\`)
)

// parseWidget reads the widget's script into entries of shelf. An answer
// that says the widget is "not correct" is an unknown or non-public user; an
// answer with books but none this reader can read is ErrFormat.
func parseWidget(js, shelf string) ([]Entry, error) {
	if !strings.Contains(js, "widget_code") {
		return nil, ErrFormat
	}
	page := jsEscapes.Replace(js)
	if strings.Contains(page, "widget is not correct") {
		return nil, ErrNotFound
	}
	const marker = `gr_custom_each_container_`
	parts := strings.Split(page, marker)
	var out []Entry
	for _, p := range parts[1:] {
		t, a := reTitle.FindStringSubmatch(p), reAuthor.FindStringSubmatch(p)
		if t == nil {
			continue
		}
		e := Entry{Title: clean(text(t[1])), Shelf: shelf}
		if a != nil {
			e.Author = clean(text(a[1]))
		}
		if e.Title != "" {
			out = append(out, e)
		}
	}
	if len(parts) > 1 && len(out) == 0 {
		return nil, ErrFormat
	}
	return out, nil
}

func text(s string) string { return html.UnescapeString(reTags.ReplaceAllString(s, "")) }
