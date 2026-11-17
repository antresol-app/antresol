package api

import (
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"golang.org/x/text/encoding/charmap"
)

// inputSrt is a one-cue SubRip file.
const inputSrt = "1\n00:00:01,000 --> 00:00:02,000\nПривет, мир\n\n"

func inputNeedFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
}

// subsInput is an input of uid "Film/x.mkv" with one audio sidecar and
// the subtitle sidecars subs (their paths /subs/0, /subs/1, … served with
// data[i] when given).
func (e *relayEnv) subsInput(uid int64, subs []addonkit.SubtitleSidecar, data ...[]byte) string {
	e.t.Helper()
	for i := range subs {
		subs[i].Path = "/subs/" + strconv.Itoa(i)
		if i < len(data) {
			e.serveFile(subs[i].Path, data[i])
		}
	}
	s := audioSidecars("Film/RUS Sound/x.mka")
	s.Subtitles = subs
	return e.addFor(uid, "/media/s", addonkit.InputRequest{Name: "Film/x.mkv", Sidecars: s})
}

func subURL(id string, n int) string {
	return "/api/play/input/" + id + "/subs/" + strconv.Itoa(n) + ".vtt"
}

func TestInputPlayListsSubs(t *testing.T) {
	e := newRelayEnv(t)
	ps, _ := e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{
		{Name: "Film/x.ru.srt", Lang: " EN "},
		{Name: "Film/Subs/x.forced.ass"},
	})
	ps.results[inputURL(id, 0)] = videoProbe("en", true)
	out := e.play200(tok, id, h264Caps)
	if len(out.Subtitles) != 2 {
		t.Fatalf("%+v", out.Subtitles)
	}
	a, b := out.Subtitles[0], out.Subtitles[1]
	if a.Index != externalSubBase || a.Codec != "subrip" || a.Lang != "en" || !a.External || a.URL != subURL(id, 0) {
		t.Errorf("first %+v", a)
	}
	if b.Index != externalSubBase+1 || b.Codec != "ass" || !b.Forced || !b.External || b.URL != subURL(id, 1) {
		t.Errorf("second %+v", b)
	}
	// a lang that is no language tag: the name's
	id2 := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.ru.srt", Lang: "Субтитры <b>"}})
	ps.results[inputURL(id2, 0)] = videoProbe("en", true)
	if s := e.play200(tok, id2, h264Caps).Subtitles; len(s) != 1 || s[0].Lang != "ru" {
		t.Errorf("display text as lang: %+v", s)
	}
}

func TestInputSubsServesVTT(t *testing.T) {
	inputNeedFFmpeg(t)
	e := newRelayEnv(t)
	e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.ru.srt"}}, []byte(inputSrt))
	w := e.as(tok, "GET", subURL(id, 0))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/vtt") ||
		!strings.HasPrefix(w.Body.String(), "WEBVTT") || !strings.Contains(w.Body.String(), "Привет, мир") {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("headers %v", w.Header())
	}
	// read part 1+A+n, as the profile
	got := e.last()
	if got.uri != "/subs/0" || got.header.Get("X-Antresol-Input") != id || !strings.Contains(got.header.Get("X-Antresol-User"), `"id":`+strconv.FormatInt(e.uid, 10)) {
		t.Errorf("asked %+v", got)
	}
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 200 || !strings.Contains(w.Body.String(), "Привет, мир") { // cached
		t.Fatalf("again: %d", w.Code)
	}
}

