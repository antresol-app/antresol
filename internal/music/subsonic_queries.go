package music

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// StarredAt returns when the user starred each item (Subsonic "starred").
func (s *Store) StarredAt(ctx context.Context, userID int64) (map[int64]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, starred_at FROM stars WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		t, err := time.Parse(tsLayout, at)
		if err != nil {
			t = time.Time{}
		}
		out[id] = t
	}
	return out, rows.Err()
}

// Genre is an album genre with its album and track counts. Spellings that
// differ only in case («рок», «Рок») are one genre, named as first seen.
type Genre struct {
	Name          string
	Albums, Songs int
	albumIDs      []int64
}

// albumRoot is the rating of an album's root (its artist "ar", else the
// album "i"), the rule of library.Repo.Visible.
const albumRoot = "(CASE WHEN ar.id IS NOT NULL THEN ar.rating ELSE i.rating END)"

// genreAlbums lists the genres of the albums visible to maxRating ("" =
// all), case-folded together, with their albums.
func (s *Store) genreAlbums(ctx context.Context, maxRating string) ([]Genre, error) {
	rate, args := library.MusicRootAllowed(albumRoot, maxRating)
	rows, err := s.db.QueryContext(ctx, `SELECT j.value, i.id,
	  (SELECT COUNT(*) FROM items t WHERE t.parent_id=i.id AND t.kind='track' AND t.missing_since IS NULL)
	  FROM items i LEFT JOIN items ar ON ar.id=i.parent_id, json_each(i.meta_json,'$.genres') j
	  WHERE i.kind='album' AND i.missing_since IS NULL AND json_valid(i.meta_json) AND j.type='text' AND `+rate+`
	  ORDER BY i.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Genre
	at := map[string]int{}
	for rows.Next() {
		var name string
		var id int64
		var n int
		if err := rows.Scan(&name, &id, &n); err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, Genre{Name: name})
		}
		g := &out[i]
		if len(g.albumIDs) > 0 && g.albumIDs[len(g.albumIDs)-1] == id {
			continue // the same genre twice on one album
		}
		g.albumIDs = append(g.albumIDs, id)
		g.Albums++
		g.Songs += n
	}
	return out, rows.Err()
}

// Genres returns the genres of the albums visible to maxRating.
func (s *Store) Genres(ctx context.Context, maxRating string) ([]Genre, error) {
	return s.genreAlbums(ctx, maxRating)
}

// visibleTracksQuery selects the ids of the tracks ("i", tags "t") of
// non-missing albums ("al") whose root (the artist "ar", else the album)
// is allowed for maxRating: the one kid-visibility track query of
// RandomTracks and ShuffleTrackIDs; callers append " AND …" filters.
func visibleTracksQuery(maxRating string) (string, []any) {
	rate, args := library.MusicRootAllowed("(CASE WHEN ar.id IS NOT NULL THEN ar.rating ELSE al.rating END)", maxRating)
	return `SELECT i.id FROM items i JOIN tracks t ON t.item_id=i.id
	  JOIN items al ON al.id=i.parent_id AND al.kind='album' AND al.missing_since IS NULL
	  LEFT JOIN items ar ON ar.id=al.parent_id
	  WHERE i.kind='track' AND i.missing_since IS NULL AND ` + rate, args
}

// genreAlbumIDs returns the ids of the albums visible to maxRating with
// genre (any case) as a JSON array for json_each, and how many there are.
func (s *Store) genreAlbumIDs(ctx context.Context, genre, maxRating string) (string, int, error) {
	ids := []int64{}
	if g := strings.TrimSpace(genre); g != "" {
		gs, err := s.genreAlbums(ctx, maxRating)
		if err != nil {
			return "", 0, err
		}
		for _, x := range gs {
			if strings.EqualFold(x.Name, g) {
				ids = append(ids, x.albumIDs...)
			}
		}
	}
	b, err := json.Marshal(ids)
	return string(b), len(ids), err
}

// RandomTracks returns up to limit random track ids visible to
// maxRating, optionally of albums with genre (any case) and of album
// years from..to (0: open).
func (s *Store) RandomTracks(ctx context.Context, limit int, genre string, fromYear, toYear int, maxRating string) ([]int64, error) {
	q, args := visibleTracksQuery(maxRating)
	if strings.TrimSpace(genre) != "" {
		ids, _, err := s.genreAlbumIDs(ctx, genre, maxRating)
		if err != nil {
			return nil, err
		}
		q += ` AND al.id IN (SELECT value FROM json_each(?))`
		args = append(args, ids)
	}
	if fromYear != 0 || toYear != 0 {
		lo, hi := fromYear, toYear
		if hi == 0 {
			hi = 9999
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		q += ` AND al.year BETWEEN ? AND ?`
		args = append(args, lo, hi)
	}
	if limit <= 0 {
		limit = 10
	}
	args = append(args, limit)
	return s.ids(ctx, q+` ORDER BY random() LIMIT ?`, args...)
}
