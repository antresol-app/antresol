package tmdb

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"
)

// TitleRef is a TMDB movie or show named around another title: one of its
// recommendations or similar titles, or a part of its collection. Images
// are TMDB paths (Client.ImageURL).
type TitleRef struct {
	ID       int    `json:"id"`
	Kind     string `json:"kind"` // movie | tv
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Date     string `json:"date,omitempty"` // release or first air date ("" = unknown)
	Poster   string `json:"poster,omitempty"`
	Backdrop string `json:"backdrop,omitempty"`
	GenreIDs []int  `json:"genres,omitempty"`
}

// CollectionRef is the collection (a franchise) a movie belongs to.
type CollectionRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RelatedTitles are the titles around a movie or show: TMDB's
// recommendations, then its similar titles (no repeats, never the title
// itself or an adult one), and a movie's collection.
type RelatedTitles struct {
	Similar    []TitleRef     `json:"similar,omitempty"`
	Collection *CollectionRef `json:"collection,omitempty"`
}

// Collection is a TMDB collection with its parts in release order (the
// undated, announced ones last).
type Collection struct {
	ID       int        `json:"id"`
	Name     string     `json:"name"`
	Overview string     `json:"overview,omitempty"`
	Poster   string     `json:"poster,omitempty"`
	Backdrop string     `json:"backdrop,omitempty"`
	Parts    []TitleRef `json:"parts,omitempty"`
}

// Related defaults.
const (
	relatedTTL   = 7 * 24 * time.Hour
	relatedWait  = 4 * time.Second
	relatedRetry = 10 * time.Minute
	relatedMem   = 1024
	// relatedRefresh bounds a background fetch (a refresh, a prefetch).
	relatedRefresh = 20 * time.Second
)

// Related caches RelatedTitles and Collections per language (table
// tmdb_related), fetched from TMDB on demand. A missing entry is fetched
// within a short wait; a stale one is served at once and refreshed in the
// background. Readers of one entry share one fetch. A TMDB failure (not a
// reader giving up, not running out of time) is not retried for a while;
// a title TMDB does not know is kept as empty. Close it before the
// database.
type Related struct {
	Client *Client
	DB     *sql.DB
	// TTL: an entry older than this is refreshed; 0: 7 days.
	TTL time.Duration
	// Wait bounds how long a reader waits for a missing entry; 0: 4 s.
	Wait time.Duration

	mu     sync.Mutex
	mem    map[string]cachedRelated
	failed map[string]time.Time    // key → last TMDB failure
	calls  map[string]*relatedCall // fetches running
	wg     sync.WaitGroup
	closed bool
}

type cachedRelated struct {
	data json.RawMessage
	at   time.Time
}

// relatedCall is one fetch, shared by its readers: done closes when c or
// err is set.
type relatedCall struct {
	done chan struct{}
	c    cachedRelated
	err  error
}

// Language is the client's TMDB tag (the library's metadata language).
func (s *Related) Language() string { return s.Client.language(context.Background()) }

// ImageURL is Client.ImageURL.
func (s *Related) ImageURL(size, p string) string { return s.Client.ImageURL(size, p) }

// Close stops the background fetches and waits for those running (the
// database may be closed after it). Later misses fetch nothing.
func (s *Related) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wg.Wait()
}

// idle waits for the fetches running (tests).
func (s *Related) idle() { s.wg.Wait() }

// failures is the number of TMDB failures remembered (tests).
func (s *Related) failures() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.failed)
}

func relatedKey(scope string, id int, tag string) string {
	return scope + "/" + strconv.Itoa(id) + "/" + tag
}

// Titles are the titles around a movie or show ("movie" | "tv") in the
// language tag; false: none known (TMDB failing or slow with nothing
// cached). A movie's collection is fetched along (Collection then finds
// it, or its fetch running).
func (s *Related) Titles(ctx context.Context, kind string, id int, tag string) (RelatedTitles, bool) {
	if kind != "movie" && kind != "tv" {
		return RelatedTitles{}, false
	}
	var out RelatedTitles
	ok := s.get(ctx, kind, id, tag, &out, func(ctx context.Context) (any, error) {
		t, err := s.Client.relatedTitles(ctx, kind, id, tag)
		if err == nil && t.Collection != nil {
			s.prefetchCollection(context.WithoutCancel(ctx), t.Collection.ID, tag)
		}
		return t, err
	})
	return out, ok
}

