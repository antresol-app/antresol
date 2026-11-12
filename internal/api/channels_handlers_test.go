package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
)

func newChannelsEnv(t *testing.T) (*libEnv, *channels.Service) { return newChannelsEnvIPTV(t, true) }

func newChannelsEnvIPTV(t *testing.T, iptv bool) (*libEnv, *channels.Service) {
	var svc *channels.Service
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = channels.New(db, nil)
		d.Channels, d.DB, d.IPTV = svc, db, iptv
	})
	return e, svc
}

func TestIPTVOffByDefault(t *testing.T) {
	e, _ := newChannelsEnvIPTV(t, false)
	if w := e.do("GET", "/api/channels/iptv", "", e.admin); w.Code != 404 {
		t.Fatalf("iptv links without IPTV: %d", w.Code)
	}
	tok, _ := e.newAppToken("iptv")
	if w := e.do("GET", "/iptv/"+tok+"/playlist.m3u", "", nil); strings.Contains(w.Body.String(), "#EXTM3U") {
		t.Fatalf("m3u without IPTV: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/channels", "", e.admin); w.Code != 200 {
		t.Fatalf("channels: %d", w.Code)
	}
}

func (e *libEnv) withDuration() {
	if _, err := e.db.Exec(`UPDATE files SET duration_ms=5400000`); err != nil {
		e.t.Fatal(err)
	}
}

type iptvLinks struct{ Playlist, Guide string }

func TestChannelsCRUDAndList(t *testing.T) {
	e, _ := newChannelsEnv(t)
	m1, _ := e.movie("Один", "12+", "a")
	m2, _ := e.movie("Два", "16+", "b")
	e.withDuration()

	w := e.do("POST", "/api/channels", `{"name":"Мои фильмы","category":"Кино","rules":{"titles":[`+itoa(m1)+`,`+itoa(m2)+`],"order":"ordered"}}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	if c.Number != channels.ManualFrom || c.Personal || !c.CanEdit {
		t.Fatalf("created %+v", c)
	}
	w = e.do("GET", "/api/channels", "", e.admin)
	var list struct {
		Channels  []channelJSON
		CanManage bool
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Channels) != 1 || list.Channels[0].Now == nil || list.Channels[0].Now.Title != "Один" || list.Channels[0].Next.Title != "Два" || !list.CanManage {
		t.Fatalf("list %s", w.Body)
	}
	// a kid does not see an unlimited channel; nor may it create one
	kid := e.kid("")
	w = e.do("GET", "/api/channels", "", kid)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Channels) != 0 {
		t.Fatalf("kid sees %s", w.Body)
	}
	if w := e.do("POST", "/api/channels", `{"name":"x"}`, kid); w.Code != 403 {
		t.Fatalf("kid create %d", w.Code)
	}
	// limited to 12+, the kid sees it
	if w := e.do("PUT", "/api/channels/"+itoa(c.ID), `{"name":"Мои фильмы","rules":{"titles":[`+itoa(m1)+`],"maxRating":"12+"}}`, e.admin); w.Code != 200 {
		t.Fatalf("update %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels", "", kid)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Channels) != 1 || list.Channels[0].Rules != nil {
		t.Fatalf("kid list %s", w.Body)
	}
	// another adult makes a personal channel: only it sees and edits it
	ann := e.login(`{"name":"Аня"}`)
	w = e.do("POST", "/api/channels", `{"name":"Анино","rules":{}}`, ann)
	json.Unmarshal(w.Body.Bytes(), &c)
	if w.Code != 201 || !c.Personal {
		t.Fatalf("personal %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/channels/"+itoa(c.ID-1), `{"name":"x"}`, ann); w.Code != 403 {
		t.Fatalf("ann edits shared: %d", w.Code)
	}
	w = e.do("GET", "/api/channels?manage=1", "", ann)
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Channels) != 1 || list.Channels[0].Name != "Анино" {
		t.Fatalf("ann manage %s", w.Body)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(c.ID), "", ann); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := e.do("POST", "/api/channels", `{"name":"Дубль","number":1,"rules":{}}`, e.admin); w.Code != 409 {
		t.Fatalf("duplicate number %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/channels", `{"name":"","rules":{}}`, e.admin); w.Code != 400 {
		t.Fatalf("empty name %d", w.Code)
	}
	// guide and options
	w = e.do("GET", "/api/channels/"+itoa(list.Channels[0].ID-1)+"/guide?hours=6", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"title":"Один"`) {
		t.Fatalf("guide %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/channels/options", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"title":"Два"`) || !strings.Contains(w.Body.String(), `"profiles":[`) {
		t.Fatalf("options %d %s", w.Code, w.Body)
	}
}

func TestIPTVPlaylistGuideAndToken(t *testing.T) {
	e, _ := newChannelsEnv(t)
	m1, _ := e.movie("Один", "12+", "a")
	e.withDuration()
	if w := e.do("POST", "/api/channels", `{"name":"Кино","category":"Фильмы","rules":{"titles":[`+itoa(m1)+`]}}`, e.admin); w.Code != 201 {
		t.Fatal(w.Body)
	}
	w := e.do("GET", "/api/channels/iptv", "", e.admin)
	var l iptvLinks
	json.Unmarshal(w.Body.Bytes(), &l)
	if !strings.HasPrefix(l.Playlist, "http://example.com/iptv/") || !strings.HasSuffix(l.Guide, "/epg.xml") {
		t.Fatalf("links %s", w.Body)
	}
	// the same link on the next visit
	w = e.do("GET", "/api/channels/iptv", "", e.admin)
	var l2 iptvLinks
	json.Unmarshal(w.Body.Bytes(), &l2)
	if l2 != l {
		t.Fatalf("link changed: %v %v", l, l2)
	}
	path := strings.TrimPrefix(l.Playlist, "http://example.com")
	w = e.do("GET", path, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `group-title="Фильмы",Кино`) || !strings.Contains(w.Body.String(), "/live/") {
		t.Fatalf("m3u %d %s", w.Code, w.Body)
	}
	w = e.do("GET", strings.TrimPrefix(l.Guide, "http://example.com"), "", nil)
	var doc struct {
		Programmes []struct {
			Title string `xml:"title"`
		} `xml:"programme"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &doc); err != nil || len(doc.Programmes) == 0 || doc.Programmes[0].Title != "Один" {
		t.Fatalf("epg %v %s", err, w.Body)
	}
	// logo
	base := strings.TrimSuffix(path, "/playlist.m3u")
	w = e.do("GET", base+"/logo/1.png", "", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("logo %d", w.Code)
	}
	// wrong tokens: 404; a reset ends the old one
	if w := e.do("GET", "/iptv/nope/playlist.m3u", "", nil); w.Code != 404 {
		t.Fatalf("bad token %d", w.Code)
	}
	if w := e.do("POST", "/api/channels/iptv/reset", "", e.admin); w.Code != 200 {
		t.Fatalf("reset %d", w.Code)
	}
	if w := e.do("GET", path, "", nil); w.Code != 404 {
		t.Fatalf("old token %d", w.Code)
	}
	// a token of another protocol is not an IPTV token
	tok, _ := e.newAppToken("jellyfin")
	if w := e.do("GET", "/iptv/"+tok+"/playlist.m3u", "", nil); w.Code != 404 {
		t.Fatalf("jellyfin token %d", w.Code)
	}
}

func (e *libEnv) newAppToken(proto string) (string, error) {
	var uid int64
	e.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&uid)
	return authTokens(e.db).Issue(context.Background(), uid, proto, authDevice())
}

func TestIPTVPasswordlessOnlyAtHome(t *testing.T) {
	e, _ := newChannelsEnv(t)
	kidless := e.login(`{"name":"Без пароля"}`)
	w := e.do("GET", "/api/channels/iptv", "", kidless)
	var l iptvLinks
	json.Unmarshal(w.Body.Bytes(), &l)
	path := strings.TrimPrefix(l.Playlist, "http://example.com")
	if w := e.do("GET", path, "", nil); w.Code != 200 {
		t.Fatalf("at home %d", w.Code)
	}
	e.peer = "203.0.113.5:4000"
	if w := e.do("GET", path, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("away %d", w.Code)
	}
	// the admin has a password: it works away too
	e.peer = ""
	w = e.do("GET", "/api/channels/iptv", "", e.admin)
	json.Unmarshal(w.Body.Bytes(), &l)
	e.peer = "203.0.113.5:4000"
	if w := e.do("GET", strings.TrimPrefix(l.Playlist, "http://example.com"), "", nil); w.Code != 200 {
		t.Fatalf("admin away %d", w.Code)
	}
}

func authTokens(d *sql.DB) *auth.AppTokens { return auth.NewAppTokens(d) }
func authDevice() auth.Device              { return auth.Device{ID: "d"} }

func TestWithSegmentQueryKeysEverySegment(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXT-X-DISCONTINUITY\n#EXTINF:6.000,\nseg5.ts\n#EXTINF:6.000,\nseg6.ts\n"
	got := string(withSegmentQuery([]byte(in), "mk=a%2Bb"))
	want := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXT-X-DISCONTINUITY\n#EXTINF:6.000,\nseg5.ts?mk=a%2Bb\n#EXTINF:6.000,\nseg6.ts?mk=a%2Bb\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	// the master: the renditions' URI attributes and the variant line
	in = "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"Русский\",URI=\"audio-ru.m3u8\"\n" +
		"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"sub\",NAME=\"English\",URI=\"subs-en.m3u8\",FORCED=NO\n#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aud\"\nvideo.m3u8\n"
	got = string(withSegmentQuery([]byte(in), "mk=k"))
	want = "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"Русский\",URI=\"audio-ru.m3u8?mk=k\"\n" +
		"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"sub\",NAME=\"English\",URI=\"subs-en.m3u8?mk=k\",FORCED=NO\n#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aud\"\nvideo.m3u8?mk=k\n"
	if got != want {
		t.Fatalf("master got %q", got)
	}
}

func TestChannelsFavoritesLastAndEPG(t *testing.T) {
	e, _ := newChannelsEnv(t)
	m1, _ := e.movie("Один", "12+", "a")
	m2, _ := e.movie("Два", "16+", "b")
	e.withDuration()
	var ids []int64
	for _, n := range []string{"Первый", "Второй"} {
		w := e.do("POST", "/api/channels", `{"name":"`+n+`","rules":{"titles":[`+itoa(m1)+`,`+itoa(m2)+`]}}`, e.admin)
		var c channelJSON
		json.Unmarshal(w.Body.Bytes(), &c)
		ids = append(ids, c.ID)
	}
	type listT struct {
		Channels    []channelJSON
		LastChannel *int64
	}
	get := func(u *http.Cookie) listT {
		var l listT
		w := e.do("GET", "/api/channels", "", u)
		json.Unmarshal(w.Body.Bytes(), &l)
		return l
	}
	l := get(e.admin)
	if len(l.Channels) != 2 || l.Channels[0].Number != 1 || l.Channels[1].Number != 2 || l.LastChannel != nil || l.Channels[0].Favorite {
		t.Fatalf("list %+v", l)
	}
	if w := e.do("PUT", "/api/channels/"+itoa(ids[1])+"/favorite", "", e.admin); w.Code != 204 {
		t.Fatalf("favorite %d %s", w.Code, w.Body)
	}
	e.do("PUT", "/api/channels/"+itoa(ids[1])+"/favorite", "", e.admin) // idempotent
	if w := e.do("PUT", "/api/channels/last", `{"channelId":`+itoa(ids[0])+`}`, e.admin); w.Code != 204 {
		t.Fatalf("last %d %s", w.Code, w.Body)
	}
	l = get(e.admin)
	if !l.Channels[1].Favorite || l.Channels[0].Favorite || l.LastChannel == nil || *l.LastChannel != ids[0] {
		t.Fatalf("after %+v", l)
	}
	// per profile
	ann := e.login(`{"name":"Аня"}`)
	if l := get(ann); l.Channels[1].Favorite || l.LastChannel != nil {
		t.Fatalf("ann sees admin's %+v", l)
	}
	if w := e.do("DELETE", "/api/channels/"+itoa(ids[1])+"/favorite", "", e.admin); w.Code != 204 {
		t.Fatalf("unfavorite %d", w.Code)
	}
	if l := get(e.admin); l.Channels[1].Favorite {
		t.Fatal("still favourite")
	}
	if w := e.do("PUT", "/api/channels/9999/favorite", "", e.admin); w.Code != 404 {
		t.Fatalf("unknown channel %d", w.Code)
	}
	// the grid
	w := e.do("GET", "/api/channels/epg?hours=4", "", e.admin)
	var g struct {
		Channels []channelEPGJSON
	}
	json.Unmarshal(w.Body.Bytes(), &g)
	if w.Code != 200 || len(g.Channels) != 2 || len(g.Channels[0].Programs) == 0 || g.Channels[0].Programs[0].ID == "" || g.Channels[0].Programs[0].Kind != "movie" {
		t.Fatalf("epg %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/channels/epg?from=yesterday", "", e.admin); w.Code != 400 {
		t.Fatalf("bad from %d", w.Code)
	}
}

// The programme on air and the next one name the renditions they fill,
// labelled in the viewer's language; the others say nothing (unknown).
func TestChannelsProgrammeTracks(t *testing.T) {
	e, _ := newChannelsEnv(t)
	m1, f1 := e.movie("Один", "12+", "a")
	m2, _ := e.movie("Два", "12+", "b")
	e.withDuration()
	probe := `{"DurationMs":5400000,"Video":{"Codec":"h264","Width":1920,"Height":1080},` +
		`"Audio":[{"Index":1,"Codec":"aac","Lang":"rus","Title":"LostFilm","Default":true},{"Index":2,"Codec":"ac3","Lang":"eng"},{"Index":3,"Codec":"aac","Lang":"fre"}],` +
		`"Subs":[{"Index":4,"Codec":"subrip","Lang":"eng"},{"Index":5,"Codec":"hdmv_pgs_subtitle","Lang":"rus"}]}`
	if _, err := e.db.Exec(`UPDATE files SET probe_json=? WHERE id=?`, probe, f1); err != nil {
		t.Fatal(err)
	}
	// a voice-over beside the film and Russian subtitles
	os.WriteFile(filepath.Join(e.media, "Один.Кураж-Бамбей.mka"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(e.media, "Один.ru.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nА\n"), 0o644)
	w := e.do("POST", "/api/channels", `{"name":"Канал","rules":{"titles":[`+itoa(m1)+`,`+itoa(m2)+`]}}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	type listT struct{ Channels []channelJSON }
	var l listT
	json.Unmarshal(e.do("GET", "/api/channels", "", e.admin).Body.Bytes(), &l)
	var one *programJSON
	for _, p := range []*programJSON{l.Channels[0].Now, l.Channels[0].Next} {
		if p != nil && p.ItemID == m1 {
			one = p
		} else if p != nil && (p.Audio != nil || p.Subtitles != nil) {
			t.Fatalf("a file without a probe has no known tracks: %+v", p)
		}
	}
	if one == nil {
		t.Fatalf("no Один in now/next: %s", e.do("GET", "/api/channels", "", e.admin).Body)
	}
	got := fmt.Sprint(one.Audio, one.Subtitles)
	// the renditions of the set the channel would have now (its next 48 h:
	// Два has no probe, nothing known), named as the master names them
	want := "[{a0 LostFilm Русский — LostFilm ru} {a1 Кураж-Бамбей Русский — Кураж-Бамбей (внешняя) ru} {a2 Оригинал (Français) Оригинал (Français) fr} {a3 English English en}] " +
		"[{s0 Русские Русские ru} {s1 English English en}]"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	json.Unmarshal(e.do("GET", "/api/channels", "", e.admin, "Accept-Language", "en").Body.Bytes(), &l)
	for _, p := range []*programJSON{l.Channels[0].Now, l.Channels[0].Next} {
		if p != nil && p.ItemID == m1 && (p.Audio[1].Label != "Russian — Кураж-Бамбей (external)" || p.Audio[1].Name != "Кураж-Бамбей" || p.Subtitles[0].Label != "Russian" ||
			p.Audio[2].Label != "Original (Français)" || p.Audio[2].Name != "Оригинал (Français)") {
			t.Fatalf("english %+v", p)
		}
	}
	// the guide: only now and next carry them
	var g struct{ Programs []programJSON }
	json.Unmarshal(e.do("GET", "/api/channels/"+itoa(l.Channels[0].ID)+"/guide?hours=24", "", e.admin).Body.Bytes(), &g)
	if len(g.Programs) < 3 {
		t.Fatalf("guide %d", len(g.Programs))
	}
	for i, p := range g.Programs[2:] {
		if p.Audio != nil {
			t.Fatalf("programme %d has tracks", i+2)
		}
	}
}

// TV is TV: nothing a viewer does with channels is written as media
// progress or history (owner, round 3).
func TestChannelsWriteNoHistory(t *testing.T) {
	t.Run("no stream", func(t *testing.T) { channelsWriteNoHistory(t, false) })
	t.Run("live", func(t *testing.T) {
		if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
			t.Skip("no ffmpeg")
		}
		channelsWriteNoHistory(t, true)
	})
}

func channelsWriteNoHistory(t *testing.T, stream bool) {
	var live *channels.Live
	if stream {
		live = &channels.Live{FFmpeg: "ffmpeg", HW: "none", Dir: t.TempDir(), ReadRate: -1, Width: 320, Height: 180, ReadySegments: 1}
		t.Cleanup(live.Close)
	}
	tv := newTVEnvLive(t, live)
	e := tv.libEnv
	// an external channel too (internal/iptv), with its guide
	tv.addSource("Провайдер")
	ext := tv.firstExternal()
	m1, f1 := e.movie("Один", "12+", "a")
	e.withDuration()
	if stream {
		// a real film: 20 s, Russian and English audio, Russian subtitles
		var p string
		e.db.QueryRow(`SELECT path FROM files WHERE id=?`, f1).Scan(&p)
		srt := filepath.Join(t.TempDir(), "s.srt")
		os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:09,000\nПривет\n"), 0o644)
		if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=20",
			"-f", "lavfi", "-i", "sine=f=440:duration=20", "-f", "lavfi", "-i", "sine=f=880:duration=20", "-i", srt,
			"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-metadata:s:a:0", "language=rus", "-metadata:s:a:1", "language=eng",
			"-metadata:s:s:0", "language=rus", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s", "srt", "-f", "matroska", p).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		probe := `{"DurationMs":20000,"Video":{"Codec":"h264","Width":160,"Height":120},` +
			`"Audio":[{"Index":1,"Codec":"aac","Lang":"rus","Default":true},{"Index":2,"Codec":"aac","Lang":"eng"}],"Subs":[{"Index":3,"Codec":"subrip","Lang":"rus"}]}`
		if _, err := e.db.Exec(`UPDATE files SET probe_json=?, duration_ms=20000 WHERE id=?`, probe, f1); err != nil {
			t.Fatal(err)
		}
	}
	w := e.do("POST", "/api/channels", `{"name":"Канал","rules":{"titles":[`+itoa(m1)+`]}}`, e.admin)
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	snap := func() map[string]string {
		rows, err := e.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'channel%'`)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var n string
			rows.Scan(&n)
			names = append(names, n)
		}
		rows.Close()
		out := map[string]string{}
		for _, n := range names {
			rs, err := e.db.Query(`SELECT * FROM "` + n + `"`)
			if err != nil {
				t.Fatal(err)
			}
			cols, _ := rs.Columns()
			var b strings.Builder
			for rs.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				rs.Scan(ptrs...)
				fmt.Fprintln(&b, vals...)
			}
			rs.Close()
			out[n] = b.String()
		}
		return out
	}
	before := snap()
	id := itoa(c.ID)
	xid := itoa(ext)
	for _, q := range [][3]string{
		{"GET", "/api/channels/" + xid + "/guide", ""}, {"PUT", "/api/channels/" + xid + "/favorite", ""},
		{"PUT", "/api/channels/last", `{"channelId":` + xid + `}`}, {"GET", "/api/channels/live/ext/" + xid + "/0", ""},
		{"DELETE", "/api/channels/" + xid + "/favorite", ""},
		{"GET", "/api/channels", ""}, {"GET", "/api/channels/epg", ""}, {"GET", "/api/channels/" + id + "/guide", ""},
		{"PUT", "/api/channels/" + id + "/favorite", ""}, {"PUT", "/api/channels/last", `{"channelId":` + id + `}`},
		{"GET", "/api/channels/live/" + id + "/index.m3u8", ""}, {"GET", "/api/channels/live/" + id + "/video.m3u8", ""},
		{"DELETE", "/api/channels/" + id + "/favorite", ""},
	} {
		w := e.do(q[0], q[1], q[2], e.admin)
		if (strings.Contains(q[1], "/"+xid+"/") || strings.Contains(q[2], xid)) && w.Code >= 400 {
			t.Fatalf("external %s %s: %d %s", q[0], q[1], w.Code, w.Body)
		}
		if stream && strings.HasSuffix(q[1], ".m3u8") && w.Code != 200 {
			t.Fatalf("%s: %d %s", q[1], w.Code, w.Body)
		}
	}
	if stream {
		// every rendition of the master and one segment of each
		base := "/api/channels/live/" + id + "/"
		master := e.do("GET", base+"index.m3u8", "", e.admin).Body.String()
		renditions := []string{"video.m3u8"}
		for _, l := range strings.Split(master, "\n") {
			if _, uri, ok := strings.Cut(l, `URI="`); ok {
				renditions = append(renditions, strings.TrimSuffix(uri, `"`))
			}
		}
		// the film's Russian and English audio (its original), its Russian subtitles
		if !reflect.DeepEqual(renditions, []string{"video.m3u8", "audio-0.m3u8", "audio-1.m3u8", "subs-0.m3u8"}) {
			t.Fatalf("master %s", master)
		}
		// the programme on air names the running run's renditions
		var l struct{ Channels []channelJSON }
		json.Unmarshal(e.do("GET", "/api/channels", "", e.admin).Body.Bytes(), &l)
		if now := l.Channels[0].Now; now == nil || fmt.Sprint(now.Audio, now.Subtitles) !=
			"[{a0 Русский Русский ru} {a1 Оригинал (English) Оригинал (English) en}] [{s0 Русские Русские ru}]" ||
			!strings.Contains(master, `NAME="Русский",LANGUAGE="ru",DEFAULT=YES`) || !strings.Contains(master, `NAME="Оригинал (English)",LANGUAGE="en"`) {
			t.Fatalf("on air %+v\n%s", now, master)
		}
		var fetched int
		for _, n := range renditions {
			w := e.do("GET", base+n, "", e.admin)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "#EXTINF") {
				t.Fatalf("%s: %d %s", n, w.Code, w.Body)
			}
			var seg string
			for _, l := range strings.Split(w.Body.String(), "\n") {
				if l != "" && !strings.HasPrefix(l, "#") {
					seg = l
					break
				}
			}
			s := e.do("GET", base+seg, "", e.admin)
			if s.Code != 200 || s.Body.Len() == 0 {
				t.Fatalf("%s: %d", seg, s.Code)
			}
			if strings.HasSuffix(seg, ".vtt") && !strings.HasPrefix(s.Body.String(), "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:") {
				t.Fatalf("%s: %s", seg, s.Body)
			}
			fetched++
		}
		if fetched != 4 {
			t.Fatal(fetched)
		}
		live.Stop(c.ID)
	}
	after := snap()
	for n, v := range before {
		if after[n] != v && n != "sessions" && n != "app_tokens" {
			t.Errorf("table %s changed by watching TV:\nbefore %q\nafter  %q", n, v, after[n])
		}
	}
}
