package channels

import (
	"context"
	"log"
	"time"
)

// WarmFunc, set with SetWarm before Run, is handed the library items airing from
// now to the schedules' horizon after a schedule is built or refreshed:
// what the guide shows is prepared ahead (the programmes' text in the
// viewers' languages). It runs in Run's own goroutine, one call at a
// time, never on a request; a build during a call asks for one more.
type WarmFunc func(ctx context.Context, items []int64)

// SetWarm sets the warm hook (before Run).
func (s *Service) SetWarm(f WarmFunc) { s.warm = f }

func (s *Service) askWarm() {
	if s == nil || s.warm == nil {
		return
	}
	s.init()
	select {
	case s.warmKick <- struct{}{}:
	default:
	}
}

// warmLoop calls the warm hook on each ask until ctx ends.
func (s *Service) warmLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.warmKick:
		}
		ids, err := s.Store.Airing(ctx, s.Store.now(), s.Store.now().Add(s.Builder.horizon()))
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("channels: warm: %v", err)
			}
			continue
		}
		if len(ids) > 0 {
			s.warm(ctx, ids)
		}
	}
}

// Airing is the library items (not titles outside it) scheduled on any
// channel within [from, to), each once, in airing order.
func (s *Store) Airing(ctx context.Context, from, to time.Time) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT item_id FROM channel_playout WHERE end_ms > ? AND start_ms < ? AND item_id > 0
	  GROUP BY item_id ORDER BY MIN(start_ms)`, ms(from), ms(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
