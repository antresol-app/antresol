package library

import "context"

// The music lists of Subsonic page in SQL with the rating of each item's
// root (Repo.Visible) already applied, so a limited profile gets full
// pages: sync clients stop at the first short page.

// maxSearchWindow bounds offset+limit of a text search (it loads them all).
const maxSearchWindow = 10000

// SearchMusic finds artists, albums or tracks (kind) by text, visible to
// maxRating, from offset.
func (r *Repo) SearchMusic(ctx context.Context, text, kind string, limit, offset int, maxRating string) ([]Item, error) {
	if limit <= 0 {
		return []Item{}, nil
	}
	offset = min(max(offset, 0), maxSearchWindow)
	rate, args := MusicRootAllowed(musicRootExpr, maxRating)
	out, err := r.searchWith(ctx, text, []string{kind}, limit+offset, musicRootJoins, rate, args)
	if err != nil || offset >= len(out) {
		return []Item{}, err
	}
	return out[offset:min(offset+limit, len(out))], nil
}

// MusicArtists is Artists with the rating filter in SQL.
func (r *Repo) MusicArtists(ctx context.Context, limit, offset int, maxRating string) ([]ArtistRow, error) {
	const has = `EXISTS (SELECT 1 FROM items al WHERE al.parent_id=i.id AND al.kind='album' AND al.missing_since IS NULL)`
	rate, args := MusicRootAllowed("i.rating", maxRating)
	if limit <= 0 {
		limit = -1
	}
	args = append(args, limit, max(offset, 0))
	items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.kind='artist' AND i.missing_since IS NULL AND `+has+`
	  AND `+rate+` ORDER BY i.sort_title, i.id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	return r.artistRows(ctx, items)
}

// MusicSongs is Songs with the rating filter in SQL.
func (r *Repo) MusicSongs(ctx context.Context, limit, offset int, maxRating string) ([]Track, error) {
	rate, args := MusicRootAllowed("(CASE WHEN ar.id IS NOT NULL THEN ar.rating WHEN al.id IS NOT NULL THEN al.rating ELSE i.rating END)", maxRating)
	if limit <= 0 {
		limit = -1
	}
	args = append(args, limit, max(offset, 0))
	rows, err := r.db.QueryContext(ctx, trackSelect+` LEFT JOIN items ar ON ar.id=al.parent_id
	  WHERE i.kind='track' AND i.missing_since IS NULL AND `+rate+` ORDER BY i.sort_title, i.id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		tr, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}
