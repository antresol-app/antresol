package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

const (
	missRetry = 7 * 24 * time.Hour // re-search titles TMDB did not know
	epsRetry  = 24 * time.Hour     // re-check series episodes still lacking titles

	imageRetry    = 24 * time.Hour // retry failed images after this long
	maxImageTries = 5
)

// Image sizes: posters/logos w500, backdrops w1280, episode stills w500.
const (
	sizePoster   = "w500"
	sizeBackdrop = "w1280"
	sizeLogo     = "w500"
	sizeStill    = "w500"
	// the thumbnail of notification letters (meta poster_url)
	sizeNotifyPoster = "w185"
)

// Enricher fills library items with TMDB metadata. Local artwork
// ("file:..." posters) and other non-empty fields are never overwritten.
type Enricher struct {
	Client *Client
	Repo   *library.Repo
	Images *metadata.Images
	Log    *slog.Logger

	mu     sync.Mutex
	probed bool // image host probe memo, reset per EnrichPending run
	hostOK bool
}

func (e *Enricher) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// errOffline marks failures that should stop the whole run quietly.
var errOffline = errors.New("tmdb unreachable")

// errLocal marks local storage failures (disk, permissions): the run stops,
// but this is not a network condition.
var errLocal = errors.New("local storage error")

type imgVerdict int

const (
	imgItemLocal imgVerdict = iota // this image failed, the host is fine
	imgOffline                     // image host unreachable
	imgLocalErr                    // local I/O failure
)

// hostReachable probes the image host once per run (any HTTP answer counts).
func (e *Enricher) hostReachable(ctx context.Context) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.probed {
		return e.hostOK
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ok := false
	if req, err := http.NewRequestWithContext(pctx, http.MethodGet, e.Client.imageRoot(), nil); err == nil {
		if resp, err := (&http.Client{}).Do(req); err == nil {
			resp.Body.Close()
			ok = true
		}
	}
	if ctx.Err() == nil { // do not memoize a cancelled probe
		e.probed, e.hostOK = true, ok
	}
	return ok
}

// classifyImage decides what an Images.Fetch failure means.
func (e *Enricher) classifyImage(ctx context.Context, err error) imgVerdict {
	var bad *metadata.BadImageError
	var loc *metadata.LocalError
	switch {
	case errors.As(err, &bad):
		return imgItemLocal
	case errors.As(err, &loc):
		return imgLocalErr
	case e.hostReachable(ctx):
		return imgItemLocal // e.g. one image timed out, host answers
	}
	return imgOffline
}

// EnrichPending enriches movies and series that have no TMDB metadata yet
// and fills episode titles of known series. Network failures are logged and
// leave items untouched; only context cancellation is returned.
func (e *Enricher) EnrichPending(ctx context.Context) error {
	e.mu.Lock()
	e.probed = false
	e.mu.Unlock()
	items, _, err := e.Repo.List(ctx, library.ListQuery{})
	if err != nil {
		return err
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if it.Kind != library.KindMovie && it.Kind != library.KindSeries {
			continue
		}
		meta := parseMeta(it.MetaJSON)
		var err error
		if pending(meta, it) {
			err = e.enrichItem(ctx, it, meta)
		}
		// the cast's photos, so the page shows them with no network
		if err == nil && e.Images != nil && photosPending(meta) {
			err = e.cachePhotos(ctx, it, meta)
		}
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, errLocal):
			e.log().Error("tmdb: local storage error, run aborted", "err", err)
			return err
		case errors.Is(err, errOffline):
			e.log().Warn("tmdb: unreachable, run aborted (items not yet processed stay pending)", "err", err)
			return nil
		default:
			e.log().Warn("tmdb: enrich failed", "item", it.ID, "title", it.Title, "err", err)
		}
	}
	return nil
}

