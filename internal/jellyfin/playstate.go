package jellyfin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/screentime"
)

// mountPlaystate registers the playstate routes (behind requireToken):
// the player's reports and the viewer's «Просмотрено» / «Хочу посмотреть».
func (s *Server) mountPlaystate(r chi.Router) {
	r.Post("/sessions/playing", s.playing(reportStart))
	r.Post("/sessions/playing/progress", s.playing(reportProgress))
	r.Post("/sessions/playing/ping", s.playingPing)
	r.Post("/sessions/playing/stopped", s.playing(reportStopped))
	// the legacy forms (Infuse, older Emby clients): the item in the path
	r.Post("/users/{userId}/playingitems/{id}", s.playing(reportStart))
	r.Post("/users/{userId}/playingitems/{id}/progress", s.playing(reportProgress))
	r.Delete("/users/{userId}/playingitems/{id}", s.playing(reportStopped))
	r.Post("/playingitems/{id}", s.playing(reportStart))
	r.Post("/playingitems/{id}/progress", s.playing(reportProgress))
	r.Delete("/playingitems/{id}", s.playing(reportStopped))

	r.Post("/userplayeditems/{id}", s.markPlayed(true))
	r.Delete("/userplayeditems/{id}", s.markPlayed(false))
	r.Post("/users/{userId}/playeditems/{id}", s.markPlayed(true))
	r.Delete("/users/{userId}/playeditems/{id}", s.markPlayed(false))
	r.Post("/userfavoriteitems/{id}", s.markFavorite(true))
	r.Delete("/userfavoriteitems/{id}", s.markFavorite(false))
	r.Post("/users/{userId}/favoriteitems/{id}", s.markFavorite(true))
	r.Delete("/users/{userId}/favoriteitems/{id}", s.markFavorite(false))
	r.Get("/useritems/{id}/userdata", s.getUserData)
	r.Get("/users/{userId}/items/{id}/userdata", s.getUserData)
}

type reportKind int

const (
	reportStart reportKind = iota
	reportProgress
	reportStopped
)

// PlaybackStateInfo is a player's report (PlaybackStartInfo,
// PlaybackProgressInfo, PlaybackStopInfo).
type PlaybackStateInfo struct {
	ItemId        string `json:"ItemId"`
	MediaSourceId string `json:"MediaSourceId"`
	PlaySessionId string `json:"PlaySessionId"`
	PositionTicks *int64 `json:"PositionTicks"`
	IsPaused      bool   `json:"IsPaused"`
	Failed        bool   `json:"Failed"`
}

// minReportMs: a position before this is not saved. A player reports its
// start (and often a first tick) at 0 before it seeks to the resume point:
// saved, that would lose where the viewer stopped.
const minReportMs = 1000

