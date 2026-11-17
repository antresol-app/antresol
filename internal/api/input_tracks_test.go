package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// testClock is the injected clock of the inputs (addons.Inputs.Now),
// which the input player reads too.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func (e *relayEnv) withClock() *testClock {
	c := &testClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	e.inputs.Now = c.now
	return c
}

// audioSidecars are audio sidecars named names (paths /side/0, /side/1, …).
func audioSidecars(names ...string) addonkit.InputSidecars {
	var s addonkit.InputSidecars
	for i, n := range names {
		s.Audio = append(s.Audio, addonkit.AudioSidecar{Path: "/side/" + strconv.Itoa(i), Name: n, Size: 4000})
	}
	return s
}

// sidecarProbe is the probe of an audio sidecar of the video's cut.
func sidecarProbe(lang, title string) media.ProbeResult {
	return media.ProbeResult{Container: "matroska", DurationMs: 6_000_500,
		Audio: []media.AudioStream{{Index: 0, Codec: "aac", Channels: 2, Lang: lang, Title: title}}}
}

// videoProbe is the main file's probe with its own audio in lang (none: nil audio).
func videoProbe(lang string, audio bool) media.ProbeResult {
	p := media.ProbeResult{Container: "matroska", DurationMs: 6_000_000, Video: &media.VideoStream{Codec: "hevc", Width: 1920, Height: 1080, BitDepth: 8}}
	if audio {
		p.Audio = []media.AudioStream{{Index: 1, Codec: "ac3", Channels: 6, Lang: lang, Default: true}}
	}
	return p
}

const (
	inputRuPrefs = `{"audioLangs":["ru"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"translucent"}`
	inputEnPrefs = `{"audioLangs":["en"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"translucent"}`
)

func (e *relayEnv) prefer(tok, prefs string) {
	e.t.Helper()
	r := e.put(tok, "/api/me/playback-prefs", prefs)
	if r.Code != 200 {
		e.t.Fatalf("prefs %d %s", r.Code, r.Body)
	}
}

func (e *relayEnv) put(tok, target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PUT", target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = loopAddr
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

type inputPlayOut struct {
	Mode       string       `json:"mode"`
	AudioIndex int          `json:"audioIndex"`
	Audio      []AudioTrack `json:"audio"`
	Subtitles  []SubTrack   `json:"subtitles"`
	Pending    bool         `json:"pending"`
}

// playIn is one POST /api/play/input/{id} with tok (hdr: more headers).
func (e *relayEnv) playIn(tok, id, body string, hdr ...string) (int, inputPlayOut, string) {
	e.t.Helper()
	r := httptest.NewRequest("POST", "/api/play/input/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = loopAddr
	r.Header.Set("Authorization", "Bearer "+tok)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	var out inputPlayOut
	if w.Code != 200 && w.Code != 202 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), relayToken) || strings.Contains(w.Body.String(), "/side/") {
		e.t.Fatalf("answer leaks the relay: %s", w.Body)
	}
	return w.Code, out, w.Body.String()
}

func (e *relayEnv) play200(tok, id, body string, hdr ...string) inputPlayOut {
	e.t.Helper()
	code, out, raw := e.playIn(tok, id, body, hdr...)
	if code != 200 {
		e.t.Fatalf("%d %s", code, raw)
	}
	return out
}

func externals(o inputPlayOut) []AudioTrack {
	var x []AudioTrack
	for _, a := range o.Audio {
		if a.External {
			x = append(x, a)
		}
	}
	return x
}

// tracksInput is an input "Film/x.mkv" with sidecars names, its video
// probed as v and each sidecar as tr[i].
func (e *relayEnv) tracksInput(ps *probeStub, uid int64, v media.ProbeResult, names []string, tr ...media.ProbeResult) string {
	e.t.Helper()
	id := e.addFor(uid, "/media/t", addonkit.InputRequest{Name: "Film/x.mkv", Sidecars: audioSidecars(names...)})
	ps.mu.Lock()
	ps.results[inputURL(id, 0)] = v
	for i := range names {
		if i < len(tr) {
			ps.results[inputURL(id, 1+i)] = tr[i]
		}
	}
	ps.mu.Unlock()
	return id
}