// Collection is a collection with its parts in the language tag; false:
// none known.
func (s *Related) Collection(ctx context.Context, id int, tag string) (Collection, bool) {
	var out Collection
	ok := s.get(ctx, "collection", id, tag, &out, s.collectionFetch(id, tag))
	return out, ok
}

func (s *Related) collectionFetch(id int, tag string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) { return s.Client.collection(ctx, id, tag) }
}

// prefetchCollection starts the collection's fetch unless it is cached
// and fresh.
func (s *Related) prefetchCollection(ctx context.Context, id int, tag string) {
	key := relatedKey("collection", id, tag)
	if c, ok := s.cached(ctx, key, "collection", id, tag); ok && time.Since(c.at) < s.ttl() {
		return
	}
	s.start(key, "collection", id, tag, s.collectionFetch(id, tag), relatedRefresh)
}

func (s *Related) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return relatedTTL
}

func (s *Related) wait() time.Duration {
	if s.Wait > 0 {
		return s.Wait
	}
	return relatedWait
}

// get decodes the entry into out: the cached one (a stale one is
// refreshed in the background), else the one fetched within Wait.
func (s *Related) get(ctx context.Context, scope string, id int, tag string, out any, fetch func(context.Context) (any, error)) bool {
	key := relatedKey(scope, id, tag)
	c, ok := s.cached(ctx, key, scope, id, tag)
	switch {
	case ok && time.Since(c.at) >= s.ttl():
		s.start(key, scope, id, tag, fetch, relatedRefresh)
	case !ok:
		call := s.start(key, scope, id, tag, fetch, s.wait())
		if call == nil {
			return false
		}
		timer := time.NewTimer(s.wait())
		defer timer.Stop()
		select {
		case <-call.done:
		case <-ctx.Done():
			return false
		case <-timer.C: // joined a slower background fetch
			return false
		}
		if call.err != nil {
			return false
		}
		c = call.c
	}
	if err := json.Unmarshal(c.data, out); err != nil {
		log.Printf("tmdb: related %s: %v", key, err)
		return false
	}
	return true
}

// start is the fetch of key running, else a new one bounded by wait; nil
// while TMDB failed lately for key, or after Close. The fetch outlives the
// reader that started it (its readers may give up, it does not).
func (s *Related) start(key, scope string, id int, tag string, fetch func(context.Context) (any, error), wait time.Duration) *relatedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if call := s.calls[key]; call != nil {
		return call
	}
	if at, ok := s.failed[key]; ok && time.Since(at) < relatedRetry {
		return nil
	}
	if s.calls == nil {
		s.calls = map[string]*relatedCall{}
	}
	call := &relatedCall{done: make(chan struct{})}
	s.calls[key] = call
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		call.c, call.err = s.fetch(ctx, key, scope, id, tag, fetch)
		cancel()
		s.mu.Lock()
		delete(s.calls, key)
		s.mu.Unlock()
		close(call.done)
	}()
	return call
}

