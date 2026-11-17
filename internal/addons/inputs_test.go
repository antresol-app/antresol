package addons

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

func validInputReq() addonkit.InputRequest {
	return addonkit.InputRequest{
		UserID: 7,
		Path:   "/media/1",
		Name:   "Show/Show.S01E01.mkv",
		Size:   1 << 20,
		Title:  &addonkit.InputTitle{Kind: "series", TMDBID: 42, Season: 1, Episode: 1},
		Sidecars: addonkit.InputSidecars{
			Audio: []addonkit.AudioSidecar{
				{Path: "/media/1/a/0", Name: "Show/Rus Sound/Studio/Show.S01E01.mka", Size: 100},
				{Path: "/media/1/a/1", Name: "Show/Eng Sound/Show.S01E01.mka", Size: 200},
			},
			Subtitles: []addonkit.SubtitleSidecar{
				{Path: "/media/1/s/0", Name: "Show/Subs/Show.S01E01.srt", Lang: "ru"},
			},
		},
		Meta: json.RawMessage(`{"note":"x"}`),
	}
}

func TestValidateInput(t *testing.T) {
	if err := ValidateInput(validInputReq()); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	ok := []func(*addonkit.InputRequest){
		func(r *addonkit.InputRequest) { r.Path = "/media/x?part=2" },
		func(r *addonkit.InputRequest) { r.Path = "/media/%C3%A9%20x" }, // canonical escaping

		func(r *addonkit.InputRequest) { r.Title = nil; r.Meta = nil },
		func(r *addonkit.InputRequest) { r.Title = &addonkit.InputTitle{Kind: "movie", TMDBID: 1} },
		func(r *addonkit.InputRequest) { r.Sidecars = addonkit.InputSidecars{} },
	}
	for i, mod := range ok {
		r := validInputReq()
		mod(&r)
		if err := ValidateInput(r); err != nil {
			t.Errorf("ok case %d refused: %v", i, err)
		}
	}

	badPaths := []string{
		"", "media/x", "/hooks/files.plan", "/hooks", "/hooks%2Ffiles.plan", "/health",
		"/a/../b", "/a/%2E%2E/b", "/a%5cb", "/a%2fb", "/a%41b", "//evil.example/x", "http://x/a",
		"/a\\b", "/a//b", "/a/./b", "/a/", "/a#frag", "/a#", "/a\x00b",
		"/a%c3%a9", "/a\u00e9", "/a b", // not canonical: lowercase escape, raw non-ASCII, raw space
		"/a?x y", "/a?x\ty", "/a?x\x7fy", "/a?x\u00e9", // a query with a space, a control character, non-ASCII
		"/" + strings.Repeat("a", 1024),
	}
	audio := func(n int) []addonkit.AudioSidecar {
		out := make([]addonkit.AudioSidecar, n)
		for i := range out {
			out[i] = addonkit.AudioSidecar{Path: fmt.Sprintf("/a/%d", i), Name: fmt.Sprintf("a%d.mka", i), Size: 1}
		}
		return out
	}
	subs := func(n int) []addonkit.SubtitleSidecar {
		out := make([]addonkit.SubtitleSidecar, n)
		for i := range out {
			out[i] = addonkit.SubtitleSidecar{Path: fmt.Sprintf("/s/%d", i), Name: fmt.Sprintf("s%d.srt", i)}
		}
		return out
	}
	type tc struct {
		name string
		mod  func(*addonkit.InputRequest)
	}
	cases := []tc{
		{"userId 0", func(r *addonkit.InputRequest) { r.UserID = 0 }},
		{"userId neg", func(r *addonkit.InputRequest) { r.UserID = -1 }},
		{"size 0", func(r *addonkit.InputRequest) { r.Size = 0 }},
		{"audio size 0", func(r *addonkit.InputRequest) { r.Sidecars.Audio[0].Size = 0 }},
		{"name empty", func(r *addonkit.InputRequest) { r.Name = "" }},
		{"name ..", func(r *addonkit.InputRequest) { r.Name = "../x" }},
		{"name inner ..", func(r *addonkit.InputRequest) { r.Name = "a/../x" }},
		{"name abs", func(r *addonkit.InputRequest) { r.Name = "/x.mkv" }},
		{"name NUL", func(r *addonkit.InputRequest) { r.Name = "x\x00.mkv" }},
		{"name unclean", func(r *addonkit.InputRequest) { r.Name = "a//x.mkv" }},
		{"name dot", func(r *addonkit.InputRequest) { r.Name = "a/./x.mkv" }},
		{"name long", func(r *addonkit.InputRequest) { r.Name = strings.Repeat("a", 513) }},
		{"audio name ..", func(r *addonkit.InputRequest) { r.Sidecars.Audio[1].Name = "../x.mka" }},
		{"sub name empty", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Name = "" }},
		{"dup sidecar names", func(r *addonkit.InputRequest) { r.Sidecars.Audio[1].Name = r.Sidecars.Audio[0].Name }},
		{"dup main and sidecar", func(r *addonkit.InputRequest) { r.Sidecars.Audio[0].Name = r.Name }},
		{"33 audio", func(r *addonkit.InputRequest) { r.Sidecars.Audio = audio(33) }},
		{"65 subs", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles = subs(65) }},
		{"txt subtitle", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Name = "Show/Subs/x.txt" }},
		{"meta array", func(r *addonkit.InputRequest) { r.Meta = json.RawMessage(`[1]`) }},
		{"meta null", func(r *addonkit.InputRequest) { r.Meta = json.RawMessage(`null`) }},
		{"meta invalid", func(r *addonkit.InputRequest) { r.Meta = json.RawMessage(`{"a":`) }},
		{"meta big", func(r *addonkit.InputRequest) {
			r.Meta = json.RawMessage(`{"a":"` + strings.Repeat("x", 4<<10) + `"}`)
		}},
		{"title kind", func(r *addonkit.InputRequest) { r.Title.Kind = "show" }},
		{"title tmdb", func(r *addonkit.InputRequest) { r.Title.TMDBID = 0 }},
		{"title season", func(r *addonkit.InputRequest) { r.Title.Season = -1 }},
		{"title episode", func(r *addonkit.InputRequest) { r.Title.Episode = -1 }},
		{"name newline", func(r *addonkit.InputRequest) { r.Name = "a\nb.mkv" }},
		{"name tab", func(r *addonkit.InputRequest) { r.Name = "a\tb.mkv" }},
		{"name DEL", func(r *addonkit.InputRequest) { r.Name = "a\x7fb.mkv" }},
		{"name ESC", func(r *addonkit.InputRequest) { r.Name = "a\x1b[31m.mkv" }},
		{"audio name CR", func(r *addonkit.InputRequest) { r.Sidecars.Audio[0].Name = "a\r.mka" }},
		{"sub name newline", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Name = "s\n.srt" }},
		{"sub lang control", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Lang = "ru\n" }},
		{"sub lang DEL", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Lang = "r\x7fu" }},
		{"sub lang long", func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Lang = strings.Repeat("a", 36) }},
	}
	for _, p := range badPaths {
		p := p
		cases = append(cases,
			tc{"main path " + p, func(r *addonkit.InputRequest) { r.Path = p }},
			tc{"audio path " + p, func(r *addonkit.InputRequest) { r.Sidecars.Audio[0].Path = p }},
			tc{"sub path " + p, func(r *addonkit.InputRequest) { r.Sidecars.Subtitles[0].Path = p }},
		)
	}
	for _, c := range cases {
		r := validInputReq()
		c.mod(&r)
		err := ValidateInput(r)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		msg, isUser := UserMessage(err)
		if !isUser || !strings.HasPrefix(msg, "Неверное описание видео: ") {
			t.Errorf("%s: not a user error with the input prefix: %v", c.name, err)
		}
	}
	// a 35-byte lang and non-ASCII names are still fine
	r := validInputReq()
	r.Sidecars.Subtitles[0].Lang = strings.Repeat("a", 35)
	r.Name = "Сёгун/Сёгун 01.mkv"
	if err := ValidateInput(r); err != nil {
		t.Errorf("35-byte lang / non-ASCII name refused: %v", err)
	}
	// 32 audio and 64 subtitle sidecars are still fine.
	r = validInputReq()
	r.Sidecars.Audio, r.Sidecars.Subtitles = audio(32), subs(64)
	if err := ValidateInput(r); err != nil {
		t.Errorf("32 audio + 64 subs refused: %v", err)
	}
}

