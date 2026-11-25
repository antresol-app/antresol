package addons

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/antresol-app/antresol/internal/tracking"
)

// scanHookPrefix starts a scanner hook's name: tracking.scan:<kind>.
const scanHookPrefix = "tracking.scan:"

// coreKinds are the source kinds of the core's own scanner: never an
// addon's. Only the library's: every other kind, `online` too, is an
// installed addon's to declare (D12).
var coreKinds = map[string]bool{"library": true}

// ScanKindsOf are the scan kinds a manifest declares, in its order.
func ScanKindsOf(m Manifest) []string {
	var out []string
	for _, h := range m.Hooks {
		if k, ok := strings.CutPrefix(h, scanHookPrefix); ok {
			out = append(out, k)
		}
	}
	return out
}

// Scanners are the tracking scanners of this round (tracking.Service's
// SetScanners): one per tracking.scan:<kind> of each addon that is on and
// running, in addon-id order. A stopped, crashed or disabled addon gives
// none, so its stored rows stay as they are and nothing is logged. The
// library's kind, or one an earlier addon has (install refuses both), is
// skipped.
func (h *Hooks) Scanners(ctx context.Context) []tracking.Scanner {
	list, err := h.T.List(ctx)
	if err != nil {
		h.log().Warn("addon hook: list", "hook", "tracking.scan", "err", err)
		return nil
	}
	slices.SortFunc(list, func(a, b Installed) int { return strings.Compare(a.ID, b.ID) })
	taken := map[string]bool{}
	var out []tracking.Scanner
	for _, a := range list {
		if !a.Enabled {
			continue
		}
		if st, sock := h.T.Serving(a.ID); st != StatusRunning || sock == "" {
			continue
		}
		for _, k := range ScanKindsOf(a.Manifest) {
			if coreKinds[k] || taken[k] {
				continue
			}
			taken[k] = true
			out = append(out, &hookScanner{h: h, addon: a.ID, kind: k})
		}
	}
	return out
}

