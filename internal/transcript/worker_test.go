package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/bgtask"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

const minute = 60_000

// fakeSTT is an OpenAI-compatible transcription server: the uploaded
// «audio» of the tests names its piece (file@start+length), and the answer
// says so in two phrases.
type fakeSTT struct {
	*httptest.Server
	mu sync.Mutex
	// asked are the pieces transcribed, in order
	asked []string
	// reply overrides an answer (status 0: the usual one)
	reply   func(piece string) (status int, body string, hdr []string)
	untimed bool
}

func newFakeSTT(t *testing.T) *fakeSTT {
	f := &fakeSTT{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		b, _ := io.ReadAll(file)
		piece := strings.TrimRight(string(b), " ")
		f.mu.Lock()
		f.asked = append(f.asked, piece)
		reply, untimed := f.reply, f.untimed
		f.mu.Unlock()
		if reply != nil {
			if status, body, hdr := reply(piece); status != 0 {
				for i := 0; i+1 < len(hdr); i += 2 {
					w.Header().Set(hdr[i], hdr[i+1])
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
				return
			}
		}
		if untimed {
			_ = json.NewEncoder(w).Encode(map[string]any{"text": piece})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"language": "russian", "segments": []any{
			map[string]any{"start": 1, "end": 5, "text": piece + " a"},
			map[string]any{"start": 20, "end": 25, "text": piece + " b"},
		}})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSTT) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.asked
	f.asked = nil
	return out
}