// fakeServing answers Serving for tests; status/socket per addon.
type fakeServing struct {
	mu sync.Mutex
	st map[string]Status
	sk map[string]string
}

func newFakeServing() *fakeServing {
	return &fakeServing{st: map[string]Status{}, sk: map[string]string{}}
}

func (f *fakeServing) set(id string, st Status, sock string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st[id], f.sk[id] = st, sock
}

func (f *fakeServing) Serving(id string) (Status, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.st[id]
	if !ok {
		return StatusStopped, ""
	}
	return st, f.sk[id]
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *fakeClock               { return &fakeClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)} }
func newTestInputs(f *fakeServing, c *fakeClock) *Inputs {
	return &Inputs{Serving: f.Serving, Now: c.Now}
}

func TestInputs_AddGet(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/run/a.1.sock")
	s := newTestInputs(f, c)
	in, err := s.Add("a", validInputReq())
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(in.ID) {
		t.Fatalf("id %q is not 32 lowercase hex", in.ID)
	}
	if in.AddonID != "a" || in.Socket != "/run/a.1.sock" || !in.LastUsed.Equal(c.t) {
		t.Fatalf("unexpected input: %+v", in)
	}
	got, ok := s.Get(in.ID)
	if !ok || got.ID != in.ID || got.Req.Name != "Show/Show.S01E01.mkv" {
		t.Fatalf("Get = %+v, %v", got, ok)
	}
	if p, n, sz, ok := got.Part(0); !ok || p != "/media/1" || n != "Show/Show.S01E01.mkv" || sz != 1<<20 {
		t.Errorf("Part(0) = %q %q %d %v", p, n, sz, ok)
	}
	if p, n, sz, ok := got.Part(1); !ok || p != "/media/1/a/0" || n != "Show/Rus Sound/Studio/Show.S01E01.mka" || sz != 100 {
		t.Errorf("Part(1) = %q %q %d %v", p, n, sz, ok)
	}
	if p, _, sz, ok := got.Part(2); !ok || p != "/media/1/a/1" || sz != 200 {
		t.Errorf("Part(2) = %q %d %v", p, sz, ok)
	}
	if p, n, _, ok := got.Part(3); !ok || p != "/media/1/s/0" || n != "Show/Subs/Show.S01E01.srt" {
		t.Errorf("Part(3) = %q %q %v", p, n, ok)
	}
	for _, n := range []int{-1, 4, 9} {
		if _, _, _, ok := got.Part(n); ok {
			t.Errorf("Part(%d) ok", n)
		}
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("unknown id found")
	}
	// A bad request is refused by Add too.
	bad := validInputReq()
	bad.Path = "/hooks/files.plan"
	if _, err := s.Add("a", bad); err == nil {
		t.Error("Add accepted a hook path")
	}
}