func withInputTrackWait(t *testing.T, d time.Duration) {
	old := inputTrackWait
	inputTrackWait = d
	t.Cleanup(func() { inputTrackWait = old })
}

const ruSidecar = "Film/RUS Sound/x.mka"

func TestInputPlayListsExternalAudio(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	out := e.play200(tok, id, h264Caps)
	if len(out.Audio) != 2 {
		t.Fatalf("audio %+v", out.Audio)
	}
	a := out.Audio[1]
	if a.Index != externalAudioBase || !a.External || a.Src != extaudio.ID(ruSidecar) || a.Lang != "ru" {
		t.Fatalf("external track %+v", a)
	}
}

func TestInputPlayMuxesChosenTrack(t *testing.T) {
	e := newRelayEnv(t)
	ps, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	body := `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"audioIndex":2000,"audioSrc":"` + extaudio.ID(ruSidecar) + `"}`
	out := e.play200(tok, id, body)
	if out.AudioIndex != 2000 || out.Mode == "direct" || len(hls.started) != 1 {
		t.Fatalf("%+v", out)
	}
	if d := hls.started[0]; d.AudioInput != inputURL(id, 1) {
		t.Fatalf("audio input %q, want %q", d.AudioInput, inputURL(id, 1))
	}
	if hls.opts[0].Input != inputURL(id, 0) {
		t.Fatalf("video input %q", hls.opts[0].Input)
	}
}

func TestInputPlayPrefersProfileLanguage(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("eng", true), []string{ruSidecar}, sidecarProbe("", ""))
	if out := e.play200(tok, id, h264Caps); out.AudioIndex != 2000 {
		t.Fatalf("%+v", out)
	}
	// a video that has the language keeps its own track
	id2 := e.tracksInput(ps, e.uid, videoProbe("rus", true), []string{ruSidecar}, sidecarProbe("", ""))
	if out := e.play200(tok, id2, h264Caps); len(externals(out)) != 1 || out.AudioIndex != 1 {
		t.Fatalf("embedded ru: %+v", out)
	}
}

// gateProbe blocks the probe of url until release closes.
func gateProbe(ps *probeStub, url string, release chan struct{}) {
	ps.mu.Lock()
	ps.block[url] = release
	ps.mu.Unlock()
}

func TestInputPlayChosenTrackPending(t *testing.T) {
	withInputTrackWait(t, 30*time.Millisecond)
	e := newRelayEnv(t)
	ps, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	release := make(chan struct{})
	gateProbe(ps, inputURL(id, 1), release)
	body := `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"audioIndex":2000,"audioSrc":"` + extaudio.ID(ruSidecar) + `"}`
	if code, out, raw := e.playIn(tok, id, body); code != 202 || !out.Pending {
		t.Fatalf("still probing: %d %s", code, raw)
	}
	if len(hls.started) != 0 {
		t.Fatal("a session started while the track was probing")
	}
	close(release)
	var out inputPlayOut
	for range 100 {
		code, o, _ := e.playIn(tok, id, body)
		if code == 200 {
			out = o
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if out.AudioIndex != 2000 {
		t.Fatalf("after the probe: %+v", out)
	}
}

func TestInputPlayChosenTrackFailedPlaysDefault(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar})
	ps.errs[inputURL(id, 1)] = errors.New("ffprobe " + inputURL(id, 1) + ": 404")
	body := `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"audioIndex":2000,"audioSrc":"` + extaudio.ID(ruSidecar) + `"}`
	if out := e.play200(tok, id, body); out.AudioIndex != 1 || len(externals(out)) != 0 {
		t.Fatalf("%+v", out)
	}
}

// blockedSidecars registers inputs × n sidecars whose probes wait for
// release; the video probes answer at once.
func (e *relayEnv) blockedSidecars(ps *probeStub, inputs, n int, release chan struct{}) []string {
	var ids []string
	for range inputs {
		var names []string
		for k := range n {
			names = append(names, "Film/RUS Sound/S"+strconv.Itoa(k)+"/x.mka")
		}
		id := e.tracksInput(ps, e.uid, videoProbe("en", true), names)
		for k := range n {
			ps.results[inputURL(id, 1+k)] = sidecarProbe("", "")
			gateProbe(ps, inputURL(id, 1+k), release)
		}
		ids = append(ids, id)
	}
	return ids
}

