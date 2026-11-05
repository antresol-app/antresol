package api

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

type tVol struct{ id, file int64 }

func jpegBytes(t testing.TB) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// cbz writes a zip volume with the given page entries.
func writeCBZ(t testing.TB, p string, pages map[string][]byte) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, data := range pages {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	zw.Close()
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// comic adds a series of n zip volumes (3 JPEG pages each).
func (e *libEnv) comic(series, rating string, n int) (int64, []tVol) {
	e.t.Helper()
	ctx := context.Background()
	dir := filepath.Join(e.media, "manga", series)
	jpg := jpegBytes(e.t)
	var vols []tVol
	var seriesID int64
	for i := 1; i <= n; i++ {
		p := filepath.Join(dir, series+" v"+itoa(int64(i))+".cbz")
		writeCBZ(e.t, p, map[string][]byte{"01.jpg": jpg, "02.jpg": jpg, "10.jpg": jpg})
		st, _ := os.Stat(p)
		id, err := e.repo.UpsertComicVolume(ctx, library.ComicInput{File: library.FileInput{Path: p, Size: st.Size(), Mtime: st.ModTime().Unix()},
			SeriesDir: dir, Series: series, Volume: float64(i), Title: "Том " + itoa(int64(i)), Rating: rating, Direction: "rtl", Pages: 3})
		if err != nil {
			e.t.Fatal(err)
		}
		fs, _ := e.repo.FilesOf(ctx, id)
		it, _ := e.repo.Item(ctx, id)
		seriesID = it.ParentID
		vols = append(vols, tVol{id, fs[0].ID})
	}
	return seriesID, vols
}

// book adds an e-book with files of the given extensions.
func (e *libEnv) book(title string, exts ...string) int64 {
	e.t.Helper()
	dir := filepath.Join(e.media, "books", title)
	os.MkdirAll(dir, 0o755)
	var fs []library.FileInput
	for _, x := range exts {
		p := filepath.Join(dir, "Булгаков - "+title+x)
		os.WriteFile(p, []byte("book"), 0o644)
		fs = append(fs, library.FileInput{Path: p, Size: 4, Mtime: 1})
	}
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{Files: fs, Title: title, Authors: []string{"Михаил Булгаков", "Соавтор"},
		Series: "Москва", SeriesIndex: 2, Lang: "ru", Year: 1967, Description: "Роман"})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// audiobook adds an audiobook of n files with a chapter per file.
func (e *libEnv) audiobook(title string, n int) (int64, []int64) {
	e.t.Helper()
	dir := filepath.Join(e.media, "audiobooks", title)
	os.MkdirAll(dir, 0o755)
	var fs []library.FileInput
	var chs []library.ChapterInput
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, itoa(int64(i+1))+".mp3")
		os.WriteFile(p, []byte("mp3"), 0o644)
		fs = append(fs, library.FileInput{Path: p, Size: 3, Mtime: 1})
		chs = append(chs, library.ChapterInput{Title: "Глава " + itoa(int64(i+1)), FileIndex: i})
	}
	id, err := e.repo.UpsertAudiobook(context.Background(), library.AudiobookInput{Dir: dir, Files: fs, Title: title,
		Authors: []string{"Лев Толстой"}, Narrator: "Чтец", Chapters: chs})
	if err != nil {
		e.t.Fatal(err)
	}
	files, _ := e.repo.FilesOf(context.Background(), id)
	var ids []int64
	for _, f := range files {
		ids = append(ids, f.ID)
	}
	return id, ids
}