func TestInputs_Starting(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusStarting, "")
	if _, err := newTestInputs(f, c).Add("a", validInputReq()); !errors.Is(err, ErrInputStarting) {
		t.Fatalf("err = %v, want ErrInputStarting", err)
	}
}

func TestInputs_NotRunning(t *testing.T) {
	f, c := newFakeServing(), newClock()
	s := newTestInputs(f, c)
	for _, st := range []Status{StatusStopped, StatusCrashed, StatusIncompatible} {
		f.set("a", st, "")
		if _, err := s.Add("a", validInputReq()); !errors.Is(err, ErrInputNotRunning) {
			t.Errorf("%s: err = %v, want ErrInputNotRunning", st, err)
		}
	}
	f.set("a", StatusRunning, "") // running without a socket
	if _, err := s.Add("a", validInputReq()); !errors.Is(err, ErrInputNotRunning) {
		t.Errorf("no socket: err = %v", err)
	}
	if _, err := s.Add("unknown", validInputReq()); !errors.Is(err, ErrInputNotRunning) {
		t.Errorf("unknown addon: err = %v", err)
	}
}

func TestInputs_Expire(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/s")
	s := newTestInputs(f, c)
	in, err := s.Add("a", validInputReq())
	if err != nil {
		t.Fatal(err)
	}
	c.add(InputTTL - time.Second)
	got, ok := s.Get(in.ID)
	if !ok || !got.LastUsed.Equal(c.t) {
		t.Fatalf("alive at TTL-1s: %v, lastUsed %v", ok, got.LastUsed)
	}
	c.add(InputTTL - time.Second) // touched above, so still alive
	if _, ok := s.Get(in.ID); !ok {
		t.Fatal("touched input expired early")
	}
	c.add(InputTTL + time.Second)
	if _, ok := s.Get(in.ID); ok {
		t.Fatal("alive after TTL+1s")
	}
	s.mu.Lock()
	n := len(s.m)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("expired entry kept (%d)", n)
	}
}

