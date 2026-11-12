package tmdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Translation is a title's text in one language: what a library page shows
// over the text the scan stored in the metadata language («Язык описаний
// медиатеки») when the viewer reads another one. Empty fields mean TMDB has
// no text in that language: the stored one stays.
type Translation struct {
	Title    string   `json:"title,omitempty"`
	Overview string   `json:"overview,omitempty"`
	Cast     []Person `json:"cast,omitempty"`
	// Seasons: TV episodes' names and overviews by season number, for the
	// seasons asked for (Covers).
	Seasons map[int][]EpisodeText `json:"seasons,omitempty"`
	// Covers: the season numbers fetched (with or without episodes).
	Covers []int `json:"covers,omitempty"`
	// Poster and Logo: TMDB paths ("/abc.png") of the title's poster and
	// logo with text in this language, else textless ones; "" (none, or an
	// entry cached before they were kept): the stored art stays.
	Poster string `json:"poster,omitempty"`
	Logo   string `json:"logo,omitempty"`
	// Art: Poster and Logo were asked for. An entry cached before is
	// fetched again once (served meanwhile), as a stale one.
	Art bool `json:"art,omitempty"`
}

// EpisodeText is an episode's name and overview in one language.
type EpisodeText struct {
	Number   int    `json:"n"`
	Name     string `json:"name,omitempty"`
	Overview string `json:"overview,omitempty"`
}

// Episode is the text of episode n of season s (ok false: none).
func (t Translation) Episode(s, n int) (EpisodeText, bool) {
	for _, e := range t.Seasons[s] {
		if e.Number == n {
			return e, true
		}
	}
	return EpisodeText{}, false
}

func (t Translation) covers(seasons []int) bool {
	for _, s := range seasons {
		if !slices.Contains(t.Covers, s) {
			return false
		}
	}
	return true
}

// Translations defaults.
const (
	translationTTL   = 30 * 24 * time.Hour
	translationWait  = 3 * time.Second
	translationRetry = 10 * time.Minute
	// translationMem bounds the decoded entries kept in memory.
	translationMem = 2048
	// prefetchQueue bounds the titles waiting for a background fetch;
	// more are dropped (asked again on a later view).
	prefetchQueue = 64
	// maxAppend: TMDB answers at most 20 append_to_response parts.
	maxAppend = 20
)

// Translations is a cache of titles' text per language (table
// tmdb_translations), filled from TMDB on demand: Get waits for TMDB (a
// short while) on a title page, Peek never does (cards: a missing text is
// fetched in the background for the next view). A failed fetch is not
// retried for a while and leaves the stored text in place.
type Translations struct {
	Client *Client
	DB     *sql.DB
	// TTL: a cached text older than this is fetched again by Get (Peek
	// still serves it); 0: 30 days.
	TTL time.Duration
	// Wait bounds Get's TMDB fetch; 0: 3 s.
	Wait time.Duration
	// Art, when set, downloads a fetched text's Poster and Logo in the
	// background (one at a time, with the text fetches), so a card shows
	// them once they are on the disk; nil: nothing downloaded.
	Art *Photos

	mu     sync.Mutex
	saveMu sync.Mutex // one fetch at a time merges into the cached entry
	mem    map[string]cachedTranslation
	failed map[string]time.Time // key → last failed fetch
	queued map[string]bool
	queue  chan prefetch
	once   sync.Once
	closed bool
	done   chan struct{} // the prefetcher ended (after Close)
}

type cachedTranslation struct {
	t  Translation
	at time.Time
}

type prefetch struct {
	kind string
	id   int
	tag  string
	art  string // a poster or logo path to download instead (Art)
}

// Language is the TMDB tag of the text the scans store (the client's).
func (s *Translations) Language() string { return s.Client.language(context.Background()) }

func translationKey(kind string, id int, tag string) string {
	return kind + "/" + strconv.Itoa(id) + "/" + tag
}

func (s *Translations) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return translationTTL
}

// Peek is the cached text of a title ("movie" | "tv") in the TMDB language
// tag, without asking TMDB; a missing or stale one is fetched in the
// background.
func (s *Translations) Peek(ctx context.Context, kind string, id int, tag string) (Translation, bool) {
	c, ok := s.cached(ctx, kind, id, tag)
	if !ok || !s.fresh(c) {
		s.prefetch(kind, id, tag)
	}
	return c.t, ok
}

// Has: the cache holds a fresh text of the title in the tag with at least
// the given seasons (TMDB is not asked, nothing is fetched).
func (s *Translations) Has(ctx context.Context, kind string, id int, tag string, seasons []int) bool {
	c, ok := s.cached(ctx, kind, id, tag)
	return ok && s.fresh(c) && c.t.covers(seasons)
}