func (s *probeStub) runningNow() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func TestInputPlayTrackProbesCapped(t *testing.T) {
	withInputTrackWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs) // the ru tracks are not waited for
	release := make(chan struct{})
	ids := e.blockedSidecars(ps, 1, 5, release)
	if out := e.play200(tok, ids[0], h264Caps); len(externals(out)) != 0 {
		t.Fatalf("listed before any probe ended: %+v", out)
	}
	time.Sleep(80 * time.Millisecond)
	if n := ps.runningNow(); n != inputTrackProbes {
		t.Errorf("%d probes running, want %d", n, inputTrackProbes)
	}
	close(release)
	var out inputPlayOut
	for range 100 {
		if out = e.play200(tok, ids[0], h264Caps); len(externals(out)) == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(externals(out)) != 5 {
		t.Fatalf("all five tracks: %+v", out.Audio)
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.maxRun > inputTrackProbes {
		t.Fatalf("peak %d probes at once", ps.maxRun)
	}
}

func TestInputPlayTrackProbesGlobalCap(t *testing.T) {
	withInputTrackWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs)
	release := make(chan struct{})
	ids := e.blockedSidecars(ps, 3, 3, release)
	// the main files probed first (an app without external audio): their
	// probes would otherwise wait behind the sidecars' for a slot
	dev, err := e.ss.CreateDevice(context.Background(), e.uid, auth.DeviceInfo{ID: "phone-0001", Name: "Телефон"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		e.play200(dev, id, h264Caps)
	}
	if n := ps.total(); n != len(ids) {
		t.Fatalf("%d probes before the sidecars'", n)
	}
	for _, id := range ids {
		e.play200(tok, id, h264Caps)
	}
	deadline := time.Now().Add(5 * time.Second)
	for ps.runningNow() < inputProbeSlots && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a fifth would have started by now
	if n := ps.runningNow(); n != inputProbeSlots {
		t.Errorf("%d probes running server-wide, want %d", n, inputProbeSlots)
	}
	close(release)
	for _, id := range ids {
		var out inputPlayOut
		for range 200 {
			if out = e.play200(tok, id, h264Caps); len(externals(out)) == 3 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if len(externals(out)) != 3 {
			t.Fatalf("%s: %+v", id, out.Audio)
		}
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.maxRun > inputProbeSlots {
		t.Fatalf("peak %d probes at once", ps.maxRun)
	}
}

func TestInputPlayFailedTrackProbedAgainAfterMemory(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar})
	ps.errs[inputURL(id, 1)] = errors.New("not ready")
	play := func() {
		t.Helper()
		if out := e.play200(tok, id, h264Caps); len(externals(out)) != 0 {
			t.Fatalf("%+v", out)
		}
	}
	play()
	play() // remembered: no new probe
	if n := ps.count(inputURL(id, 1)); n != 1 {
		t.Fatalf("probed %d times within the memory", n)
	}
	clock.add(inputTrackFailMemory - time.Second)
	play()
	if n := ps.count(inputURL(id, 1)); n != 1 {
		t.Fatalf("probed %d times just before the memory lapsed", n)
	}
	clock.add(2 * time.Second)
	play()
	if n := ps.count(inputURL(id, 1)); n != 2 {
		t.Fatalf("probed %d times after the memory lapsed", n)
	}
}

func TestInputPlayWaitsForPreferredTrackProbe(t *testing.T) {
	withInputTrackWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	e.withClock()
	ps, hls := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	release := make(chan struct{})
	gateProbe(ps, inputURL(id, 1), release)
	for range 2 {
		if code, out, raw := e.playIn(tok, id, h264Caps); code != 202 || !out.Pending {
			t.Fatalf("still probing: %d %s", code, raw)
		}
	}
	if len(hls.started) != 0 {
		t.Fatal("a session started while the preferred track was probing")
	}
	close(release)
	var out inputPlayOut
	for range 100 {
		code, o, _ := e.playIn(tok, id, h264Caps)
		if code == 200 {
			out = o
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if out.AudioIndex != 2000 {
		t.Fatalf("after the probe: %+v", out)
	}
}

func TestInputPlayPreferredTrackWaitBounded(t *testing.T) {
	withInputTrackWait(t, 10*time.Millisecond)
	e := newRelayEnv(t)
	clock := e.withClock()
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	gateProbe(ps, inputURL(id, 1), release)
	if code, _, raw := e.playIn(tok, id, h264Caps); code != 202 {
		t.Fatalf("first answer %d %s", code, raw)
	}
	clock.add(inputTrackPreferWait - time.Second)
	if code, _, raw := e.playIn(tok, id, h264Caps); code != 202 {
		t.Fatalf("within the bound %d %s", code, raw)
	}
	clock.add(2 * time.Second)
	if out := e.play200(tok, id, h264Caps); out.AudioIndex != 1 || len(externals(out)) != 0 {
		t.Fatalf("past the bound: %+v", out)
	}
}

func TestInputPlayVideoWithoutAudioTakesExternal(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("", false), []string{ruSidecar}, sidecarProbe("", ""))
	if out := e.play200(tok, id, h264Caps); out.AudioIndex != 2000 {
		t.Fatalf("%+v", out)
	}
}

// seriesWithVoice is a library series (tmdb 42) with episode S01E02, and
// uid's remembered voice «Студия Б» in it; the episode's id.
func (e *relayEnv) seriesWithVoice(tok string) int64 {
	e.t.Helper()
	ep, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Шоу", SeriesTmdbID: 42, Season: 1, Episode: 2,
		File: library.FileInput{Path: e.t.TempDir() + "/Show.S01E02.mkv", Size: 1}})
	if err != nil {
		e.t.Fatal(err)
	}
	if w := e.put(tok, "/api/items/"+strconv.FormatInt(ep, 10)+"/track-prefs", `{"audio":{"lang":"ru","title":"Студия Б"}}`); w.Code/100 != 2 {
		e.t.Fatalf("remember: %d %s", w.Code, w.Body)
	}
	return ep
}