func (f *fakeSTT) set(reply func(string) (int, string, []string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply = reply
}

type rig struct {
	t     *testing.T
	db    *sql.DB
	repo  *library.Repo
	stt   *fakeSTT
	dir   string
	now   time.Time
	busy  func() bool
	llm   Asker
	off   bool
	added int
}

func newRig(t *testing.T) *rig {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return &rig{t: t, db: d, repo: library.NewRepo(d), stt: newFakeSTT(t), dir: t.TempDir(),
		now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
}

// worker is a worker of the rig (a new one: as after a restart).
func (r *rig) worker() *Worker {
	return &Worker{DB: r.db,
		STT: func(context.Context) (Config, bool) {
			return Config{Provider: ProviderCustom, URL: r.stt.URL + "/v1", Model: "w"}, !r.off
		},
		LLM:  func(context.Context) Asker { return r.llm },
		Busy: func() bool { return r.busy != nil && r.busy() },
		Now:  func() time.Time { return r.now },
		Cut: func(_ context.Context, path string, startMs, durMs int64) ([]byte, error) {
			s := fmt.Sprintf("%s@%d+%d", strings.TrimSuffix(filepath.Base(path), ".mp3"), startMs/1000, durMs/1000)
			return []byte(s + strings.Repeat(" ", 2048)), nil
		},
	}
}

func (r *rig) pass(w *Worker) {
	r.t.Helper()
	if err := w.Pass(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

// book adds an audiobook of files with the given durations; its chapter
// titles are the files' names unless titles are given. Later books are
// newer.
func (r *rig) book(name string, titles []string, durations ...int64) (int64, []int64) {
	r.t.Helper()
	ctx := context.Background()
	dir := filepath.Join(r.dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	var files []library.FileInput
	var chs []library.ChapterInput
	for i := range durations {
		p := filepath.Join(dir, fmt.Sprintf("%s%d.mp3", name, i+1))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			r.t.Fatal(err)
		}
		files = append(files, library.FileInput{Path: p, Size: 1, Mtime: 1})
		title := fmt.Sprintf("%02d_%s", i+1, name)
		if titles != nil {
			title = titles[i]
		}
		chs = append(chs, library.ChapterInput{Title: title, FileIndex: i})
	}
	id, err := r.repo.UpsertAudiobook(ctx, library.AudiobookInput{Dir: dir, Files: files, Title: "Книга " + name, Chapters: chs})
	if err != nil {
		r.t.Fatal(err)
	}
	r.added++
	r.exec(`UPDATE items SET added_at=? WHERE id=?`, time.Date(2026, 1, r.added, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), id)
	var ids []int64
	for i, f := range files {
		var fid int64
		if err := r.db.QueryRow(`SELECT id FROM files WHERE path=?`, f.Path).Scan(&fid); err != nil {
			r.t.Fatal(err)
		}
		r.exec(`UPDATE files SET duration_ms=? WHERE id=?`, durations[i], fid)
		ids = append(ids, fid)
	}
	return id, ids
}

func (r *rig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.db.Exec(q, args...); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) stored(id int64) (Stored, bool) {
	r.t.Helper()
	s, ok, err := Get(context.Background(), r.db, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return s, ok
}

func (r *rig) segments(id int64) []FileSegment {
	r.t.Helper()
	s, err := Segments(context.Background(), r.db, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

func wantAsked(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != 0 || len(want) != 0 {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("asked %q, want %q", got, want)
		}
	}
}

// A book is cut into pieces of about ten minutes, each file on its own;
// the transcript is stored by file and is ready when every file is done.
func TestWorkerTranscribesABook(t *testing.T) {
	r := newRig(t)
	id, files := r.book("a", nil, 25*minute, 5*minute, 10*minute+20_000)
	w := r.worker()
	r.pass(w)
	// a tail of a few seconds joins the piece before it
	wantAsked(t, r.stt.take(), "a1@0+600", "a1@600+600", "a1@1200+300", "a2@0+300", "a3@0+620")
	s, ok := r.stored(id)
	if !ok || s.State != StateReady || s.Language != "ru" {
		t.Fatalf("%+v %v", s, ok)
	}
	var sig []File
	for i, f := range files {
		sig = append(sig, File{ID: f, Size: 1, Mtime: 1, DurationMs: []int64{25 * minute, 5 * minute, 10*minute + 20_000}[i]})
	}
	if s.Signature != Signature(sig) {
		t.Errorf("signature %q, want %q", s.Signature, Signature(sig))
	}
	segs := r.segments(id)
	if len(segs) != 10 || segs[2] != (FileSegment{files[0], Segment{601_000, 605_000, "a1@600+600 a"}}) {
		t.Fatalf("%d %+v", len(segs), segs)
	}
	abs := Absolute(segs, map[int64]int64{files[0]: 0, files[1]: 25 * minute, files[2]: 30 * minute})
	if abs[6] != (Segment{25*minute + 1000, 25*minute + 5000, "a2@0+300 a"}) {
		t.Errorf("absolute %+v", abs[6])
	}
	if p := w.Progress.Snapshot(); p.Done != 1 || p.Total != 1 || p.Running || p.State != "" || p.Error != "" {
		t.Errorf("progress %+v", p)
	}
	// nothing to do: nothing asked, as after a restart
	r.pass(w)
	r.pass(r.worker())
	wantAsked(t, r.stt.take())
}

// A restart (or any stop) takes up where the finished pieces end.
func TestWorkerResumes(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, 25*minute)
	r.stt.set(func(p string) (int, string, []string) {
		if p == "a1@1200+300" {
			return 500, "boom", nil
		}
		return 0, "", nil
	})
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take(), "a1@0+600", "a1@600+600", "a1@1200+300")
	if s, _ := r.stored(id); s.State != StatePending {
		t.Fatalf("an outage is not the book's failure: %+v", s)
	}
	if p := w.Progress.Snapshot(); p.State != bgtask.StateWaiting || !strings.Contains(p.Error, "500") {
		t.Fatalf("progress %+v", p)
	}
	r.stt.set(nil)
	r.pass(w) // held for a while
	wantAsked(t, r.stt.take())
	r.pass(r.worker())
	wantAsked(t, r.stt.take(), "a1@1200+300")
	if s, _ := r.stored(id); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
}

// 429: the worker waits as long as it is told, for every book, and counts
// no failure.
func TestWorkerRateLimit(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, 25*minute)
	r.book("b", nil, 5*minute) // newer: first
	limited := true
	r.stt.set(func(p string) (int, string, []string) {
		if limited && p == "a1@600+600" {
			return 429, `{"error":{"message":"audio seconds per hour"}}`, []string{"Retry-After", "600"}
		}
		return 0, "", nil
	})
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take(), "b1@0+300", "a1@0+600", "a1@600+600")
	if p := w.Progress.Snapshot(); p.State != bgtask.StateWaiting || p.Done != 1 || p.Total != 2 || !strings.Contains(p.Error, "429") {
		t.Fatalf("progress %+v", p)
	}
	if d := w.wait(); d < 10*time.Minute || d > 11*time.Minute {
		t.Errorf("next pass in %v", d)
	}
	r.now = r.now.Add(9 * time.Minute)
	r.pass(w)
	wantAsked(t, r.stt.take())
	r.now = r.now.Add(2 * time.Minute)
	limited = false
	r.pass(w)
	wantAsked(t, r.stt.take(), "a1@600+600", "a1@1200+300")
	var fails int
	if err := r.db.QueryRow(`SELECT fails FROM transcripts WHERE item_id=?`, id).Scan(&fails); err != nil || fails != 0 {
		t.Fatal(fails, err)
	}
	if p := w.Progress.Snapshot(); p.State != "" || p.Error != "" || p.Done != 2 {
		t.Fatalf("progress %+v", p)
	}
}

// Audio the service refuses fails its book alone: retried later, given up
// on after a few times, tried again after the settings change.
func TestWorkerBookFailure(t *testing.T) {
	r := newRig(t)
	good, _ := r.book("good", nil, 5*minute)
	bad, _ := r.book("bad", nil, 5*minute) // newer: first
	r.stt.set(func(p string) (int, string, []string) {
		if strings.HasPrefix(p, "bad") {
			return 400, `{"error":{"message":"could not decode the audio"}}`, nil
		}
		return 0, "", nil
	})
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take(), "bad1@0+300", "good1@0+300")
	if s, _ := r.stored(good); s.State != StateReady {
		t.Fatalf("good %+v", s)
	}
	if s, _ := r.stored(bad); s.State != StateFailed {
		t.Fatalf("bad %+v", s)
	}
	if p := w.Progress.Snapshot(); !strings.Contains(p.Error, "Книга bad") || !strings.Contains(p.Error, "could not decode") {
		t.Fatalf("progress %+v", p)
	}
	r.pass(w)
	wantAsked(t, r.stt.take()) // not before an hour
	for i := 2; i <= maxFails; i++ {
		r.now = r.now.Add(8 * 24 * time.Hour)
		r.pass(w)
		wantAsked(t, r.stt.take(), "bad1@0+300")
	}
	r.now = r.now.Add(30 * 24 * time.Hour)
	r.pass(w)
	wantAsked(t, r.stt.take()) // given up on
	r.stt.set(nil)
	if err := w.Reconfigured(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.pass(w)
	wantAsked(t, r.stt.take(), "bad1@0+300")
	if s, _ := r.stored(bad); s.State != StateReady {
		t.Fatalf("after the change %+v", s)
	}
}

// A piece the service keeps failing on (500 for it alone) does not hold
// the library for ever.
func TestWorkerPoisonPiece(t *testing.T) {
	r := newRig(t)
	good, _ := r.book("good", nil, 5*minute)
	bad, _ := r.book("bad", nil, 5*minute)
	r.stt.set(func(p string) (int, string, []string) {
		if strings.HasPrefix(p, "bad") {
			return 500, "boom", nil
		}
		return 0, "", nil
	})
	w := r.worker()
	for range providerTries {
		r.pass(w)
		r.now = r.now.Add(2 * time.Hour)
	}
	if s, _ := r.stored(bad); s.State != StateFailed {
		t.Fatalf("bad %+v", s)
	}
	r.pass(w)
	if s, _ := r.stored(good); s.State != StateReady {
		t.Fatalf("good %+v", s)
	}
}

// A model without segment times is sent short pieces, whose times are
// used; the next book is cut short from the start.
func TestWorkerWithoutSegmentTimes(t *testing.T) {
	r := newRig(t)
	b, _ := r.book("b", nil, minute)
	a, files := r.book("a", nil, 70_000)
	r.stt.untimed = true
	r.pass(r.worker())
	wantAsked(t, r.stt.take(), "a1@0+70", "a1@0+30", "a1@30+40", "b1@0+30", "b1@30+30")
	segs := r.segments(a)
	if !reflect.DeepEqual(segs, []FileSegment{{files[0], Segment{0, 30_000, "a1@0+30"}}, {files[0], Segment{30_000, 70_000, "a1@30+40"}}}) {
		t.Fatalf("%+v", segs)
	}
	if s, _ := r.stored(b); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
}

// While something plays nothing is read; a play that starts mid-book stops
// the pass after the piece under way.
func TestWorkerStepsAsideForPlayback(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, 25*minute)
	playing := true
	r.busy = func() bool { return playing }
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take())
	if d := w.wait(); d != busyRetry {
		t.Errorf("next pass in %v", d)
	}
	playing = false
	r.stt.set(func(string) (int, string, []string) { playing = true; return 0, "", nil })
	r.pass(w)
	wantAsked(t, r.stt.take(), "a1@0+600")
	playing = false
	r.stt.set(nil)
	r.pass(w)
	wantAsked(t, r.stt.take(), "a1@600+600", "a1@1200+300")
	if s, _ := r.stored(id); s.State != StateReady {
		t.Fatalf("%+v", s)
	}
}

