package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// playEnv is libEnv with the web player behind the Jellyfin API (a real
// api.Player, HLS manager and ffmpeg) and two tiny generated films:
// «Клип» (clip.mp4: H.264/AAC, 12 s) and «Ролик» (clip.mkv: H.264, AC-3 +
// AAC, an embedded SubRip track and an external Russian .srt), both 0+.
type playEnv struct {
	*libEnv
	hls       *media.HLSManager
	mp4, mkv  int64
	mkvFile   library.File
	avProfile json.RawMessage
	mpvProf   json.RawMessage
}

func newPlayEnv(t *testing.T) *playEnv {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	e := &playEnv{libEnv: newLibEnv(t)}
	dir := filepath.Join(e.media, "clips")
	os.MkdirAll(dir, 0o755)
	srt := filepath.Join(dir, "embedded.srt")
	os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:03,000\nВстроенные\n\n2\n00:00:07,000 --> 00:00:09,000\nВторая\n"), 0o644)
	src := []string{"-f", "lavfi", "-i", "testsrc=size=160x90:rate=24:duration=12", "-f", "lavfi", "-i", "sine=frequency=440:duration=12"}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(ff, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
		}
	}
	mp4 := filepath.Join(dir, "Clip.2020.mp4")
	run(append(src, "-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "48k",
		"-shortest", mp4)...)
	mkv := filepath.Join(dir, "Film.2021.mkv")
	run(append(src, "-i", srt, "-map", "0:v", "-map", "1:a", "-map", "1:a", "-map", "2:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-pix_fmt", "yuv420p",
		"-c:a:0", "ac3", "-b:a:0", "96k", "-c:a:1", "aac", "-b:a:1", "48k", "-metadata:s:a:0", "language=rus",
		"-metadata:s:a:1", "language=eng", "-c:s", "srt", "-metadata:s:s:0", "language=eng", "-t", "12", mkv)...)
	os.WriteFile(filepath.Join(dir, "Film.2021.ru.srt"),
		[]byte("1\n00:00:02,000 --> 00:00:04,500\nПривет, <i>мир</i>\n\n2\n00:00:10,000 --> 00:00:11,000\nКонец\n"), 0o644)
	e.mp4 = e.realMovie("Клип", 2020, mp4)
	e.mkv = e.realMovie("Ролик", 2021, mkv)
	fs, _ := e.repo.FilesOf(context.Background(), e.mkv)
	e.mkvFile = fs[0]

	e.hls = &media.HLSManager{FFmpeg: ff, WorkDir: t.TempDir(), HW: "none"}
	t.Cleanup(e.hls.Close)
	_, player := api.NewRouterWithPlayer(api.Deps{Users: e.users, Sessions: auth.NewSessions(e.db), Network: e.network,
		Library: e.repo, Progress: e.prog, HLS: e.hls, DataDir: e.s.DataDir, FFmpeg: ff})
	if player == nil {
		t.Fatal("no player")
	}
	e.s.Player = player
	var err2 error
	if e.avProfile, err2 = os.ReadFile("testdata/swiftfin-avplayer-profile.json"); err2 != nil {
		t.Fatal(err2)
	}
	if e.mpvProf, err2 = os.ReadFile("testdata/swiftfin-mpv-profile.json"); err2 != nil {
		t.Fatal(err2)
	}
	return e
}

