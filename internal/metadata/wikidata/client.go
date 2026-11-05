// Package wikidata finds books on Wikidata by a title in any language: the
// catalog of Russian titles Open Library lacks («Шантарам» is not on Open
// Library, Wikidata knows it is «Shantaram» by Gregory David Roberts, Open
// Library work OL19061442W). It also reads the Rotten Tomatoes scores of
// movies and series by their IMDb id (reviews.go).
package wikidata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// DefaultMinInterval spaces requests when Client.Limiter is nil (a search
// is up to four requests).
const DefaultMinInterval = 100 * time.Millisecond

const (
	maxRetries = 2 // after HTTP 429 / 503
	searchSize = 12
	maxWorks   = 8
)

var retryDelay = 2 * time.Second

// scholarlyArticle (Q13442814) has authors too, but is not a book.
const scholarlyArticle = "Q13442814"

// Client talks to the Wikidata API (wbsearchentities, wbgetentities).
type Client struct {
	BaseURL   string // https://www.wikidata.org
	UserAgent string // Wikimedia asks for one with a contact
	HTTP      *http.Client
	Limiter   *metadata.Limiter

	once sync.Once
	lim  *metadata.Limiter
}

// Work is a written work found by a search: the work itself when an edition
// (a translation) matched.
type Work struct {
	ID       string // Q2373810
	TitleRU  string // the Russian label or Russian title, "" = none
	TitleEN  string // the English label, "" = none
	Original string // the title in its own language (P1476), "" = unknown
	// AuthorsRU and AuthorsEN: author names in Russian and English (each
	// falls back to the other language when a label is missing).
	AuthorsRU, AuthorsEN []string
	Year                 int    // first publication (P577), 0 = unknown
	OpenLibrary          string // "/works/OL19061442W" (P648), "" = none
	Sitelinks            int    // Wikipedia articles: how known the work is
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

func (c *Client) get(ctx context.Context, q url.Values, out any) error {
	q.Set("format", "json")
	u := strings.TrimRight(c.BaseURL, "/") + "/w/api.php?" + q.Encode()
	g := metadata.Getter{Service: "wikidata", HTTP: c.HTTP, Limiter: c.limiter(), UserAgent: c.UserAgent,
		Retry:      func(s int) bool { return s == http.StatusServiceUnavailable || s == http.StatusTooManyRequests },
		RetryDelay: retryDelay, MaxRetries: maxRetries}
	path := "/w/api.php " + q.Get("action")
	var raw json.RawMessage
	if err := g.JSON(ctx, u, path, &raw); err != nil {
		return err
	}
	// a refused request is HTTP 200 with {"error":{"code","info"}}
	var e struct {
		Error *struct {
			Code string `json:"code"`
			Info string `json:"info"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != nil {
		return fmt.Errorf("wikidata %s: %s: %s", path, e.Error.Code, e.Error.Info)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &metadata.DecodeError{Service: "wikidata", Path: path, Err: err}
	}
	return nil
}

type snak struct {
	DataValue struct {
		Value any `json:"value"`
	} `json:"datavalue"`
}

type claim struct {
	Mainsnak   snak              `json:"mainsnak"`
	Rank       string            `json:"rank"` // preferred | normal | deprecated
	Qualifiers map[string][]snak `json:"qualifiers"`
}

type entity struct {
	ID        string                            `json:"id"`
	Labels    map[string]struct{ Value string } `json:"labels"`
	Claims    map[string][]claim                `json:"claims"`
	Sitelinks map[string]json0                  `json:"sitelinks"`
}

// json0 skips a value.
type json0 struct{}

func (json0) UnmarshalJSON([]byte) error { return nil }

func (e entity) label(lang string) string { return strings.TrimSpace(e.Labels[lang].Value) }

// items are the item ids of property p.
func (e entity) items(p string) []string {
	var out []string
	for _, c := range e.Claims[p] {
		if m, ok := c.Mainsnak.DataValue.Value.(map[string]any); ok {
			if id, _ := m["id"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// str is the first string value of p.
func (e entity) str(p string) string {
	for _, c := range e.Claims[p] {
		if s, ok := c.Mainsnak.DataValue.Value.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// title is the first monolingual title (P1476): text and language.
func (e entity) title() (string, string) {
	for _, c := range e.Claims["P1476"] {
		if m, ok := c.Mainsnak.DataValue.Value.(map[string]any); ok {
			t, _ := m["text"].(string)
			l, _ := m["language"].(string)
			if t = strings.TrimSpace(t); t != "" {
				return t, l
			}
		}
	}
	return "", ""
}

// year is the earliest year of P577.
func (e entity) year() int {
	y := 0
	for _, c := range e.Claims["P577"] {
		m, ok := c.Mainsnak.DataValue.Value.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["time"].(string) // "+2003-00-00T00:00:00Z"
		t = strings.TrimPrefix(t, "+")
		if i := strings.IndexByte(t, '-'); i > 0 {
			if n, err := strconv.Atoi(t[:i]); err == nil && n > 0 && (y == 0 || n < y) {
				y = n
			}
		}
	}
	return y
}

func (c *Client) entities(ctx context.Context, ids []string, props string) (map[string]entity, error) {
	out := map[string]entity{}
	for len(ids) > 0 {
		n := min(len(ids), 50)
		var raw struct {
			Entities map[string]entity `json:"entities"`
		}
		q := url.Values{"action": {"wbgetentities"}, "ids": {strings.Join(ids[:n], "|")}, "props": {props}, "languages": {"ru|en"}}
		if err := c.get(ctx, q, &raw); err != nil {
			return nil, err
		}
		for k, e := range raw.Entities {
			out[k] = e
		}
		ids = ids[n:]
	}
	return out, nil
}

// SearchBooks finds written works whose label or alias matches q (lang:
// the language of q, "ru" or "en"), in Wikidata's order (best match first).
// An edition found (a translation, P629 «edition of») stands for its work.
// Only items with an author (P50) count; scholarly articles do not.
func (c *Client) SearchBooks(ctx context.Context, q, lang string) ([]Work, error) {
	var found struct {
		Search []struct {
			ID string `json:"id"`
		} `json:"search"`
	}
	sq := url.Values{"action": {"wbsearchentities"}, "search": {q}, "language": {lang}, "uselang": {lang},
		"type": {"item"}, "limit": {strconv.Itoa(searchSize)}}
	if err := c.get(ctx, sq, &found); err != nil {
		return nil, err
	}
	if len(found.Search) == 0 {
		return []Work{}, nil
	}
	ids := make([]string, 0, len(found.Search))
	for _, s := range found.Search {
		ids = append(ids, s.ID)
	}
	ents, err := c.entities(ctx, ids, "labels|claims|sitelinks")
	if err != nil {
		return nil, err
	}
	// editions stand for their works: fetch the works not found themselves
	var order, missing []string
	edRU := map[string]string{} // work id -> the Russian title of its edition found
	seen := map[string]bool{}
	for _, id := range ids {
		e, ok := ents[id]
		// a film or a series (a director, P57) credits the book's author too
		if !ok || len(e.items("P50")) == 0 || len(e.Claims["P57"]) > 0 || slices.Contains(e.items("P31"), scholarlyArticle) {
			continue
		}
		work := id
		if of := e.items("P629"); len(of) > 0 {
			work = of[0]
			if t, l := e.title(); l == "ru" && edRU[work] == "" {
				edRU[work] = t
			} else if t := e.label("ru"); t != "" && edRU[work] == "" {
				edRU[work] = t
			}
			if _, have := ents[work]; !have && !seen[work] {
				missing = append(missing, work)
			}
		}
		if !seen[work] {
			seen[work] = true
			order = append(order, work)
		}
	}
	if len(missing) > 0 {
		more, err := c.entities(ctx, missing, "labels|claims|sitelinks")
		if err != nil {
			return nil, err
		}
		for k, e := range more {
			ents[k] = e
		}
	}
	if len(order) > maxWorks {
		order = order[:maxWorks]
	}
	var authorIDs []string
	for _, id := range order {
		for _, a := range ents[id].items("P50") {
			if !slices.Contains(authorIDs, a) {
				authorIDs = append(authorIDs, a)
			}
		}
	}
	authors := map[string]entity{}
	if len(authorIDs) > 0 {
		if authors, err = c.entities(ctx, authorIDs, "labels"); err != nil {
			return nil, err
		}
	}
	out := make([]Work, 0, len(order))
	for _, id := range order {
		e, ok := ents[id]
		if !ok {
			continue
		}
		w := Work{ID: id, TitleRU: e.label("ru"), TitleEN: e.label("en"), Year: e.year(), Sitelinks: len(e.Sitelinks)}
		if t, l := e.title(); t != "" {
			w.Original = t
			if l == "ru" && w.TitleRU == "" {
				w.TitleRU = t
			}
		}
		if w.TitleRU == "" {
			w.TitleRU = edRU[id]
		}
		if ol := e.str("P648"); strings.HasPrefix(ol, "OL") && strings.HasSuffix(ol, "W") {
			w.OpenLibrary = "/works/" + ol
		}
		for _, a := range e.items("P50") {
			ae := authors[a]
			ru, en := ae.label("ru"), ae.label("en")
			if ru == "" {
				ru = en
			}
			if en == "" {
				en = ru
			}
			if ru != "" {
				w.AuthorsRU = append(w.AuthorsRU, ru)
				w.AuthorsEN = append(w.AuthorsEN, en)
			}
		}
		if w.TitleRU == "" && w.TitleEN == "" && w.Original == "" {
			continue
		}
		out = append(out, w)
	}
	return out, nil
}
