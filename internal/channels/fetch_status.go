package channels

import (
	"context"
	"time"
)

// FetchStatus is what the channel editor shows of a channel with
// fetching: the disk it takes of its budget and of the global one, and
// each programme's state.
type FetchStatus struct {
	// Available: something fetches programmes now (an addon).
	Available bool       `json:"available"`
	Channel   FetchUsage `json:"channel"`
	Global    FetchUsage `json:"global"`
	// GlobalGB: the global budget as set (the editor's field).
	GlobalGB   float64              `json:"globalGB"`
	Programmes []FetchProgrammeInfo `json:"programmes"`
}

// FetchProgrammeInfo is one programme of FetchStatus.
type FetchProgrammeInfo struct {
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	// State: wanted | requested | ready | library | kept | removed | failed
	State string `json:"state"`
	Size  int64  `json:"size,omitempty"`
	Done  int64  `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
	// Airs: its next slot in the schedule (nil: none within the horizon).
	Airs *time.Time `json:"airs,omitempty"`
}

// FetchStatusOf is channel c's FetchStatus (its rows in order).
func (s *Service) FetchStatusOf(ctx context.Context, c Channel) (FetchStatus, error) {
	now := s.Store.now()
	out := FetchStatus{Available: s.FetchReady(ctx), GlobalGB: s.Store.FetchGlobalGB(ctx), Programmes: []FetchProgrammeInfo{}}
	use, err := fetchUsageRows(ctx, s.Store.DB, now)
	if err != nil {
		return out, err
	}
	by, all := sumUsage(use)
	out.Channel = FetchUsage{Used: by[c.ID], Budget: c.Rules.Fetch.budget()}
	out.Global = FetchUsage{Used: all, Budget: int64(out.GlobalGB * (1 << 30))}
	rows, err := fetchRows(ctx, s.Store.DB, `channel_id=? AND pos >= 0 ORDER BY pos`, c.ID)
	if err != nil {
		return out, err
	}
	// the next slot of each: by row (not on disk) or by item (on disk)
	airs := map[int64]time.Time{}
	byItem := map[int64]time.Time{}
	es, err := s.Store.Range(ctx, c.ID, now, now.Add(s.Builder.horizon()))
	if err != nil {
		return out, err
	}
	for _, e := range es {
		if e.FetchID != 0 {
			if _, ok := airs[e.FetchID]; !ok {
				airs[e.FetchID] = e.Start
			}
		} else if _, ok := byItem[e.ItemID]; !ok {
			byItem[e.ItemID] = e.Start
		}
	}
	for _, r := range rows {
		p := FetchProgrammeInfo{Kind: r.Kind, Title: r.Title, EpisodeTitle: r.EpTitle, Season: r.Season, Episode: r.Episode,
			State: r.State, Size: r.Size, Done: r.Done, Error: r.Error}
		if r.State == fetchFailed && r.FailedUntilMs <= ms(now) {
			p.State = fetchWanted
		}
		if t, ok := airs[r.ID]; ok {
			p.Airs = &t
		} else if t, ok := byItem[r.ItemID]; ok && r.ItemID != 0 {
			p.Airs = &t
		}
		out.Programmes = append(out.Programmes, p)
	}
	return out, nil
}
