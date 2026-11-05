package jellyfin

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/progress"
)

// userData GETs an item as the profile and returns its UserData.
func (e *libEnv) userData(tok string, item int64) UserItemDataDto {
	e.t.Helper()
	w := e.get(tok, "/Items/"+ItemID(item))
	if w.Code != 200 {
		e.t.Fatalf("item: %d %s", w.Code, w.Body)
	}
	var d BaseItemDto
	decode(e.t, w, &d)
	if d.UserData == nil {
		e.t.Fatalf("no UserData: %s", w.Body)
	}
	return *d.UserData
}

// Swiftfin's whole sequence over HLS: login (the env), the item,
// PlaybackInfo, the playlists and a segment, then the player's reports:
// start at 0 (not saved), progress at 6 s and a pause, stopped at 7.5 s.
// The position is the profile's progress — what the web and the app
// resume from — and the item's UserData and the Resume shelf show it.
// Stopped ends the HLS session.
func TestSwiftfinPlaystateSequence(t *testing.T) {
	e := newPlayEnv(t)
	hdr := swiftfinHdr(e.anyaTok)
	if w := e.get(e.anyaTok, "/Users/"+UserID(e.anya.ID)+"/Items/"+ItemID(e.mkv)); w.Code != 200 {
		t.Fatalf("item %d", w.Code)
	}
	res, _ := e.playbackInfo(e.anyaTok, e.mkv, e.avProfile, nil)
	ms := res.MediaSources[0]
	if ms.TranscodingUrl == "" {
		t.Fatalf("not HLS: %+v", ms)
	}
	master := e.getOK(ms.TranscodingUrl).Body.String()
	var mainURI string
	for _, u := range uris(master) {
		if strings.HasPrefix(u, "main.m3u8") {
			mainURI = resolve(ms.TranscodingUrl, u)
		}
	}
	segs := uris(e.getOK(mainURI).Body.String())
	e.getOK(resolve(mainURI, segs[0]))

	report := func(path string, ticks int64, extra map[string]any) {
		t.Helper()
		body := map[string]any{"ItemId": ItemID(e.mkv), "MediaSourceId": ms.Id, "PlaySessionId": res.PlaySessionId,
			"SessionId": res.PlaySessionId, "PositionTicks": ticks, "SubtitleStreamIndex": -1, "SecondarySubtitleStreamIndex": -1}
		for k, v := range extra {
			body[k] = v
		}
		if w := e.do("POST", path, hdr, body); w.Code != 204 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	report("/Sessions/Playing", 0, map[string]any{"IsPaused": false})
	if _, err := e.prog.Get(context.Background(), e.anya.ID, e.mkv); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("a start at 0 was saved: %v", err)
	}
	report("/Sessions/Playing/Progress", 60_000_000, nil)
	report("/Sessions/Playing/Progress", 60_000_000, map[string]any{"IsPaused": true})
	if w := e.do("POST", "/Sessions/Playing/Ping?playSessionId="+res.PlaySessionId, hdr, nil); w.Code != 204 {
		t.Fatalf("ping %d", w.Code)
	}
	report("/Sessions/Playing/Stopped", 75_000_000, nil)

	en, err := e.prog.Get(context.Background(), e.anya.ID, e.mkv)
	if err != nil || en.PositionMs != 7500 || en.Finished || en.DurationMs < 11000 || en.DurationMs > 13000 {
		t.Fatalf("progress %+v %v", en, err)
	}
	ud := e.userData(e.anyaTok, e.mkv)
	if ud.PlaybackPositionTicks != 75_000_000 || ud.Played || ud.PlayedPercentage == nil {
		t.Fatalf("user data %+v", ud)
	}
	if !has(e.items("", "/UserItems/Resume").Items, ItemID(e.mkv)) {
		t.Fatal("not on the Resume shelf")
	}
	if p, _ := e.s.plays.get(res.PlaySessionId, e.anya.ID); p.hls != "" {
		t.Fatal("Stopped left the HLS session running")
	}
	// another device continues; at 92 % the film counts as watched
	report("/Sessions/Playing/Stopped", 115_000_000, nil)
	if ud := e.userData(e.anyaTok, e.mkv); !ud.Played || ud.PlayCount != 1 {
		t.Fatalf("watched: %+v", ud)
	}
	if has(e.items("", "/UserItems/Resume").Items, ItemID(e.mkv)) {
		t.Fatal("a watched film on the Resume shelf")
	}
}

// A failed stop, and reports without a position, save nothing.
func TestPlaystateFailedNotSaved(t *testing.T) {
	e := newLibEnv(t)
	hdr := swiftfinHdr(e.anyaTok)
	e.do("POST", "/Sessions/Playing/Stopped", hdr, map[string]any{"ItemId": ItemID(e.interstellar), "PositionTicks": 600_000_000, "Failed": true})
	e.do("POST", "/Sessions/Playing/Progress", hdr, map[string]any{"ItemId": ItemID(e.interstellar)})
	if _, err := e.prog.Get(context.Background(), e.anya.ID, e.interstellar); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("saved: %v", err)
	}
}

