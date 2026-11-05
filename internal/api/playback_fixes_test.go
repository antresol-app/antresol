package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

func TestPlayRemoteUsesHomeNetworkRule(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Keyframe = func(context.Context, string, float64) (float64, error) { return 0, errors.New("no") }
	})
	_, f := e.movie("Волк", "18+", "x")
	e.probeJSON(f, remuxProbe)
	_, out := e.play(f, "{"+chromeBody+"}", e.admin) // 192.168.1.20
	if out.Quality.Remote || out.Mode != "remux" {
		t.Fatalf("home client: %+v", out)
	}
	e.peer = "203.0.113.7:5000"
	_, out = e.play(f, "{"+chromeBody+"}", e.admin)
	if !out.Quality.Remote || out.Quality.Applied != "1080" || out.Mode != "transcode" {
		t.Fatalf("internet client: %+v", out)
	}
}

// h264 1080p remux source at 30 Mbit/s: plays as a copy at home.
const remuxProbe = `{"Container":"mkv","DurationMs":7200000,"Video":{"Codec":"h264","Width":1920,"Height":1080,"BitDepth":8,"BitRate":30000000},
 "Audio":[{"Index":1,"Codec":"aac","Lang":"rus","Channels":2,"Default":true}]}`

var chromeBody = `"caps":{"videoCodecs":["h264","hevc"],"audioCodecs":["aac"],"containers":["mp4"]}`

type playOut struct {
	Mode     string  `json:"mode"`
	URL      string  `json:"url"`
	StartSec float64 `json:"startSec"`
	Quality  struct {
		Selected, Applied string
		Remote            bool
	} `json:"quality"`
	Error string `json:"error"`
}

func (e *libEnv) play(f int64, body string, c *http.Cookie) (int, playOut) {
	w := e.do("POST", "/api/play/"+itoa(f), body, c)
	var out playOut
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestPlayRemoteQualityCap(t *testing.T) {
	var remote atomic.Bool
	var database *sql.DB
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		d.ClientIsRemote = func(*http.Request) bool { return remote.Load() }
		d.DB, database = db, db
		d.Keyframe = func(context.Context, string, float64) (float64, error) { return 0, errors.New("no") }
	})
	_, f := e.movie("Волк", "18+", "x")
	e.probeJSON(f, remuxProbe)

	// At home «Авто» is the original: a copy.
	code, out := e.play(f, "{"+chromeBody+"}", e.admin)
	if code != 200 || out.Mode != "remux" || out.Quality.Applied != "original" || out.Quality.Remote {
		t.Fatalf("home: %d %+v", code, out)
	}
	// From outside «Авто» is capped by «Качество вне дома» (1080p 8 Мбит/с).
	remote.Store(true)
	code, out = e.play(f, "{"+chromeBody+"}", e.admin)
	last := e.hls.started[len(e.hls.started)-1]
	if code != 200 || out.Mode != "transcode" || out.Quality.Selected != "auto" || out.Quality.Applied != "1080" || !out.Quality.Remote ||
		last.MaxBitrate != 8_000_000 {
		t.Fatalf("remote: %d %+v %+v", code, out, last)
	}
	// The saved setting applies.
	if err := saveAppSettings(context.Background(), database, AppSettings{RemoteQuality: "720"}); err != nil {
		t.Fatal(err)
	}
	_, out = e.play(f, "{"+chromeBody+"}", e.admin)
	last = e.hls.started[len(e.hls.started)-1]
	if out.Quality.Applied != "720" || last.TargetHeight != 720 || last.MaxBitrate != 4_000_000 {
		t.Fatalf("remote 720: %+v %+v", out, last)
	}
	// An explicit «Оригинал» wins outside too, and 480p applies at home.
	_, out = e.play(f, "{"+chromeBody+`,"quality":"original"}`, e.admin)
	if out.Mode != "remux" || out.Quality.Applied != "original" {
		t.Fatalf("remote original: %+v", out)
	}
	remote.Store(false)
	_, out = e.play(f, "{"+chromeBody+`,"quality":"480"}`, e.admin)
	last = e.hls.started[len(e.hls.started)-1]
	if out.Mode != "transcode" || out.Quality.Applied != "480" || last.TargetHeight != 480 {
		t.Fatalf("home 480: %+v %+v", out, last)
	}
	// Junk is «Авто».
	_, out = e.play(f, "{"+chromeBody+`,"quality":"8k"}`, e.admin)
	if out.Quality.Selected != "auto" || out.Mode != "remux" {
		t.Fatalf("junk quality: %+v", out)
	}
}

func TestPlayRemuxReportsKeyframeStart(t *testing.T) {
	var asked []float64
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Keyframe = func(_ context.Context, _ string, at float64) (float64, error) {
			asked = append(asked, at)
			return 97.5, nil
		}
	})
	_, f := e.movie("Волк", "18+", "x")
	e.probeJSON(f, remuxProbe)
	_, out := e.play(f, "{"+chromeBody+`,"startSec":100}`, e.admin)
	if out.Mode != "remux" || out.StartSec != 97.5 {
		t.Fatalf("%+v", out)
	}
	// Just after the keyframe: ffmpeg's B-frame heuristic seeks 3/23 s earlier.
	if o := e.hls.opts[0]; o.StartSec < 97.5+0.14 || o.StartSec > 97.5+0.3 {
		t.Fatalf("ffmpeg start %v, want just after the keyframe", o.StartSec)
	}
	// A transcode starts exactly where asked: no keyframe lookup.
	_, out = e.play(f, "{"+chromeBody+`,"startSec":100,"quality":"720"}`, e.admin)
	if out.Mode != "transcode" || out.StartSec != 100 || len(asked) != 1 {
		t.Fatalf("%+v asked=%v", out, asked)
	}
}

