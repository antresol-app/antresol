package openlibrary

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

const (
	missRetry    = 7 * 24 * time.Hour // re-search books Open Library did not know (+ metadata.Jitter)
	coverRetry   = 7 * 24 * time.Hour // re-fetch a cover the host did not give (+ jitter)
	errRetry     = 24 * time.Hour     // retry an item the server answered badly for
	maxItemFails = 3                  // consecutive bad answers that end the run
)

// Enricher fills books and audiobooks with Open Library data: an empty
// cover, description and year. A work is accepted only when its title
// equals the item's (normalised) and one of its authors shares a surname
// with one of the item's. Titles, authors and anything the scanner or the
// user set are never touched. State lives in meta_json, stamps RFC3339
// UTC: ol_at (matched), ol_miss (no match, retried after 7 days + jitter),
// ol_cover_miss (the cover could not be fetched, the same), ol_err (the
// server answered badly, retried after 24 h); ol_key is the matched work,
// ol_cover_id its cover.
type Enricher struct {
	Client *Client
	Repo   *library.Repo
	Images *metadata.Images
	Log    *slog.Logger
	Now    func() time.Time // nil: time.Now
}

var (
	errOffline = errors.New("open library unreachable")
	errLocal   = errors.New("local storage error")
	errItem    = errors.New("openlibrary: bad answer")
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
	skip mode = iota
	full
	coverOnly
)

// EnrichPending enriches books and audiobooks not matched yet (and retries
// stale misses). No connection ends the run and returns nil, as do 3 bad
// answers in a row.
func (e *Enricher) EnrichPending(ctx context.Context) error {
	fails := 0
	for _, kind := range []string{library.KindBook, library.KindAudiobook} {
		items, _, err := e.Repo.List(ctx, library.ListQuery{Kind: kind})
		if err != nil {
			return err
		}
		for _, it := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			meta, err := metadata.ParseMeta(it.MetaJSON)
			if err != nil {
				e.log().Warn("openlibrary: corrupt meta_json, item skipped", "item", it.ID, "err", err)
				continue
			}
			md := e.pending(meta, it)
			if md == skip {
				continue
			}
			err = e.enrichItem(ctx, it, meta, md)
			if !errors.Is(err, errItem) {
				fails = 0
			}
			switch {
			case err == nil:
			case ctx.Err() != nil:
				return ctx.Err()
			case errors.Is(err, errLocal):
				e.log().Error("openlibrary: local storage error, run aborted", "err", err)
				return err
			case errors.Is(err, errOffline):
				e.log().Warn("openlibrary: unreachable, run aborted (items not yet processed stay pending)", "err", err)
				return nil
			case errors.Is(err, errItem):
				fails++
				e.log().Warn("openlibrary: bad answer, item retried in 24 h", "item", it.ID, "title", it.Title, "err", err)
				if err := e.Repo.UpdateMetadata(ctx, it.ID, func(_ library.Item, m map[string]any) library.Metadata {
					m["ol_err"] = e.stamp()
					return library.Metadata{}
				}); err != nil && ctx.Err() == nil {
					e.log().Warn("openlibrary: save", "item", it.ID, "err", err)
				}
				if fails >= maxItemFails {
					e.log().Warn("openlibrary: repeated bad answers, run aborted")
					return nil
				}
			default:
				e.log().Warn("openlibrary: enrich failed", "item", it.ID, "title", it.Title, "err", err)
			}
		}
	}
	return nil
}

func (e *Enricher) pending(meta map[string]any, it library.Item) mode {
	now := e.now()
	if metadata.Fresh(meta, "ol_err", now, errRetry) {
		return skip
	}
	if metadata.MetaTime(meta, "ol_at").IsZero() {
		if metadata.Fresh(meta, "ol_miss", now, missRetry+metadata.Jitter(it.ID)) {
			return skip
		}
		return full
	}
	if it.Poster == "" && metadata.MetaInt(meta, "ol_cover_id") > 0 && e.Client.CoversBase != "" &&
		!metadata.MetaTime(meta, "ol_cover_miss").IsZero() &&
		!metadata.Fresh(meta, "ol_cover_miss", now, coverRetry+metadata.Jitter(it.ID)) {
		return coverOnly
	}
	return skip
}