// ScanKinds are the addons' scan kinds a profile may be shown and choose
// (E2): those of every installed addon it has access to (admins: all;
// adults: those for adults), on or off, so a disabled addon's stored rows
// stay listed. Kids get none (they keep the library's). Sorted, without
// the core's kinds.
func (h *Hooks) ScanKinds(u auth.User) []string {
	if h == nil || h.T == nil || u.IsKid {
		return nil
	}
	list, err := h.T.List(context.Background())
	if err != nil {
		h.log().Warn("addon hook: list", "hook", "tracking.scan", "err", err)
		return nil
	}
	var out []string
	for _, a := range list {
		if !u.IsAdmin && !a.Manifest.ForAdults() {
			continue
		}
		for _, k := range ScanKindsOf(a.Manifest) {
			if !coreKinds[k] && !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	slices.Sort(out)
	return out
}

// hookScanner is one addon scan kind as a tracking.Scanner.
type hookScanner struct {
	h     *Hooks
	addon string
	kind  string
}

func (s *hookScanner) Kind() string { return s.kind }

// Scan calls tracking.scan:<kind> (120 s) for the household. {sources}
// gives the kind's rows, each taken as of the declared kind and without
// library ids (itemId, fileId name library rows only); {unchanged}, 204
// and 404 are tracking.ErrUnchanged (the stored rows stay, E24); a down,
// failing or slow addon is an error (the service logs it and keeps the
// rows).
func (s *hookScanner) Scan(ctx context.Context, t tracking.Title, eps []tracking.Episode) ([]tracking.Source, error) {
	name := scanHookPrefix + s.kind
	req := addonkit.ScanRequest{Title: addonkit.TrackingTitle{ID: t.ID, Kind: t.Kind, TMDBID: t.TMDBID, IMDbID: t.IMDbID,
		Name: t.Name, Poster: t.Poster, Status: t.Status, Cert: t.Cert}, Episodes: make([]addonkit.TrackingEpisode, 0, len(eps))}
	for _, e := range eps {
		req.Episodes = append(req.Episodes, addonkit.TrackingEpisode{TitleID: t.ID, Season: e.Season, Number: e.Number,
			Name: e.Name, AirDate: e.AirDate})
	}
	var raw json.RawMessage
	if err := s.h.Call(ctx, s.addon, name, nil, req, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		s.h.log().Debug("addon hook: nothing to add", "addon", s.addon, "hook", name, "title", t.ID)
		return nil, tracking.ErrUnchanged
	}
	var ans addonkit.ScanAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("addon %s hook %s: %w: %v", s.addon, name, ErrHookFailed, err)
	}
	if ans.Unchanged {
		return nil, tracking.ErrUnchanged
	}
	out := make([]tracking.Source, 0, len(ans.Sources))
	for _, x := range ans.Sources {
		m := x.Meta
		out = append(out, tracking.Source{TitleID: t.ID, Season: x.Season, Episode: x.Episode, Kind: s.kind, Ref: x.Ref,
			Provider: x.Provider, Quality: x.Quality, Labels: x.Labels,
			Meta: tracking.SourceMeta{SourceID: m.SourceID, Provider: m.Provider, Voice: m.Voice, Lang: m.Lang,
				Audio: m.Audio, Studios: m.Studios, Subs: m.Subs, FileIndex: m.FileIndex}})
	}
	return out, nil
}

// ExtraTracked is tracking.Store's ExtraTracked over the tracking.titles
// hook (E5): every addon declaring it, in parallel within 10 s, for the
// household; each title of kind series or movie with a TMDB id is made a
// tracked title (ensure) and its id given, once. An addon that is down,
// slow or answers something else adds nothing this round (logged) and is
// no error: the next round has its titles again. A title ensure fails on
// is left out (logged).
func (h *Hooks) ExtraTracked(ensure func(ctx context.Context, kind string, tmdbID int64, name, poster string) (int64, error)) func(ctx context.Context) ([]int64, error) {
	return func(ctx context.Context) ([]int64, error) {
		var titles []addonkit.ExtraTitle
		eachAnswer(h, ctx, "tracking.titles", nil, nil, func(_ string, a addonkit.TitlesAnswer) {
			titles = append(titles, a.Titles...)
		})
		seen := map[[2]any]bool{}
		var ids []int64
		for _, x := range titles {
			k := [2]any{x.Kind, x.TMDBID}
			if x.Kind != "series" && x.Kind != "movie" || x.TMDBID <= 0 || seen[k] {
				continue
			}
			seen[k] = true
			id, err := ensure(ctx, x.Kind, x.TMDBID, x.Name, x.Poster)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				h.log().Warn("addon hook: tracked title", "kind", x.Kind, "tmdbId", x.TMDBID, "err", err)
				continue
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
}

// watchStates asks every addon declaring tracking.watched (in parallel
// within 3 s, for the household: the profile is in the body) for userID's
// states of a title, merged: watched if any says so, the furthest
// position. Down, slow or failing addons are left out.
func (h *Hooks) watchStates(ctx context.Context, userID int64, kind string, tmdbID int64) map[[2]int]tracking.WatchState {
	out := map[[2]int]tracking.WatchState{}
	req := addonkit.WatchedRequest{UserID: userID, Kind: kind, TMDBID: tmdbID}
	eachAnswer(h, ctx, "tracking.watched", nil, req, func(_ string, a addonkit.WatchedAnswer) {
		for _, e := range a.Episodes {
			k := [2]int{e.Season, e.Number}
			st := out[k]
			st.Watched = st.Watched || e.Watched
			st.PositionSec = max(st.PositionSec, e.PositionSec)
			out[k] = st
		}
	})
	return out
}

// Watched is the tracking.WatchedFunc of the addons' tracking.watched
// (E6): the episodes some addon says the profile watched. Never an error.
func (h *Hooks) Watched() tracking.WatchedFunc {
	return func(ctx context.Context, userID int64, t tracking.Title) (map[[2]int]bool, error) {
		out := map[[2]int]bool{}
		for k, st := range h.watchStates(ctx, userID, t.Kind, t.TMDBID) {
			if st.Watched {
				out[k] = true
			}
		}
		return out, nil
	}
}

// WatchStates is the tracking.WatchStateFunc of the addons'
// tracking.watched (E6), for the episodes endpoint. Never an error.
func (h *Hooks) WatchStates() tracking.WatchStateFunc {
	return func(ctx context.Context, userID int64, kind string, tmdbID int64) (map[[2]int]tracking.WatchState, error) {
		return h.watchStates(ctx, userID, kind, tmdbID), nil
	}
}

// Interest is a notify.InterestFunc over the notify.interest hook (E7):
// the profiles every addon declaring it names for a title (in parallel
// within 5 s, for the household), merged once each in id order. A down,
// slow or failing addon is left out and never an error: one addon must
// not stop the letters.
func (h *Hooks) Interest() notify.InterestFunc {
	return func(ctx context.Context, kind string, tmdbID int64) ([]int64, error) {
		var ids []int64
		eachAnswer(h, ctx, "notify.interest", nil, addonkit.InterestRequest{Kind: kind, TMDBID: tmdbID}, func(_ string, a addonkit.InterestAnswer) {
			for _, u := range a.UserIDs {
				if u > 0 {
					ids = append(ids, u)
				}
			}
		})
		slices.Sort(ids)
		return slices.Compact(ids), nil
	}
}