var studioSidecars = []string{"Film/RUS Sound/Студия А/x.mka", "Film/RUS Sound/Студия Б/x.mka"}

// voiceInput is an input of uid with the two studios' ru sidecars.
func (e *relayEnv) voiceInput(ps *probeStub, uid int64, req addonkit.InputRequest) string {
	e.t.Helper()
	req.Name, req.Sidecars = "Film/x.mkv", audioSidecars(studioSidecars...)
	id := e.addFor(uid, "/media/v", req)
	ps.mu.Lock()
	ps.results[inputURL(id, 0)] = videoProbe("en", true)
	ps.results[inputURL(id, 1)] = sidecarProbe("", "")
	ps.results[inputURL(id, 2)] = sidecarProbe("", "")
	ps.mu.Unlock()
	return id
}

func TestInputPlayPrefersSeriesVoiceByTitle(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	e.seriesWithVoice(tok)
	title := &addonkit.InputTitle{Kind: "series", TMDBID: 42, Season: 1, Episode: 2}
	id := e.voiceInput(ps, e.uid, addonkit.InputRequest{Title: title})
	out := e.play200(tok, id, h264Caps)
	if out.AudioIndex != 2001 || out.Audio[2].Title != "Студия Б" {
		t.Fatalf("series voice: %+v", out)
	}
	// another profile: nothing remembered, the first
	pid, ptok := e.profile(auth.NewUser{Name: "Петя"})
	e.prefer(ptok, inputRuPrefs)
	if out := e.play200(ptok, e.voiceInput(ps, pid, addonkit.InputRequest{Title: title}), h264Caps); out.AudioIndex != 2000 || out.Audio[1].Title != "Студия А" {
		t.Fatalf("another profile: %+v", out)
	}
	// a title not in the library: the first
	other := &addonkit.InputTitle{Kind: "series", TMDBID: 99, Season: 1, Episode: 2}
	if out := e.play200(tok, e.voiceInput(ps, e.uid, addonkit.InputRequest{Title: other}), h264Caps); out.AudioIndex != 2000 {
		t.Fatalf("unknown title: %+v", out)
	}
	// a movie title of the same tmdb id is not the series
	movie := &addonkit.InputTitle{Kind: "movie", TMDBID: 42}
	if out := e.play200(tok, e.voiceInput(ps, e.uid, addonkit.InputRequest{Title: movie}), h264Caps); out.AudioIndex != 2000 {
		t.Fatalf("movie title: %+v", out)
	}
}