func TestPlaySessionLimitsAreRussian(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	for _, c := range []struct {
		err  error
		code int
	}{{media.ErrUserLimit, 429}, {media.ErrBusy, 503}, {media.ErrNoSpace, 507}} {
		e.hls.err = c.err
		code, out := e.play(f, `{"caps":{"videoCodecs":["h264"]}}`, e.admin)
		if code != c.code || !russian(out.Error) {
			t.Errorf("%v: %d %q", c.err, code, out.Error)
		}
	}
}

func TestPlayReplacesOnlyOwnSession(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	e.play(f, `{"caps":{"videoCodecs":["h264"]}}`, e.admin)
	e.play(f, `{"caps":{"videoCodecs":["h264"]},"replaceSession":"sess1"}`, e.admin)
	if o := e.hls.opts[1]; o.Replace != "sess1" || o.UserID == 0 {
		t.Fatalf("own replace: %+v", o)
	}
	other := e.login(`{"name":"Мама"}`)
	e.play(f, `{"caps":{"videoCodecs":["h264"]},"replaceSession":"sess1"}`, other)
	if r := e.hls.opts[2].Replace; r != "" {
		t.Fatalf("another profile replaced the session: %q", r)
	}
	// The next part plays direct: the replaced HLS session is stopped all the same.
	_, g := e.movie("Брат 2", "16+", "x")
	e.probeJSON(g, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	e.play(g, `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]},"replaceSession":"sess1"}`, other)
	if len(e.hls.stopped) != 1 || e.hls.stopped[0] != "sess1" {
		t.Fatalf("stopped %v", e.hls.stopped)
	}
}

func TestHLSHeartbeatAndRetryableErrors(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	e.play(f, `{"caps":{"videoCodecs":["h264"]}}`, e.admin)
	w := e.do("PUT", "/api/hls/sess1", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"segments":3`) || !strings.Contains(w.Body.String(), `"running":true`) {
		t.Fatalf("heartbeat %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/hls/sess1", "", e.login(`{"name":"Мама"}`)); w.Code != 404 {
		t.Fatalf("other profile's heartbeat %d", w.Code)
	}
	e.hls.plErr = media.ErrNotReady
	w = e.do("GET", "/api/hls/sess1/index.m3u8", "", e.admin)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" || w.Header().Get("Content-Type") == "application/vnd.apple.mpegurl" {
		t.Fatalf("not ready: %d %v", w.Code, w.Header())
	}
	e.hls.segErr = media.ErrSegmentGone
	if w := e.do("GET", "/api/hls/sess1/seg00001.ts", "", e.admin); w.Code != http.StatusGone {
		t.Fatalf("trimmed segment: %d", w.Code)
	}
	e.hls.gone = true
	if w := e.do("PUT", "/api/hls/sess1", "", e.admin); w.Code != 404 {
		t.Fatalf("gone session heartbeat %d", w.Code)
	}
}

// subtitleFFmpeg is a fake ffmpeg that counts its runs, waits and writes WebVTT.
func subtitleFFmpeg(t *testing.T, delay string) (path, counter string) {
	t.Helper()
	dir := t.TempDir()
	counter = filepath.Join(dir, "runs")
	path = filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\necho run >> " + counter + "\nsleep " + delay + "\nfor a; do last=$a; done\nprintf 'WEBVTT\\n\\n00:01.000 --> 00:02.000\\nПривет\\n' > \"$last\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, counter
}

func TestEmbeddedSubtitleSurvivesCancelledRequest(t *testing.T) {
	ff, counter := subtitleFFmpeg(t, "0.5")
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.FFmpeg = ff })
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe) // stream 3 is subrip
	path := "/api/subs/" + itoa(f) + "/3.vtt"

	// The player reloads 100 ms in: the request is cancelled...
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	r.RemoteAddr = "192.168.1.20:5000"
	r.AddCookie(e.admin)
	e.h.ServeHTTP(httptest.NewRecorder(), r)
	// ...but the extraction goes on, and the next request gets its result.
	time.Sleep(700 * time.Millisecond)
	w := e.do("GET", path, "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Привет") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(counter); strings.Count(string(b), "run") != 1 {
		t.Fatalf("ffmpeg ran %d times", strings.Count(string(b), "run"))
	}
}

func TestEmbeddedSubtitleStillWorkingIs503(t *testing.T) {
	ff, counter := subtitleFFmpeg(t, "1")
	old := subtitleWait
	subtitleWait = 100 * time.Millisecond
	t.Cleanup(func() { subtitleWait = old })
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.FFmpeg = ff })
	_, f := e.movie("Брат", "16+", "x")
	e.probeJSON(f, hevcProbe)
	path := "/api/subs/" + itoa(f) + "/3.vtt"
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i] = e.do("GET", path, "", e.admin).Code }()
	}
	wg.Wait()
	for _, c := range codes {
		if c != 503 {
			t.Fatalf("codes %v, want 503 while extracting", codes)
		}
	}
	time.Sleep(1200 * time.Millisecond)
	if w := e.do("GET", path, "", e.admin); w.Code != 200 {
		t.Fatalf("after extraction %d", w.Code)
	}
	if b, _ := os.ReadFile(counter); strings.Count(string(b), "run") != 1 {
		t.Fatalf("ffmpeg ran %d times for concurrent requests", strings.Count(string(b), "run"))
	}
}