// clientErr wraps a client failure by what it means for the item; failed
// is false for a definite miss (404, another 4xx).
func clientErr(err error) (failed bool, wrapped error) {
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

// patronymic reports a Russian patronymic ("Николаевич", "Ивановна"): in
// "Толстой Лев Николаевич" the surname is then the first word.
func patronymic(w string) bool {
	for _, s := range []string{"ович", "евич", "ич", "овна", "евна", "ична", "инична"} {
		if strings.HasSuffix(w, s) && utf8.RuneCountInString(w) > utf8.RuneCountInString(s)+2 {
			return true
		}
	}
	return false
}

// ourSurnames are the possible surnames in the item's author names. The
// last word always counts ("Лев Толстой", "Л. Н. Толстой"); the first word
// only where it is a surname by shape: a two-word "Толстой Лев", or
// "Толстой Лев Николаевич" ending in a patronymic. Initials never count.
func ourSurnames(authors []string) map[string]bool {
	out := map[string]bool{}
	add := func(s string) {
		if utf8.RuneCountInString(s) > 1 {
			out[s] = true
		}
	}
	for _, a := range authors {
		w := strings.Fields(metadata.NormTitle(a))
		if len(w) == 0 {
			continue
		}
		add(w[len(w)-1])
		if len(w) == 2 || (len(w) >= 3 && patronymic(w[len(w)-1])) {
			add(w[0])
		}
	}
	return out
}

// theirSurname is the surname of an Open Library author name: before the
// comma of "Толстой, Лев", else the last word.
func theirSurname(a string) string {
	if i := strings.Index(a, ","); i > 0 {
		a = a[:i]
	}
	w := strings.Fields(metadata.NormTitle(a))
	if len(w) == 0 {
		return ""
	}
	return w[len(w)-1]
}

func surnameMatch(ours, theirs []string) bool {
	set := ourSurnames(ours)
	for _, a := range theirs {
		if s := theirSurname(a); utf8.RuneCountInString(s) > 1 && set[s] {
			return true
		}
	}
	return false
}

func (e *Enricher) resolve(ctx context.Context, it library.Item, authors []string) (Doc, bool, error) {
	if len(authors) == 0 || strings.TrimSpace(it.Title) == "" {
		return Doc{}, false, nil
	}
	docs, err := e.Client.Search(ctx, it.Title, authors[0])
	if err != nil {
		_, werr := clientErr(err)
		return Doc{}, false, werr
	}
	want := metadata.NormTitle(it.Title)
	for _, d := range docs {
		if d.Key != "" && metadata.NormTitle(d.Title) == want && surnameMatch(authors, d.Authors) {
			return d, true, nil
		}
	}
	return Doc{}, false, nil
}

func (e *Enricher) enrichItem(ctx context.Context, it library.Item, meta map[string]any, md mode) error {
	var doc Doc
	description := ""
	if md == full {
		var ok bool
		var err error
		doc, ok, err = e.resolve(ctx, it, metadata.MetaStrings(meta, "authors"))
		if err != nil {
			return err
		}
		if !ok {
			return e.Repo.UpdateMetadata(ctx, it.ID, func(_ library.Item, m map[string]any) library.Metadata {
				delete(m, "ol_err")
				m["ol_miss"] = e.stamp()
				return library.Metadata{}
			})
		}
		if it.Overview == "" {
			w, err := e.Client.Work(ctx, doc.Key)
			if err == nil {
				description = w.Description
			} else if failed, werr := clientErr(err); failed {
				return werr
			}
		}
	} else {
		doc.CoverID = metadata.MetaInt(meta, "ol_cover_id")
	}

	cover, coverMiss := "", false
	var after error
	if u := e.Client.CoverURL(doc.CoverID); it.Poster == "" && u != "" {
		name, err := e.Images.Fetch(ctx, u)
		var bad *metadata.BadImageError
		var loc *metadata.LocalError
		switch {
		case err == nil:
			cover = name
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &loc):
			return fmt.Errorf("%w: %v", errLocal, err)
		case errors.As(err, &bad):
			// a 404 (default=false) or a bad answer (5xx, 429): the covers
			// host is often slow, so the cover is retried in 7 days
			coverMiss = true
		default:
			// the cover host did not answer: keep the match, retry the
			// cover in 7 days, stop the run
			coverMiss = true
			after = fmt.Errorf("%w: cover: %v", errOffline, err)
		}
	}

	err := e.Repo.UpdateMetadata(ctx, it.ID, func(cur library.Item, m map[string]any) library.Metadata {
		var out library.Metadata
		delete(m, "ol_err")
		if md == full {
			delete(m, "ol_miss")
			m["ol_at"] = e.stamp()
			m["ol_key"] = doc.Key
			if doc.CoverID > 0 {
				m["ol_cover_id"] = doc.CoverID
			}
			if cur.Year == 0 && doc.Year > 0 {
				out.Year = doc.Year
			}
			if cur.Overview == "" {
				out.Overview = description
			}
		}
		switch {
		case cover != "" && cur.Poster == "":
			out.Poster = cover
			delete(m, "ol_cover_miss")
		case coverMiss:
			m["ol_cover_miss"] = e.stamp()
		case cover != "":
			delete(m, "ol_cover_miss") // fetched, but a poster appeared meanwhile
		}
		return out
	})
	if err != nil {
		return err
	}
	return after
}