func TestInputPlaySeriesVoiceFromItem(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	ep := e.seriesWithVoice(tok)
	id := e.voiceInput(ps, e.uid, addonkit.InputRequest{ItemID: ep})
	if out := e.play200(tok, id, h264Caps); out.AudioIndex != 2001 || out.Audio[2].Title != "Студия Б" {
		t.Fatalf("series voice from the item: %+v", out)
	}
}

func TestInputPlaySidecarOrder(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs)
	names := []string{"Film/Sound/b.en.mka", "Film/Sound/a.ru.mka"}
	id := e.tracksInput(ps, e.uid, videoProbe("de", true), names, sidecarProbe("", ""), sidecarProbe("", ""))
	ext := externals(e.play200(tok, id, h264Caps))
	if len(ext) != 2 {
		t.Fatalf("%+v", ext)
	}
	for i, want := range []struct{ src, lang string }{{extaudio.ID(names[0]), "en"}, {extaudio.ID(names[1]), "ru"}} {
		if a := ext[i]; a.Index != externalAudioBase+i || a.Src != want.src || a.Lang != want.lang {
			t.Errorf("track %d: %+v, want %+v", i, a, want)
		}
	}
	// part n = 1+i: the muxed track of index 2001 is the second sidecar
	hls := e.lastHLS()
	body := `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"audioIndex":2001,"audioSrc":"` + extaudio.ID(names[1]) + `"}`
	if out := e.play200(tok, id, body); out.AudioIndex != 2001 {
		t.Fatalf("%+v", out)
	}
	if d := hls(); d.AudioInput != inputURL(id, 2) {
		t.Fatalf("audio input %q", d.AudioInput)
	}
}

func TestInputPlayUnmatchedSidecarListed(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs)
	name := "Other/voice.mka"
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{name}, sidecarProbe("ger", "Синхрон"))
	ext := externals(e.play200(tok, id, h264Caps))
	if len(ext) != 1 || ext[0].Src != extaudio.ID(name) || ext[0].Lang != "de" || ext[0].Title != "Синхрон" {
		t.Fatalf("%+v", ext)
	}
}