// Infuse's legacy forms: /Users/{u}/PlayingItems/{id}/Progress?positionTicks=
// and /Users/{u}/PlayedItems/{id}.
func TestPlaystateLegacyRoutes(t *testing.T) {
	e := newLibEnv(t)
	u, it := UserID(e.anya.ID), ItemID(e.interstellar)
	if w := e.do("POST", "/Users/"+u+"/PlayingItems/"+it+"/Progress?positionTicks=6000000000", infuseHdr(e.anyaTok), nil); w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	if ud := e.userData(e.anyaTok, e.interstellar); ud.PlaybackPositionTicks != 6_000_000_000 {
		t.Fatalf("%+v", ud)
	}
	w := e.do("POST", "/Users/"+u+"/PlayedItems/"+it, infuseHdr(e.anyaTok), nil)
	var ud UserItemDataDto
	decode(t, w, &ud)
	if w.Code != 200 || !ud.Played || ud.Key == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// another profile's path is refused
	if w := e.do("POST", "/Users/"+UserID(e.misha.ID)+"/PlayedItems/"+it, infuseHdr(e.anyaTok), nil); w.Code != 403 {
		t.Fatalf("other profile: %d", w.Code)
	}
}

// /UserPlayedItems marks and unmarks; a series marks its episodes; the
// answer is the new UserData. /UserFavoriteItems is the watchlist.
func TestPlayedAndFavorite(t *testing.T) {
	e := newLibEnv(t)
	hdr := swiftfinHdr(e.anyaTok)
	q := "?userId=" + url.QueryEscape(UserID(e.anya.ID))
	w := e.do("POST", "/UserPlayedItems/"+ItemID(e.interstellar)+q, hdr, nil)
	var ud UserItemDataDto
	decode(t, w, &ud)
	if !ud.Played {
		t.Fatalf("%s", w.Body)
	}
	w = e.do("DELETE", "/UserPlayedItems/"+ItemID(e.interstellar)+q, hdr, nil)
	decode(t, w, &ud)
	if ud.Played {
		t.Fatalf("%s", w.Body)
	}
	w = e.do("POST", "/UserPlayedItems/"+ItemID(e.series)+q, hdr, nil)
	decode(t, w, &ud)
	if !ud.Played || ud.UnplayedItemCount == nil || *ud.UnplayedItemCount != 0 {
		t.Fatalf("series: %s", w.Body)
	}
	for _, ep := range e.eps {
		if en, err := e.prog.Get(context.Background(), e.anya.ID, ep); err != nil || !en.Finished {
			t.Fatalf("episode %d: %+v %v", ep, en, err)
		}
	}
	w = e.do("POST", "/UserFavoriteItems/"+ItemID(e.interstellar)+q, hdr, nil)
	decode(t, w, &ud)
	if !ud.IsFavorite {
		t.Fatalf("%s", w.Body)
	}
	ids, _ := e.prog.Watchlist(context.Background(), e.anya.ID)
	if len(ids) != 1 || ids[0] != e.interstellar {
		t.Fatalf("watchlist %v", ids)
	}
	w = e.do("DELETE", "/UserFavoriteItems/"+ItemID(e.interstellar)+q, hdr, nil)
	decode(t, w, &ud)
	if ud.IsFavorite {
		t.Fatalf("%s", w.Body)
	}
	if w := e.get(e.anyaTok, "/UserItems/"+ItemID(e.interstellar)+"/UserData"); w.Code != 200 {
		t.Fatalf("user data %d", w.Code)
	}
}

// A kid can neither mark nor record progress on what it may not see.
func TestPlaystateKidHidden(t *testing.T) {
	e := newLibEnv(t)
	hdr := swiftfinHdr(e.mishaTok)
	e.do("POST", "/Sessions/Playing/Stopped", hdr, map[string]any{"ItemId": ItemID(e.deadpool), "PositionTicks": 600_000_000})
	if _, err := e.prog.Get(context.Background(), e.misha.ID, e.deadpool); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("saved for a hidden film: %v", err)
	}
	for _, m := range []string{"POST", "DELETE"} {
		for _, p := range []string{"/UserPlayedItems/", "/UserFavoriteItems/"} {
			if w := e.do(m, p+ItemID(e.deadpool), hdr, nil); w.Code != 404 {
				t.Fatalf("%s %s: %d", m, p, w.Code)
			}
		}
	}
	if w := e.get(e.mishaTok, "/UserItems/"+ItemID(e.deadpool)+"/UserData"); w.Code != 404 {
		t.Fatalf("user data %d", w.Code)
	}
}
