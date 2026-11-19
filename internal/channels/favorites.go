package channels

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// Favorites are the channels profile uid starred.
func (s *Store) Favorites(ctx context.Context, uid int64) (map[int64]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT channel_id FROM channel_favorites WHERE user_id=?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SetFavorite stars (on) or unstars channel id for profile uid.
func (s *Store) SetFavorite(ctx context.Context, uid, id int64, on bool) error {
	var err error
	if on {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO channel_favorites (user_id, channel_id) VALUES (?,?) ON CONFLICT DO NOTHING`, uid, id)
	} else {
		_, err = s.DB.ExecContext(ctx, `DELETE FROM channel_favorites WHERE user_id=? AND channel_id=?`, uid, id)
	}
	return err
}

// LastChannel is the channel profile uid tuned in last (0: none). It is a
// TV's «last channel», kept apart from media progress.
func (s *Store) LastChannel(ctx context.Context, uid int64) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT channel_id FROM channel_last WHERE user_id=?`, uid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// SetLastChannel records that profile uid tuned in channel id.
func (s *Store) SetLastChannel(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO channel_last (user_id, channel_id, at) VALUES (?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	  ON CONFLICT (user_id) DO UPDATE SET channel_id=excluded.channel_id, at=excluded.at`, uid, id)
	return err
}

// Prefs are a profile's own choices for its channel list.
type Prefs struct {
	// FavoritesFirst: the starred channels stand at the top of the list
	// (default on).
	FavoritesFirst bool `json:"favoritesFirst"`
}

// Prefs reads profile uid's choices (the defaults when it has made none).
func (s *Store) Prefs(ctx context.Context, uid int64) (Prefs, error) {
	p := Prefs{FavoritesFirst: true}
	var ff int
	err := s.DB.QueryRowContext(ctx, `SELECT favorites_first FROM channel_prefs WHERE user_id=?`, uid).Scan(&ff)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.FavoritesFirst = ff != 0
	return p, nil
}

// SetPrefs stores profile uid's choices.
func (s *Store) SetPrefs(ctx context.Context, uid int64, p Prefs) error {
	ff := 0
	if p.FavoritesFirst {
		ff = 1
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO channel_prefs (user_id, favorites_first) VALUES (?,?)
	  ON CONFLICT (user_id) DO UPDATE SET favorites_first=excluded.favorites_first`, uid, ff)
	return err
}

// FavoritesFirst orders a list already in number order: the starred
// channels first (each part keeps its order). id is a channel's id.
func FavoritesFirst[T any](list []T, favs map[int64]bool, id func(T) int64) {
	if len(favs) == 0 {
		return
	}
	sort.SliceStable(list, func(i, j int) bool { return favs[id(list[i])] && !favs[id(list[j])] })
}
