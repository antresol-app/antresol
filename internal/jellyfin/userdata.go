package jellyfin

import (
	"errors"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// epCounts: children (seasons of a series, episodes of a season), the
// episodes under it, and those the profile has not finished.
type epCounts struct {
	children, episodes, unplayed int
}

// episodeCounts counts a series' or a season's episodes for the profile.
func (c *conv) episodeCounts(it library.Item) (epCounts, error) {
	if n, ok := c.counts[it.ID]; ok {
		return n, nil
	}
	var n epCounts
	under := `e.parent_id=?`
	if it.Kind == library.KindSeries {
		under = `e.parent_id IN (SELECT id FROM items WHERE parent_id=? AND kind='season' AND missing_since IS NULL)`
		if err := c.s.DB.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM items WHERE parent_id=? AND kind='season' AND missing_since IS NULL`,
			it.ID).Scan(&n.children); err != nil {
			return n, err
		}
	}
	var finished int
	if err := c.s.DB.QueryRowContext(c.ctx, `SELECT COUNT(*), COALESCE(SUM(COALESCE(p.finished,0)),0) FROM items e
	  LEFT JOIN progress p ON p.item_id=e.id AND p.user_id=?
	  WHERE e.kind='episode' AND e.missing_since IS NULL AND `+under, c.u.ID, it.ID).Scan(&n.episodes, &finished); err != nil {
		return n, err
	}
	n.unplayed = n.episodes - finished
	if it.Kind == library.KindSeason {
		n.children = n.episodes
	}
	c.counts[it.ID] = n
	return n, nil
}

// entry is the profile's progress on an item (nil: none).
func (c *conv) entry(id int64) (*progress.Entry, error) {
	if e, ok := c.prog[id]; ok {
		return e, nil
	}
	e, err := c.s.Progress.Get(c.ctx, c.u.ID, id)
	if errors.Is(err, progress.ErrNotFound) {
		c.prog[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.prog[id] = &e
	return &e, nil
}

// favorite: the item is in the profile's watchlist («Хочу посмотреть»).
func (c *conv) favorite(id int64) (bool, error) {
	if c.fav == nil {
		ids, err := c.s.Progress.Watchlist(c.ctx, c.u.ID)
		if err != nil {
			return false, err
		}
		c.fav = map[int64]bool{}
		for _, i := range ids {
			c.fav[i] = true
		}
	}
	return c.fav[id], nil
}

// resumable is the rule of the Resume shelf and the IsResumable filter:
// started and not finished.
func resumable(e *progress.Entry) bool { return e != nil && e.PositionMs > 0 && !e.Finished }

// played: a finished movie or episode; a series or season with episodes,
// all finished.
func (c *conv) played(it library.Item) (bool, error) {
	switch it.Kind {
	case library.KindSeries, library.KindSeason:
		n, err := c.episodeCounts(it)
		return n.episodes > 0 && n.unplayed == 0, err
	}
	e, err := c.entry(it.ID)
	return e != nil && e.Finished, err
}

// userData is the profile's UserItemDataDto of an item.
func (c *conv) userData(it library.Item) (*UserItemDataDto, error) {
	id := ItemID(it.ID)
	fav, err := c.favorite(it.ID)
	if err != nil {
		return nil, err
	}
	ud := &UserItemDataDto{Key: id, ItemId: id, IsFavorite: fav}
	switch it.Kind {
	case library.KindSeries, library.KindSeason:
		n, err := c.episodeCounts(it)
		if err != nil {
			return nil, err
		}
		ud.UnplayedItemCount = intp(n.unplayed)
		ud.Played = n.episodes > 0 && n.unplayed == 0
		return ud, nil
	}
	e, err := c.entry(it.ID)
	if err != nil || e == nil {
		return ud, err
	}
	ud.Played = e.Finished
	if e.Finished {
		ud.PlayCount = 1
	}
	if resumable(e) {
		ud.PlaybackPositionTicks = ticks(e.PositionMs)
		if e.DurationMs > 0 {
			pct := float64(e.PositionMs) * 100 / float64(e.DurationMs)
			ud.PlayedPercentage = &pct
		}
	}
	if !e.UpdatedAt.IsZero() {
		ud.LastPlayedDate = jfTime(e.UpdatedAt)
	}
	return ud, nil
}