type bookDetailOut struct {
	Card     Card     `json:"card"`
	Kind     string   `json:"kind"`
	Authors  []string `json:"authors"`
	Narrator string   `json:"narrator"`
	Series   *struct {
		Name  string  `json:"name"`
		Index float64 `json:"index"`
	} `json:"series"`
	Files []struct {
		ID         int64  `json:"id"`
		Format     string `json:"format"`
		Size       int64  `json:"size"`
		DurationMs int64  `json:"durationMs"`
	} `json:"files"`
	SeriesID   int64 `json:"seriesId"`
	NextID     int64 `json:"nextId"`
	ReadFileID int64 `json:"readFileId"`
	Progress   *struct {
		Fraction   float64 `json:"fraction"`
		Locator    string  `json:"locator"`
		PositionMs int64   `json:"positionMs"`
		DurationMs int64   `json:"durationMs"`
		Finished   bool    `json:"finished"`
	} `json:"progress"`
	Want       bool  `json:"want"`
	DurationMs int64 `json:"durationMs"`
	Chapters   []struct {
		Idx     int    `json:"idx"`
		Title   string `json:"title"`
		FileID  int64  `json:"fileId"`
		StartMs int64  `json:"startMs"`
		EndMs   int64  `json:"endMs"`
	} `json:"chapters"`
	Volumes []struct {
		Card
		Pages     int    `json:"pages"`
		Direction string `json:"direction"`
		FileID    int64  `json:"fileId"`
	} `json:"volumes"`
	Direction string `json:"direction"`
}

func (e *libEnv) bookDetail(id int64) bookDetailOut {
	e.t.Helper()
	w := e.do("GET", "/api/books/"+itoa(id), "", e.admin)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return decodeJSON[bookDetailOut](e.t, w.Body.Bytes())
}

