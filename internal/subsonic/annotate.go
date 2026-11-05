package subsonic

import (
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// scrobble records plays (submission=true, the default) of id… at time…
// (ms since the epoch; now when absent). A "now playing" note
// (submission=false) is accepted and not stored.
func (c *call) scrobble() *response {
	ids := c.p["id"]
	if len(ids) == 0 {
		return c.missing("id")
	}
	if s := c.p.Get("submission"); s == "false" || s == "0" {
		return c.ok()
	}
	times := c.p["time"]
	for i, v := range ids {
		id, ok := parseID(v, "")
		if !ok {
			return c.fail(codeNotFound, msgSongMissing)
		}
		tr, err := c.track(id)
		if err != nil {
			return c.internal("scrobble", err)
		}
		if tr == nil {
			return c.fail(codeNotFound, msgSongMissing)
		}
		var at time.Time
		if i < len(times) {
			if ms, err := strconv.ParseInt(times[i], 10, 64); err == nil && ms > 0 {
				at = time.UnixMilli(ms)
				if now := time.Now(); at.After(now) {
					at = now
				}
			}
		}
		if err := c.s.Music.Played(c.ctx, c.u.ID, tr.ID, at); err != nil {
			return c.internal("scrobble", err)
		}
	}
	return c.ok()
}

func (c *call) star() *response   { return c.setStar(true) }
func (c *call) unstar() *response { return c.setStar(false) }

// setStar stars or unstars id… (a track, album or artist), albumId… and
// artistId…; every id must be a visible item of its kind.
func (c *call) setStar(on bool) *response {
	type target struct {
		param string
		kinds []string
	}
	targets := []target{
		{"id", []string{library.KindTrack, library.KindAlbum, library.KindArtist}},
		{"albumId", []string{library.KindAlbum}},
		{"artistId", []string{library.KindArtist}},
	}
	var ids []int64
	for _, t := range targets {
		for _, v := range c.p[t.param] {
			id, ok := parseID(v, "")
			if !ok {
				if id, ok = parseID(v, "al-"); !ok {
					id, ok = parseID(v, "ar-")
				}
			}
			if !ok {
				return c.fail(codeNotFound, msgNotFound)
			}
			it, err := c.item(id, t.kinds...)
			if err != nil {
				return c.internal("star", err)
			}
			if it == nil {
				return c.fail(codeNotFound, msgNotFound)
			}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return c.missing("id")
	}
	for _, id := range ids {
		var err error
		if on {
			err = c.s.Music.Star(c.ctx, c.u.ID, id)
		} else {
			err = c.s.Music.Unstar(c.ctx, c.u.ID, id)
		}
		if err != nil {
			return c.internal("star", err)
		}
	}
	return c.ok()
}
