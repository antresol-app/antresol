package accounts

import (
	"context"
	"slices"
	"time"
)

// Reconciling a profile's lists. A profile's mark on a title is one row
// (title_marks), while its lists (Antresol, Shikimori, MyShows, ...) each
// hold their own state. Two rules settle which state the mark takes:
//
//   - A real change in a list (made after the mark: Shikimori's updated_at,
//     a MyShows status that differs from the last one read, a change made
//     here) wins by recency, so «I want to watch it again» set anywhere
//     sticks.
//   - A list read with no history against the mark (a provider's first
//     pull, an entry mapped for the first time) is an import: it wins only
//     when it says the profile got further (ImportWins), so linking a list
//     that has a title «planned» never undoes «completed» from another one.
//
// Whatever the mark becomes is written back to every other linked list that
// can be written (MarkChanged: today Shikimori; MyShows has no write API
// here), unless the profile declined write-back there. A list never gets
// its own change back (no echo), and our pushes are recognised on the next
// pull (pushed_at), so the lists settle instead of ping-ponging.

// Rank orders mark states by how far they say the profile got: planned,
// then on hold or dropped (started, stopped), watching, completed. 0 is
// an unknown state.
func Rank(state string) int {
	switch state {
	case "planned":
		return 1
	case "on_hold", "dropped":
		return 2
	case "watching":
		return 3
	case "completed":
		return 4
	}
	return 0
}

// ImportWins: a list's state read with no history against the profile's
// mark (local, hasLocal) replaces it. The further state wins; on a tie the
// newer one (remoteAt zero: the list gives no time, the mark stays).
func ImportWins(state string, remoteAt time.Time, local Mark, hasLocal bool) bool {
	if !hasLocal {
		return true
	}
	r, l := Rank(state), Rank(local.State)
	if r != l {
		return r > l
	}
	return !remoteAt.IsZero() && remoteAt.After(local.UpdatedAt)
}

// watchedStatus: a list entry's status that says it was watched through
// (Shikimori completed or rewatching, MyShows finished).
func watchedStatus(s string) bool {
	return s == "completed" || s == "rewatching" || s == "finished"
}

// remoteProviders are the lists whose entries (account_rates) tell what the
// profile watched elsewhere.
var remoteProviders = []string{Shikimori, originMyShows}

// RemoteWatched is what the profile's linked lists say it watched of a
// title ({season, episode}; a movie is {0, 0}), except list skip ("" =
// none): a Shikimori cour's episode count within its window (all of it
// once completed), a MyShows show's count as the title's first regular
// episodes (all aired ones once finished with no count). «Вышло новое»
// leaves these out like episodes watched here, and a push carries them to
// the other lists.
func (s *Syncer) RemoteWatched(ctx context.Context, uid int64, t TitleRef, skip string) (map[[2]int]bool, error) {
	out := map[[2]int]bool{}
	var eps []Episode // the title's regular episodes in order, read once
	epsRead := false
	for _, p := range remoteProviders {
		if p == skip {
			continue
		}
		rates, err := s.Accounts.TitleRates(ctx, uid, p, t.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range rates {
			if t.Kind == "movie" {
				if watchedStatus(r.RemoteStatus) || r.RemoteEpisodes > 0 {
					out[[2]int{0, 0}] = true
				}
				continue
			}
			n := r.RemoteEpisodes
			if p == Shikimori {
				if r.RemoteStatus == "completed" && r.AnimeEpisodes > n {
					n = r.AnimeEpisodes
				}
				for i := 1; i <= n; i++ {
					out[[2]int{r.TMDBSeason, r.TMDBOffset + i}] = true
				}
				continue
			}
			if n == 0 && !watchedStatus(r.RemoteStatus) {
				continue
			}
			if !epsRead {
				epsRead = true
				if s.Episodes != nil {
					all, err := s.Episodes(ctx, t.ID)
					if err != nil {
						return nil, err
					}
					for _, e := range all {
						if e.Season > 0 {
							eps = append(eps, e)
						}
					}
					slices.SortFunc(eps, func(a, b Episode) int {
						if a.Season != b.Season {
							return a.Season - b.Season
						}
						return a.Number - b.Number
					})
				}
			}
			today := s.now().Format(time.DateOnly)
			for i, e := range eps {
				if n > 0 && i >= n || n == 0 && (e.AirDate == "" || e.AirDate > today) {
					break
				}
				out[[2]int{e.Season, e.Number}] = true
			}
		}
	}
	return out, nil
}
