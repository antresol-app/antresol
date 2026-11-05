package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

const (
	missRetry  = 7 * 24 * time.Hour // re-search albums MusicBrainz did not know (+ metadata.Jitter)
	coverRetry = 7 * 24 * time.Hour // re-ask the Cover Art Archive for a missing front (+ jitter)
	errRetry   = 24 * time.Hour     // retry an album the server answered badly for
	minScore   = 90
	// maxItemFails consecutive bad answers (5xx, garbage) end the run: an
	// outage that shows up as 5xx, not a single broken album.
	maxItemFails = 3
)

// Enricher fills albums with MusicBrainz data: an empty cover (Cover Art
// Archive), an empty year, genres when the tags gave none. Titles, the
// artist, tracks and any poster already set (scanner, "file:") are never
// touched. State lives in meta_json, stamps RFC3339 UTC: mb_at (matched),
// mb_miss (no match, retried after 7 days + jitter), mb_cover_miss (no
// front cover, the same), mb_err (the server answered badly, retried after
// 24 h); mb_id and mb_artist_id are the matched MBIDs.
type Enricher struct {
	Client *Client
	Repo   *library.Repo
	Images *metadata.Images
	Log    *slog.Logger
	Now    func() time.Time // nil: time.Now
}

var (
	// errOffline stops the run quietly: the albums not yet processed stay
	// pending for the next one.
	errOffline = errors.New("musicbrainz unreachable")
	// errLocal is a local storage failure (image cache): the run stops
	// with an error.
	errLocal = errors.New("local storage error")
	// errItem is a bad answer for this album: it is stamped mb_err and the
	// run goes on.
	errItem = errors.New("musicbrainz: bad answer")
)

func (e *Enricher) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func (e *Enricher) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Enricher) stamp() string { return metadata.Stamp(e.now()) }

type mode int

const (
	skip      mode = iota
	full           // search / lookup, then the cover
	coverOnly      // matched earlier, only the cover is missing
)

// EnrichPending enriches albums not matched yet (and retries stale misses).
// No connection ends the run and returns nil, as do 3 bad answers in a
// row; a context or local storage error is returned.
func (e *Enricher) EnrichPending(ctx context.Context) error {
	items, _, err := e.Repo.List(ctx, library.ListQuery{Kind: library.KindAlbum})
	if err != nil {
		return err
	}
	fails := 0
	for _, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		meta, err := metadata.ParseMeta(it.MetaJSON)
		if err != nil {
			e.log().Warn("musicbrainz: corrupt meta_json, album skipped", "item", it.ID, "err", err)
			continue
		}
		md := e.pending(meta, it)
		if md == skip {
			continue
		}
		err = e.enrichAlbum(ctx, it, meta, md)
		if !errors.Is(err, errItem) {
			fails = 0
		}
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, errLocal):
			e.log().Error("musicbrainz: local storage error, run aborted", "err", err)
			return err
		case errors.Is(err, errOffline):
			e.log().Warn("musicbrainz: unreachable, run aborted (albums not yet processed stay pending)", "err", err)
			return nil
		case errors.Is(err, errItem):
			fails++
			e.log().Warn("musicbrainz: bad answer, album retried in 24 h", "item", it.ID, "title", it.Title, "err", err)
			if err := e.Repo.UpdateMetadata(ctx, it.ID, func(_ library.Item, m map[string]any) library.Metadata {
				m["mb_err"] = e.stamp()
				return library.Metadata{}
			}); err != nil && ctx.Err() == nil {
				e.log().Warn("musicbrainz: save", "item", it.ID, "err", err)
			}
			if fails >= maxItemFails {
				e.log().Warn("musicbrainz: repeated bad answers, run aborted")
				return nil
			}
		default:
			e.log().Warn("musicbrainz: enrich failed", "item", it.ID, "title", it.Title, "err", err)
		}
	}
	return nil
}

func (e *Enricher) pending(meta map[string]any, it library.Item) mode {
	now := e.now()
	if metadata.Fresh(meta, "mb_err", now, errRetry) {
		return skip
	}
	if metadata.MetaTime(meta, "mb_at").IsZero() {
		if metadata.Fresh(meta, "mb_miss", now, missRetry+metadata.Jitter(it.ID)) {
			return skip
		}
		return full
	}
	if it.Poster == "" && metadata.MetaStr(meta, "mb_id") != "" && e.Client.CoverBase != "" &&
		!metadata.Fresh(meta, "mb_cover_miss", now, coverRetry+metadata.Jitter(it.ID)) {
		return coverOnly
	}
	return skip
}

// clientErr wraps a client failure by what it means for the album; ok is
// false for a definite miss (404, another 4xx).
func clientErr(err error) (ok bool, wrapped error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true, err
	}
	switch metadata.Classify(err) {
	case metadata.Miss:
		return false, nil
	case metadata.ItemFail:
		return true, fmt.Errorf("%w: %v", errItem, err)
	}
	return true, fmt.Errorf("%w: %v", errOffline, err)
}