func TestSaveLocatorRoundTrip(t *testing.T) {
	e := newLibEnv(t)
	b := e.book("Мастер и Маргарита", ".epub")
	if w := e.do("PUT", "/api/progress/"+itoa(b), `{"locator":"epubcfi(/6/4!/4/2)","fraction":0.34}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d := e.bookDetail(b)
	if d.Progress == nil || d.Progress.Locator != "epubcfi(/6/4!/4/2)" || d.Progress.Fraction != 0.34 || d.Progress.Finished || d.Card.Progress != 0.34 {
		t.Fatalf("%+v", d.Progress)
	}
	if w := e.do("PUT", "/api/progress/"+itoa(b), `{"fraction":0.5}`, e.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "Не передана позиция") {
		t.Fatalf("no locator %d %s", w.Code, w.Body)
	}
}

func TestSaveLocatorFinished(t *testing.T) {
	e := newLibEnv(t)
	b := e.book("Идиот", ".epub")
	e.do("PUT", "/api/progress/"+itoa(b), `{"locator":"epubcfi(/6/90)","fraction":0.99}`, e.admin)
	if d := e.bookDetail(b); d.Progress == nil || !d.Progress.Finished {
		t.Fatalf("%+v", d.Progress)
	}
	out := decodeJSON[shelvesOut](t, e.do("GET", "/api/books/home", "", e.admin).Body.Bytes())
	for _, s := range out.Shelves {
		if s.ID == "done" {
			if len(s.Cards) != 1 || s.Cards[0].ID != b {
				t.Fatalf("done %+v", s)
			}
			return
		}
	}
	t.Fatalf("no done shelf: %+v", out)
}

func TestSaveProgressAudiobook(t *testing.T) {
	e := newLibEnv(t)
	ab, _ := e.audiobook("Война и мир", 2)
	if w := e.do("PUT", "/api/progress/"+itoa(ab), `{"positionMs":61000,"durationMs":120000}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d := e.bookDetail(ab)
	if d.Progress == nil || d.Progress.PositionMs != 61000 || d.Progress.DurationMs != 120000 || d.Kind != "audiobook" || d.Narrator != "Чтец" {
		t.Fatalf("%+v", d)
	}
	out := decodeJSON[shelvesOut](t, e.do("GET", "/api/books/home", "", e.admin).Body.Bytes())
	if out.Shelves[0].ID != "reading" || out.Shelves[0].Title != "Читаю сейчас" || out.Shelves[0].Cards[0].ID != ab {
		t.Fatalf("%+v", out.Shelves)
	}
}

func TestAudiobookFinishedOnlyAtTheEnd(t *testing.T) {
	e := newLibEnv(t)
	ab, _ := e.audiobook("Война и мир", 2)
	// 18.5 of 20 hours: 92.5% is not the end of a book
	if w := e.do("PUT", "/api/progress/"+itoa(ab), `{"positionMs":66600000,"durationMs":72000000}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d := e.bookDetail(ab); d.Progress == nil || d.Progress.Finished {
		t.Fatalf("finished at 92.5%%: %+v", d.Progress)
	}
	if w := e.do("PUT", "/api/progress/"+itoa(ab), `{"positionMs":71975000,"durationMs":72000000}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d := e.bookDetail(ab); !d.Progress.Finished {
		t.Fatalf("not finished 25 s before the end: %+v", d.Progress)
	}
}

func TestStaleProgressIgnored(t *testing.T) {
	e := newLibEnv(t)
	ab, _ := e.audiobook("Анна Каренина", 1)
	e.do("PUT", "/api/progress/"+itoa(ab), `{"positionMs":50000,"durationMs":100000}`, e.admin)
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if w := e.do("PUT", "/api/progress/"+itoa(ab), `{"positionMs":1000,"durationMs":100000,"updatedAt":"`+old+`"}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d := e.bookDetail(ab); d.Progress.PositionMs != 50000 {
		t.Fatalf("stale write applied: %+v", d.Progress)
	}
	if w := e.do("PUT", "/api/progress/"+itoa(ab), `{"positionMs":1,"updatedAt":"вчера"}`, e.admin); w.Code != 400 {
		t.Fatalf("bad time %d", w.Code)
	}
}

func TestSaveProgressRejectsAlbum(t *testing.T) {
	e := newLibEnv(t)
	album, _ := e.simpleAlbum("Кино", "Группа крови", 1)
	w := e.do("PUT", "/api/progress/"+itoa(album), `{"positionMs":1000,"durationMs":2000}`, e.admin)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Прогресс для этого элемента не сохраняется") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestBooksHomeShelves(t *testing.T) {
	e := newLibEnv(t)
	b1 := e.book("Мастер и Маргарита", ".epub")
	b2 := e.book("Белая гвардия", ".fb2")
	ab, _ := e.audiobook("Война и мир", 1)
	series, vols := e.comic("Берсерк", "", 2)
	e.do("PUT", "/api/progress/"+itoa(vols[0].id), `{"locator":"page:2","fraction":0.5}`, e.admin)
	e.do("POST", "/api/watchlist/"+itoa(b2), "", e.admin)
	e.do("POST", "/api/items/"+itoa(b1)+"/watched", `{"watched":true}`, e.admin)
	w := e.do("GET", "/api/books/home", "", e.admin)
	out := decodeJSON[shelvesOut](t, w.Body.Bytes())
	want := map[string][2]any{
		"reading": {"Читаю сейчас", vols[0].id}, "want": {"Хочу прочитать", b2}, "recent": {"Недавно добавленные", b2},
		"audiobooks": {"Аудиокниги", ab}, "comics": {"Манга и комиксы", series}, "done": {"Прочитано", b1},
	}
	order := []string{"reading", "want", "recent", "audiobooks", "comics", "done"}
	if len(out.Shelves) != len(order) {
		t.Fatalf("%s", w.Body)
	}
	for i, s := range out.Shelves {
		wnt := want[s.ID]
		if s.ID != order[i] || s.Title != wnt[0] || len(s.Cards) == 0 || s.Cards[0].ID != wnt[1].(int64) {
			t.Fatalf("shelf %d %+v", i, s)
		}
	}
	if c := out.Shelves[0].Cards[0]; c.Kind != "comic_volume" || c.Subtitle != "Том 1" || c.Title != "Берсерк" || c.Progress != 0.5 {
		t.Fatalf("volume card %+v", c)
	}
	if c := out.Shelves[4].Cards[0]; c.Subtitle != "2 тома" {
		t.Fatalf("comic card %+v", c)
	}
	if c := out.Shelves[2].Cards[1]; c.Subtitle != "Михаил Булгаков, Соавтор" {
		t.Fatalf("book card %+v", c)
	}
	if c := out.Shelves[3].Cards[0]; c.Subtitle != "Лев Толстой" {
		t.Fatalf("audiobook card %+v", c)
	}
	// the books' watchlist is not the video one
	home := decodeJSON[shelvesOut](t, e.do("GET", "/api/home", "", e.admin).Body.Bytes())
	for _, s := range home.Shelves {
		if s.ID == "watchlist" && len(s.Cards) != 0 {
			t.Fatalf("video watchlist has a book: %+v", s)
		}
	}
	if w := e.do("GET", "/api/books?kind=movie", "", e.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "Неизвестный раздел") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/books?kind=book&sort=author", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestBookDetailReadFilePrefersEPUB(t *testing.T) {
	e := newLibEnv(t)
	b := e.book("Мастер и Маргарита", ".fb2", ".pdf", ".epub")
	d := e.bookDetail(b)
	var epub int64
	formats := map[string]bool{}
	for _, f := range d.Files {
		formats[f.Format] = true
		if f.Format == "EPUB" {
			epub = f.ID
		}
	}
	if epub == 0 || d.ReadFileID != epub || !formats["FB2"] || !formats["PDF"] {
		t.Fatalf("%+v", d)
	}
	if d.Kind != "book" || len(d.Authors) != 2 || d.Series == nil || d.Series.Name != "Москва" || d.Series.Index != 2 || d.Progress != nil || d.SeriesID != 0 {
		t.Fatalf("%+v", d)
	}
}

func TestAudiobookChaptersAbsolute(t *testing.T) {
	e := newLibEnv(t)
	ab, files := e.audiobook("Война и мир", 3)
	d := e.bookDetail(ab)
	if len(d.Chapters) != 3 || d.DurationMs != 0 {
		t.Fatalf("%+v", d)
	}
	for _, c := range d.Chapters {
		if c.StartMs != 0 || c.EndMs != 0 {
			t.Fatalf("without probe %+v", d.Chapters)
		}
	}
	for _, f := range files {
		e.db.Exec(`UPDATE files SET duration_ms=60000 WHERE id=?`, f)
	}
	d = e.bookDetail(ab)
	for i, c := range d.Chapters {
		if c.StartMs != int64(i)*60000 || c.EndMs != int64(i+1)*60000 || c.FileID != files[i] {
			t.Fatalf("chapter %d %+v", i, c)
		}
	}
	if d.DurationMs != 180000 || d.Files[2].DurationMs != 60000 || d.Files[0].Format != "MP3" {
		t.Fatalf("%+v", d)
	}
}

func TestComicVolumeSeriesAndNext(t *testing.T) {
	e := newLibEnv(t)
	series, vols := e.comic("Берсерк", "", 2)
	d := e.bookDetail(vols[0].id)
	if d.SeriesID != series || d.NextID != vols[1].id || d.Direction != "rtl" || d.Kind != "comic_volume" || d.Files[0].Format != "CBZ" {
		t.Fatalf("%+v", d)
	}
	if d := e.bookDetail(vols[1].id); d.NextID != 0 || d.SeriesID != series {
		t.Fatalf("last %+v", d)
	}
	s := e.bookDetail(series)
	if len(s.Volumes) != 2 || s.Volumes[0].ID != vols[0].id || s.Volumes[0].Pages != 3 || s.Volumes[0].FileID != vols[0].file ||
		s.Volumes[0].Direction != "rtl" || s.SeriesID != 0 || s.Card.Subtitle != "2 тома" {
		t.Fatalf("%+v", s)
	}
}