func TestInputs_SweepOnAdd(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/s")
	f.set("b", StatusRunning, "/t")
	s := newTestInputs(f, c)
	old, _ := s.Add("b", validInputReq())
	c.add(InputTTL + time.Second)
	if _, err := s.Add("a", validInputReq()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, kept := s.m[old.ID]
	s.mu.Unlock()
	if kept {
		t.Fatal("expired entry of another addon not swept on Add")
	}
}

func TestInputs_DieWithProcess(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/run/a.1.sock")
	s := newTestInputs(f, c)
	in, err := s.Add("a", validInputReq())
	if err != nil {
		t.Fatal(err)
	}
	f.set("a", StatusRunning, "/run/a.2.sock") // restarted: new process, new socket
	if _, ok := s.Get(in.ID); ok {
		t.Fatal("input survived a restart")
	}
	f.set("a", StatusRunning, "/run/a.1.sock") // even if the old answer came back
	if _, ok := s.Get(in.ID); ok {
		t.Fatal("dead entry was not deleted")
	}

	in2, err := s.Add("a", validInputReq())
	if err != nil {
		t.Fatal(err)
	}
	f.set("a", StatusStopped, "")
	if _, ok := s.Get(in2.ID); ok {
		t.Fatal("input alive while the addon is stopped")
	}
	f.set("a", StatusRunning, "/run/a.1.sock")
	if _, ok := s.Get(in2.ID); ok {
		t.Fatal("dead entry was not deleted on stop")
	}
}

func TestInputs_LRUPerAddon(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/s")
	f.set("b", StatusRunning, "/t")
	s := newTestInputs(f, c)
	bIn, err := s.Add("b", validInputReq())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < MaxInputsPerAddon; i++ {
		in, err := s.Add("a", validInputReq())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, in.ID)
		c.add(time.Second)
	}
	// The oldest one is used again, so the second is now the least recently used.
	if _, ok := s.Get(ids[0]); !ok {
		t.Fatal("first input gone before the limit")
	}
	last, err := s.Add("a", validInputReq())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(ids[1]); ok {
		t.Fatal("least recently used input was not evicted")
	}
	for _, id := range append([]string{ids[0], last.ID}, ids[2:]...) {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("input %s evicted wrongly", id)
		}
	}
	if _, ok := s.Get(bIn.ID); !ok {
		t.Fatal("another addon's input evicted")
	}
	s.mu.Lock()
	n := len(s.m)
	s.mu.Unlock()
	if n != MaxInputsPerAddon+1 {
		t.Fatalf("%d entries, want %d", n, MaxInputsPerAddon+1)
	}
}

