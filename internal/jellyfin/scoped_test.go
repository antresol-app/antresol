package jellyfin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// hlsURLs plays an item over HLS as AVPlayer and returns every URL it
// fetches (TranscodingUrl, playlists, a segment, subtitle renditions).
func (e *playEnv) hlsURLs(t *testing.T, tok string) (pbResult, []string) {
	t.Helper()
	res, w := e.playbackInfo(tok, e.mkv, e.avProfile, nil)
	if w.Code != 200 || res.MediaSources[0].TranscodingUrl == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	tu := res.MediaSources[0].TranscodingUrl
	urls := []string{tu}
	var mainURI string
	for _, u := range uris(e.getOK(tu).Body.String()) {
		abs := resolve(tu, u)
		urls = append(urls, abs)
		if strings.HasPrefix(u, "main.m3u8") {
			mainURI = abs
		} else {
			for _, s := range uris(e.getOK(abs).Body.String()) {
				urls = append(urls, resolve(abs, s))
			}
		}
	}
	segs := uris(e.getOK(mainURI).Body.String())
	urls = append(urls, resolve(mainURI, segs[0]))
	return res, urls
}

// MAJOR 1: no URL a player fetches carries the access token: playlists,
// segments and subtitles are opened by the play session (128 bits, bound
// to the profile and the token, gone with it). The session opens only its
// own play's files, never the direct stream; a revoked token ends it.
func TestPlayURLsCarryThePlaySessionNotTheToken(t *testing.T) {
	e := newPlayEnv(t)
	res, urls := e.hlsURLs(t, e.anyaTok)
	for _, u := range urls {
		if strings.Contains(u, e.anyaTok) {
			t.Fatalf("token in %s", u)
		}
		if w := e.do("GET", u, nil, nil); w.Code != 200 {
			t.Fatalf("GET %s without headers: %d", u, w.Code)
		}
	}
	// the sidecar subtitles of a direct play (mpv) too
	dres, _ := e.playbackInfo(e.anyaTok, e.mkv, e.mpvProf, nil)
	for _, st := range dres.MediaSources[0].MediaStreams {
		if st.DeliveryUrl != "" && (strings.Contains(st.DeliveryUrl, e.anyaTok) || !strings.Contains(st.DeliveryUrl, "PlaySessionId="+dres.PlaySessionId)) {
			t.Fatalf("delivery URL %s", st.DeliveryUrl)
		}
	}
	// an unknown play session opens nothing
	bad := strings.Replace(urls[0], res.PlaySessionId, strings.Repeat("0", 32), 1)
	if w := e.do("GET", bad, nil, nil); w.Code != 401 {
		t.Fatalf("unknown play session: %d", w.Code)
	}
	// the play session is not a key to the file itself
	stream := "/Videos/" + ItemID(e.mkv) + "/stream?static=true&PlaySessionId=" + res.PlaySessionId
	if w := e.do("GET", stream, nil, nil); w.Code != 401 {
		t.Fatalf("stream by play session: %d", w.Code)
	}
	// nor to another file's subtitles (the mp4's file id under the mkv's play)
	other := "/Videos/" + ItemID(e.mp4) + "/" + FileID(e.fileOf(e.mp4).ID) + "/Subtitles/1000/0/Stream.vtt?PlaySessionId=" + res.PlaySessionId
	if w := e.do("GET", other, nil, nil); w.Code != 404 {
		t.Fatalf("another file's subtitle by play session: %d", w.Code)
	}
	// signing out revokes the token: its play sessions stop working
	if w := e.do("POST", "/Sessions/Logout", swiftfinHdr(e.anyaTok), nil); w.Code != 204 {
		t.Fatalf("logout %d", w.Code)
	}
	if w := e.do("GET", urls[len(urls)-1], nil, nil); w.Code != 401 {
		t.Fatalf("segment after logout: %d", w.Code)
	}
}

// A passwordless profile's play session works at home only, like its token.
func TestPlaySessionPasswordlessHomeOnly(t *testing.T) {
	e := newPlayEnv(t)
	tok := e.login("Гость", e.guestApp)
	res, _ := e.playbackInfo(tok, e.mp4, e.avProfile, nil)
	u := "/Videos/" + ItemID(e.mp4) + "/" + res.MediaSources[0].Id + "/Subtitles/3/subtitles.m3u8?PlaySessionId=" + res.PlaySessionId
	if w := e.remote("GET", u, nil, nil); w.Code != 401 {
		t.Fatalf("outside home: %d", w.Code)
	}
}

