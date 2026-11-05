package vofit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

type wfix struct {
	t     *testing.T
	d     *sql.DB
	w     *Worker
	dir   string
	fits  []string // tracks measured, by name
	busy  bool
	fail  error
	state map[string]Result // by track name (default: ok, +200 ms)
}

func newWFix(t *testing.T) *wfix {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	fx := &wfix{t: t, d: d, dir: t.TempDir(), state: map[string]Result{}}
	fx.w = &Worker{DB: d, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Busy: func() bool { return fx.busy },
		Fit: func(_ context.Context, in Input) (Result, error) {
			name := filepath.Base(in.Track)
			fx.fits = append(fx.fits, fmt.Sprintf("%s/%d", name, in.OrigIndex))
			if fx.fail != nil {
				return Result{}, fx.fail
			}
			if r, ok := fx.state[name]; ok {
				return r, nil
			}
			return Result{State: StateOK, OffsetMs: 200, Tempo: 1}, nil
		}}
	return fx
}

const probeRuEn = `{"durationMs":3600000,"video":{"codec":"h264"},"audio":[{"index":1,"codec":"ac3","lang":"rus","title":"Дубляж","default":true},{"index":2,"codec":"ac3","lang":"eng"}]}`

// video adds an episode file on disk and returns its id and path.
func (fx *wfix) video(name string) (int64, string) {
	fx.t.Helper()
	p := filepath.Join(fx.dir, name+".mkv")
	if err := os.WriteFile(p, []byte("v"), 0o644); err != nil {
		fx.t.Fatal(err)
	}
	r, err := fx.d.Exec(`INSERT INTO items(kind, title) VALUES('movie',?)`, name)
	if err != nil {
		fx.t.Fatal(err)
	}
	item, _ := r.LastInsertId()
	r, err = fx.d.Exec(`INSERT INTO files(item_id, path, size, mtime, duration_ms, probe_json) VALUES(?,?,1,1,3600000,?)`, item, p, probeRuEn)
	if err != nil {
		fx.t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id, p
}

func (fx *wfix) track(video, tag string) string {
	fx.t.Helper()
	p := strings.TrimSuffix(video, ".mkv") + tag
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		fx.t.Fatal(err)
	}
	return p
}

func (fx *wfix) pass() {
	fx.t.Helper()
	if err := fx.w.Pass(context.Background()); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *wfix) fit(path string) (Stored, bool) {
	fx.t.Helper()
	f, ok, err := Lookup(context.Background(), fx.d, path)
	if err != nil {
		fx.t.Fatal(err)
	}
	return f, ok
}

// queue puts a stage-B track beside the video (the file and its pending
// row, the title's original language lang).
func (fx *wfix) queue(id int64, video, tag, lang string) string {
	fx.t.Helper()
	p := fx.track(video, tag)
	if err := Queue(context.Background(), fx.d, p, id, lang); err != nil {
		fx.t.Fatal(err)
	}
	return p
}