// playing records a player's report: the position in the profile's
// progress (progress.PlayedAt, the native player's rule: 92 % = watched,
// played time counted, stale writes ignored), the heartbeat of the HLS
// session, and on stop its end. A report for an item the profile may not
// see, or not a movie or episode, saves nothing (204 all the same: a
// player never retries a report).
func (s *Server) playing(kind reportKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in PlaybackStateInfo
		if r.ContentLength != 0 {
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		if id := chi.URLParam(r, "id"); id != "" {
			in.ItemId = id
		}
		if in.PositionTicks == nil && qv(r, "positionTicks") != "" {
			n := int64(qInt(r, "positionTicks", 0))
			in.PositionTicks = &n
		}
		if in.PlaySessionId == "" {
			in.PlaySessionId = qv(r, "playSessionId")
		}
		switch kind {
		case reportStopped:
			s.stopPlay(r, in.PlaySessionId)
		default:
			s.touchPlay(r, in.PlaySessionId)
		}
		if in.PositionTicks == nil || in.Failed {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		posMs := *in.PositionTicks / 10_000
		if posMs < minReportMs {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		it, ok, err := s.reportItem(r, in.ItemId)
		if err != nil {
			s.internal(w, r, "playstate", err)
			return
		}
		if ok {
			fs, err := s.Library.FilesOf(r.Context(), it.ID)
			if err != nil {
				s.internal(w, r, "playstate", err)
				return
			}
			// the file played: MediaSourceId, else the play session's
			var fileID int64
			if k, id, ok := ParseID(in.MediaSourceId); ok && k == kindFile {
				fileID = id
			} else if p, ok := s.plays.get(in.PlaySessionId, userOf(r).ID); ok {
				fileID = p.file.ID
			}
			abs, dur := timeline(fs, fileID, posMs)
			if dur == 0 {
				dur = int64(metaOf(it).Runtime) * 60_000
			}
			if err := s.Progress.PlayedAt(r.Context(), userOf(r).ID, it.ID, abs, dur, time.Time{}); err != nil {
				s.internal(w, r, "playstate", err)
				return
			}
			// a playing player's report is screen time (a paused one's, or the stop, is not)
			if kind != reportStopped && !in.IsPaused {
				s.ScreenTime.Signal(r.Context(), userOf(r), screentime.Signal{Category: screentime.Video,
					Key: "item:" + strconv.FormatInt(it.ID, 10), ItemID: it.ID, Report: true})
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// playingPing is /Sessions/Playing/Ping: the heartbeat of a paused player.
func (s *Server) playingPing(w http.ResponseWriter, r *http.Request) {
	s.touchPlay(r, qv(r, "playSessionId"))
	w.WriteHeader(http.StatusNoContent)
}

// reportItem is a movie or an episode the profile may see.
func (s *Server) reportItem(r *http.Request, raw string) (library.Item, bool, error) {
	k, id, ok := ParseID(raw)
	if !ok || k != kindItem {
		return library.Item{}, false, nil
	}
	it, ok, err := s.conv(r.Context(), userOf(r), nil).visibleItem(id)
	if err != nil || !ok {
		return it, false, err
	}
	return it, it.Kind == library.KindMovie || it.Kind == library.KindEpisode, nil
}

// markable is the item of a played/favourite route the profile may see
// (a movie, series, season or episode); ok false: 404 written.
func (s *Server) markable(w http.ResponseWriter, r *http.Request) (*conv, library.Item, bool) {
	c := s.conv(r.Context(), userOf(r), nil)
	k, id, ok := ParseID(chi.URLParam(r, "id"))
	if !ok || k != kindItem {
		w.WriteHeader(http.StatusNotFound)
		return c, library.Item{}, false
	}
	it, ok, err := c.visibleItem(id)
	if err != nil {
		s.internal(w, r, "item", err)
		return c, it, false
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return c, it, false
	}
	return c, it, true
}

// markPlayed is «Просмотрено» (POST) / «Не просмотрено» (DELETE): a movie
// or an episode, or every episode the profile may see under a series or a
// season, as of datePlayed (or now; the stale-write rule of
// progress.MarkWatchedAt). The answer is the item's new UserData.
func (s *Server) markPlayed(played bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, it, ok := s.markable(w, r)
		if !ok {
			return
		}
		var at time.Time
		if v := qv(r, "datePlayed"); v != "" && played {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				at = t
			}
		}
		ids := []int64{it.ID}
		if it.Kind == library.KindSeries || it.Kind == library.KindSeason {
			eps, err := c.descendants(it, true)
			if err != nil {
				s.internal(w, r, "played", err)
				return
			}
			ids = ids[:0]
			for _, e := range eps {
				if e.Kind == library.KindEpisode {
					ids = append(ids, e.ID)
				}
			}
		}
		for _, id := range ids {
			if err := s.Progress.MarkWatchedAt(r.Context(), userOf(r).ID, id, played, at); err != nil {
				s.internal(w, r, "played", err)
				return
			}
		}
		s.writeUserData(w, r, it)
	}
}

// markFavorite is the watchlist («Хочу посмотреть»): a movie or a series.
func (s *Server) markFavorite(fav bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, it, ok := s.markable(w, r)
		if !ok {
			return
		}
		var err error
		switch {
		case !fav:
			err = s.Progress.RemoveFromWatchlist(r.Context(), userOf(r).ID, it.ID)
		case it.Kind == library.KindMovie || it.Kind == library.KindSeries:
			err = s.Progress.AddToWatchlist(r.Context(), userOf(r).ID, it.ID)
		default:
			w.WriteHeader(http.StatusBadRequest) // the web lists movies and series only
			return
		}
		if err != nil {
			s.internal(w, r, "favorite", err)
			return
		}
		s.writeUserData(w, r, it)
	}
}

func (s *Server) getUserData(w http.ResponseWriter, r *http.Request) {
	if _, it, ok := s.markable(w, r); ok {
		s.writeUserData(w, r, it)
	}
}

// writeUserData answers with the item's UserItemDataDto, read afresh.
func (s *Server) writeUserData(w http.ResponseWriter, r *http.Request, it library.Item) {
	ud, err := s.conv(r.Context(), userOf(r), nil).userData(it)
	if err != nil {
		s.internal(w, r, "user data", err)
		return
	}
	writeJSON(w, ud)
}