func TestInputs_DropOwnOnly(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/s")
	f.set("b", StatusRunning, "/t")
	s := newTestInputs(f, c)
	in, _ := s.Add("a", validInputReq())
	if s.Drop("b", in.ID) {
		t.Fatal("another addon dropped the input")
	}
	if _, ok := s.Get(in.ID); !ok {
		t.Fatal("input gone after a foreign Drop")
	}
	if s.Drop("a", "nope") {
		t.Fatal("unknown id dropped")
	}
	if !s.Drop("a", in.ID) {
		t.Fatal("own Drop refused")
	}
	if _, ok := s.Get(in.ID); ok {
		t.Fatal("input alive after Drop")
	}
	if s.Drop("a", in.ID) {
		t.Fatal("second Drop succeeded")
	}
}

func TestInputs_ReqIsCopied(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/s")
	s := newTestInputs(f, c)
	req := validInputReq()
	in, _ := s.Add("a", req)
	req.Sidecars.Audio[0].Path = "/hooks/files.plan"
	req.Meta[2] = 'X'
	got, _ := s.Get(in.ID)
	if got.Req.Sidecars.Audio[0].Path != "/media/1/a/0" || string(got.Req.Meta) != `{"note":"x"}` {
		t.Fatal("stored request aliases the caller's")
	}
}

func TestInputs_GetIsDeepCopy(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/s")
	s := newTestInputs(f, c)
	in, _ := s.Add("a", validInputReq())
	got, _ := s.Get(in.ID)
	got.Req.Sidecars.Audio[0].Path = "/hooks/files.plan"
	got.Req.Sidecars.Subtitles[0].Name = "x.srt"
	got.Req.Meta[2] = 'X'
	got.Req.Title.TMDBID = 9
	again, _ := s.Get(in.ID)
	if again.Req.Sidecars.Audio[0].Path != "/media/1/a/0" || again.Req.Sidecars.Subtitles[0].Name != "Show/Subs/Show.S01E01.srt" ||
		string(again.Req.Meta) != `{"note":"x"}` || again.Req.Title.TMDBID != 42 {
		t.Fatalf("Get exposes the registry: %+v", again.Req)
	}
	// Add's answer is a copy too
	in.Req.Sidecars.Audio[0].Path = "/hooks/x"
	if again, _ = s.Get(in.ID); again.Req.Sidecars.Audio[0].Path != "/media/1/a/0" {
		t.Fatal("Add's answer aliases the registry")
	}
}

func TestInputs_SweepEarlierProcessOnAdd(t *testing.T) {
	f, c := newFakeServing(), newClock()
	f.set("a", StatusRunning, "/run/a.1.sock")
	f.set("b", StatusRunning, "/run/b.1.sock")
	s := newTestInputs(f, c)
	old, _ := s.Add("a", validInputReq())
	other, _ := s.Add("b", validInputReq())
	f.set("a", StatusRunning, "/run/a.2.sock") // restarted
	f.set("b", StatusRunning, "/run/b.2.sock") // b restarted too, but Add("a") sweeps only a's
	if _, err := s.Add("a", validInputReq()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, oldKept := s.m[old.ID]
	_, otherKept := s.m[other.ID]
	n := len(s.m)
	s.mu.Unlock()
	if oldKept {
		t.Fatal("a's input of the earlier process survived Add")
	}
	if !otherKept || n != 2 {
		t.Fatalf("Add(a) swept another addon's live-by-TTL entry (kept %v, %d entries)", otherKept, n)
	}
}