// A track placed by «Найти озвучку…» is pending (not offered) until its
// fit; it is fitted even while something plays.
func TestWorkerFitsPending(t *testing.T) {
	fx := newWFix(t)
	id, v := fx.video("Film")
	p := strings.TrimSuffix(v, ".mkv") + ".ru.LostFilm.ac3"
	if err := Queue(context.Background(), fx.d, p, id, "en"); err != nil {
		t.Fatal(err)
	}
	if f, ok := fx.fit(p); !ok || f.Offered() {
		t.Fatalf("queued %+v %v", f, ok)
	}
	fx.busy = true
	fx.pass() // the link is not made yet: nothing to measure, soon again
	if len(fx.fits) != 0 || !fx.w.again {
		t.Fatalf("fits %v again %v", fx.fits, fx.w.again)
	}
	fx.track(v, ".ru.LostFilm.ac3")
	fx.pass()
	f, ok := fx.fit(p)
	if !ok || f.State != StateOK || f.OffsetMs != 200 || !f.Offered() || !f.Applies() {
		t.Fatalf("fit %+v %v", f, ok)
	}
	if fmt.Sprint(fx.fits) != "[Film.ru.LostFilm.ac3/2]" {
		t.Fatalf("fits %v", fx.fits)
	}
	// the file's rows go with it
	if _, err := fx.d.Exec(`DELETE FROM files WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = fx.d.QueryRow(`SELECT COUNT(*) FROM external_audio_fit`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows left", n)
	}
}

// A pass that fitted a track says so (voclean then cleans it).
func TestWorkerFittedHook(t *testing.T) {
	fx := newWFix(t)
	kicks := 0
	fx.w.Fitted = func() { kicks++ }
	id, v := fx.video("Film")
	fx.pass()
	if kicks != 0 {
		t.Fatal("nothing fitted, yet kicked")
	}
	fx.queue(id, v, ".ru.LostFilm.ac3", "en")
	fx.pass()
	if kicks != 1 {
		t.Fatalf("kicks %d", kicks)
	}
}

// Only the tracks «Найти озвучку…» placed are fitted: a release's track
// beside the video (stage A) is never measured, so never hidden.
func TestWorkerFitsOnlyStageB(t *testing.T) {
	fx := newWFix(t)
	id, v := fx.video("Film")
	a := fx.track(v, ".ru.Release.ac3")
	fx.pass()
	if len(fx.fits) != 0 {
		t.Fatalf("measured a stage-A track: %v", fx.fits)
	}
	if _, ok := fx.fit(a); ok {
		t.Fatal("a stage-A track got a row")
	}
	fx.queue(id, v, ".ru.LostFilm.ac3", "en")
	fx.pass()
	if fmt.Sprint(fx.fits) != "[Film.ru.LostFilm.ac3/2]" {
		t.Fatalf("fits %v", fx.fits)
	}
}

// The fit measures against a track in the title's original language
// (TMDB's; unknown: one that is not Russian), never a dub: a file without
// one leaves the track unmeasured.
func TestWorkerOriginalLanguageOnly(t *testing.T) {
	fx := newWFix(t)
	id, v := fx.video("Film") // tracks: 1 Russian (default), 2 English
	en := fx.queue(id, v, ".ru.A.ac3", "en")
	unknown := fx.queue(id, v, ".ru.B.ac3", "")
	ja := fx.queue(id, v, ".ru.C.ac3", "ja")
	fx.pass()
	if fmt.Sprint(fx.fits) != "[Film.ru.A.ac3/2 Film.ru.B.ac3/2]" {
		t.Fatalf("fits %v", fx.fits)
	}
	for _, p := range []string{en, unknown} {
		if f, _ := fx.fit(p); f.State != StateOK {
			t.Fatalf("%s: %+v", p, f)
		}
	}
	var st, why string
	if err := fx.d.QueryRow(`SELECT state, json_extract(measure_json,'$.why') FROM external_audio_fit WHERE path=?`, ja).Scan(&st, &why); err != nil {
		t.Fatal(err)
	}
	if st != StateUnmeasured || !strings.Contains(why, "original language") {
		t.Fatalf("ja: %s %q", st, why)
	}
	if f, _ := fx.fit(ja); f.Offered() {
		t.Fatalf("unmeasured offered: %+v", f)
	}
}

// A fit made of another video or track than now counts as none: the
// track is not offered (Lookup and ForFile say so) and is fitted again at
// once, even while something plays.
func TestWorkerRefitsStale(t *testing.T) {
	fx := newWFix(t)
	id, v := fx.video("Film")
	p := fx.queue(id, v, ".ru.LostFilm.ac3", "en")
	fx.pass()
	if f, _ := fx.fit(p); !f.Offered() {
		t.Fatalf("fitted %+v", f)
	}
	if _, err := fx.d.Exec(`UPDATE files SET size=2 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if f, ok := fx.fit(p); !ok || f.Offered() || f.Current {
		t.Fatalf("stale %+v %v", f, ok)
	}
	if m, _ := ForFile(context.Background(), fx.d, id); m[p].Offered() {
		t.Fatalf("stale listed %+v", m)
	}
	fx.busy, fx.fits = true, nil
	fx.pass()
	if len(fx.fits) != 1 {
		t.Fatalf("not refitted while busy: %v", fx.fits)
	}
	if f, _ := fx.fit(p); !f.Offered() || !f.Current {
		t.Fatalf("refitted %+v", f)
	}
}

// Without the original language for sure (none stored, none found when
// the fit runs) the fallback track may be a dub: a failed match is
// unmeasured, never a misfit. A language stored empty is looked up again.
func TestWorkerUnknownLanguage(t *testing.T) {
	fx := newWFix(t)
	id, v := fx.video("Film")
	p := fx.queue(id, v, ".ru.LostFilm.ac3", "")
	fx.state["Film.ru.LostFilm.ac3"] = Result{State: StateMisfit, Tempo: 1, Why: "another version"}
	fx.pass()
	if f, _ := fx.fit(p); f.State != StateUnmeasured {
		t.Fatalf("fallback misfit: %+v", f)
	}

	fx2 := newWFix(t)
	asked := []int64{}
	fx2.w.OrigLang = func(_ context.Context, fileID int64) string { asked = append(asked, fileID); return "en" }
	id2, v2 := fx2.video("Film")
	p2 := fx2.queue(id2, v2, ".ru.LostFilm.ac3", "")
	fx2.state["Film.ru.LostFilm.ac3"] = Result{State: StateMisfit, Tempo: 1, Why: "another version"}
	fx2.pass()
	var lang string
	_ = fx2.d.QueryRow(`SELECT orig_lang FROM external_audio_fit WHERE path=?`, p2).Scan(&lang)
	if f, _ := fx2.fit(p2); f.State != StateMisfit || lang != "en" || len(asked) != 1 || asked[0] != id2 {
		t.Fatalf("looked up: %+v lang %q asked %v", f, lang, asked)
	}
}

// A failed measurement is retried after an hour.
func TestWorkerRetriesFailure(t *testing.T) {
	fx := newWFix(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	fx.w.Now = func() time.Time { return now }
	id, v := fx.video("Film")
	a := fx.queue(id, v, ".ru.LostFilm.ac3", "en")
	fx.fail = errors.New("ffmpeg: broken")
	fx.pass()
	var st string
	var fails int
	_ = fx.d.QueryRow(`SELECT state, fails FROM external_audio_fit WHERE path=?`, a).Scan(&st, &fails)
	if st != StateFailed || fails != 1 {
		t.Fatalf("%s %d", st, fails)
	}
	if f, _ := fx.fit(a); f.Offered() || f.GaveUp {
		t.Fatalf("failed, retried later: %+v", f)
	}
	fx.fail, fx.fits = nil, nil
	fx.pass()
	if len(fx.fits) != 0 {
		t.Fatal("retried at once")
	}
	now = now.Add(2 * time.Hour)
	fx.pass()
	if f, ok := fx.fit(a); !ok || f.State != StateOK || len(fx.fits) != 1 {
		t.Fatalf("%+v %v", f, fx.fits)
	}
	// failing every time: given up after maxFails, never offered
	b := fx.queue(id, v, ".ru.Other.ac3", "en")
	fx.fail = errors.New("ffmpeg: broken")
	for range maxFails {
		now = now.Add(retryMax + time.Hour)
		fx.pass()
	}
	if f, _ := fx.fit(b); f.State != StateFailed || !f.GaveUp || f.Offered() {
		t.Fatalf("given up: %+v", f)
	}
}