func artistMatches(ours, theirs string) bool {
	if ours == library.VariousArtists {
		return metadata.NormTitle(theirs) == "various artists"
	}
	return metadata.NormTitle(ours) != "" && metadata.NormTitle(ours) == metadata.NormTitle(theirs)
}

// resolve finds the album's release group: the tagged MBID, else a search
// accepted only with score >= 90 and the same normalised title and artist.
func (e *Enricher) resolve(ctx context.Context, it library.Item, meta map[string]any) (ReleaseGroup, bool, error) {
	if id := metadata.MetaStr(meta, "mb_release_group"); id != "" {
		rg, err := e.Client.ReleaseGroup(ctx, id)
		if err == nil {
			return rg, true, nil
		}
		if failed, werr := clientErr(err); failed {
			return rg, false, werr
		}
		e.log().Info("musicbrainz: tagged release group unknown, searching", "item", it.ID, "mbid", id)
	}
	artist := ""
	if it.ParentID != 0 {
		if p, err := e.Repo.Item(ctx, it.ParentID); err == nil {
			artist = p.Title
		} else if !errors.Is(err, library.ErrNotFound) {
			return ReleaseGroup{}, false, err
		}
	}
	if artist == "" || strings.TrimSpace(it.Title) == "" {
		return ReleaseGroup{}, false, nil
	}
	q := artist
	if artist == library.VariousArtists {
		q = "Various Artists"
	}
	rs, err := e.Client.SearchReleaseGroups(ctx, q, it.Title, it.Year)
	if err != nil {
		_, werr := clientErr(err)
		return ReleaseGroup{}, false, werr
	}
	want := metadata.NormTitle(it.Title)
	for _, r := range rs {
		if r.Score < minScore || metadata.NormTitle(r.Title) != want || !artistMatches(artist, r.Artist) {
			continue
		}
		// search results carry no genres: look the group up
		full, err := e.Client.ReleaseGroup(ctx, r.ID)
		if err == nil {
			full.Score = r.Score
			return full, true, nil
		}
		if failed, werr := clientErr(err); failed {
			return ReleaseGroup{}, false, werr
		}
		return r, true, nil
	}
	return ReleaseGroup{}, false, nil
}

func (e *Enricher) enrichAlbum(ctx context.Context, it library.Item, meta map[string]any, md mode) error {
	var rg ReleaseGroup
	if md == full {
		var ok bool
		var err error
		rg, ok, err = e.resolve(ctx, it, meta)
		if err != nil {
			return err
		}
		if !ok {
			return e.Repo.UpdateMetadata(ctx, it.ID, func(_ library.Item, m map[string]any) library.Metadata {
				delete(m, "mb_err")
				m["mb_miss"] = e.stamp()
				return library.Metadata{}
			})
		}
	} else {
		rg.ID = metadata.MetaStr(meta, "mb_id")
	}

	cover, coverMiss := "", false
	var after error // returned once the album is saved
	if u := e.Client.CoverURL(rg.ID); it.Poster == "" && u != "" {
		name, err := e.Images.Fetch(ctx, u)
		var bad *metadata.BadImageError
		var loc *metadata.LocalError
		switch {
		case err == nil:
			cover = name
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &loc):
			return fmt.Errorf("%w: %v", errLocal, err) // nothing saved, retried next run
		case errors.As(err, &bad):
			coverMiss = true // 404 (no front yet) or a bad answer: retried in 7 days
		default:
			// The cover host did not answer. The album keeps its match and
			// waits 7 days for the cover; the run stops here, so an outage
			// costs at most this one album's cover retry delay.
			coverMiss = true
			after = fmt.Errorf("%w: cover: %v", errOffline, err)
		}
	}

	err := e.Repo.UpdateMetadata(ctx, it.ID, func(cur library.Item, m map[string]any) library.Metadata {
		var out library.Metadata
		delete(m, "mb_err")
		if md == full {
			delete(m, "mb_miss")
			m["mb_at"] = e.stamp()
			m["mb_id"] = rg.ID
			if rg.ArtistID != "" {
				m["mb_artist_id"] = rg.ArtistID
			}
			if _, ok := m["genres"]; !ok && len(rg.Genres) > 0 {
				m["genres"] = rg.Genres
			}
			if cur.Year == 0 && rg.Year > 0 {
				out.Year = rg.Year
			}
		}
		switch {
		case cover != "" && cur.Poster == "":
			out.Poster = cover
			delete(m, "mb_cover_miss")
		case coverMiss:
			m["mb_cover_miss"] = e.stamp()
		}
		return out
	})
	if err != nil {
		return err
	}
	return after
}