// realMovie adds a 0+ movie on a real file (not probed yet).
func (e *playEnv) realMovie(title string, year int, path string) int64 {
	e.t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		e.t.Fatal(err)
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: title, Year: year, Rating: "0+",
		File: library.FileInput{Path: path, Size: st.Size(), Mtime: st.ModTime().Unix()}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

type pbResult struct {
	PlaySessionId string
	ErrorCode     string
	MediaSources  []MediaSourceInfo
}

// playbackInfo POSTs PlaybackInfo as Swiftfin does (profile: raw JSON).
func (e *playEnv) playbackInfo(tok string, item int64, profile json.RawMessage, extra map[string]any) (pbResult, *httptest.ResponseRecorder) {
	e.t.Helper()
	body := map[string]any{"UserId": "", "AutoOpenLiveStream": true, "MaxStreamingBitrate": 120000000}
	if profile != nil {
		body["DeviceProfile"] = profile
	}
	for k, v := range extra {
		body[k] = v
	}
	w := e.do("POST", "/Items/"+ItemID(item)+"/PlaybackInfo", swiftfinHdr(tok), body)
	var res pbResult
	if w.Code == 200 {
		decode(e.t, w, &res)
	}
	return res, w
}

// The direct-play leg of Swiftfin's sequence (AVPlayer): the bitrate
// probe, the full item, PlaybackInfo with the AVPlayer profile → direct
// play of the MP4, then a Range read of /Videos/{id}/stream?static=true
// with the token in the query, as AVPlayer fetches it.
func TestSwiftfinDirectPlayMP4(t *testing.T) {
	e := newPlayEnv(t)
	if w := e.get(e.anyaTok, "/Playback/BitrateTest?size=500000"); w.Code != 200 || w.Body.Len() != 500000 {
		t.Fatalf("bitrate test: %d %d", w.Code, w.Body.Len())
	}
	if w := e.get(e.anyaTok, "/Users/"+UserID(e.anya.ID)+"/Items/"+ItemID(e.mp4)); w.Code != 200 {
		t.Fatalf("item: %d", w.Code)
	}
	res, w := e.playbackInfo(e.anyaTok, e.mp4, e.avProfile, nil)
	if w.Code != 200 || !hex32.MatchString(res.PlaySessionId) || len(res.MediaSources) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ms := res.MediaSources[0]
	if !ms.SupportsDirectPlay || ms.TranscodingUrl != "" || ms.Id != FileID(e.fileOf(e.mp4).ID) || ms.ETag == "" {
		t.Fatalf("not direct: %+v", ms)
	}
	q := url.Values{"static": {"true"}, "tag": {ms.ETag}, "playSessionId": {res.PlaySessionId}, "mediaSourceId": {ms.Id},
		"ApiKey": {e.anyaTok}}
	r := httptest.NewRequest("GET", "/Videos/"+ItemID(e.mp4)+"/stream?"+q.Encode(), nil)
	r.RemoteAddr = homeAddr
	r.Header.Set("Range", "bytes=0-99")
	rw := httptest.NewRecorder()
	e.s.ServeHTTP(rw, r)
	if rw.Code != 206 || rw.Body.Len() != 100 || rw.Header().Get("Content-Type") != "video/mp4" || rw.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("stream: %d %d %v", rw.Code, rw.Body.Len(), rw.Header())
	}
	// Infuse's form: stream.{container} with api_key
	if w := e.do("HEAD", "/Videos/"+ItemID(e.mp4)+"/stream.mp4?static=true&api_key="+e.anyaTok, nil, nil); w.Code != 200 {
		t.Fatalf("head stream.mp4: %d", w.Code)
	}
	// without a token: no stream
	if w := e.do("GET", "/Videos/"+ItemID(e.mp4)+"/stream?static=true", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous stream: %d", w.Code)
	}
}

func (e *playEnv) fileOf(item int64) library.File {
	e.t.Helper()
	fs, err := e.repo.FilesOf(context.Background(), item)
	if err != nil || len(fs) == 0 {
		e.t.Fatal(err)
	}
	return fs[0]
}

// getOK GETs an absolute path (with its query) without headers, as
// AVPlayer and mpv fetch HLS URIs; fails unless 200.
func (e *playEnv) getOK(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	var w *httptest.ResponseRecorder
	for try := 0; try < 60; try++ {
		w = e.do("GET", path, nil, nil)
		if w.Code != 503 {
			break
		}
		time.Sleep(250 * time.Millisecond) // ffmpeg is on the first segment
	}
	if w.Code != 200 {
		e.t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
	}
	return w
}

// resolve is ref (a playlist URI) against the playlist's path.
func resolve(base, ref string) string {
	b, _ := url.Parse(base)
	r, _ := url.Parse(ref)
	return b.ResolveReference(r).RequestURI()
}