func TestInputSubsDecodesCP1251(t *testing.T) {
	inputNeedFFmpeg(t)
	b, err := charmap.Windows1251.NewEncoder().String(inputSrt)
	if err != nil {
		t.Fatal(err)
	}
	e := newRelayEnv(t)
	e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(b))
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 200 || !strings.Contains(w.Body.String(), "Привет, мир") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestInputSubsTooLarge(t *testing.T) {
	e := newRelayEnv(t)
	e.withPlay(func(d *Deps) { d.FFmpeg = "false" })
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, bytes.Repeat([]byte("x"), maxInputSub+1))
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 413 || !strings.Contains(w.Body.String(), "Файл субтитров слишком большой") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// remembered: not asked again
	asked := e.seenCount()
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 413 || !strings.Contains(w.Body.String(), "Файл субтитров слишком большой") {
		t.Fatalf("again: %d %s", w.Code, w.Body)
	}
	if n := e.seenCount() - asked; n != 0 {
		t.Fatalf("asked the addon %d more times", n)
	}
	// exactly the bound is read
	id2 := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, bytes.Repeat([]byte("x"), maxInputSub))
	if w := e.as(tok, "GET", subURL(id2, 0)); w.Code == 413 {
		t.Fatalf("at the bound: %d %s", w.Code, w.Body)
	}
}

// A conversion that failed is remembered for a while: the next fetches
// fail at once, without asking the addon again.
func TestInputSubsFailureRemembered(t *testing.T) {
	e := newRelayEnv(t)
	clock := e.withClock()
	e.withPlay(func(d *Deps) { d.FFmpeg = "false" })
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(inputSrt))
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 502 || !strings.Contains(w.Body.String(), "Не удалось прочитать субтитры") {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	asked := e.seenCount()
	for range 3 {
		if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 502 || !strings.Contains(w.Body.String(), "Не удалось прочитать субтитры") {
			t.Fatalf("again: %d %s", w.Code, w.Body)
		}
	}
	if n := e.seenCount() - asked; n != 0 {
		t.Fatalf("asked the addon %d more times", n)
	}
	clock.add(inputSubFailMemory + time.Second)
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 502 || e.seenCount() == asked {
		t.Fatalf("after the memory: %d, %d reads", w.Code, e.seenCount()-asked)
	}
}

// The addon's file at the same path changed (another length): a new input
// of it serves the new cue, not the old conversion.
func TestInputSubsReplacedFile(t *testing.T) {
	inputNeedFFmpeg(t)
	e := newRelayEnv(t)
	e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(inputSrt))
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 200 || !strings.Contains(w.Body.String(), "Привет, мир") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	other := "1\n00:00:01,000 --> 00:00:02,000\nДо свидания, мир\n\n"
	id2 := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(other))
	if w := e.as(tok, "GET", subURL(id2, 0)); w.Code != 200 || !strings.Contains(w.Body.String(), "До свидания, мир") {
		t.Fatalf("replaced: %d %s", w.Code, w.Body)
	}
	ents, err := os.ReadDir(e.deps.DataDir + "/subs")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ents {
		if !strings.HasPrefix(f.Name(), "i-") || !strings.HasSuffix(f.Name(), ".vtt") || strings.Contains(f.Name(), id) {
			t.Errorf("cache file %q", f.Name())
		}
	}
}

func TestInputSubsOtherProfile404(t *testing.T) {
	e := newRelayEnv(t)
	e.withPlay(nil)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(inputSrt))
	_, other := e.profile(auth.NewUser{Name: "Петя", IsAdmin: true, Password: "secret-pass-1"})
	if w := e.as(other, "GET", subURL(id, 0)); w.Code != 404 || !strings.Contains(w.Body.String(), msgAddonNoInput) {
		t.Fatalf("another profile: %d %s", w.Code, w.Body)
	}
	_, kid := e.profile(auth.NewUser{Name: "Ваня", IsKid: true})
	if w := e.as(kid, "GET", subURL(id, 0)); w.Code != 403 {
		t.Fatalf("kid: %d %s", w.Code, w.Body)
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
}

