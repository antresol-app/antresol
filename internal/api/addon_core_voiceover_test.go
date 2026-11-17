package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/vofit"
	"github.com/go-chi/chi/v5"
)

// coreVoiceover is the addon env with the fit worker w: the addon "vo"
// has the voiceover permission, "hello" does not.
type coreVoiceover struct {
	*addonEnv
	deps Deps
}

func newCoreVoiceover(t *testing.T, w *vofit.Worker) *coreVoiceover {
	t.Helper()
	c := &coreVoiceover{}
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		if w != nil {
			w.DB = d.DB
			d.VoiceOverFit = w
		}
		c.deps = *d
	})
	c.fake.mu.Lock()
	in := c.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "vo", "vo", []string{"voiceover"}
	c.fake.installed["vo"], c.fake.tokens["tok-vo"], c.fake.status["vo"] = in, "vo", addons.StatusRunning
	c.fake.mu.Unlock()
	return c
}

func (c *coreVoiceover) call(tok, method, path, body string) *httptest.ResponseRecorder {
	return c.do(method, "/api/addon-core/v1"+path, body, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
}

// file adds a library file with this id at path (an item of its own).
func addFitFile(t *testing.T, d *sql.DB, id int64, path, probe string, durationMs int64) {
	t.Helper()
	r, err := d.Exec(`INSERT INTO items(kind, title) VALUES('movie', ?)`, filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	item, _ := r.LastInsertId()
	if _, err := d.Exec(`INSERT INTO files(id, item_id, path, size, mtime, duration_ms, probe_json) VALUES(?,?,?,1,1,?,?)`,
		id, item, path, durationMs, probe); err != nil {
		t.Fatal(err)
	}
}

func fitBody(path string, fileID int64, lang string) string {
	b, _ := json.Marshal(map[string]any{"path": path, "fileId": fileID, "origLang": lang})
	return string(b)
}

// fitRouter is the core's own voiceover routes with a recording kick in
// the worker's place.
func (c *coreVoiceover) fitRouter(kicks *int, mu *sync.Mutex) chi.Router {
	a := newAddonsAPI(c.deps)
	a.fitKick = func() { mu.Lock(); *kicks++; mu.Unlock() }
	r := chi.NewRouter()
	r.Route("/api/addon-core/v1", func(r chi.Router) {
		r.Use(a.requireToken)
		mountAddonCoreVoiceover(r, a)
	})
	return r
}

func serveAs(h chi.Router, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/addon-core/v1"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestVoiceoverFit_Queue(t *testing.T) {
	c := newCoreVoiceover(t, &vofit.Worker{})
	addFitFile(t, c.db, 7, "/m/Show/S01E01.mkv", "", 0)
	var mu sync.Mutex
	kicks := 0
	h := c.fitRouter(&kicks, &mu)
	const track = "/m/Show/S01E01.rus.mka" // not a files row: placed next
	w := serveAs(h, "tok-vo", "POST", "/voiceover/fit", fitBody(track, 7, "JA"))
	if w.Code != 202 {
		t.Fatalf("queue: %d %s", w.Code, w.Body)
	}
	f, ok, err := vofit.Lookup(context.Background(), c.db, track)
	if err != nil || !ok || f.FileID != 7 || f.State != vofit.StatePending {
		t.Fatalf("lookup %+v %v %v", f, ok, err)
	}
	var lang string
	if err := c.db.QueryRow(`SELECT orig_lang FROM external_audio_fit WHERE path=?`, track).Scan(&lang); err != nil || lang != "ja" {
		t.Fatalf("orig_lang %q %v", lang, err)
	}
	mu.Lock()
	if kicks != 1 {
		t.Fatalf("kicks %d", kicks)
	}
	mu.Unlock()

	// through the real router: the worker's own kick; without the
	// permission 403 first
	if w := c.call("tok-vo", "POST", "/voiceover/fit", fitBody(track, 7, "")); w.Code != 202 {
		t.Fatalf("router: %d %s", w.Code, w.Body)
	}
	if w := c.call("tok-hello", "POST", "/voiceover/fit", fitBody(track, 7, "")); w.Code != 403 {
		t.Fatalf("no permission: %d %s", w.Code, w.Body)
	}
	if w := c.call("tok-hello", "GET", "/voiceover/fit?path="+qe(track), ""); w.Code != 403 {
		t.Fatalf("no permission get: %d %s", w.Code, w.Body)
	}
}

// A server without the fit worker (no ffmpeg) answers 503.
func TestVoiceoverFit_NoWorker(t *testing.T) {
	c := newCoreVoiceover(t, nil)
	addFitFile(t, c.db, 7, "/m/Show/S01E01.mkv", "", 0)
	if w := c.call("tok-vo", "POST", "/voiceover/fit", fitBody("/m/Show/S01E01.rus.mka", 7, "")); w.Code != 503 {
		t.Fatalf("queue: %d %s", w.Code, w.Body)
	}
	if w := c.call("tok-vo", "GET", "/voiceover/fit?path="+qe("/m/Show/S01E01.rus.mka"), ""); w.Code != 503 {
		t.Fatalf("state: %d %s", w.Code, w.Body)
	}
}

// Only an audio track beside a library file is queued.
func TestVoiceoverFit_Refused(t *testing.T) {
	c := newCoreVoiceover(t, &vofit.Worker{})
	addFitFile(t, c.db, 7, "/m/Show/S01E01.mkv", "", 0)
	if _, err := c.db.Exec(`INSERT INTO files(id, item_id, path, size, mtime, missing_since) SELECT 8, item_id, '/m/Gone/x.mkv', 1, 1, '2026-01-01T00:00:00Z' FROM files WHERE id=7`); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	kicks := 0
	h := c.fitRouter(&kicks, &mu)
	for _, b := range []string{
		fitBody("/m/Show/S01E01.rus.mka", 99, ""),        // an unknown file
		fitBody("/m/Gone/x.rus.mka", 8, ""),              // a file missing from disk
		fitBody("/m/Other/S01E01.rus.mka", 7, ""),        // another folder
		fitBody("/m/Show/Sub/S01E01.rus.mka", 7, ""),     // below it
		fitBody("/m/Show/../Show/S01E01.rus.mka", 7, ""), // ..
		fitBody("/m/Show/../Other/S01E01.rus.mka", 7, ""),
		fitBody("/m/Show/S01E01.txt", 7, ""),      // not audio
		fitBody("/m/Show/S01E01.mkv", 7, ""),      // the video itself
		fitBody("S01E01.rus.mka", 7, ""),          // relative
		fitBody("/m/Show//S01E01.rus.mka", 7, ""), // not clean
		fitBody("", 7, ""),
		`{"path":"/m/Show/S01E01.rus.mka"}`, // no fileId
	} {
		w := serveAs(h, "tok-vo", "POST", "/voiceover/fit", b)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "Файл не найден в медиатеке") {
			t.Errorf("%s: %d %s", b, w.Code, w.Body)
		}
	}
	for _, b := range []string{`not json`, fitBody("/m/Show/S01E01.rus.mka", 7, "japanese!")} {
		if w := serveAs(h, "tok-vo", "POST", "/voiceover/fit", b); w.Code != 400 {
			t.Errorf("%s: %d %s", b, w.Code, w.Body)
		}
	}
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM external_audio_fit`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("queued %d %v", n, err)
	}
	if kicks != 0 {
		t.Fatalf("kicks %d", kicks)
	}
}

func TestVoiceoverFit_State(t *testing.T) {
	c := newCoreVoiceover(t, &vofit.Worker{})
	dir := t.TempDir()
	video := filepath.Join(dir, "S01E01.mkv")
	addFitFile(t, c.db, 7, video, "", 0)
	ctx := context.Background()
	// track: a placed track, its fit stored as state (a measured one made
	// of the files as they are now unless stale)
	track := func(name, state string, stale bool, retry any) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := vofit.Queue(ctx, c.db, p, 7, "en"); err != nil {
			t.Fatal(err)
		}
		if state == vofit.StatePending {
			return p
		}
		st, _ := os.Stat(p)
		size := st.Size()
		if stale {
			size++
		}
		if _, err := c.db.Exec(`UPDATE external_audio_fit SET state=?, offset_ms=-1500, tempo=0.959, track_size=?, track_mtime=?, file_size=1, file_mtime=1, retry_at=? WHERE path=?`,
			state, size, st.ModTime().UnixNano(), retry, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, x := range []struct {
		name, state string
		stale       bool
		retry       any
		want        addonkit.FitState
	}{
		{"a.rus.mka", vofit.StatePending, false, nil, addonkit.FitState{State: "pending"}},
		{"b.rus.mka", vofit.StateOK, false, nil, addonkit.FitState{State: "ok", OffsetMs: -1500, Tempo: 0.959}},
		{"c.rus.mka", vofit.StateOK, true, nil, addonkit.FitState{State: "pending"}}, // stale: fitted again
		{"d.rus.mka", vofit.StateMisfit, false, nil, addonkit.FitState{State: "misfit"}},
		{"e.rus.mka", vofit.StateUnmeasured, false, nil, addonkit.FitState{State: "unmeasured"}},
		{"f.rus.mka", vofit.StateFailed, false, "2099-01-01T00:00:00Z", addonkit.FitState{State: "pending"}}, // retried
		{"g.rus.mka", vofit.StateFailed, false, nil, addonkit.FitState{State: "unmeasured"}},                 // given up
	} {
		p := track(x.name, x.state, x.stale, x.retry)
		w := c.call("tok-vo", "GET", "/voiceover/fit?path="+qe(p), "")
		var got addonkit.FitState
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got != x.want {
			t.Errorf("%s: %d %s, want %+v", x.name, w.Code, w.Body, x.want)
		}
	}
	for _, p := range []string{filepath.Join(dir, "none.rus.mka"), video} {
		if w := c.call("tok-vo", "GET", "/voiceover/fit?path="+qe(p), ""); w.Code != 404 {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := c.call("tok-vo", "GET", "/voiceover/fit", ""); w.Code != 400 {
		t.Errorf("no path: %d %s", w.Code, w.Body)
	}
}

// voiceover.origLang: the first addon answering a language gives it (a
// row queued without one is fitted against that track); an addon down
// gives "" and the fit runs without it.
func TestOrigLang_Hook(t *testing.T) {
	e := newReqEnv(t, nil, nil)
	e.addon("vo", "VO", []string{"voiceover"}, "voiceover.origLang")
	e.answer("vo", "voiceover.origLang", 200, `{"lang":"JA"}`)
	e.addon("zz", "ZZ", []string{"voiceover"}, "voiceover.origLang")
	e.answer("zz", "voiceover.origLang", 200, `{"lang":"ko"}`)
	orig := AddonOrigLang(e.hooks)
	ctx := context.Background()
	if l := orig(ctx, 7); l != "ja" {
		t.Fatalf("lang %q", l)
	}
	calls := e.hookCalls("voiceover.origLang")
	if len(calls) != 2 || calls[0].body != `{"fileId":7}` || calls[0].user != "" {
		t.Fatalf("calls %+v", calls)
	}
	if AddonOrigLang(nil)(ctx, 7) != "" {
		t.Fatal("no hooks: a language")
	}

	// the worker asks for a row queued without a language
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	dir := t.TempDir()
	video := filepath.Join(dir, "Film.mkv")
	addFitFile(t, d, 1, video, `{"Audio":[{"Index":1,"Lang":"eng"},{"Index":2,"Lang":"jpn"}]}`, 3600000)
	var used []int
	w := &vofit.Worker{DB: d, OrigLang: orig, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Fit: func(_ context.Context, in vofit.Input) (vofit.Result, error) {
			used = append(used, in.OrigIndex)
			return vofit.Result{State: vofit.StateOK, Tempo: 1}, nil
		}}
	queue := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := vofit.Queue(ctx, d, p, 1, ""); err != nil {
			t.Fatal(err)
		}
		return p
	}
	langOf := func(p string) string {
		var l string
		d.QueryRow(`SELECT orig_lang FROM external_audio_fit WHERE path=?`, p).Scan(&l)
		return l
	}
	p := queue("Film.rus.mka")
	if err := w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(used) != 1 || used[0] != 2 || langOf(p) != "ja" {
		t.Fatalf("with the hook: %v %q", used, langOf(p))
	}

	// down (and the other one too): "" — fitted against the English track
	e.setStatus("vo", addons.StatusCrashed)
	e.setStatus("zz", addons.StatusCrashed)
	if l := orig(ctx, 7); l != "" {
		t.Fatalf("down: %q", l)
	}
	p = queue("Film.rus2.mka")
	used = nil
	if err := w.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(used) != 1 || used[0] != 1 || langOf(p) != "" {
		t.Fatalf("down: %v %q", used, langOf(p))
	}
	f, ok, err := vofit.Lookup(ctx, d, p)
	if err != nil || !ok || f.State != vofit.StateOK {
		t.Fatalf("down: not fitted %+v %v %v", f, ok, err)
	}
}

// E25: the real worker fits, with ffmpeg, a track queued through POST
// /voiceover/fit: the video's audio delayed 1.5 s is fitted (the players
// shift it back by 1.5 s, OffsetMs ≈ -1500 within the worker's 20 ms),
// the same audio 20 % shorter (another cut) is a misfit.
func TestFitWorker_RealFit(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found: the real fit is not checked")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command(ff, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
		}
	}
	// a 90 s sine gated at random (a soundtrack's envelope: never
	// periodic), the video's English track; the voice-overs made of it
	wav := filepath.Join(dir, "orig.wav")
	run("-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=90",
		"-af", "volume='if(lt(random(0),0.3),0,random(1))':eval=frame", "-ac", "1", wav)
	video := filepath.Join(dir, "Film.mkv")
	run("-f", "lavfi", "-i", "testsrc=size=160x120:rate=5:duration=90", "-i", wav,
		"-map", "0:v", "-map", "1:a", "-c:v", "mpeg4", "-c:a", "flac", "-metadata:s:a:0", "language=eng", video)
	delayed := filepath.Join(dir, "Film.rus.mka")
	run("-i", wav, "-af", "adelay=1500:all=1", "-c:a", "flac", delayed)
	shorter := filepath.Join(dir, "Film.short.rus.mka")
	run("-i", wav, "-af", "atempo=1.25", "-c:a", "flac", shorter)

	w := &vofit.Worker{FFmpeg: ff, StartDelay: time.Hour, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c := newCoreVoiceover(t, w)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	addFitFile(t, c.db, 1, video, `{"Audio":[{"Index":1,"Codec":"flac","Lang":"eng"}]}`, 90000)

	for _, p := range []string{delayed, shorter} {
		if w := c.call("tok-vo", "POST", "/voiceover/fit", fitBody(p, 1, "en")); w.Code != 202 {
			t.Fatalf("queue %s: %d %s", p, w.Code, w.Body)
		}
	}
	state := func(p string) addonkit.FitState {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for {
			w := c.call("tok-vo", "GET", "/voiceover/fit?path="+qe(p), "")
			var f addonkit.FitState
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &f) != nil {
				t.Fatalf("%s: %d %s", p, w.Code, w.Body)
			}
			if f.State != addonkit.FitPending {
				return f
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: still pending", p)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	f := state(delayed)
	t.Logf("delayed 1.5 s: %+v", f)
	if f.State != addonkit.FitOK || f.Tempo != 1 || math.Abs(f.OffsetMs-(-1500)) > 20 {
		t.Fatalf("delayed: %+v", f)
	}
	f = state(shorter)
	t.Logf("20 %% shorter: %+v", f)
	if f.State != addonkit.FitMisfit {
		t.Fatalf("shorter: %+v", f)
	}
}