// uris are the URIs of a playlist (lines and URI="…").
func uris(playlist string) []string {
	var out []string
	for _, l := range strings.Split(playlist, "\n") {
		l = strings.TrimSpace(l)
		if m := regexp.MustCompile(`URI="([^"]*)"`).FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		} else if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// The HLS leg (AVPlayer and an MKV with AC-3): PlaybackInfo answers a
// TranscodingUrl; master.m3u8 starts the remux; every URI of the master
// and media playlists carries the token; a segment is MPEG-TS video. The
// English AAC track asked for (AudioStreamIndex) is the one in the session.
// The subtitle renditions' clock matches the segments' for a remux and for
// a transcode (AllowVideoStreamCopy false).
func TestSwiftfinHLSMKV(t *testing.T) {
	for _, copyVideo := range []bool{true, false} {
		t.Run(fmt.Sprintf("copy=%v", copyVideo), func(t *testing.T) { testSwiftfinHLS(t, copyVideo) })
	}
}

func testSwiftfinHLS(t *testing.T, copyVideo bool) {
	e := newPlayEnv(t)
	res, w := e.playbackInfo(e.anyaTok, e.mkv, e.avProfile, map[string]any{"AudioStreamIndex": 2, "AllowVideoStreamCopy": copyVideo})
	if p, _ := e.s.plays.get(res.PlaySessionId, e.anya.ID); p.d.VideoCopy != copyVideo {
		t.Fatalf("decision %+v", p.d)
	}
	if w.Code != 200 || len(res.MediaSources) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ms := res.MediaSources[0]
	if ms.SupportsDirectPlay || !strings.HasPrefix(ms.TranscodingUrl, "/videos/"+ItemID(e.mkv)+"/master.m3u8?") ||
		strings.Contains(ms.TranscodingUrl, e.anyaTok) || !strings.Contains(ms.TranscodingUrl, "PlaySessionId="+res.PlaySessionId) || ms.TranscodingSubProtocol != "hls" ||
		ms.DefaultAudioStreamIndex == nil || *ms.DefaultAudioStreamIndex != 2 {
		t.Fatalf("not HLS: %+v", ms)
	}
	// subtitles: the embedded SubRip and the external .srt go into the manifest (AVPlayer: vtt as Hls)
	hlsSubs := 0
	for _, st := range ms.MediaStreams {
		if st.Type == "Subtitle" {
			if st.DeliveryMethod != "Hls" {
				t.Fatalf("subtitle %d: %s", st.Index, st.DeliveryMethod)
			}
			hlsSubs++
		}
	}
	if hlsSubs != 2 {
		t.Fatalf("subtitles: %+v", ms.MediaStreams)
	}
	// AVPlayer fetches everything without the app's headers
	master := e.getOK(ms.TranscodingUrl)
	if ct := master.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("master type %q", ct)
	}
	mbody := master.Body.String()
	if !strings.Contains(mbody, "#EXT-X-STREAM-INF:BANDWIDTH=") || !strings.Contains(mbody, `SUBTITLES="subs"`) ||
		strings.Count(mbody, "TYPE=SUBTITLES") != 2 {
		t.Fatalf("master:\n%s", mbody)
	}
	var mainURI, subURI string
	for _, u := range uris(mbody) {
		if strings.Contains(u, e.anyaTok) || !strings.Contains(u, "PlaySessionId="+res.PlaySessionId) {
			t.Fatalf("a master URI with the token or without the play session: %s", u)
		}
		if strings.HasPrefix(u, "main.m3u8") {
			mainURI = resolve(ms.TranscodingUrl, u)
		} else if subURI == "" {
			subURI = resolve(ms.TranscodingUrl, u)
		}
	}
	media := e.getOK(mainURI).Body.String()
	segs := uris(media)
	if len(segs) == 0 || !strings.Contains(media, "#EXT-X-ENDLIST") {
		t.Fatalf("media playlist:\n%s", media)
	}
	for _, u := range segs {
		if !strings.HasPrefix(u, "hls1/"+res.PlaySessionId+"/") || strings.Contains(u, e.anyaTok) {
			t.Fatalf("segment URI %s", u)
		}
	}
	seg := e.getOK(resolve(mainURI, segs[0]))
	if ct := seg.Header().Get("Content-Type"); ct != "video/mp2t" && ct != "video/mp4" || seg.Body.Len() < 1000 {
		t.Fatalf("segment: %q %d", ct, seg.Body.Len())
	}
	// the session copies the video and plays the AAC track (index 2)
	pr := ffprobeStreams(t, seg.Body.Bytes())
	segStart := segmentStartPTS(t, seg.Body.Bytes())
	if !strings.Contains(pr, "h264") || !strings.Contains(pr, "aac") || strings.Contains(pr, "ac3") {
		t.Fatalf("segment streams: %s", pr)
	}
	// the subtitle rendition: a one-segment VOD playlist of WebVTT with the timestamp map
	sub := e.getOK(subURI).Body.String()
	su := uris(sub)
	if len(su) != 1 || !strings.Contains(sub, "#EXT-X-ENDLIST") {
		t.Fatalf("subtitle playlist:\n%s", sub)
	}
	vtt := e.getOK(resolve(subURI, su[0])).Body.String()
	m := regexp.MustCompile(`X-TIMESTAMP-MAP=MPEGTS:(\d+),LOCAL:00:00:00\.000`).FindStringSubmatch(vtt)
	if !strings.HasPrefix(vtt, "WEBVTT") || m == nil || !strings.Contains(vtt, "Встроенные") && !strings.Contains(vtt, "Привет") {
		t.Fatalf("vtt:\n%s", vtt)
	}
	// cue time 0 is the first segment's first picture (within 1/8 s: an encoder's B-frame delay)
	if mpegts, _ := strconv.ParseInt(m[1], 10, 64); mpegts-segStart > 11250 || segStart-mpegts > 11250 {
		t.Fatalf("X-TIMESTAMP-MAP MPEGTS %d, the segment starts at %d", mpegts, segStart)
	}

	// another profile cannot use the play session (Миша may see «Ролик»: 0+)
	other := mainURI + "&ApiKey=" + e.mishaTok
	if w := e.do("GET", other, nil, nil); w.Code != 404 {
		t.Fatalf("other profile's playlist: %d", w.Code)
	}
	// DELETE /Videos/ActiveEncodings stops ffmpeg; the next request starts a new session
	if w := e.do("DELETE", "/Videos/ActiveEncodings?deviceId=3F2B-iphone&playSessionId="+res.PlaySessionId, swiftfinHdr(e.anyaTok), nil); w.Code != 204 {
		t.Fatalf("stop: %d", w.Code)
	}
	if p, _ := e.s.plays.get(res.PlaySessionId, e.anya.ID); p.hls != "" {
		t.Fatal("still has an HLS session")
	}
	e.getOK(resolve(mainURI, segs[0]))
}

