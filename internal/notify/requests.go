package notify

import (
	"context"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

func init() {
	renderers[Requests] = renderRequests
	headings[Requests] = func(l string) string { return i18n.TL(l, "Просьбы") }
}

// Request statuses of Data.Status.
const (
	StatusApproved  = "approved"
	StatusInLibrary = "in_library"
)

func renderRequests(lang string, evs []stored, link func(string) string) []entry {
	out := make([]entry, 0, len(evs))
	for _, e := range evs {
		line := i18n.TL(lang, "Просьбу одобрили: %s", titleYear(e.data))
		path := "/requests"
		if e.data.Status == StatusInLibrary {
			line = i18n.TL(lang, "Уже в медиатеке: %s", titleYear(e.data))
			path = itemPath(e.data.Kind, e.itemID)
		}
		out = append(out, entry{Line: line, Link: link(path), Poster: e.data.Poster})
	}
	return out
}

// libraryKind is the library kind of an acquire kind.
func libraryKind(kind string) string {
	if kind == "music" {
		return library.KindAlbum
	}
	return kind
}

// Request is a kid's request of a title.
type Request struct {
	ID, UserID int64
	Kind       string // acquire kind
	Title      string
	Year       int
	Poster     string
}

// RequestApproved tells the kid its request was approved.
func (s *Service) RequestApproved(ctx context.Context, r Request) error {
	if s == nil {
		return nil
	}
	_, err := s.Enqueue(ctx, Event{UserID: r.UserID, Topic: Requests, Key: "req:" + strconv.FormatInt(r.ID, 10) + ":approved",
		Data: Data{Title: r.Title, Year: r.Year, Kind: libraryKind(r.Kind), Poster: r.Poster, Status: StatusApproved}})
	return err
}

// LibraryFinder finds the library item of a requested title.
type LibraryFinder func(ctx context.Context, kind string, tmdbID int, title, subtitle string) (int64, bool)

// requestWindow: requests younger than this are watched for their title.
const requestWindow = 60 * 24 * time.Hour

// RequestsInLibrary is a collector: the requests (not declined) whose
// title the library has now, for the kids who want to hear it.
func (s *Service) RequestsInLibrary(find LibraryFinder) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		want, err := s.wanting(ctx, Requests)
		if err != nil || len(want) == 0 {
			return err
		}
		since := s.now().UTC().Add(-requestWindow).Format("2006-01-02T15:04:05Z")
		rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.user_id, r.kind, r.tmdb_id, r.title, r.subtitle, r.year, r.poster FROM kid_requests r
			WHERE r.status <> 'declined' AND r.created_at >= ?
			AND NOT EXISTS (SELECT 1 FROM notify_events e WHERE e.user_id = r.user_id AND e.key = 'req:' || r.id || ':lib')`, since)
		if err != nil {
			return err
		}
		type req struct {
			Request
			tmdb     int
			subtitle string
		}
		var rs []req
		for rows.Next() {
			var r req
			if err := rows.Scan(&r.ID, &r.UserID, &r.Kind, &r.tmdb, &r.Title, &r.subtitle, &r.Year, &r.Poster); err != nil {
				rows.Close()
				return err
			}
			if _, ok := want[r.UserID]; ok {
				rs = append(rs, r)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, r := range rs {
			id, ok := find(ctx, r.Kind, r.tmdb, r.Title, r.subtitle)
			if !ok {
				continue
			}
			// only what arrived after «Просьбы» was turned on: turning it on
			// is no news of the old requests' titles
			since, err := s.OnSince(ctx, r.UserID, Requests)
			if err != nil {
				return err
			}
			var added string
			if err := s.DB.QueryRowContext(ctx, `SELECT added_at FROM items WHERE id=?`, id).Scan(&added); err != nil {
				continue
			}
			if at, err := time.Parse(time.RFC3339, added); err != nil || at.Before(since) {
				continue
			}
			if _, err := s.insert(ctx, Event{UserID: r.UserID, Topic: Requests, Key: "req:" + strconv.FormatInt(r.ID, 10) + ":lib", ItemID: id,
				Data: Data{Title: r.Title, Year: r.Year, Kind: libraryKind(r.Kind), Poster: r.Poster, Status: StatusInLibrary}}); err != nil {
				return err
			}
		}
		return nil
	}
}
