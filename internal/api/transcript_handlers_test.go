package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/transcript"
)

type transcriptBook struct {
	Chapters     []BookChapter `json:"chapters"`
	ChaptersAuto *bool         `json:"chaptersAuto"`
	Transcript   *string       `json:"transcript"`
}

// timedBook is an audiobook of two files of 60 and 30 seconds, its chapters
// titled as given.
func timedBook(e *sttEnv, titles ...string) (int64, []int64) {
	e.t.Helper()
	id, files := e.audiobook("Старик", 2)
	for i, f := range files {
		if _, err := e.db.Exec(`UPDATE files SET duration_ms=? WHERE id=?`, []int{60_000, 30_000}[i], f); err != nil {
			e.t.Fatal(err)
		}
		if _, err := e.db.Exec(`UPDATE chapters SET title=? WHERE item_id=? AND idx=?`, titles[i], id, i); err != nil {
			e.t.Fatal(err)
		}
	}
	return id, files
}

// storeTranscript puts a finished transcript with automatic chapters in
// the database, as the worker would.
func storeTranscript(e *sttEnv, id int64, files []int64, state string) {
	e.t.Helper()
	fs, err := e.repo.FilesOf(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	var sig []transcript.File
	for _, f := range fs {
		sig = append(sig, transcript.File{ID: f.ID, Size: f.Size, Mtime: f.Mtime})
	}
	exec := func(q string, args ...any) {
		if _, err := e.db.Exec(q, args...); err != nil {
			e.t.Fatal(err)
		}
	}
	exec(`DELETE FROM transcripts WHERE item_id=?`, id)
	exec(`INSERT INTO transcripts(item_id, signature, state, language, chapters_done, updated_at) VALUES(?,?,?,'ru',1,'2026-01-01T00:00:00Z')`,
		id, transcript.Signature(sig), state)
	exec(`INSERT INTO transcript_pieces(item_id, file_id, start_ms, end_ms, segments) VALUES(?,?,0,30000,?)`, id, files[1],
		`[{"startMs":1000,"endMs":2000,"text":"Море."}]`)
	exec(`INSERT INTO transcript_pieces(item_id, file_id, start_ms, end_ms, segments) VALUES(?,?,0,60000,?)`, id, files[0],
		`[{"startMs":0,"endMs":4200,"text":"Старик."},{"startMs":40000,"endMs":41000,"text":"Лодка."}]`)
	for i, c := range []struct {
		title string
		file  int64
		at    int64
	}{{"Старик", files[0], 0}, {"Лодка", files[0], 40_000}, {"Море", files[1], 1000}} {
		exec(`INSERT INTO auto_chapters(item_id, idx, title, file_id, start_ms) VALUES(?,?,?,?,?)`, id, i, c.title, c.file, c.at)
	}
}

func (e *sttEnv) bookPage(id int64, hdr ...string) transcriptBook {
	e.t.Helper()
	w := e.do("GET", "/api/books/"+strconv.FormatInt(id, 10), "", e.admin, hdr...)
	var b transcriptBook
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || w.Code != 200 {
		e.t.Fatal(w.Code, w.Body, err)
	}
	if b.ChaptersAuto == nil || b.Transcript == nil {
		e.t.Fatalf("chaptersAuto and transcript are always there: %s", w.Body)
	}
	return b
}

func chapterTitlesOf(c []BookChapter) string {
	var out []string
	for _, x := range c {
		out = append(out, x.Title)
	}
	return strings.Join(out, "|")
}

func TestBookTranscript(t *testing.T) {
	noSpeechEnv(t)
	e := newSTTEnv(t)
	id, files := timedBook(e, "00_Starik_i_more", "01_Starik_i_more")
	url := "/api/books/" + strconv.FormatInt(id, 10) + "/transcript"

	// the feature off, nothing stored: file names show as chapter numbers
	b := e.bookPage(id)
	if *b.Transcript != "" || *b.ChaptersAuto || chapterTitlesOf(b.Chapters) != "Глава 1|Глава 2" {
		t.Fatalf("off: %q %v %+v", *b.Transcript, *b.ChaptersAuto, b.Chapters)
	}
	if b := e.bookPage(id, "Accept-Language", "en"); chapterTitlesOf(b.Chapters) != "Chapter 1|Chapter 2" {
		t.Fatalf("en: %+v", b.Chapters)
	}
	if w := e.do("GET", url, "", e.admin); w.Code != 404 {
		t.Fatalf("no transcript: %d %s", w.Code, w.Body)
	}
	e.groqSpeech(false)
	if b := e.bookPage(id); *b.Transcript != "pending" {
		t.Fatalf("on: %q", *b.Transcript)
	}

	storeTranscript(e, id, files, "pending")
	if b := e.bookPage(id); *b.Transcript != "pending" {
		t.Fatalf("pending: %q", *b.Transcript)
	}
	if w := e.do("GET", url, "", e.admin); w.Code != 404 {
		t.Fatalf("unfinished: %d %s", w.Code, w.Body)
	}
	storeTranscript(e, id, files, "failed")
	if b := e.bookPage(id); *b.Transcript != "failed" {
		t.Fatalf("failed: %q", *b.Transcript)
	}

	storeTranscript(e, id, files, "ready")
	b = e.bookPage(id)
	want := []BookChapter{
		{Idx: 0, Title: "Старик", FileID: files[0], StartMs: 0, EndMs: 40_000},
		{Idx: 1, Title: "Лодка", FileID: files[0], StartMs: 40_000, EndMs: 61_000},
		{Idx: 2, Title: "Море", FileID: files[1], StartMs: 61_000, EndMs: 90_000},
	}
	if *b.Transcript != "ready" || !*b.ChaptersAuto || !reflect.DeepEqual(b.Chapters, want) {
		t.Fatalf("ready: %q %v %+v", *b.Transcript, *b.ChaptersAuto, b.Chapters)
	}
	w := e.do("GET", url, "", e.admin)
	wantBody := `{"language":"ru","segments":[{"startMs":0,"endMs":4200,"text":"Старик."},{"startMs":40000,"endMs":41000,"text":"Лодка."},{"startMs":61000,"endMs":62000,"text":"Море."}]}`
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != wantBody {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	z := e.do("GET", url, "", e.admin, "Accept-Encoding", "gzip")
	if z.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("not compressed: %v", z.Header())
	}
	r, err := gzip.NewReader(z.Body)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(r); strings.TrimSpace(string(plain)) != wantBody {
		t.Fatalf("%s", plain)
	}
	// what is stored stays when the feature is turned off
	e.groqSpeech(true)
	if b := e.bookPage(id); *b.Transcript != "ready" || !*b.ChaptersAuto {
		t.Fatalf("off with a transcript: %q", *b.Transcript)
	}
	e.groqSpeech(false)

	// who may not open the book has no transcript either
	if _, err := e.db.Exec(`UPDATE items SET rating='18+' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	kid := e.kid("6+")
	page := e.do("GET", "/api/books/"+strconv.FormatInt(id, 10), "", kid).Code
	if page == 200 {
		t.Fatal("the test's kid sees the book")
	}
	if w := e.do("GET", url, "", kid); w.Code != page {
		t.Fatalf("kid: %d, the page %d", w.Code, page)
	}
	if w := e.do("GET", url, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", w.Code)
	}
	other := e.book("Книга", ".epub")
	if w := e.do("GET", "/api/books/"+strconv.FormatInt(other, 10)+"/transcript", "", e.admin); w.Code != 404 {
		t.Fatalf("an e-book: %d", w.Code)
	}
	if b := e.bookPage(other); *b.Transcript != "" || *b.ChaptersAuto {
		t.Fatalf("an e-book's page: %q", *b.Transcript)
	}

	// a file replaced: the old transcript is not the book's
	if _, err := e.db.Exec(`UPDATE files SET size=size+1 WHERE id=?`, files[0]); err != nil {
		t.Fatal(err)
	}
	if b := e.bookPage(id); *b.Transcript != "pending" || *b.ChaptersAuto || chapterTitlesOf(b.Chapters) != "Глава 1|Глава 2" {
		t.Fatalf("stale: %q %+v", *b.Transcript, b.Chapters)
	}
	if w := e.do("GET", url, "", e.admin); w.Code != 404 {
		t.Fatalf("stale: %d", w.Code)
	}
}

// Real chapter titles are the book's: automatic ones never replace them.
func TestBookTranscriptKeepsRealChapters(t *testing.T) {
	noSpeechEnv(t)
	e := newSTTEnv(t)
	id, files := timedBook(e, "Пролог", "Встреча")
	storeTranscript(e, id, files, "ready")
	b := e.bookPage(id)
	if *b.Transcript != "ready" || *b.ChaptersAuto || chapterTitlesOf(b.Chapters) != "Пролог|Встреча" {
		t.Fatalf("%q %v %+v", *b.Transcript, *b.ChaptersAuto, b.Chapters)
	}
}