// segmentStartPTS is the video start PTS of an MPEG-TS segment (90 kHz).
func segmentStartPTS(t *testing.T, b []byte) int64 {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seg.ts")
	os.WriteFile(p, b, 0o644)
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=start_pts",
		"-of", "csv=p=0", p).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	n, err := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
	if err != nil {
		t.Fatalf("start_pts %q", out)
	}
	return n
}

// ffprobeStreams lists the codecs of a media segment.
func ffprobeStreams(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "seg")
	os.WriteFile(p, b, 0o644)
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", p).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	return string(out)
}

// mpv (Swiftfin's default player) plays the MKV directly; its text
// subtitles are embedded (in the file) or an external sidecar: the
// external .srt comes as WebVTT and as SRT.
func TestSwiftfinMPVDirectMKVSubtitles(t *testing.T) {
	e := newPlayEnv(t)
	res, w := e.playbackInfo(e.anyaTok, e.mkv, e.mpvProf, map[string]any{"AudioStreamIndex": 2, "SubtitleStreamIndex": 1000})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ms := res.MediaSources[0]
	if !ms.SupportsDirectPlay || ms.TranscodingUrl != "" {
		t.Fatalf("not direct: %+v", ms)
	}
	var ext MediaStream
	for _, st := range ms.MediaStreams {
		switch {
		case st.Type == "Subtitle" && st.IsExternal:
			ext = st
		case st.Type == "Subtitle" && st.DeliveryMethod != "Embed":
			t.Fatalf("embedded subtitle %+v", st)
		}
	}
	if ext.DeliveryMethod != "External" || !strings.HasSuffix(strings.Split(ext.DeliveryUrl, "?")[0], "/stream.vtt") || ext.Codec != "subrip" {
		t.Fatalf("external: %+v", ext)
	}
	vtt := e.getOK(ext.DeliveryUrl)
	if !strings.HasPrefix(vtt.Body.String(), "WEBVTT") || !strings.Contains(vtt.Body.String(), "Привет") {
		t.Fatalf("vtt: %q", vtt.Body)
	}
	srt := e.getOK(strings.Replace(ext.DeliveryUrl, "/stream.vtt", "/Stream.srt", 1)).Body.String()
	if !strings.HasPrefix(srt, "1\n00:00:02,000 --> 00:00:04,500\nПривет") {
		t.Fatalf("srt: %q", srt)
	}
	// the embedded track extracted (Jellyfin's form with the start ticks)
	emb := e.getOK(fmt.Sprintf("/Videos/%s/%s/Subtitles/3/0/Stream.vtt?ApiKey=%s", ItemID(e.mkv), ms.Id, e.anyaTok))
	if !strings.Contains(emb.Body.String(), "Встроенные") {
		t.Fatalf("embedded: %q", emb.Body)
	}
	// a file of another item is not this item's subtitle
	if w := e.do("GET", fmt.Sprintf("/Videos/%s/%s/Subtitles/3/0/Stream.vtt?ApiKey=%s", ItemID(e.mp4), ms.Id, e.anyaTok), nil, nil); w.Code != 404 {
		t.Fatalf("foreign file: %d", w.Code)
	}
}

