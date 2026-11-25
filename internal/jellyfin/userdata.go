package jellyfin

import (
	"errors"
	"slices"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// batch is how many ids a prefetch query takes.
const batch = 500

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
	  WHERE e.kind='episode' AND e.missing_since IS NULL AND `+under, c.hid, it.ID).Scan(&n.episodes, &finished); err != nil {
		return n, err
	}
	n.unplayed = n.episodes - finished
	if it.Kind == library.KindSeason {
		n.children = n.episodes
	}
	c.counts[it.ID] = n
	return n, nil
}

// prefetchCounts fills the episode counts of the series and seasons among
// items in a few queries (episodeCounts is the same count for one).
func (c *conv) prefetchCounts(items []library.Item) error {
	var series, seasons []int64
	for _, it := range items {
		if _, ok := c.counts[it.ID]; ok {
			continue
		}
		switch it.Kind {
		case library.KindSeries:
			series = append(series, it.ID)
		case library.KindSeason:
			seasons = append(seasons, it.ID)
		}
	}
	for part := range slices.Chunk(series, batch) {
		in, args := db.InList(part)
		kids, err := c.countRows(`SELECT parent_id, COUNT(*), 0 FROM items WHERE kind='season' AND missing_since IS NULL AND parent_id IN `+in+`
		  GROUP BY parent_id`, args)
		if err != nil {
			return err
		}
		eps, err := c.countRows(`SELECT se.parent_id, COUNT(*), SUM(COALESCE(p.finished,0)) FROM items se
		  JOIN items e ON e.parent_id=se.id AND e.kind='episode' AND e.missing_since IS NULL
		  LEFT JOIN progress p ON p.item_id=e.id AND p.user_id=?
		  WHERE se.kind='season' AND se.missing_since IS NULL AND se.parent_id IN `+in+` GROUP BY se.parent_id`, append([]any{c.hid}, args...))
		if err != nil {
			return err
		}
		for _, id := range part {
			c.counts[id] = epCounts{children: kids[id][0], episodes: eps[id][0], unplayed: eps[id][0] - eps[id][1]}
		}
	}
	for part := range slices.Chunk(seasons, batch) {
		in, args := db.InList(part)
		eps, err := c.countRows(`SELECT e.parent_id, COUNT(*), SUM(COALESCE(p.finished,0)) FROM items e
		  LEFT JOIN progress p ON p.item_id=e.id AND p.user_id=?
		  WHERE e.kind='episode' AND e.missing_since IS NULL AND e.parent_id IN `+in+` GROUP BY e.parent_id`, append([]any{c.hid}, args...))
		if err != nil {
			return err
		}
		for _, id := range part {
			n := eps[id][0]
			c.counts[id] = epCounts{children: n, episodes: n, unplayed: n - eps[id][1]}
		}
	}
	return nil
}

// countRows reads rows of (id, a, b) into id → {a, b}.
func (c *conv) countRows(q string, args []any) (map[int64][2]int, error) {
	rows, err := c.s.DB.QueryContext(c.ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][2]int{}
	for rows.Next() {
		var id int64
		var n [2]int
		if err := rows.Scan(&id, &n[0], &n[1]); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// prefetchProgress reads the profile's progress on items in a few queries
// (an item with none is cached as none).
func (c *conv) prefetchProgress(items []library.Item) error {
	var ids []int64
	for _, it := range items {
		if _, ok := c.prog[it.ID]; !ok {
			ids = append(ids, it.ID)
		}
	}
	for part := range slices.Chunk(ids, batch) {
		entries, err := c.s.Progress.Of(c.ctx, []int64{c.hid}, part)
		if err != nil {
			return err
		}
		for _, id := range part {
			c.prog[id] = nil
		}
		for i := range entries {
			c.prog[entries[i].ItemID] = &entries[i]
		}
	}
	return nil
}

// prefetchUserData reads what played, resumable and the DatePlayed sort
// look at, for all of items at once: progress of films and episodes, the
// episode counts of series and seasons.
func (c *conv) prefetchUserData(items []library.Item) error {
	var leaves, folders []library.Item
	for _, it := range items {
		if it.Kind == library.KindSeries || it.Kind == library.KindSeason {
			folders = append(folders, it)
		} else {
			leaves = append(leaves, it)
		}
	}
	if err := c.prefetchProgress(leaves); err != nil {
		return err
	}
	return c.prefetchCounts(folders)
}

// entry is the profile's progress on an item (nil: none).
func (c *conv) entry(id int64) (*progress.Entry, error) {
	if e, ok := c.prog[id]; ok {
		return e, nil
	}
	e, err := c.s.Progress.Get(c.ctx, c.hid, id)
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