// MAJOR 2: one profile calling PlaybackInfo in a loop keeps at most
// maxPlaysPerProfile play sessions and evicts only its own oldest.
func TestPlaySessionsCappedPerProfile(t *testing.T) {
	e := newPlayEnv(t)
	kid, _ := e.playbackInfo(e.mishaTok, e.mp4, e.avProfile, nil)
	first, _ := e.playbackInfo(e.anyaTok, e.mp4, e.avProfile, nil)
	for i := 0; i < maxPlaysPerProfile+5; i++ {
		e.playbackInfo(e.anyaTok, e.mp4, e.avProfile, nil)
	}
	if n := e.s.plays.count(e.anya.ID); n != maxPlaysPerProfile {
		t.Fatalf("Аня has %d play sessions", n)
	}
	if _, ok := e.s.plays.get(first.PlaySessionId, e.anya.ID); ok {
		t.Fatal("the oldest play session was kept")
	}
	if _, ok := e.s.plays.get(kid.PlaySessionId, e.misha.ID); !ok {
		t.Fatal("another profile's play session was evicted")
	}
}

// MINOR 4: two devices of one profile playing the same film over HLS keep
// both sessions; a new PlaybackInfo on the same device replaces its own.
func TestTwoDevicesDoNotStopEachOther(t *testing.T) {
	e := newPlayEnv(t)
	start := func(hdr map[string]string) (string, string) {
		t.Helper()
		w := e.do("POST", "/Items/"+ItemID(e.mkv)+"/PlaybackInfo", hdr, map[string]any{"DeviceProfile": e.avProfile})
		var res pbResult
		decode(t, w, &res)
		e.getOK(res.MediaSources[0].TranscodingUrl)
		p, _ := e.s.plays.get(res.PlaySessionId, e.anya.ID)
		return res.PlaySessionId, p.hls
	}
	tvTok := e.anyaTok // iPhone (swiftfinHdr's DeviceId)
	atvHdr := map[string]string{"Authorization": "MediaBrowser DeviceId=ATV-2, Device=Apple TV, Client=Swiftfin tvOS, Version=1.3.0"}
	w := e.do("POST", "/Users/AuthenticateByName", atvHdr, map[string]string{"Username": "Аня", "Pw": e.anyaApp})
	var login struct{ AccessToken string }
	decode(t, w, &login)
	atvHdr["Authorization"] += ", Token=" + login.AccessToken

	_, phone := start(swiftfinHdr(tvTok))
	_, atv := start(atvHdr)
	if phone == "" || atv == "" || phone == atv {
		t.Fatalf("sessions %q %q", phone, atv)
	}
	for _, sid := range []string{phone, atv} {
		if _, ok := e.hls.TimelineOffset(sid); !ok {
			t.Fatalf("session %s was stopped by the other device", sid)
		}
	}
	_, phone2 := start(swiftfinHdr(tvTok))
	if _, ok := e.hls.TimelineOffset(phone); ok {
		t.Fatal("the same device's earlier session was kept")
	}
	if _, ok := e.hls.TimelineOffset(atv); !ok || phone2 == "" {
		t.Fatal("the other device's session was stopped")
	}
}

// MINOR 5: a bitrate test is at most Swiftfin's largest; one full test per
// token in 10 s, a test meanwhile gets a short answer (not an error).
func TestBitrateTestBounded(t *testing.T) {
	e := newLibEnv(t)
	if w := e.get(e.anyaTok, "/Playback/BitrateTest?size=20000000"); w.Code != 400 {
		t.Fatalf("oversized: %d", w.Code)
	}
	if w := e.get(e.anyaTok, "/Playback/BitrateTest?size=5000000"); w.Code != 200 || w.Body.Len() != 5_000_000 {
		t.Fatalf("first: %d %d", w.Code, w.Body.Len())
	}
	if w := e.get(e.anyaTok, "/Playback/BitrateTest?size=5000000"); w.Code != 200 || w.Body.Len() != bitrateTestShort {
		t.Fatalf("second: %d %d", w.Code, w.Body.Len())
	}
	if w := e.get(e.mishaTok, "/Playback/BitrateTest?size=1000000"); w.Body.Len() != 1_000_000 {
		t.Fatalf("another token: %d", w.Body.Len())
	}
}