func (s *Related) cached(ctx context.Context, key, scope string, id int, tag string) (cachedRelated, bool) {
	s.mu.Lock()
	c, ok := s.mem[key]
	s.mu.Unlock()
	if ok || s.DB == nil {
		return c, ok
	}
	var data string
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT data, fetched_at FROM tmdb_related WHERE scope=? AND tmdb_id=? AND lang=?`,
		scope, id, tag).Scan(&data, &at)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			log.Printf("tmdb: related %s: %v", key, err)
		}
		return cachedRelated{}, false
	}
	c = cachedRelated{data: json.RawMessage(data), at: time.Unix(at, 0)}
	s.remember(key, c)
	return c, true
}

func (s *Related) remember(key string, c cachedRelated) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mem == nil || len(s.mem) >= relatedMem {
		s.mem = map[string]cachedRelated{} // the table still has them
	}
	s.mem[key] = c
}

// noted records the outcome of a fetch: a TMDB failure is remembered (and
// the expired ones dropped), a success forgets it. Running out of time is
// not TMDB's answer: nothing is remembered.
func (s *Related) noted(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err == nil:
		delete(s.failed, key)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
	default:
		now := time.Now()
		for k, at := range s.failed {
			if now.Sub(at) >= relatedRetry {
				delete(s.failed, k)
			}
		}
		if s.failed == nil {
			s.failed = map[string]time.Time{}
		}
		s.failed[key] = now
	}
}

func (s *Related) fetch(ctx context.Context, key, scope string, id int, tag string, fetch func(context.Context) (any, error)) (cachedRelated, error) {
	v, err := fetch(ctx)
	if errors.Is(err, ErrNotFound) {
		v, err = struct{}{}, nil
	}
	var b []byte
	if err == nil {
		b, err = json.Marshal(v)
	}
	s.noted(key, err)
	if err != nil {
		log.Printf("tmdb: related %s: %v", key, err)
		return cachedRelated{}, err
	}
	c := cachedRelated{data: b, at: time.Now()}
	s.remember(key, c)
	if s.DB != nil {
		if _, err := s.DB.ExecContext(context.WithoutCancel(ctx), `INSERT INTO tmdb_related (scope, tmdb_id, lang, data, fetched_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (scope, tmdb_id, lang) DO UPDATE SET data=excluded.data, fetched_at=excluded.fetched_at`,
			scope, id, tag, string(b), c.at.Unix()); err != nil {
			log.Printf("tmdb: save related %s: %v", key, err)
		}
	}
	return c, nil
}

type rawRef struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
	GenreIDs     []int  `json:"genre_ids"`
	Adult        bool   `json:"adult"`
}

func (r rawRef) ref(kind string) TitleRef {
	d := first(r.ReleaseDate, r.FirstAirDate)
	return TitleRef{ID: r.ID, Kind: kind, Title: first(r.Title, r.Name), Year: yearOf(d), Date: d,
		Poster: r.PosterPath, Backdrop: r.BackdropPath, GenreIDs: r.GenreIDs}
}

// relatedTitles is one request: the details with recommendations and
// similar appended (a movie's details carry its collection).
func (c *Client) relatedTitles(ctx context.Context, kind string, id int, tag string) (RelatedTitles, error) {
	var raw struct {
		Collection *struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"belongs_to_collection"`
		Recommendations struct {
			Results []rawRef `json:"results"`
		} `json:"recommendations"`
		Similar struct {
			Results []rawRef `json:"results"`
		} `json:"similar"`
	}
	v := url.Values{"language": {tag}, "append_to_response": {"recommendations,similar"}}
	if err := c.get(ctx, fmt.Sprintf("/%s/%d", kind, id), v, &raw); err != nil {
		return RelatedTitles{}, err
	}
	var out RelatedTitles
	if raw.Collection != nil && raw.Collection.ID > 0 {
		out.Collection = &CollectionRef{ID: raw.Collection.ID, Name: raw.Collection.Name}
	}
	seen := map[int]bool{id: true}
	for _, r := range slices.Concat(raw.Recommendations.Results, raw.Similar.Results) {
		if r.ID <= 0 || r.Adult || seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		out.Similar = append(out.Similar, r.ref(kind))
	}
	return out, nil
}

func (c *Client) collection(ctx context.Context, id int, tag string) (Collection, error) {
	var raw struct {
		ID           int      `json:"id"`
		Name         string   `json:"name"`
		Overview     string   `json:"overview"`
		PosterPath   string   `json:"poster_path"`
		BackdropPath string   `json:"backdrop_path"`
		Parts        []rawRef `json:"parts"`
	}
	if err := c.get(ctx, fmt.Sprintf("/collection/%d", id), url.Values{"language": {tag}}, &raw); err != nil {
		return Collection{}, err
	}
	out := Collection{ID: raw.ID, Name: raw.Name, Overview: raw.Overview, Poster: raw.PosterPath, Backdrop: raw.BackdropPath}
	for _, p := range raw.Parts {
		if p.ID > 0 && !p.Adult {
			out.Parts = append(out.Parts, p.ref("movie"))
		}
	}
	slices.SortStableFunc(out.Parts, func(a, b TitleRef) int {
		switch {
		case a.Date == "" && b.Date == "":
			return 0
		case a.Date == "":
			return 1
		case b.Date == "":
			return -1
		}
		return cmp.Compare(a.Date, b.Date)
	})
	return out, nil
}
