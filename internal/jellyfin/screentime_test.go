package jellyfin

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/screentime"
)

// Screen time of a kid on the Jellyfin API: PlaybackInfo is refused with
// NotAllowed, a direct stream with 403, and the playstate reports of a
// playing player count (a paused one's do not).
func TestScreenTimeJellyfin(t *testing.T) {
	e := newPlayEnv(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	st := &screentime.Service{DB: e.db, Now: func() time.Time { return now }, Loc: time.UTC}
	_, player := api.NewRouterWithPlayer(api.Deps{Users: e.users, Sessions: auth.NewSessions(e.db), Network: e.network,
		Library: e.repo, Progress: e.prog, HLS: e.hls, DataDir: e.s.DataDir, ScreenTime: st})
	e.s.Player, e.s.ScreenTime = player, st
	ctx := context.Background()

	res, w := e.playbackInfo(e.mishaTok, e.mp4, e.avProfile, nil)
	if w.Code != 200 || res.ErrorCode != "" || len(res.MediaSources) != 1 {
		t.Fatalf("no limits: %d %s", w.Code, w.Body)
	}
	ms := res.MediaSources[0]
	stream := func() int {
		q := url.Values{"static": {"true"}, "mediaSourceId": {ms.Id}, "ApiKey": {e.mishaTok}}
		r := httptest.NewRequest("GET", "/Videos/"+ItemID(e.mp4)+"/stream?"+q.Encode(), nil)
		r.RemoteAddr = homeAddr
		r.Header.Set("Range", "bytes=0-99")
		rw := httptest.NewRecorder()
		e.s.ServeHTTP(rw, r)
		return rw.Code
	}
	if c := stream(); c != 206 {
		t.Fatalf("stream %d", c)
	}

	// reports of a playing player count; paused ones do not
	hdr := swiftfinHdr(e.mishaTok)
	report := func(paused bool) {
		body := map[string]any{"ItemId": ItemID(e.mp4), "PlaySessionId": res.PlaySessionId, "PositionTicks": 50_000_000, "IsPaused": paused}
		if w := e.do("POST", "/Sessions/Playing/Progress", hdr, body); w.Code != 204 {
			t.Fatalf("report %d", w.Code)
		}
	}
	for i := 0; i < 4; i++ {
		report(false)
		now = now.Add(10 * time.Second)
	}
	now = now.Add(time.Minute)
	for i := 0; i < 4; i++ {
		report(true)
		now = now.Add(10 * time.Second)
	}
	s, err := st.Status(ctx, e.misha)
	if err != nil || s.Video.UsedSec < 30 || s.Video.UsedSec > 50 {
		t.Fatalf("%+v %v", s.Video, err)
	}

	if err := st.SetLimits(ctx, e.misha.ID, screentime.Limits{VideoMin: new(int)}); err != nil {
		t.Fatal(err)
	}
	if res, w := e.playbackInfo(e.mishaTok, e.mp4, e.avProfile, nil); w.Code != 200 || res.ErrorCode != "NotAllowed" || len(res.MediaSources) != 0 {
		t.Fatalf("limited: %d %s", w.Code, w.Body)
	}
	if c := stream(); c != 403 {
		t.Fatalf("limited stream %d", c)
	}
	// no offline copies (Swiftfin's downloads) while a rule is set
	if w := e.do("GET", "/Items/"+ItemID(e.mp4)+"/Download?api_key="+e.mishaTok, nil, nil); w.Code != 403 {
		t.Fatalf("download %d", w.Code)
	}
	// the adult plays on
	if res, w := e.playbackInfo(e.anyaTok, e.mp4, e.avProfile, nil); w.Code != 200 || res.ErrorCode != "" {
		t.Fatalf("adult: %d %s", w.Code, w.Body)
	}
}
