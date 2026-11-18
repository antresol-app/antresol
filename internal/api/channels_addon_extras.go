package api

import (
	"context"
	"errors"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
)

// A kid never gets a title an addon gave a channel, on any channel, its
// own or a shared one: the channel list, the guide and the EPG leave such
// a programme out for a kid, and a kid's live stream of the channel is
// refused while its stream may hold one (the stream is the channel's, made once
// for every viewer, so the refusal is the viewer's, not the resolve's).

// kidHides: u is a kid and x is a programme an addon gave.
func kidHides(u auth.User, x channels.Info) bool {
	return u.IsKid && x.External && addons.IsChannelExtraKey(x.ExtraKey)
}

// kidHideEntries drops from es the entries a kid does not get (by info).
func kidHideEntries(u auth.User, es []channels.Entry, info map[int64]channels.Info) []channels.Entry {
	if !u.IsKid {
		return es
	}
	out := es[:0:0]
	for _, e := range es {
		if x, ok := info[e.ItemID]; ok && kidHides(u, x) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// addonExtraNear: a programme of channel ch that a viewer's stream may
// hold between from and to (the encoder runs ahead of the clock, and kept
// segments lie behind it: Live.Span) is a title an addon gave.
func (a *channelsAPI) addonExtraNear(ctx context.Context, ch int64, from, to time.Time) (bool, error) {
	es, err := a.svc.Store.Range(ctx, ch, from, to)
	if err != nil {
		return false, err
	}
	for _, e := range es {
		if e.ExtraID == 0 {
			continue
		}
		t, err := a.svc.Store.Extra(ctx, e.ExtraID)
		if errors.Is(err, channels.ErrNothing) {
			continue
		}
		if err != nil {
			return false, err
		}
		if addons.IsChannelExtraKey(t.Key) {
			return true, nil
		}
	}
	return false, nil
}