// Infuse sends no DeviceProfile: it plays the file directly; text
// subtitles are sidecars.
func TestPlaybackInfoNoProfileIsDirect(t *testing.T) {
	e := newPlayEnv(t)
	w := e.do("GET", "/Items/"+ItemID(e.mkv)+"/PlaybackInfo?UserId="+UserID(e.anya.ID), infuseHdr(e.anyaTok), nil)
	var res pbResult
	decode(t, w, &res)
	if w.Code != 200 || len(res.MediaSources) != 1 || !res.MediaSources[0].SupportsDirectPlay || res.MediaSources[0].TranscodingUrl != "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, st := range res.MediaSources[0].MediaStreams {
		if st.Type == "Subtitle" && st.DeliveryMethod != "External" {
			t.Fatalf("%+v", st)
		}
	}
}

// Outside home «Качество вне дома» applies to Jellyfin clients as to the
// web: a 1080p cap turns a 4K source into a transcode; at home the same
// profile plays it directly.
func TestPlaybackInfoRemoteQualityCap(t *testing.T) {
	e := newPlayEnv(t)
	probe, _ := json.Marshal(media.ProbeResult{Container: "mp4", DurationMs: 12_000,
		Video: &media.VideoStream{Codec: "h264", Width: 3840, Height: 2160, BitRate: 30_000_000, PixFmt: "yuv420p", BitDepth: 8},
		Audio: []media.AudioStream{{Index: 1, Codec: "aac", Channels: 2, Default: true}}})
	if _, err := e.db.Exec(`UPDATE files SET probe_json=? WHERE item_id=?`, string(probe), e.mp4); err != nil {
		t.Fatal(err)
	}
	res, _ := e.playbackInfo(e.anyaTok, e.mp4, e.avProfile, nil)
	if !res.MediaSources[0].SupportsDirectPlay {
		t.Fatalf("home: %+v", res.MediaSources[0])
	}
	body := map[string]any{"DeviceProfile": e.avProfile, "MaxStreamingBitrate": 120000000}
	w := e.remote("POST", "/Items/"+ItemID(e.mp4)+"/PlaybackInfo", swiftfinHdr(e.anyaTok), body)
	decode(t, w, &res)
	if res.MediaSources[0].SupportsDirectPlay || res.MediaSources[0].TranscodingUrl == "" {
		t.Fatalf("remote: %s", w.Body)
	}
	p, _ := e.s.plays.get(res.PlaySessionId, e.anya.ID)
	if p.d.Mode != "transcode" || p.d.TargetHeight != 1080 || p.d.MaxBitrate > 8_000_000 {
		t.Fatalf("remote decision %+v", p.d)
	}
}

// A kid sees and plays only what the native API lets it: an 18+ film is
// 404 for PlaybackInfo, the stream, the download and subtitles.
func TestKidCannotPlayHidden(t *testing.T) {
	e := newPlayEnv(t)
	// «Дэдпул» (18+) gets a real file
	b, err := os.ReadFile(e.fileOf(e.mp4).Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.fileOf(e.deadpool).Path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	dp := ItemID(e.deadpool)
	if _, w := e.playbackInfo(e.mishaTok, e.deadpool, e.avProfile, nil); w.Code != 404 {
		t.Fatalf("kid playback info: %d", w.Code)
	}
	for _, p := range []string{"/Videos/" + dp + "/stream?static=true&ApiKey=" + e.mishaTok, "/Items/" + dp + "/Download?ApiKey=" + e.mishaTok,
		"/Videos/" + dp + "/" + FileID(e.fileOf(e.deadpool).ID) + "/Subtitles/1000/0/Stream.vtt?ApiKey=" + e.mishaTok} {
		if w := e.do("GET", p, nil, nil); w.Code != 404 {
			t.Fatalf("kid %s: %d", p, w.Code)
		}
	}
	// the adult plays it
	if _, w := e.playbackInfo(e.anyaTok, e.deadpool, e.avProfile, nil); w.Code != 200 {
		t.Fatalf("adult: %d", w.Code)
	}
	// a kid's PlaybackInfo of an allowed film works
	if res, w := e.playbackInfo(e.mishaTok, e.mp4, e.avProfile, nil); w.Code != 200 || res.PlaySessionId == "" {
		t.Fatalf("kid allowed: %d", w.Code)
	}
}

func TestVTTToSRT(t *testing.T) {
	in := "WEBVTT\n\n00:01.000 --> 00:02.500 align:start\nОдин\n\n01:00:03.000 --> 01:00:04.000\n<i>Два</i>\nтри\n\n"
	want := "1\n00:00:01,000 --> 00:00:02,500\nОдин\n\n2\n01:00:03,000 --> 01:00:04,000\n<i>Два</i>\nтри\n\n"
	if got := string(vttToSRT([]byte(in))); got != want {
		t.Fatalf("%q", got)
	}
}
