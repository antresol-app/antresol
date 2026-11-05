package jellyfin

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An E-AC-3 «Film.2021.LostFilm.mka» beside «Ролик»: PlaybackInfo lists
// it after the file's own tracks, and choosing it plays it from the HLS
// session (the segments carry E-AC-3, which the file itself has not).
func TestExternalAudioStream(t *testing.T) {
	e := newPlayEnv(t)
	ext := filepath.Join(filepath.Dir(e.mkvFile.Path), "Film.2021.LostFilm.mka")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=660:duration=12",
		"-c:a", "eac3", "-b:a", "96k", ext).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	res, w := e.playbackInfo(e.anyaTok, e.mkv, e.avProfile, nil)
	if w.Code != 200 || len(res.MediaSources) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var audio []MediaStream
	for _, st := range res.MediaSources[0].MediaStreams {
		if st.Type == "Audio" {
			audio = append(audio, st)
		}
	}
	if len(audio) != 3 || audio[2].Index != 2000 || !audio[2].IsExternal || audio[2].Title != "LostFilm" || audio[2].Language != "rus" ||
		audio[2].Codec != "eac3" || audio[2].IsDefault || !strings.Contains(audio[2].DisplayTitle, "LostFilm") {
		t.Fatalf("audio streams: %+v", audio)
	}

	// a direct-play answer (mpv plays the MKV itself) and the item never
	// list it: the player would look for it in the file
	res2, w := e.playbackInfo(e.anyaTok, e.mkv, e.mpvProf, nil)
	if w.Code != 200 || len(res2.MediaSources) != 1 || !res2.MediaSources[0].SupportsDirectPlay {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, st := range res2.MediaSources[0].MediaStreams {
		if st.Type == "Audio" && (st.Index >= 2000 || st.IsExternal) {
			t.Fatalf("direct play lists %+v", st)
		}
	}
	if d := res2.MediaSources[0].DefaultAudioStreamIndex; d == nil || *d >= 2000 {
		t.Fatalf("default audio %v", d)
	}
	if strings.Contains(e.get(e.anyaTok, "/Users/"+UserID(e.anya.ID)+"/Items/"+ItemID(e.mkv)).Body.String(), "LostFilm") {
		t.Fatal("the item lists the external track")
	}

	res, w = e.playbackInfo(e.anyaTok, e.mkv, e.avProfile, map[string]any{"AudioStreamIndex": 2000})
	if w.Code != 200 || len(res.MediaSources) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	ms := res.MediaSources[0]
	p, _ := e.s.plays.get(res.PlaySessionId, e.anya.ID)
	if ms.SupportsDirectPlay || ms.TranscodingUrl == "" || p.d.AudioInput != ext || ms.DefaultAudioStreamIndex == nil || *ms.DefaultAudioStreamIndex != 2000 {
		t.Fatalf("decision %+v; source %+v", p.d, ms)
	}
	var mainURI string
	for _, u := range uris(e.getOK(ms.TranscodingUrl).Body.String()) {
		if strings.HasPrefix(u, "main.m3u8") {
			mainURI = resolve(ms.TranscodingUrl, u)
		}
	}
	segs := uris(e.getOK(mainURI).Body.String())
	if len(segs) == 0 {
		t.Fatal("no segments")
	}
	pr := ffprobeStreams(t, e.getOK(resolve(mainURI, segs[0])).Body.Bytes())
	// (MPEG-TS lists its program's streams again)
	for _, c := range strings.Fields(pr) {
		if c != "h264" && c != "eac3" {
			t.Fatalf("segment streams: %q", pr)
		}
	}
	if !strings.Contains(pr, "eac3") {
		t.Fatalf("segment streams: %q", pr)
	}
}
