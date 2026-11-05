package tmdb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// genreTTL is how long a language's genre list is kept: TMDB changes it
// rarely, and a fresh list costs one request per kind and language.
const genreTTL = 24 * time.Hour

// genreRetry: after a failed fetch, the list is not asked for again this
// long (with TMDB unreachable every card and page would otherwise wait
// for it; they fall back to the stored genre names).
const genreRetry = 5 * time.Minute

type genreList struct {
	names map[int]string
	at    time.Time
}

// genreCache holds TMDB's genre lists per kind and language.
type genreCache struct {
	mu     sync.Mutex
	lists  map[string]genreList
	failed map[string]time.Time // key → last failed fetch
}

// GenreNames is TMDB's genre id → name list for kind ("movie" | "tv") in
// the lookup language (WithLanguage, else the client's), cached for a day.
// A failed fetch returns the last list it had, if any, with the error.
func (c *Client) GenreNames(ctx context.Context, kind string) (map[int]string, error) {
	if kind != "movie" && kind != "tv" {
		return nil, fmt.Errorf("tmdb: genre list of %q", kind)
	}
	lang := c.language(ctx)
	key := kind + "/" + lang
	c.genres.mu.Lock()
	cached, ok := c.genres.lists[key]
	failedAt, failed := c.genres.failed[key]
	c.genres.mu.Unlock()
	if ok && time.Since(cached.at) < genreTTL {
		return cached.names, nil
	}
	if failed && time.Since(failedAt) < genreRetry {
		return cached.names, fmt.Errorf("tmdb: genre list %s: backing off after a failure", key)
	}
	var raw struct {
		Genres []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"genres"`
	}
	if err := c.get(ctx, "/genre/"+kind+"/list", map[string][]string{"language": {lang}}, &raw); err != nil {
		if !errors.Is(ctx.Err(), context.Canceled) { // a deadline counts: TMDB was too slow
			c.genres.mu.Lock()
			if c.genres.failed == nil {
				c.genres.failed = map[string]time.Time{}
			}
			c.genres.failed[key] = time.Now()
			c.genres.mu.Unlock()
		}
		return cached.names, err
	}
	names := make(map[int]string, len(raw.Genres))
	for _, g := range raw.Genres {
		names[g.ID] = g.Name
	}
	c.genres.mu.Lock()
	if c.genres.lists == nil {
		c.genres.lists = map[string]genreList{}
	}
	c.genres.lists[key] = genreList{names: names, at: time.Now()}
	delete(c.genres.failed, key)
	c.genres.mu.Unlock()
	return names, nil
}