// The book someone is listening to goes first.
func TestWorkerListenedBookFirst(t *testing.T) {
	r := newRig(t)
	old, _ := r.book("old", nil, minute)
	r.book("new", nil, minute)
	r.exec(`INSERT INTO users(id, name, avatar) VALUES (7, 'u', 'a')`)
	r.exec(`INSERT INTO progress(user_id, item_id, position_ms, duration_ms, finished, updated_at) VALUES (7, ?, 5, 10, 0, '2026-04-01T00:00:00Z')`, old)
	r.pass(r.worker())
	wantAsked(t, r.stt.take(), "old1@0+60", "new1@0+60")
}

// A book with a file whose length is unknown waits for the probe.
func TestWorkerWaitsForDurations(t *testing.T) {
	r := newRig(t)
	id, files := r.book("a", nil, minute, 0)
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take())
	if _, ok := r.stored(id); ok {
		t.Fatal("stored without a timeline")
	}
	r.exec(`UPDATE files SET probe_json='{"DurationMs":60000}' WHERE id=?`, files[1])
	r.pass(w)
	wantAsked(t, r.stt.take(), "a1@0+60", "a2@0+60")
}

// A changed or removed book drops its transcript; a book away for a while
// (a disk unplugged) keeps it.
func TestWorkerDropsStaleTranscripts(t *testing.T) {
	r := newRig(t)
	id, files := r.book("a", nil, minute, minute)
	w := r.worker()
	r.pass(w)
	r.stt.take()
	// away: everything missing
	away := r.now.Format(time.RFC3339)
	r.exec(`UPDATE items SET missing_since=? WHERE id=?`, away, id)
	r.exec(`UPDATE files SET missing_since=? WHERE item_id=?`, away, id)
	r.pass(w)
	if _, ok := r.stored(id); !ok {
		t.Fatal("dropped at once")
	}
	// back
	r.exec(`UPDATE items SET missing_since=NULL WHERE id=?`, id)
	r.exec(`UPDATE files SET missing_since=NULL WHERE item_id=?`, id)
	// one file away: kept, nothing redone
	r.exec(`UPDATE files SET missing_since=? WHERE id=?`, away, files[1])
	r.pass(w)
	if _, ok := r.stored(id); !ok {
		t.Fatal("dropped for one file away")
	}
	wantAsked(t, r.stt.take())
	r.exec(`UPDATE files SET missing_since=NULL WHERE id=?`, files[1])
	r.pass(w)
	wantAsked(t, r.stt.take())
	// replaced: made again
	r.exec(`UPDATE files SET size=2 WHERE id=?`, files[1])
	r.pass(w)
	wantAsked(t, r.stt.take(), "a1@0+60", "a2@0+60")
	// gone for a week: dropped, with the feature off too
	r.off = true
	gone := r.now.Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	r.exec(`UPDATE items SET missing_since=? WHERE id=?`, gone, id)
	r.exec(`UPDATE files SET missing_since=? WHERE item_id=?`, gone, id)
	r.pass(w)
	if _, ok := r.stored(id); ok {
		t.Fatal("kept")
	}
	var pieces int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM transcript_pieces`).Scan(&pieces); err != nil || pieces != 0 {
		t.Fatal(pieces, err)
	}
}

func TestWorkerOff(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, minute)
	r.off = true
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take())
	if _, ok := r.stored(id); ok {
		t.Fatal("stored while off")
	}
	if p := w.Progress.Snapshot(); p.State != bgtask.StateOff {
		t.Fatalf("progress %+v", p)
	}
}

// A file that is not on disk is left for later, nothing counted.
func TestWorkerFileNotOnDisk(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, minute)
	if err := os.Remove(filepath.Join(r.dir, "a", "a1.mp3")); err != nil {
		t.Fatal(err)
	}
	w := r.worker()
	r.pass(w)
	wantAsked(t, r.stt.take())
	if s, ok := r.stored(id); ok && s.State != StatePending {
		t.Fatalf("%+v", s)
	}
}

// Chapters: made for a book whose own titles say nothing, once its
// transcript is ready; a bad answer is retried later; real titles stay.
func TestWorkerChapters(t *testing.T) {
	r := newRig(t)
	real, _ := r.book("real", []string{"Пролог", "Встреча"}, minute, minute)
	id, files := r.book("a", nil, 12*minute, 5*minute)
	llm := &fakeLLM{answers: []string{`{"chapters":[]}`, `nonsense`,
		`{"chapters":[{"line":1,"title":"Начало"},{"line":3,"title":"Буря"},{"line":5,"title":"Берег"}]}`}}
	r.llm = llm
	w := r.worker()
	r.pass(w)
	if s, _ := r.stored(id); s.State != StateReady || s.Chapters {
		t.Fatalf("after two bad answers %+v", s)
	}
	if len(llm.asked) != 2 {
		t.Fatalf("asked %d times", len(llm.asked))
	}
	if p := w.Progress.Snapshot(); !strings.Contains(p.Error, "Книга a") || p.Done != 1 {
		t.Fatalf("progress %+v", p)
	}
	r.pass(w)
	if len(llm.asked) != 2 {
		t.Fatal("asked again at once")
	}
	r.now = r.now.Add(2 * time.Hour)
	r.pass(w)
	if s, _ := r.stored(id); !s.Chapters {
		t.Fatalf("%+v", s)
	}
	chs, err := AutoChapters(context.Background(), r.db, id)
	if err != nil {
		t.Fatal(err)
	}
	want := []Chapter{{0, "Начало", files[0], 0}, {1, "Буря", files[0], 601_000}, {2, "Берег", files[1], 1000}}
	if !reflect.DeepEqual(chs, want) {
		t.Fatalf("%+v", chs)
	}
	if p := w.Progress.Snapshot(); p.Done != 2 || p.Total != 2 || p.Error != "" {
		t.Errorf("progress %+v", p)
	}
	if s, _ := r.stored(real); s.Chapters {
		t.Error("real titles replaced")
	}
	r.pass(w)
	if len(llm.asked) != 3 {
		t.Errorf("asked %d times", len(llm.asked))
	}
}

// Without a text model a transcript is still made; its chapters follow
// once there is one, even with speech recognition off by then.
func TestWorkerChaptersLater(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, minute)
	w := r.worker()
	r.pass(w)
	if p := w.Progress.Snapshot(); p.Done != 1 {
		t.Fatalf("progress %+v", p)
	}
	r.off = true
	r.llm = &fakeLLM{answers: []string{`{"chapters":[{"line":1,"title":"Всё"}]}`}}
	r.pass(w)
	if s, _ := r.stored(id); !s.Chapters {
		t.Fatalf("%+v", s)
	}
}

// A model that cannot be asked holds the chapters, and a book it keeps
// failing on is set aside.
func TestWorkerChaptersModelFails(t *testing.T) {
	r := newRig(t)
	id, _ := r.book("a", nil, minute)
	llm := &fakeLLM{err: fmt.Errorf("llm: 500 down")}
	r.llm = llm
	w := r.worker()
	r.pass(w)
	r.pass(w)
	if len(llm.asked) != 1 {
		t.Fatalf("asked %d times", len(llm.asked))
	}
	if p := w.Progress.Snapshot(); !strings.Contains(p.Error, "500 down") {
		t.Fatalf("progress %+v", p)
	}
	for range providerTries {
		r.now = r.now.Add(7 * time.Hour)
		r.pass(w)
	}
	var fails int
	if err := r.db.QueryRow(`SELECT chapters_fails FROM transcripts WHERE item_id=?`, id).Scan(&fails); err != nil || fails == 0 {
		t.Fatal(fails, err)
	}
}

// A text model turned off while a pass runs is not asked any more.
func TestWorkerChaptersModelTurnedOff(t *testing.T) {
	r := newRig(t)
	r.book("a", nil, minute)
	llm := &fakeLLM{answers: []string{`{"chapters":[{"line":1,"title":"Всё"}]}`}}
	r.llm = llm
	r.stt.set(func(string) (int, string, []string) { r.llm = nil; return 0, "", nil })
	w := r.worker()
	r.pass(w)
	if len(llm.asked) != 0 {
		t.Fatalf("asked %d times", len(llm.asked))
	}
}

func TestWorkerRunStops(t *testing.T) {
	r := newRig(t)
	r.book("a", nil, minute)
	w := r.worker()
	w.StartDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	w.Kick()
	deadline := time.After(10 * time.Second)
	for len(r.stt.take()) == 0 {
		select {
		case <-deadline:
			t.Fatal("no pass")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("did not stop")
	}
	(*Worker)(nil).Kick() // nil-safe
}