func TestInputSubsBadIndex404(t *testing.T) {
	e := newRelayEnv(t)
	e.withPlay(nil)
	tok := e.session(e.uid)
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(inputSrt))
	for _, n := range []string{"1", "-1", "01", "x", "99999999999999999999"} {
		if w := e.as(tok, "GET", "/api/play/input/"+id+"/subs/"+n+".vtt"); w.Code != 404 {
			t.Errorf("%s: %d %s", n, w.Code, w.Body)
		}
	}
	if n := e.seenCount(); n != 0 {
		t.Fatalf("the addon was asked %d times", n)
	}
}

// A subtitle sidecar without a Content-Length is cut at maxInputSub while
// it is read: 413, remembered.
func TestInputSubsTooLargeChunked(t *testing.T) {
	e := newRelayEnv(t)
	e.withPlay(func(d *Deps) { d.FFmpeg = "false" })
	tok := e.session(e.uid)
	s := audioSidecars("Film/RUS Sound/x.mka")
	s.Subtitles = []addonkit.SubtitleSidecar{{Path: "/big-chunked", Name: "Film/x.srt"}}
	id := e.addFor(e.uid, "/media/s", addonkit.InputRequest{Name: "Film/x.mkv", Sidecars: s})
	var w *httptest.ResponseRecorder
	for range 100 { // the read runs detached: 503 until it ends
		if w = e.as(tok, "GET", subURL(id, 0)); w.Code != 503 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if w.Code != 413 || !strings.Contains(w.Body.String(), "Файл субтитров слишком большой") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	asked := e.seenCount()
	if w := e.as(tok, "GET", subURL(id, 0)); w.Code != 413 || e.seenCount() != asked {
		t.Fatalf("again: %d, %d more asks", w.Code, e.seenCount()-asked)
	}
}

// Converted input subtitles unused for InputTTL are removed when another
// is converted; library subtitles and fresh ones stay.
func TestInputSubsCachePruned(t *testing.T) {
	e := newRelayEnv(t)
	e.withPlay(func(d *Deps) { d.FFmpeg = "false" })
	tok := e.session(e.uid)
	dir := filepath.Join(e.deps.DataDir, "subs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-addons.InputTTL - time.Hour)
	for _, n := range []string{"i-old.vtt", "1-3-abc-1-2.vtt", "i-new.vtt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("WEBVTT\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if n != "i-new.vtt" {
			if err := os.Chtimes(filepath.Join(dir, n), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	id := e.subsInput(e.uid, []addonkit.SubtitleSidecar{{Name: "Film/x.srt"}}, []byte(inputSrt))
	e.as(tok, "GET", subURL(id, 0))
	for n, want := range map[string]bool{"i-old.vtt": false, "1-3-abc-1-2.vtt": true, "i-new.vtt": true} {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != want {
			t.Errorf("%s: kept %v, want %v", n, err == nil, want)
		}
	}
}

// blockingConverter is a fake ffmpeg for subtitle conversions: each run
// leaves a file in dir/running while it lasts, waits while dir/gate
// exists, then writes a one-line WebVTT to its output (the last argument).
type blockingConverter struct {
	t   *testing.T
	dir string
}

func newBlockingConverter(t *testing.T) *blockingConverter {
	t.Helper()
	c := &blockingConverter{t: t, dir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(c.dir, "running"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nfor a; do last=$a; done\n" +
		"r=" + c.dir + "/running/$$\n: > \"$r\"\n" +
		"while [ -e " + c.dir + "/gate ]; do sleep 0.02; done\n" +
		"printf 'WEBVTT\\n' > \"$last\"\nrm -f \"$r\"\n"
	if err := os.WriteFile(c.path(), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.open) // never leave a run waiting
	return c
}

func (c *blockingConverter) path() string { return filepath.Join(c.dir, "ffmpeg") }

// close makes the runs started from now on (and those running) wait.
func (c *blockingConverter) close() {
	c.t.Helper()
	if err := os.WriteFile(filepath.Join(c.dir, "gate"), nil, 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *blockingConverter) open() { os.Remove(filepath.Join(c.dir, "gate")) }

// running is the number of runs under way.
func (c *blockingConverter) running() int {
	ents, _ := os.ReadDir(filepath.Join(c.dir, "running"))
	return len(ents)
}

// waitRunning waits until n runs are under way.
func (c *blockingConverter) waitRunning(n int) {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.running() != n {
		if time.Now().After(deadline) {
			c.t.Fatalf("%d conversions running, want %d", c.running(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// subsInputs registers n inputs, each with one SubRip sidecar of its own.
func (e *relayEnv) subsInputs(n int) []string {
	e.t.Helper()
	var ids []string
	for i := range n {
		sub := addonkit.SubtitleSidecar{Path: "/subs/own-" + strconv.Itoa(i), Name: "Film/x.srt"}
		e.serveFile(sub.Path, []byte(inputSrt+strconv.Itoa(i)+"\n"))
		s := audioSidecars("Film/RUS Sound/x.mka")
		s.Subtitles = []addonkit.SubtitleSidecar{sub}
		ids = append(ids, e.addFor(e.uid, "/media/s"+strconv.Itoa(i), addonkit.InputRequest{Name: "Film/x.mkv", Sidecars: s}))
	}
	return ids
}

// Input subtitle conversions share one server-wide cap of inputSubSlots:
// a third one waits for a slot, and past the wait it is 503 (not
// remembered as a failure); once a slot is free it runs.
func TestInputSubs_ServerWideCap(t *testing.T) {
	e := newRelayEnv(t)
	conv := newBlockingConverter(t)
	e.withPlay(func(d *Deps) { d.FFmpeg = conv.path(); d.inputSubWait = 50 * time.Millisecond })
	tok := e.session(e.uid)
	ids := e.subsInputs(3)
	conv.close()
	codes := make(chan int, 2)
	for _, id := range ids[:2] {
		go func() { codes <- e.as(tok, "GET", subURL(id, 0)).Code }()
	}
	conv.waitRunning(2)
	w := e.as(tok, "GET", subURL(ids[2], 0))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Сервер занят, попробуйте ещё раз") || w.Header().Get("Retry-After") == "" {
		t.Fatalf("third: %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	if n := conv.running(); n != 2 {
		t.Fatalf("%d conversions running", n)
	}
	if w := e.as(tok, "GET", subURL(ids[2], 0)); w.Code != 503 { // busy is no failure: still waiting, not 502
		t.Fatalf("third again: %d %s", w.Code, w.Body)
	}
	conv.open()
	for range 2 {
		if c := <-codes; c != 200 {
			t.Fatalf("first two: %d", c)
		}
	}
	if w := e.as(tok, "GET", subURL(ids[2], 0)); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "WEBVTT") {
		t.Fatalf("third once a slot is free: %d %s", w.Code, w.Body)
	}
}

// A converted sidecar is served from the cache with every slot taken.
func TestInputSubs_CachedTakesNoSlot(t *testing.T) {
	e := newRelayEnv(t)
	conv := newBlockingConverter(t)
	e.withPlay(func(d *Deps) { d.FFmpeg = conv.path(); d.inputSubWait = 50 * time.Millisecond })
	tok := e.session(e.uid)
	ids := e.subsInputs(4)
	if w := e.as(tok, "GET", subURL(ids[0], 0)); w.Code != 200 {
		t.Fatalf("convert: %d %s", w.Code, w.Body)
	}
	conv.close()
	done := make(chan struct{}, 2)
	for _, id := range ids[1:3] {
		go func() { e.as(tok, "GET", subURL(id, 0)); done <- struct{}{} }()
	}
	conv.waitRunning(2)
	if w := e.as(tok, "GET", subURL(ids[3], 0)); w.Code != 503 { // the slots are taken
		t.Fatalf("a new one: %d %s", w.Code, w.Body)
	}
	if w := e.as(tok, "GET", subURL(ids[0], 0)); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "WEBVTT") {
		t.Fatalf("cached: %d %s", w.Code, w.Body)
	}
	conv.open()
	<-done
	<-done
}