func TestInputPlayExternalOnlyForCapableClients(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	dev, err := e.ss.CreateDevice(context.Background(), e.uid, auth.DeviceInfo{ID: "phone-0001", Name: "Телефон"})
	if err != nil {
		t.Fatal(err)
	}
	e.prefer(dev, inputEnPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	if out := e.play200(dev, id, h264Caps, "X-Vault-Caps", "external-audio"); len(externals(out)) != 1 {
		t.Fatalf("capable app: %+v", out)
	}
	if out := e.play200(dev, id, h264Caps); len(externals(out)) != 0 || len(out.Audio) != 1 {
		t.Fatalf("old app: %+v", out)
	}
}

// the HLS decisions of e's router, read when called
func (e *relayEnv) lastHLS() func() media.Decision {
	return func() media.Decision {
		hls := e.deps.HLS.(*fakeHLS)
		if len(hls.started) == 0 {
			e.t.Fatal("no HLS session")
		}
		return hls.started[len(hls.started)-1]
	}
}

// holdProbeSlots fills the server's inputProbeSlots with main probes of
// other inputs blocked on release (each played once to start them).
func (e *relayEnv) holdProbeSlots(ps *probeStub, tok string, release chan struct{}) {
	e.t.Helper()
	for i := range inputProbeSlots {
		id := e.addFor(e.uid, "/media/hold"+strconv.Itoa(i), addonkit.InputRequest{})
		gateProbe(ps, inputURL(id, 0), release)
		if w := e.post(tok, "/api/play/input/"+id, h264Caps); w.Code != 202 {
			e.t.Fatalf("hold %d: %d %s", i, w.Code, w.Body)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for ps.runningNow() < inputProbeSlots && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := ps.runningNow(); n != inputProbeSlots {
		e.t.Fatalf("%d slots held", n)
	}
}

// probedFirst plays id once from an app without external audio: its main
// file is probed (and kept) before the sidecars ever are.
func (e *relayEnv) probedFirst(id string) {
	e.t.Helper()
	dev, err := e.ss.CreateDevice(context.Background(), e.uid, auth.DeviceInfo{ID: "phone-0002", Name: "Телефон"})
	if err != nil {
		e.t.Fatal(err)
	}
	e.play200(dev, id, h264Caps)
}

// A sidecar job that ends while waiting for a slot is no probe failure:
// not logged, not remembered, probed by the next play.
func TestInputPlayTrackSlotWaitNotAFailure(t *testing.T) {
	withInputTrackWait(t, 10*time.Millisecond)
	shortProbeWait(t, 20*time.Millisecond)
	old := inputTrackProbeTimeout
	inputTrackProbeTimeout = 30 * time.Millisecond // the job's bound: 90 ms
	t.Cleanup(func() { inputTrackProbeTimeout = old })
	e := newRelayEnv(t)
	e.withClock()
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputEnPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	e.probedFirst(id)
	release := make(chan struct{})
	e.holdProbeSlots(ps, tok, release)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if out := e.play200(tok, id, h264Caps); len(externals(out)) != 0 {
		t.Fatalf("listed while waiting: %+v", out)
	}
	time.Sleep(200 * time.Millisecond) // the job's bound passes while it waits
	close(release)
	log.SetOutput(os.Stderr)
	if strings.Contains(logs.String(), "probe input track") {
		t.Errorf("logged as a failure: %q", logs.String())
	}
	var out inputPlayOut
	for range 100 {
		if out = e.play200(tok, id, h264Caps); len(externals(out)) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(externals(out)) != 1 || ps.count(inputURL(id, 1)) != 1 {
		t.Fatalf("after the slots freed: %+v, %d probes", out.Audio, ps.count(inputURL(id, 1)))
	}
}

// The preferred-voice wait counts from the probe's start, not from the
// job's: a sidecar waiting for a slot keeps the play pending.
func TestInputPlayPreferWaitFromProbeStart(t *testing.T) {
	withInputTrackWait(t, 10*time.Millisecond)
	shortProbeWait(t, 20*time.Millisecond)
	e := newRelayEnv(t)
	clock := e.withClock()
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	id := e.tracksInput(ps, e.uid, videoProbe("en", true), []string{ruSidecar}, sidecarProbe("", ""))
	e.probedFirst(id)
	release := make(chan struct{})
	e.holdProbeSlots(ps, tok, release)
	if code, _, raw := e.playIn(tok, id, h264Caps); code != 202 {
		t.Fatalf("first: %d %s", code, raw)
	}
	clock.add(inputTrackPreferWait + time.Second)
	if code, _, raw := e.playIn(tok, id, h264Caps); code != 202 {
		t.Fatalf("still waiting for a slot: %d %s", code, raw)
	}
	close(release)
	var out inputPlayOut
	for range 100 {
		code, o, _ := e.playIn(tok, id, h264Caps)
		if code == 200 {
			out = o
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if out.AudioIndex != 2000 {
		t.Fatalf("after the probe: %+v", out)
	}
}

// A series title the library does not know: the voice of itemId's series.
func TestInputPlaySeriesVoiceTitleUnknownFallsBackToItem(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	e.prefer(tok, inputRuPrefs)
	ep := e.seriesWithVoice(tok)
	title := &addonkit.InputTitle{Kind: "series", TMDBID: 99, Season: 1, Episode: 2}
	id := e.voiceInput(ps, e.uid, addonkit.InputRequest{Title: title, ItemID: ep})
	if out := e.play200(tok, id, h264Caps); out.AudioIndex != 2001 || out.Audio[2].Title != "Студия Б" {
		t.Fatalf("fallback to the item: %+v", out)
	}
}