func parseMeta(s string) map[string]any {
	m := map[string]any{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &m)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func metaTime(m map[string]any, k string) time.Time {
	s, _ := m[k].(string)
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func pending(meta map[string]any, it library.Item) bool {
	at := metaTime(meta, "tmdb_at")
	if at.IsZero() {
		return true
	}
	if meta["images_incomplete"] == true {
		tries, _ := meta["images_tries"].(float64)
		return tries < maxImageTries && time.Since(at) > imageRetry
	}
	if meta["tmdb_miss"] == true {
		return time.Since(at) > missRetry
	}
	// enriched before genre ids were kept: once more, for them
	if _, ok := meta["genreIds"]; !ok {
		return true
	}
	// enriched before the IMDb id was kept (the ratings need it): once more
	if _, ok := meta["imdb_id"]; !ok {
		return true
	}
	// Enriched series: new episodes may still lack titles.
	return it.Kind == library.KindSeries && time.Since(metaTime(meta, "eps_at")) > epsRetry
}

func metaStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

// resolve finds the TMDB id: nfo tmdb id, then external ids, then title search.
func (e *Enricher) resolve(ctx context.Context, it library.Item, meta map[string]any) (int, error) {
	if it.TmdbID != 0 {
		return int(it.TmdbID), nil
	}
	kind := "tv"
	if it.Kind == library.KindMovie {
		kind = "movie"
	}
	for _, src := range []string{"tvdb_id", "imdb_id"} {
		if kind == "movie" && src == "tvdb_id" {
			continue
		}
		if v := metaStr(meta, src); v != "" {
			id, err := e.Client.Find(ctx, kind, src, v)
			if err == nil {
				return id, nil
			}
			if !errors.Is(err, ErrNotFound) {
				return 0, wrapNet(err)
			}
		}
	}
	var queries []string
	seen := map[string]bool{}
	for _, t := range []string{it.Title, it.OriginalTitle} {
		if t = strings.TrimSpace(t); t != "" && !seen[t] {
			seen[t] = true
			queries = append(queries, t)
		}
	}
	for _, q := range queries {
		for _, year := range []int{it.Year, 0} {
			var rs []Result
			var err error
			if kind == "movie" {
				rs, err = e.Client.SearchMovie(ctx, q, year)
			} else {
				rs, err = e.Client.SearchTV(ctx, q, year)
			}
			if err != nil {
				return 0, wrapNet(err)
			}
			if len(rs) > 0 {
				return pickResult(rs, it.Year), nil
			}
			if it.Year == 0 {
				break // the year-less pass was just done
			}
		}
	}
	return 0, ErrNotFound
}

// wrapNet turns anything that is not a definite TMDB answer into errOffline.
func wrapNet(err error) error {
	if errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("%w: %v", errOffline, err)
}

func (e *Enricher) enrichItem(ctx context.Context, it library.Item, meta map[string]any) error {
	id, err := e.resolve(ctx, it, meta)
	if errors.Is(err, ErrNotFound) {
		return e.markMiss(ctx, it, meta)
	}
	if err != nil {
		return err
	}
	var d Details
	if it.Kind == library.KindMovie {
		d, err = e.Client.Movie(ctx, id)
	} else {
		d, err = e.Client.TV(ctx, id)
	}
	if errors.Is(err, ErrNotFound) {
		return e.markMiss(ctx, it, meta)
	}
	if err != nil {
		return wrapNet(err)
	}

	m := library.Metadata{TmdbID: int64(d.ID)}
	if it.OriginalTitle == "" {
		m.OriginalTitle = d.OriginalTitle
	}
	if it.Year == 0 {
		m.Year = d.Year
	}
	if it.Overview == "" {
		m.Overview = d.Overview
	}
	if it.Rating == "" {
		m.Rating = d.Rating
	}

	// Image failures are item-local: log, mark for a delayed retry, go on.
	imgFailed := false
	var imgAbort error // offline or local: stop before saving anything
	fetch := func(size, p string) string {
		if p == "" {
			return ""
		}
		name, err := e.Images.Fetch(ctx, e.Client.ImageURL(size, p))
		if err != nil {
			switch e.classifyImage(ctx, err) {
			case imgItemLocal:
				imgFailed = true
				e.log().Warn("tmdb: image failed", "item", it.ID, "err", err)
			case imgLocalErr:
				if imgAbort == nil {
					imgAbort = fmt.Errorf("%w: %v", errLocal, err)
				}
			default:
				if imgAbort == nil {
					imgAbort = fmt.Errorf("%w: image host: %v", errOffline, err)
				}
			}
			return ""
		}
		return name
	}
	if it.Poster == "" {
		m.Poster = fetch(sizePoster, d.Poster)
	}
	if it.Backdrop == "" {
		m.Backdrop = fetch(sizeBackdrop, d.Backdrop)
	}
	// the poster's own address: notification letters link it (no copy of
	// the cached file leaves the server)
	if d.Poster != "" {
		meta["poster_url"] = e.Client.ImageURL(sizeNotifyPoster, d.Poster)
	}
	if metaStr(meta, "logo") == "" {
		if l := fetch(sizeLogo, d.Logo); l != "" {
			meta["logo"] = l
		}
	}
	if _, ok := meta["genres"]; !ok && len(d.Genres) > 0 {
		meta["genres"] = d.Genres
	}
	// the ids name the genres in any language (GenreNames); always the
	// current ones, [] when TMDB lists none (so the item is not asked again)
	ids := d.GenreIDs
	if ids == nil {
		ids = []int{}
	}
	meta["genreIds"] = ids
	if _, ok := meta["cast"]; !ok && len(d.Cast) > 0 {
		meta["cast"] = d.Cast
	}
	// the IMDb id (IMDb and Rotten Tomatoes ratings); one from a .nfo is
	// kept; "" when TMDB knows none (so the item is not asked again)
	if metaStr(meta, "imdb_id") == "" {
		meta["imdb_id"] = d.IMDbID
	}
	if _, ok := meta["runtime"]; !ok && d.Runtime > 0 {
		meta["runtime"] = d.Runtime
	}
	// the language of the text kept above (pages in another one show
	// Translations over it); an item enriched before is taken to be in the
	// current metadata language
	if _, ok := meta["tmdb_lang"]; !ok {
		meta["tmdb_lang"] = e.Client.language(ctx)
	}
	if imgAbort != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Nothing saved, tries untouched: the item stays pending for the next run.
		return imgAbort
	}
	delete(meta, "tmdb_miss")
	meta["tmdb_at"] = time.Now().UTC().Format(time.RFC3339)
	if imgFailed {
		tries, _ := meta["images_tries"].(float64)
		meta["images_incomplete"] = true
		meta["images_tries"] = tries + 1
	} else {
		delete(meta, "images_incomplete")
		delete(meta, "images_tries")
	}
	var netErr error
	if it.Kind == library.KindSeries {
		if err := e.enrichEpisodes(ctx, it.ID, d.ID); err != nil {
			if errors.Is(err, errOffline) || errors.Is(err, errLocal) || ctx.Err() != nil {
				netErr = err // logged once by EnrichPending
			} else {
				e.log().Warn("tmdb: episodes failed", "series", it.ID, "err", err)
			}
		} else {
			meta["eps_at"] = time.Now().UTC().Format(time.RFC3339)
		}
	}
	b, _ := json.Marshal(meta)
	m.MetaJSON = string(b)
	if err := e.Repo.SetMetadata(ctx, it.ID, m); err != nil {
		return err
	}
	if err := e.Repo.AddTitles(ctx, it.ID, d.AltTitles); err != nil {
		return err
	}
	return netErr
}

func (e *Enricher) markMiss(ctx context.Context, it library.Item, meta map[string]any) error {
	meta["tmdb_miss"] = true
	meta["tmdb_at"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(meta)
	return e.Repo.SetMetadata(ctx, it.ID, library.Metadata{MetaJSON: string(b)})
}

func isPlaceholder(title string, ep int) bool {
	return title == "" || title == fmt.Sprintf("Серия %d", ep)
}

// enrichEpisodes fills titles, overviews and stills of a series' episodes
// from TMDB season details, touching only empty fields.
func (e *Enricher) enrichEpisodes(ctx context.Context, seriesID int64, tvID int) error {
	seasons, err := e.Repo.Children(ctx, seriesID)
	if err != nil {
		return err
	}
	for _, s := range seasons {
		if s.Kind != library.KindSeason {
			continue
		}
		eps, err := e.Repo.Children(ctx, s.ID)
		if err != nil {
			return err
		}
		need := false
		for _, ep := range eps {
			if isPlaceholder(ep.Title, ep.EpisodeNo) || ep.Overview == "" || ep.Poster == "" {
				need = true
				break
			}
		}
		if !need {
			continue
		}
		sd, err := e.Client.Season(ctx, tvID, s.SeasonNo)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return wrapNet(err)
		}
		byNo := map[int]Episode{}
		for _, x := range sd.Episodes {
			byNo[x.Number] = x
		}
		for _, ep := range eps {
			x, ok := byNo[ep.EpisodeNo]
			if !ok {
				continue
			}
			var m library.Metadata
			if isPlaceholder(ep.Title, ep.EpisodeNo) {
				m.Title = x.Name
			}
			if ep.Overview == "" {
				m.Overview = x.Overview
			}
			if ep.Poster == "" && x.Still != "" {
				name, err := e.Images.Fetch(ctx, e.Client.ImageURL(sizeStill, x.Still))
				if err == nil {
					m.Poster = name
				} else {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					switch e.classifyImage(ctx, err) {
					case imgLocalErr:
						return fmt.Errorf("%w: %v", errLocal, err)
					case imgOffline:
						return fmt.Errorf("%w: image host: %v", errOffline, err)
					}
					e.log().Warn("tmdb: still failed", "episode", ep.ID, "err", err)
				}
			}
			if m == (library.Metadata{}) {
				continue
			}
			if err := e.Repo.SetMetadata(ctx, ep.ID, m); err != nil {
				return err
			}
		}
	}
	return nil
}

// pickResult prefers the first result whose year is within one of want.
func pickResult(rs []Result, want int) int {
	if want != 0 {
		for _, r := range rs {
			if r.Year != 0 && r.Year >= want-1 && r.Year <= want+1 {
				return r.ID
			}
		}
	}
	return rs[0].ID
}