// Get is the text of a title in the tag with at least the given seasons
// (TV): the cached one while fresh, else fetched from TMDB within Wait.
// When TMDB fails, the cached one (stale) if any.
func (s *Translations) Get(ctx context.Context, kind string, id int, tag string, seasons []int) (Translation, bool) {
	c, ok := s.cached(ctx, kind, id, tag)
	if ok && s.fresh(c) && c.t.covers(seasons) {
		return c.t, true
	}
	if s.backingOff(translationKey(kind, id, tag)) {
		return c.t, ok
	}
	wait := s.Wait
	if wait <= 0 {
		wait = translationWait
	}
	fctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	t, err := s.fetch(fctx, kind, id, tag, seasons)
	if err != nil {
		return c.t, ok
	}
	return t, true
}

// fresh: c is younger than the TTL and has the art fields.
func (s *Translations) fresh(c cachedTranslation) bool {
	return time.Since(c.at) < s.ttl() && c.t.Art
}

func (s *Translations) backingOff(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.failed[key]
	return ok && time.Since(at) < translationRetry
}

// cached is the entry in memory, else in the table (then kept in memory).
func (s *Translations) cached(ctx context.Context, kind string, id int, tag string) (cachedTranslation, bool) {
	key := translationKey(kind, id, tag)
	s.mu.Lock()
	c, ok := s.mem[key]
	s.mu.Unlock()
	if ok || s.DB == nil {
		return c, ok
	}
	var data string
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT data, fetched_at FROM tmdb_translations WHERE kind=? AND tmdb_id=? AND lang=?`,
		kind, id, tag).Scan(&data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false
	}
	if err == nil {
		err = json.Unmarshal([]byte(data), &c.t)
	}
	if err != nil {
		log.Printf("tmdb: translation %s: %v", key, err)
		return cachedTranslation{}, false
	}
	c.at = time.Unix(at, 0)
	s.remember(key, c)
	return c, true
}

func (s *Translations) remember(key string, c cachedTranslation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mem == nil || len(s.mem) >= translationMem {
		s.mem = map[string]cachedTranslation{} // the table still has them
	}
	s.mem[key] = c
}

// fetch asks TMDB for the title's text (and the seasons') and keeps it. A
// title TMDB does not know is kept as an empty text (not asked again until
// the TTL).
func (s *Translations) fetch(ctx context.Context, kind string, id int, tag string, seasons []int) (Translation, error) {
	key := translationKey(kind, id, tag)
	t, err := s.Client.translation(ctx, kind, id, tag, seasons)
	if errors.Is(err, ErrNotFound) {
		t, err = Translation{Covers: seasons, Art: true}, nil
	}
	s.mu.Lock()
	if err != nil {
		if s.failed == nil {
			s.failed = map[string]time.Time{}
		}
		s.failed[key] = time.Now()
	} else {
		delete(s.failed, key)
	}
	s.mu.Unlock()
	if err != nil {
		log.Printf("tmdb: translation %s: %v", key, err)
		return t, err
	}
	s.prefetchArt(t.Poster, t.Logo)
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	t = s.withCachedSeasons(context.WithoutCancel(ctx), kind, id, tag, t)
	c := cachedTranslation{t: t, at: time.Now()}
	s.remember(key, c)
	if s.DB != nil {
		b, _ := json.Marshal(t)
		if _, err := s.DB.ExecContext(context.WithoutCancel(ctx), `INSERT INTO tmdb_translations (kind, tmdb_id, lang, data, fetched_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (kind, tmdb_id, lang) DO UPDATE SET data=excluded.data, fetched_at=excluded.fetched_at`,
			kind, id, tag, string(b), c.at.Unix()); err != nil {
			log.Printf("tmdb: save translation %s: %v", key, err)
		}
	}
	return t, nil
}

// withCachedSeasons adds to a fetched t the seasons a fresh cached entry
// has and t was not asked for: the episode pages of S1, then S2, then S1
// again ask TMDB twice, not three times, and a background refresh does not
// drop the seasons a page fetched meanwhile.
func (s *Translations) withCachedSeasons(ctx context.Context, kind string, id int, tag string, t Translation) Translation {
	old, ok := s.cached(ctx, kind, id, tag)
	if !ok || time.Since(old.at) >= s.ttl() {
		return t
	}
	t.Covers = slices.Clone(t.Covers)
	for _, n := range old.t.Covers {
		if slices.Contains(t.Covers, n) {
			continue
		}
		t.Covers = append(t.Covers, n)
		if eps, ok := old.t.Seasons[n]; ok {
			if t.Seasons == nil {
				t.Seasons = map[int][]EpisodeText{}
			}
			t.Seasons[n] = eps
		}
	}
	return t
}

// prefetch queues a background fetch (no seasons) unless one is queued,
// failed lately or the queue is full.
func (s *Translations) prefetch(kind string, id int, tag string) {
	s.enqueue(prefetch{kind: kind, id: id, tag: tag}, translationKey(kind, id, tag))
}

// prefetchArt queues the download of the TMDB art paths not on the disk
// yet (Art; Photos skips a path TMDB lately had no image for).
func (s *Translations) prefetchArt(paths ...string) {
	if s.Art == nil {
		return
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") || !ArtFile.MatchString(p[1:]) {
			continue
		}
		if _, ok := s.Art.Cached(p); !ok {
			s.enqueue(prefetch{art: p}, "art:"+p)
		}
	}
}

func (s *Translations) enqueue(item prefetch, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.once.Do(func() {
		s.queue = make(chan prefetch, prefetchQueue)
		s.done = make(chan struct{})
		go s.prefetcher()
	})
	if s.queued[key] {
		return
	}
	if at, ok := s.failed[key]; ok && time.Since(at) < translationRetry {
		return
	}
	select {
	case s.queue <- item:
		if s.queued == nil {
			s.queued = map[string]bool{}
		}
		s.queued[key] = true
	default:
	}
}

// Close stops the background fetches and waits for the one running (the
// database may be closed after it). Later Peeks fetch nothing.
func (s *Translations) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	q, done := s.queue, s.done
	if q != nil {
		close(q)
	}
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Translations) prefetcher() {
	defer close(s.done)
	for p := range s.queue {
		if p.art != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if _, err := s.Art.Fetch(ctx, p.art); err != nil {
				log.Printf("tmdb: art %s: %v", p.art, err)
			}
			cancel()
			s.mu.Lock()
			delete(s.queued, "art:"+p.art)
			s.mu.Unlock()
			continue
		}
		key := translationKey(p.kind, p.id, p.tag)
		// a page may have fetched it meanwhile
		if c, ok := s.cached(context.Background(), p.kind, p.id, p.tag); !ok || !s.fresh(c) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			var covers []int
			if ok {
				covers = c.t.Covers // a refresh keeps the seasons it had
			}
			s.fetch(ctx, p.kind, p.id, p.tag, covers)
			cancel()
		}
		s.mu.Lock()
		delete(s.queued, key)
		s.mu.Unlock()
	}
}

// translation fetches a title's text in the tag: its details with credits
// and images and, for TV, the seasons appended (append_to_response, 20
// parts a request: 18 seasons to the first, with credits and images, 20
// to the next ones).
func (c *Client) translation(ctx context.Context, kind string, id int, tag string, seasons []int) (Translation, error) {
	if kind != "movie" && kind != "tv" {
		return Translation{}, fmt.Errorf("tmdb: translation of %q", kind)
	}
	if kind == "movie" {
		seasons = nil
	}
	t := Translation{Covers: slices.Clone(seasons)}
	rest := seasons
	for head := true; head || len(rest) > 0; head = false {
		parts := []string{}
		if head {
			parts = append(parts, "credits", "images")
		}
		n := min(len(rest), maxAppend-len(parts))
		for _, s := range rest[:n] {
			parts = append(parts, "season/"+strconv.Itoa(s))
		}
		rest = rest[n:]
		v := url.Values{"language": {tag}}
		if head {
			// the art with text in the language, else textless
			v.Set("include_image_language", isoLang(tag)+",null")
		}
		if len(parts) > 0 {
			v.Set("append_to_response", strings.Join(parts, ","))
		}
		var body json.RawMessage
		if err := c.get(ctx, fmt.Sprintf("/%s/%d", kind, id), v, &body); err != nil {
			return Translation{}, err
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return Translation{}, err
		}
		if head {
			var r rawDetails
			if err := json.Unmarshal(body, &r); err != nil {
				return Translation{}, err
			}
			t.Title, t.Overview = first(r.Title, r.Name), r.Overview
			for i, p := range r.Credits.Cast {
				if i >= maxCast {
					break
				}
				t.Cast = append(t.Cast, Person{Name: p.Name, Character: p.Character, Profile: p.Profile})
			}
			t.Poster = pickIn(r.Images.Posters, isoLang(tag))
			t.Logo = pickIn(r.Images.Logos, isoLang(tag))
			t.Art = true
		}
		for _, p := range parts {
			n, ok := strings.CutPrefix(p, "season/")
			if !ok || raw[p] == nil {
				continue
			}
			var se struct {
				Episodes []struct {
					Number   int    `json:"episode_number"`
					Name     string `json:"name"`
					Overview string `json:"overview"`
				} `json:"episodes"`
			}
			if err := json.Unmarshal(raw[p], &se); err != nil {
				return Translation{}, err
			}
			sn, _ := strconv.Atoi(n)
			for _, e := range se.Episodes {
				if t.Seasons == nil {
					t.Seasons = map[int][]EpisodeText{}
				}
				t.Seasons[sn] = append(t.Seasons[sn], EpisodeText{Number: e.Number, Name: e.Name, Overview: e.Overview})
			}
		}
	}
	return t, nil
}

// isoLang is the ISO 639-1 code of a TMDB tag ("en-US" -> "en").
func isoLang(tag string) string {
	l, _, _ := strings.Cut(strings.ToLower(tag), "-")
	return l
}
