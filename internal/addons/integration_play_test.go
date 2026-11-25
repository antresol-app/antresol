package addons_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/progress"
)

const (
	inputToken = "integration-input-token-0123456789"
	inputBase  = "http://127.0.0.1:1" // ffmpeg's way in; the probe is a stub
)

type mediaCall struct {
	Name  string `json:"name"`
	User  string `json:"user"`
	Input string `json:"input"`
}

// waitRunning waits for the addon id to be running.
func (s *stage2a) waitRunning(id string) {
	s.t.Helper()
	end := time.Now().Add(30 * time.Second)
	for s.mgr.Status(id) != addons.StatusRunning {
		if time.Now().After(end) {
			s.t.Fatalf("%s: status %s", id, s.mgr.Status(id))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// input asks the running fake for its input (registered on the first ask).
func (s *stage2a) input() string {
	s.t.Helper()
	var got struct{ ID string }
	if c := s.sock("fake", "GET", "/input", "", &got); c != 200 || got.ID == "" {
		s.t.Fatalf("GET /input: %d %+v", c, got)
	}
	return got.ID
}

// relay asks the loopback relay as ffmpeg would.
func (s *stage2a) relay(id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/addon-input/"+id+"/0?k="+inputToken, nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Range", "bytes=0-9")
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

// TestStage2b_EndToEnd runs the fake addon in player mode against the real
// core: it registers an input of a profile through the core API, that
// profile plays it directly (the bytes come from the addon's socket, with
// the input and the profile in the request), no one else sees it, and a
// stopped or restarted addon takes its inputs with it.
func TestStage2b_EndToEnd(t *testing.T) {
	bin := addons.FakeAddonBin(t)
	t.Cleanup(func() {
		if exec.Command("pgrep", "-f", bin).Run() == nil {
			t.Error("a fake addon process outlived the test")
		}
	})
	data := make([]byte, 256<<10)
	rng := rand.New(rand.NewPCG(7, 8))
	for i := range data {
		data[i] = byte(rng.Uint32())
	}
	file := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	s := newStage2a(t, func(s *stage2a, d *api.Deps) {
		d.Progress = progress.New(d.DB)
		d.AddonInputs = &addons.Inputs{Serving: s.mgr.Serving}
		d.InputBase, d.InputToken, d.DataDir = inputBase, inputToken, t.TempDir()
		d.Probe = func(ctx context.Context, path string) (media.ProbeResult, error) {
			probes.Add(1)
			return media.ProbeResult{Container: "mp4", DurationMs: 5000,
				Video: &media.VideoStream{Codec: "h264", Height: 720, BitDepth: 8},
				Audio: []media.AudioStream{{Index: 1, Codec: "aac", Default: true}}}, nil
		}
	})
	var me struct{ ID int64 }
	if w := s.do("GET", "/api/me", "", s.adult); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &me) != nil || me.ID == 0 {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	// the addon's own variables (ANTRESOL_FAKE_*): the server passes no other
	t.Setenv("ANTRESOL_FAKE_MEDIA", file)
	t.Setenv("ANTRESOL_FAKE_USER", strconv.FormatInt(me.ID, 10))
	addons.WriteLocalAddon(t, s.mgr.Dev, addons.Manifest{ID: "fake", Name: addons.Text{RU: "Плеер", EN: "Player"},
		Version: "1.0.0", Access: "adults", Permissions: []string{"play"}}, "player")
	if w := s.do("POST", "/api/addons", `{"url":"local:fake","version":"1.0.0"}`, s.admin); w.Code != 200 {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	s.waitRunning("fake")
	id := s.input()
	if again := s.input(); again != id {
		t.Fatalf("a second GET /input registered again: %s, then %s", id, again)
	}

	const caps = `{"caps":{"videoCodecs":["h264"],"audioCodecs":["aac"],"containers":["mp4"]}}`
	plays := func(id string) {
		t.Helper()
		w := s.do("POST", "/api/play/input/"+id, caps, s.adult)
		var out struct{ Mode, URL string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Mode != "direct" || out.URL != "/api/play/input/"+id+"/direct" {
			t.Fatalf("play: %d %s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), inputToken) || strings.Contains(w.Body.String(), "/media/x") {
			t.Fatalf("play answer leaks the relay: %s", w.Body)
		}
		w = s.do("GET", out.URL, "", s.adult, "Range", "bytes=1000-1999")
		if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), data[1000:2000]) {
			t.Fatalf("direct: %d, %d bytes", w.Code, w.Body.Len())
		}
		if cr := w.Header().Get("Content-Range"); cr != "bytes 1000-1999/"+strconv.Itoa(len(data)) {
			t.Fatalf("Content-Range %q", cr)
		}
	}
	plays(id)
	if probes.Load() == 0 {
		t.Fatal("the input was not probed")
	}

	// the addon saw the input and the profile on each read
	var calls []mediaCall
	s.sock("fake", "GET", "/calls", "", &calls)
	if len(calls) == 0 {
		t.Fatal("the addon was not read")
	}
	for _, c := range calls {
		var who struct{ ID int64 }
		if c.Name != "media/x" || c.Input != id || json.Unmarshal([]byte(c.User), &who) != nil || who.ID != me.ID {
			t.Fatalf("addon read %+v", c)
		}
	}
	if w := s.relay(id); w.Code != 206 || !bytes.Equal(w.Body.Bytes(), data[:10]) {
		t.Fatalf("relay: %d %s", w.Code, w.Body)
	}

	// another profile (the admin included) and a kid do not get it
	for _, c := range []struct {
		who    string
		cookie *http.Cookie
		code   int
	}{{"admin", s.admin, 404}, {"kid", s.kid, 403}} {
		if w := s.do("GET", "/api/play/input/"+id, "", c.cookie); w.Code != c.code {
			t.Errorf("%s info: %d", c.who, w.Code)
		}
		if w := s.do("POST", "/api/play/input/"+id, caps, c.cookie); w.Code != c.code {
			t.Errorf("%s play: %d", c.who, w.Code)
		}
		if w := s.do("GET", "/api/play/input/"+id+"/direct", "", c.cookie); w.Code != c.code {
			t.Errorf("%s direct: %d", c.who, w.Code)
		}
	}

	// every route of an input answers 404 «Видео не найдено»
	gone := func(id, when string) {
		t.Helper()
		for _, r := range []struct{ method, path, body string }{
			{"GET", "/api/play/input/" + id, ""},
			{"POST", "/api/play/input/" + id, caps},
			{"GET", "/api/play/input/" + id + "/direct", ""},
			{"HEAD", "/api/play/input/" + id + "/direct", ""},
			{"GET", "/api/play/input/" + id + "/subs/1.vtt", ""},
			{"PUT", "/api/play/input/" + id + "/progress", `{"positionMs":1000,"durationMs":5000}`},
		} {
			w := s.do(r.method, r.path, r.body, s.adult)
			if w.Code != 404 || (r.method != "HEAD" && !strings.Contains(w.Body.String(), "Видео не найдено")) {
				t.Errorf("%s: %s %s: %d %s", when, r.method, r.path, w.Code, w.Body)
			}
		}
		if w := s.relay(id); w.Code != 404 {
			t.Errorf("%s: relay %d", when, w.Code)
		}
	}

	// stopped: every route 404
	if w := s.do("POST", "/api/addons/fake/disable", "", s.admin); w.Code != 200 && w.Code != 204 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	gone(id, "stopped")

	// started again: the old id stays gone (the new process is not asked
	// for it), a new input plays
	if w := s.do("POST", "/api/addons/fake/enable", "", s.admin); w.Code != 200 && w.Code != 204 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	s.waitRunning("fake")
	gone(id, "restarted")
	if s.sock("fake", "GET", "/calls", "", &calls); len(calls) != 0 {
		t.Fatalf("the new process was asked for the old input: %+v", calls)
	}
	id2 := s.input()
	if id2 == id {
		t.Fatal("the new process got the old id")
	}
	plays(id2)
	s.sock("fake", "GET", "/calls", "", &calls)
	for _, c := range calls {
		if c.Input != id2 {
			t.Fatalf("addon read %+v", c)
		}
	}
}