type countActivity struct{ begins atomic.Int32 }

func (c *countActivity) Begin() { c.begins.Add(1) }
func (c *countActivity) End()   {}

// MINOR 6: a media read counts as playback activity only once it is
// authorised (an anonymous request never holds the analysis off).
func TestActivityOnlyAfterAuth(t *testing.T) {
	e := newPlayEnv(t)
	a := &countActivity{}
	e.s.Activity = a
	path := "/Videos/" + ItemID(e.mp4) + "/stream?static=true"
	for _, p := range []string{path, path + "&ApiKey=" + strings.Repeat("f", 32), "/Videos/" + ItemID(e.mp4) + "/hls1/" + strings.Repeat("0", 32) + "/seg00000.ts"} {
		e.do("GET", p, nil, nil)
	}
	if n := a.begins.Load(); n != 0 {
		t.Fatalf("%d reads counted before auth", n)
	}
	if w := e.do("GET", path+"&ApiKey="+e.anyaTok, nil, nil); w.Code != 200 {
		t.Fatalf("stream %d", w.Code)
	}
	if n := a.begins.Load(); n != 1 {
		t.Fatalf("%d reads counted", n)
	}
}

// Multi-part movies: a position reported for part 2 (MediaSourceId) is
// stored on the movie's timeline (part 1's length + the position), as the
// web player stores it; alternative versions keep their own timeline.
func TestPlaystateMultiPartTimeline(t *testing.T) {
	e := newLibEnv(t)
	dir := filepath.Join(e.media, "Parts")
	os.MkdirAll(dir, 0o755)
	var files []library.FileInput
	for _, n := range []string{"Film.cd1.avi", "Film.cd2.avi"} {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte("x"), 0o644)
		files = append(files, library.FileInput{Path: p, Size: 1, Mtime: 1})
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Две части", Year: 1999, Rating: "0+", File: files[0]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Две части", Year: 1999, Rating: "0+", File: files[1]}); err != nil {
		t.Fatal(err)
	}
	fs, _ := e.repo.FilesOf(context.Background(), id)
	if len(fs) != 2 {
		t.Fatalf("files %d", len(fs))
	}
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=3600000 WHERE item_id=?`, id); err != nil {
		t.Fatal(err)
	}
	var cd2 library.File
	for _, f := range fs {
		if strings.Contains(f.Path, "cd2") {
			cd2 = f
		}
	}
	w := e.do("POST", "/Sessions/Playing/Stopped", swiftfinHdr(e.anyaTok),
		map[string]any{"ItemId": ItemID(id), "MediaSourceId": FileID(cd2.ID), "PositionTicks": int64(600_000) * 10_000})
	if w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	en, err := e.prog.Get(context.Background(), e.anya.ID, id)
	if err != nil || en.PositionMs != 3_600_000+600_000 || en.DurationMs != 7_200_000 {
		t.Fatalf("progress %+v %v", en, err)
	}
	// the item's run time is the whole movie
	if d := e.userData(e.anyaTok, id); d.PlaybackPositionTicks != (3_600_000+600_000)*10_000 {
		t.Fatalf("user data %+v", d)
	}
}

func TestPlaybackPartsLikeTheWeb(t *testing.T) {
	f := func(names ...string) []library.File {
		var out []library.File
		for i, n := range names {
			out = append(out, library.File{ID: int64(i + 1), Path: "/m/" + n})
		}
		return out
	}
	if p := playbackParts(f("Фильм - cd2.mkv", "Фильм - cd1.mkv")); len(p) != 2 || p[0].ID != 2 {
		t.Fatalf("%+v", p)
	}
	if p := playbackParts(f("Film.Part.1.mkv", "Film.часть 2.mkv")); len(p) != 2 {
		t.Fatalf("%+v", p)
	}
	if p := playbackParts(f("Film.1080p.mkv", "Film.2160p.mkv")); p != nil {
		t.Fatalf("versions: %+v", p)
	}
	if p := playbackParts(f("Film.cd1.mkv", "Film.cd1.avi")); p != nil {
		t.Fatalf("same part twice: %+v", p)
	}
}
