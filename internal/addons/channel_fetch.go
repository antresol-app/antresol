package addons

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/channels"
)

// ChannelFetcher gets «ТВ» channels' programmes on disk through an addon
// (channels.Fetcher over the channels.fetch hook): the first addon in id
// order that is on, running and declares the hook answers. The addon
// downloads (never streams) and puts the files into the library as any
// download of it; the core sees them arrive there and removes them after
// they aired (channels/fetch_pass.go).
type ChannelFetcher struct {
	H   *Hooks
	Log *slog.Logger
}

var _ channels.Fetcher = (*ChannelFetcher)(nil)

// errNoFetchAddon: no addon answers channels.fetch now.
var errNoFetchAddon = errors.New("addons: no addon answers channels.fetch now")

func (x *ChannelFetcher) log() *slog.Logger {
	if x.Log != nil {
		return x.Log
	}
	return slog.Default()
}

func (x *ChannelFetcher) provider(ctx context.Context) (Installed, bool) {
	if x == nil || x.H == nil || x.H.T == nil {
		return Installed{}, false
	}
	list, err := x.H.T.List(ctx)
	if err != nil {
		x.log().Warn("addon channels.fetch: list", "err", err)
		return Installed{}, false
	}
	slices.SortFunc(list, func(a, b Installed) int { return strings.Compare(a.ID, b.ID) })
	for _, a := range list {
		if !a.Enabled || !a.Manifest.Answers("channels.fetch") {
			continue
		}
		if st, _ := x.H.T.Serving(a.ID); st == StatusRunning {
			return a, true
		}
	}
	return Installed{}, false
}

// Ready: an addon that fetches runs now.
func (x *ChannelFetcher) Ready(ctx context.Context) bool {
	_, ok := x.provider(ctx)
	return ok
}

// Fetch tells the addon what is wanted and what to drop. Its answer for a
// key it was not asked about is left out.
func (x *ChannelFetcher) Fetch(ctx context.Context, want []channels.FetchWant, drop []string) ([]channels.FetchState, error) {
	in, ok := x.provider(ctx)
	if !ok {
		return nil, errNoFetchAddon
	}
	body := addonkit.ChannelFetch{Want: make([]addonkit.ChannelFetchTitle, 0, len(want)), Drop: drop}
	asked := map[string]bool{}
	for _, w := range want {
		asked[w.Key] = true
		body.Want = append(body.Want, addonkit.ChannelFetchTitle{Key: w.Key, Kind: w.Kind, TMDBID: w.TMDBID, Season: w.Season,
			Episode: w.Episode, Title: w.Title, Year: w.Year})
	}
	var ans addonkit.ChannelFetchAnswer
	if err := x.H.Call(ctx, in.ID, "channels.fetch", nil, body, &ans); err != nil {
		return nil, err
	}
	var out []channels.FetchState
	for _, s := range ans.States {
		if !asked[s.Key] {
			continue
		}
		switch s.State {
		case channels.FetchSearching, channels.FetchDownloading, channels.FetchDone, channels.FetchFailed:
		default:
			continue
		}
		out = append(out, channels.FetchState{Key: s.Key, State: s.State, Size: s.Size, Done: s.Done, Rate: s.Rate,
			Existing: s.Existing, Keep: s.Keep, Error: s.Error})
	}
	return out, nil
}
