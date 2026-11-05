package library

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"github.com/antresol-app/antresol/internal/media"
)

// audiobookScanner returns a scanner with only an audiobooks root.
func audiobookScanner(t *testing.T) (*Scanner, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "books", "audiobooks")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Scanner{Repo: newRepo(t), Audiobooks: root, ArtDir: filepath.Join(base, "images")}, root
}

func scanAudiobooksOK(t *testing.T, s *Scanner) ScanStats {
	t.Helper()
	st, err := s.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return st
}

// junkAudio writes a 16-byte file that no tag reader understands.
func junkAudio(t *testing.T, path string) {
	t.Helper()
	writeBytes(t, path, []byte("not really audio"))
}

func oneAudiobook(t *testing.T, r *Repo) (Item, []Chapter) {
	t.Helper()
	books := listKind(t, r, KindAudiobook)
	if len(books) != 1 {
		t.Fatalf("audiobooks: %v", titles(books))
	}
	chs, err := r.Chapters(context.Background(), books[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return books[0], chs
}

func chapterTitles(chs []Chapter) []string {
	var out []string
	for _, c := range chs {
		out = append(out, c.Title)
	}
	return out
}

func filePathByID(t *testing.T, r *Repo, itemID, fileID int64) string {
	t.Helper()
	files, err := r.FilesOf(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.ID == fileID {
			return f.Path
		}
	}
	t.Fatalf("file %d is not a file of item %d", fileID, itemID)
	return ""
}

func TestParseAudiobookFolder(t *testing.T) {
	for _, c := range []struct {
		in, author, title, narrator string
		year                        int
		idx                         float64
	}{
		{"Толстой Лев - Война и мир [Клюквин Александр, 2012, 128 kbps, MP3]", "Толстой Лев", "Война и мир", "Клюквин Александр", 2012, 0},
		{"1 - Пикник на обочине", "", "Пикник на обочине", "", 0, 1},
		{"Война и мир", "", "Война и мир", "", 0, 0},
		{"2.5 - Повесть", "", "Повесть", "", 0, 2.5},
		{"Стругацкие - Улитка на склоне [2008, Ким Валерий]", "Стругацкие", "Улитка на склоне", "Ким Валерий", 2008, 0},
		{"Книга 3 - Полдень", "", "Полдень", "", 0, 3},
		{"Vol. 2 - Полдень", "", "Полдень", "", 0, 2},
		{"#3 - Полдень", "", "Полдень", "", 0, 3},
		{"03. Полдень", "", "Полдень", "", 0, 3},
		{"Том 2. Полдень", "", "Полдень", "", 0, 2},
		{"1984", "", "1984", "", 0, 0},
	} {
		a, ti, n, y, i := parseAudiobookFolder(c.in)
		if a != c.author || ti != c.title || n != c.narrator || y != c.year || i != c.idx {
			t.Errorf("%q → (%q, %q, %q, %d, %v)", c.in, a, ti, n, y, i)
		}
	}
}

func TestAudiobookFolderNaturalOrder(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Толстой Лев - Война и мир [Клюквин Александр, 2012, 128 kbps, MP3]")
	for _, n := range []string{"10.mp3", "1.mp3", "2.mp3"} {
		junkAudio(t, filepath.Join(dir, n))
	}
	st := scanAudiobooksOK(t, s)
	if st.Audiobooks != 1 || st.Files != 3 {
		t.Fatalf("stats %+v", st)
	}
	b, chs := oneAudiobook(t, s.Repo)
	if b.Title != "Война и мир" || b.Year != 2012 {
		t.Fatalf("book %+v", b)
	}
	if a := authorsOf(t, b); len(a) != 1 || a[0] != "Толстой Лев" {
		t.Fatalf("authors %v", a)
	}
	if n := bookMeta(t, b)["narrator"]; n != "Клюквин Александр" {
		t.Fatalf("narrator %v", n)
	}
	if got := strings.Join(chapterTitles(chs), "|"); got != "Глава 1|Глава 2|Глава 10" {
		t.Fatalf("chapters %s", got)
	}
	for i, c := range chs {
		want := []string{"1.mp3", "2.mp3", "10.mp3"}[i]
		if c.Idx != i || filepath.Base(filePathByID(t, s.Repo, b.ID, c.FileID)) != want || c.StartMs != 0 || c.EndMs != nil {
			t.Fatalf("chapter %d %+v", i, c)
		}
	}
	// files_order is the playback order
	order, _ := bookMeta(t, b)["files_order"].([]any)
	if len(order) != 3 || int64(order[0].(float64)) != chs[0].FileID || int64(order[2].(float64)) != chs[2].FileID {
		t.Fatalf("files_order %v", order)
	}
	if hits, err := s.Repo.SearchKinds(context.Background(), "Клюквин", []string{KindAudiobook}, 10, ""); err != nil || len(hits) != 1 {
		t.Fatalf("search by narrator %v %v", hits, err)
	}
}

func TestAudiobook200FilesNoTags(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Автор", "Книга")
	for i := 200; i >= 1; i-- {
		junkAudio(t, filepath.Join(dir, fmt.Sprintf("%03d.mp3", i)))
	}
	writeBytes(t, filepath.Join(dir, "cover.jpg"), coverJPEG(t))
	start := time.Now()
	scanAudiobooksOK(t, s)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("scan took %v", d)
	}
	b, chs := oneAudiobook(t, s.Repo)
	if b.Title != "Книга" || b.Poster != "file:"+filepath.Join(dir, "cover.jpg") {
		t.Fatalf("book %+v", b)
	}
	if a := authorsOf(t, b); len(a) != 1 || a[0] != "Автор" {
		t.Fatalf("authors %v", a)
	}
	if len(chs) != 200 {
		t.Fatalf("%d chapters", len(chs))
	}
	for i, c := range chs {
		if c.Idx != i || c.Title != "Глава "+strconv.Itoa(i+1) ||
			filepath.Base(filePathByID(t, s.Repo, b.ID, c.FileID)) != fmt.Sprintf("%03d.mp3", i+1) {
			t.Fatalf("chapter %d %+v", i, c)
		}
	}
}

func TestAudiobookCDSubfolders(t *testing.T) {
	s, root := audiobookScanner(t)
	junkAudio(t, filepath.Join(root, "Книга", "CD2", "01.mp3"))
	junkAudio(t, filepath.Join(root, "Книга", "CD1", "01.mp3"))
	b, chs := func() (Item, []Chapter) { scanAudiobooksOK(t, s); return oneAudiobook(t, s.Repo) }()
	if b.Title != "Книга" || len(authorsOf(t, b)) != 0 {
		t.Fatalf("book %+v", b)
	}
	if got := strings.Join(chapterTitles(chs), "|"); got != "Глава 1|Глава 2" {
		t.Fatalf("chapters %s", got)
	}
	if p := filePathByID(t, s.Repo, b.ID, chs[0].FileID); filepath.Base(filepath.Dir(p)) != "CD1" {
		t.Fatalf("first chapter from %s", p)
	}
	// "Часть N" folders collapse too
	junkAudio(t, filepath.Join(root, "Другая", "Часть 2", "a.mp3"))
	junkAudio(t, filepath.Join(root, "Другая", "Часть 10", "a.mp3"))
	scanAudiobooksOK(t, s)
	if n := len(listKind(t, s.Repo, KindAudiobook)); n != 2 {
		t.Fatalf("%d audiobooks", n)
	}
}

func TestAudiobookSeriesLayout(t *testing.T) {
	s, root := audiobookScanner(t)
	junkAudio(t, filepath.Join(root, "Стругацкие", "Мир Полудня", "1 - Полдень, XXII век", "01.mp3"))
	junkAudio(t, filepath.Join(root, "Стругацкие", "Пикник на обочине", "01.mp3")) // Автор/Название
	scanAudiobooksOK(t, s)
	books := listKind(t, s.Repo, KindAudiobook)
	if len(books) != 2 {
		t.Fatalf("audiobooks %v", titles(books))
	}
	for _, b := range books {
		m := bookMeta(t, b)
		if a := authorsOf(t, b); len(a) != 1 || a[0] != "Стругацкие" {
			t.Fatalf("%s authors %v", b.Title, a)
		}
		switch b.Title {
		case "Полдень, XXII век":
			if m["series"] != "Мир Полудня" || m["series_index"] != 1.0 {
				t.Fatalf("series %v", m)
			}
		case "Пикник на обочине":
			if m["series"] != nil {
				t.Fatalf("no series expected %v", m)
			}
		default:
			t.Fatalf("title %q", b.Title)
		}
	}
	// a numbered folder right under an author is not a series
	s2, root2 := audiobookScanner(t)
	junkAudio(t, filepath.Join(root2, "Стругацкие", "2 - Трудно быть богом", "01.mp3"))
	scanAudiobooksOK(t, s2)
	b, _ := oneAudiobook(t, s2.Repo)
	if m := bookMeta(t, b); b.Title != "Трудно быть богом" || m["series"] != nil || m["series_index"] != nil {
		t.Fatalf("book %+v", b)
	}
}

// writeM4B builds an .m4b of len(chapters) seconds with one chapter per
// second (skips without ffmpeg).
func writeM4B(t *testing.T, path string, tags map[string]string, chapters []string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := ";FFMETADATA1\n"
	for k, v := range tags {
		meta += k + "=" + v + "\n"
	}
	for i, ti := range chapters {
		meta += fmt.Sprintf("[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", i*1000, (i+1)*1000, ti)
	}
	mf := filepath.Join(t.TempDir(), "meta.txt")
	if err := os.WriteFile(mf, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration="+strconv.Itoa(max(1, len(chapters))),
		"-i", mf, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1", "-ac", "1", "-c:a", "aac", "-b:a", "32k",
		"-f", "mp4", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

// probeChapters is what cmd/vault wires as Scanner.Chapters; calls counts them.
func probeChapters(t *testing.T, calls *atomic.Int64) func(ctx context.Context, p string) ([]ChapterInput, error) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	return func(ctx context.Context, p string) ([]ChapterInput, error) {
		calls.Add(1)
		chs, err := media.Chapters(ctx, ffprobe, p)
		out := make([]ChapterInput, len(chs))
		for i, c := range chs {
			out[i] = ChapterInput{Title: c.Title, StartMs: c.StartMs, EndMs: c.EndMs}
		}
		return out, err
	}
}

func TestAudiobookABSLayoutM4B(t *testing.T) {
	s, root := audiobookScanner(t)
	var calls atomic.Int64
	s.Chapters = probeChapters(t, &calls)
	p := filepath.Join(root, "Стругацкие", "Пикник на обочине", "Пикник на обочине.m4b")
	writeM4B(t, p, nil, []string{"Пролог", "Глава 1", "Глава 2"})
	scanAudiobooksOK(t, s)
	b, chs := oneAudiobook(t, s.Repo)
	if b.Title != "Пикник на обочине" {
		t.Fatalf("book %+v", b)
	}
	if a := authorsOf(t, b); len(a) != 1 || a[0] != "Стругацкие" {
		t.Fatalf("authors %v", a)
	}
	if got := strings.Join(chapterTitles(chs), "|"); got != "Пролог|Глава 1|Глава 2" {
		t.Fatalf("chapters %s", got)
	}
	for i, c := range chs {
		if c.Idx != i || c.StartMs != int64(i)*1000 || c.EndMs == nil || *c.EndMs != int64(i+1)*1000 ||
			filePathByID(t, s.Repo, b.ID, c.FileID) != p {
			t.Fatalf("chapter %d %+v", i, c)
		}
	}
	// a rescan of the unchanged file keeps its chapters without ffprobe
	scanAudiobooksOK(t, s)
	if _, chs2 := oneAudiobook(t, s.Repo); calls.Load() != 1 || len(chs2) != 3 || chs2[1].StartMs != 1000 {
		t.Fatalf("rescan: %d probes, %+v", calls.Load(), chs2)
	}
}

func TestAudiobookM4BInAuthorFolder(t *testing.T) {
	s, root := audiobookScanner(t)
	var calls atomic.Int64
	s.Chapters = probeChapters(t, &calls)
	writeM4B(t, filepath.Join(root, "Стругацкие", "Пикник на обочине.m4b"), map[string]string{"album": "Пикник на обочине"}, []string{"Пролог", "Глава 1"})
	writeM4B(t, filepath.Join(root, "Стругацкие", "Улитка на склоне.m4b"), nil, nil)
	writeM4B(t, filepath.Join(root, "Солярис.m4b"), map[string]string{"artist": "Лем Станислав"}, nil)
	scanAudiobooksOK(t, s)
	books := listKind(t, s.Repo, KindAudiobook)
	if got := strings.Join(titles(books), "|"); got != "Пикник на обочине|Солярис|Улитка на склоне" {
		t.Fatalf("audiobooks %s", got)
	}
	for _, b := range books {
		want := map[string]string{"Солярис": "Лем Станислав"}[b.Title]
		if want == "" {
			want = "Стругацкие"
		}
		if a := authorsOf(t, b); len(a) != 1 || a[0] != want {
			t.Fatalf("%s authors %v", b.Title, a)
		}
		chs, _ := s.Repo.Chapters(context.Background(), b.ID)
		wantN := map[string]int{"Пикник на обочине": 2}[b.Title]
		if wantN == 0 {
			wantN = 1 // no embedded chapters: one chapter for the file
		}
		if len(chs) != wantN {
			t.Fatalf("%s chapters %v", b.Title, chapterTitles(chs))
		}
	}
}

func TestAudiobookTagsAndEmbeddedCover(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Без имени")
	cover := testJPEG(t, color.RGBA{200, 30, 30, 255})
	writeAudio(t, filepath.Join(dir, "a1.mp3"), map[string]string{"album": "Мастер и Маргарита", "artist": "Булгаков Михаил",
		"title": "Часть первая", "date": "2005", "narrator": "Козий Николай", "comment": "Роман о дьяволе, посетившем Москву"}, 1, cover)
	writeAudio(t, filepath.Join(dir, "a2.mp3"), map[string]string{"album": "Мастер и Маргарита", "title": "Часть вторая"}, 1, nil)
	scanAudiobooksOK(t, s)
	b, chs := oneAudiobook(t, s.Repo)
	m := bookMeta(t, b)
	if b.Title != "Мастер и Маргарита" || b.Year != 2005 || b.Overview != "Роман о дьяволе, посетившем Москву" || m["narrator"] != "Козий Николай" {
		t.Fatalf("book %+v", b)
	}
	if a := authorsOf(t, b); len(a) != 1 || a[0] != "Булгаков Михаил" {
		t.Fatalf("authors %v", a)
	}
	if got := strings.Join(chapterTitles(chs), "|"); got != "Часть первая|Часть вторая" {
		t.Fatalf("chapters %s", got)
	}
	if b.Poster != coverName(cover, "jpg") {
		t.Fatalf("poster %q", b.Poster)
	}
	if _, err := os.Stat(filepath.Join(s.ArtDir, b.Poster)); err != nil {
		t.Fatal(err)
	}
	// same title tag in every file: names give the chapters; composer is the narrator
	s2, root2 := audiobookScanner(t)
	dir2 := filepath.Join(root2, "Книга")
	for _, n := range []string{"01 - Начало", "02 - Конец"} {
		writeAudio(t, filepath.Join(dir2, n+".mp3"), map[string]string{"album": "Книга", "title": "Книга", "composer": "Чтец Иван"}, 1, nil)
	}
	scanAudiobooksOK(t, s2)
	b2, chs2 := oneAudiobook(t, s2.Repo)
	if got := strings.Join(chapterTitles(chs2), "|"); got != "Начало|Конец" || bookMeta(t, b2)["narrator"] != "Чтец Иван" {
		t.Fatalf("chapters %s, book %+v", got, b2)
	}
}

func TestAudiobookSidecars(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Автор", "Книга")
	junkAudio(t, filepath.Join(dir, "01.mp3"))
	writeBytes(t, filepath.Join(dir, "desc.txt"), []byte("  Описание книги.\nВторая строка.\n"))
	writeBytes(t, filepath.Join(dir, "reader.txt"), []byte("Иванов Иван\n"))
	writeBytes(t, filepath.Join(dir, "poster.png"), coverJPEG(t)) // the only image
	scanAudiobooksOK(t, s)
	b, chs := oneAudiobook(t, s.Repo)
	if b.Overview != "Описание книги.\nВторая строка." || bookMeta(t, b)["narrator"] != "Иванов Иван" {
		t.Fatalf("book %+v", b)
	}
	if b.Poster != "file:"+filepath.Join(dir, "poster.png") || len(chs) != 1 || chs[0].Title != "Глава 1" {
		t.Fatalf("book %+v chapters %v", b, chapterTitles(chs))
	}
}

func TestRescanAudiobooksIdempotent(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Автор", "Книга")
	for _, n := range []string{"1.mp3", "2.mp3", "3.mp3"} {
		junkAudio(t, filepath.Join(dir, n))
	}
	scanAudiobooksOK(t, s)
	b1, chs1 := oneAudiobook(t, s.Repo)
	st := scanAudiobooksOK(t, s)
	b2, chs2 := oneAudiobook(t, s.Repo)
	if b1.ID != b2.ID || b1.MetaJSON != b2.MetaJSON || fmt.Sprint(chs1) != fmt.Sprint(chs2) || st.Removed != 0 {
		t.Fatalf("rescan changed: %+v %+v %v %v", b1, b2, chs1, chs2)
	}
	// a removed file: the chapters are rewritten, the book stays
	if err := os.Remove(filepath.Join(dir, "2.mp3")); err != nil {
		t.Fatal(err)
	}
	st = scanAudiobooksOK(t, s)
	b3, chs3 := oneAudiobook(t, s.Repo)
	if b3.ID != b1.ID || st.Removed != 1 || strings.Join(chapterTitles(chs3), "|") != "Глава 1|Глава 3" {
		t.Fatalf("after removal %+v %v %+v", b3, chapterTitles(chs3), st)
	}
	// the whole folder renamed: the same book is found by its files
	if err := os.Rename(filepath.Join(root, "Автор"), filepath.Join(root, "Другой автор")); err != nil {
		t.Fatal(err)
	}
	scanAudiobooksOK(t, s)
	books := listKind(t, s.Repo, KindAudiobook)
	if len(books) != 1 || authorsOf(t, books[0])[0] != "Другой автор" {
		t.Fatalf("after rename %v", titles(books))
	}
}

func TestAudiobooksAndMusicStaySeparate(t *testing.T) {
	s, root := audiobookScanner(t)
	music := filepath.Join(filepath.Dir(filepath.Dir(root)), "music")
	s.Music = music
	writeAudio(t, filepath.Join(root, "Автор", "Книга", "01.mp3"), map[string]string{"album": "Книга", "artist": "Автор"}, 1, nil)
	writeAudio(t, filepath.Join(music, "Кино - Группа крови (1988)", "01.mp3"), map[string]string{"album": "Группа крови", "artist": "Кино"}, 1, nil)
	st := scanAudiobooksOK(t, s)
	if st.Audiobooks != 1 || st.Tracks != 1 || len(listKind(t, s.Repo, KindAudiobook)) != 1 || len(listKind(t, s.Repo, KindTrack)) != 1 {
		t.Fatalf("stats %+v", st)
	}
	// a missing audiobooks root is an error and prunes nothing
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanAll(context.Background()); err == nil || !strings.Contains(err.Error(), "audiobooks") {
		t.Fatalf("err %v", err)
	}
	if b := listKind(t, s.Repo, KindAudiobook); len(b) != 1 {
		t.Fatalf("pruned %v", titles(b))
	}
}

func TestAudiobookSeriesPrefixes(t *testing.T) {
	for i, name := range []string{"Книга 3 - Полдень", "Vol. 2 - Полдень", "#3 - Полдень", "03. Полдень", "Том 2. Полдень"} {
		s, root := audiobookScanner(t)
		junkAudio(t, filepath.Join(root, "Стругацкие", "Мир Полудня", name, "01.mp3"))
		scanAudiobooksOK(t, s)
		b, _ := oneAudiobook(t, s.Repo)
		m := bookMeta(t, b)
		want := []float64{3, 2, 3, 3, 2}[i]
		if a := authorsOf(t, b); b.Title != "Полдень" || len(a) != 1 || a[0] != "Стругацкие" || m["series"] != "Мир Полудня" || m["series_index"] != want {
			t.Fatalf("%q → %q %v %v", name, b.Title, a, m)
		}
	}
}

func TestAudiobookM4BParts(t *testing.T) {
	s, root := audiobookScanner(t)
	junkAudio(t, filepath.Join(root, "Автор", "Книга", "Part2.m4b"))
	junkAudio(t, filepath.Join(root, "Автор", "Книга", "Part1.m4b"))
	junkAudio(t, filepath.Join(root, "Автор", "Солярис.m4b"))
	junkAudio(t, filepath.Join(root, "Лем - Непобедимый [Чтец]", "Часть 1.m4b"))
	junkAudio(t, filepath.Join(root, "Лем - Непобедимый [Чтец]", "Часть 2.m4b"))
	junkAudio(t, filepath.Join(root, "Пикник", "Пикник.m4b")) // title folder in the root
	scanAudiobooksOK(t, s)
	books := listKind(t, s.Repo, KindAudiobook)
	if got := strings.Join(titles(books), "|"); got != "Книга|Непобедимый|Пикник|Солярис" {
		t.Fatalf("audiobooks %s", got)
	}
	want := map[string][2]any{"Книга": {"Автор", 2}, "Непобедимый": {"Лем", 2}, "Пикник": {"", 1}, "Солярис": {"Автор", 1}}
	for _, b := range books {
		chs, _ := s.Repo.Chapters(context.Background(), b.ID)
		a := strings.Join(authorsOf(t, b), ",")
		if w := want[b.Title]; a != w[0] || len(chs) != w[1] {
			t.Fatalf("%s: authors %q chapters %v", b.Title, a, chapterTitles(chs))
		}
		if b.Title == "Книга" {
			if p := filePathByID(t, s.Repo, b.ID, chs[0].FileID); filepath.Base(p) != "Part1.m4b" {
				t.Fatalf("first part %s", p)
			}
		}
	}
}

func TestAudiobookRescanSkipsUnchanged(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Автор", "Книга")
	for _, n := range []string{"01.mp3", "02.mp3"} {
		writeAudio(t, filepath.Join(dir, n), map[string]string{"album": "Книга", "artist": "Автор"}, 1, nil)
	}
	junkAudio(t, filepath.Join(root, "Другой", "Книга 2", "01.mp3"))
	scanAudiobooksOK(t, s)
	b1, chs1 := oneAudiobookTitled(t, s.Repo, "Книга")
	before := bookTagReads.Load()
	st := scanAudiobooksOK(t, s)
	if n := bookTagReads.Load() - before; n != 0 || st.Audiobooks != 2 || st.Files != 3 || st.Removed != 0 {
		t.Fatalf("unchanged rescan read %d tags, stats %+v", n, st)
	}
	b2, chs2 := oneAudiobookTitled(t, s.Repo, "Книга")
	if b1.UpdatedAt != b2.UpdatedAt || fmt.Sprint(chs1) != fmt.Sprint(chs2) {
		t.Fatalf("rewritten %+v %+v", b1, b2)
	}
	// a new sidecar is a change: only that book is read again
	writeBytes(t, filepath.Join(dir, "reader.txt"), []byte("Чтец"))
	before = bookTagReads.Load()
	scanAudiobooksOK(t, s)
	b3, _ := oneAudiobookTitled(t, s.Repo, "Книга")
	if n := bookTagReads.Load() - before; n != 2 || bookMeta(t, b3)["narrator"] != "Чтец" {
		t.Fatalf("changed rescan read %d tags, %v", n, b3.MetaJSON)
	}
}

func oneAudiobookTitled(t *testing.T, r *Repo, title string) (Item, []Chapter) {
	t.Helper()
	for _, b := range listKind(t, r, KindAudiobook) {
		if b.Title == title {
			chs, err := r.Chapters(context.Background(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			return b, chs
		}
	}
	t.Fatalf("no audiobook %q", title)
	return Item{}, nil
}

func TestAudiobookChaptersRetriedAfterNoProbe(t *testing.T) {
	s, root := audiobookScanner(t)
	p := filepath.Join(root, "Стругацкие", "Пикник на обочине", "Пикник на обочине.m4b")
	writeM4B(t, p, nil, []string{"Пролог", "Глава 1", "Глава 2"})
	var calls atomic.Int64
	probe := probeChapters(t, &calls)
	// no prober: the whole file is one chapter for now
	scanAudiobooksOK(t, s)
	if _, chs := oneAudiobook(t, s.Repo); len(chs) != 1 {
		t.Fatalf("chapters %v", chapterTitles(chs))
	}
	// a failing prober: still one chapter, and no "probed" mark
	s.Chapters = func(ctx context.Context, p string) ([]ChapterInput, error) {
		calls.Add(1)
		return nil, fmt.Errorf("ffprobe timed out")
	}
	scanAudiobooksOK(t, s)
	if b, chs := oneAudiobook(t, s.Repo); len(chs) != 1 || bookMeta(t, b)["chapters_probed"] != nil || calls.Load() != 1 {
		t.Fatalf("after failure %v %s", chapterTitles(chs), b.MetaJSON)
	}
	// a working prober is tried again and its result kept
	s.Chapters = probe
	scanAudiobooksOK(t, s)
	scanAudiobooksOK(t, s)
	if _, chs := oneAudiobook(t, s.Repo); strings.Join(chapterTitles(chs), "|") != "Пролог|Глава 1|Глава 2" || calls.Load() != 2 {
		t.Fatalf("after retry %v, %d probes", chapterTitles(chs), calls.Load())
	}
	// a new cover changes the book but not the file: chapters are reused
	writeBytes(t, filepath.Join(filepath.Dir(p), "cover.jpg"), coverJPEG(t))
	scanAudiobooksOK(t, s)
	if b, chs := oneAudiobook(t, s.Repo); len(chs) != 3 || calls.Load() != 2 || !strings.HasSuffix(b.Poster, "cover.jpg") {
		t.Fatalf("after cover %v, %d probes, %q", chapterTitles(chs), calls.Load(), b.Poster)
	}
}

func TestAudiobookM4BPartsKeepInnerChapters(t *testing.T) {
	s, root := audiobookScanner(t)
	var calls atomic.Int64
	s.Chapters = probeChapters(t, &calls)
	dir := filepath.Join(root, "Автор", "Книга")
	writeM4B(t, filepath.Join(dir, "Part1.m4b"), nil, []string{"Пролог", "Глава 1"})
	writeM4B(t, filepath.Join(dir, "Part2.m4b"), nil, nil)
	scanAudiobooksOK(t, s)
	_, chs := oneAudiobook(t, s.Repo)
	if got := strings.Join(chapterTitles(chs), "|"); got != "Пролог|Глава 1|Part2" || chs[2].FileID == chs[0].FileID || chs[1].StartMs != 1000 {
		t.Fatalf("chapters %s %+v", got, chs)
	}
}

// id3v23 builds an ID3v2.3 tag of text frames in encoding 0 (ISO-8859-1)
// holding raw bytes, followed by a little junk audio.
func id3v23(frames map[string][]byte) []byte {
	var body []byte
	ids := make([]string, 0, len(frames))
	for id := range frames {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		data := append([]byte{0}, frames[id]...)
		n := len(data)
		body = append(body, id...)
		body = append(body, byte(n>>24), byte(n>>16), byte(n>>8), byte(n), 0, 0)
		body = append(body, data...)
	}
	n := len(body)
	out := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	out = append(out, body...)
	return append(out, make([]byte, 64)...)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// id3v1 is 2 KB of junk audio followed by an ID3v1 tag with raw bytes.
func id3v1(title, artist, album []byte) []byte {
	field := func(b []byte, n int) []byte { return append(append([]byte{}, b...), make([]byte, n-len(b))...) }
	out := make([]byte, 2048)
	out = append(out, "TAG"...)
	out = append(out, field(title, 30)...)
	out = append(out, field(artist, 30)...)
	out = append(out, field(album, 30)...)
	out = append(out, "2001"...)
	out = append(out, field(nil, 30)...)
	return append(out, 255)
}

func cp1251(t *testing.T, s string) []byte {
	t.Helper()
	b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAudiobookCP1251Tags(t *testing.T) {
	s, root := audiobookScanner(t)
	writeBytes(t, filepath.Join(root, "Книга", "01.mp3"), id3v23(map[string][]byte{
		"TALB": cp1251(t, "Война и мир"), "TPE1": cp1251(t, "Толстой Лев"), "TIT2": cp1251(t, "Том первый")}))
	writeBytes(t, filepath.Join(root, "Другая", "01.mp3"), id3v1(cp1251(t, "Глава"), cp1251(t, "Чехов"), cp1251(t, "Рассказы")))
	// unreadable after repair: the folder name wins
	writeBytes(t, filepath.Join(root, "Третья книга", "01.mp3"), id3v23(map[string][]byte{"TALB": []byte("\x01\x02\x03abc")}))
	scanAudiobooksOK(t, s)
	books := listKind(t, s.Repo, KindAudiobook)
	if got := strings.Join(titles(books), "|"); got != "Война и мир|Рассказы|Третья книга" {
		t.Fatalf("audiobooks %s", got)
	}
	for _, b := range books {
		want := map[string]string{"Война и мир": "Толстой Лев", "Рассказы": "Чехов"}[b.Title]
		if a := strings.Join(authorsOf(t, b), ","); a != want {
			t.Fatalf("%s authors %q", b.Title, a)
		}
	}
}

func TestFixTagText(t *testing.T) {
	for in, want := range map[string]string{
		"Âîéíà è ìèð":              "Война и мир", // CP1251 read as Latin-1
		"Café":                     "Café",
		"Beyoncé":                  "Beyoncé",
		"Été":                      "Été",
		"Ölçü":                     "Ölçü",
		"2×2":                      "2×2",
		"Mötley Crüe":              "Mötley Crüe",
		"Ìèð":                      "Мир",
		"×åõîâ":                    "Чехов",
		"Ìàñòåð è Ìàðãàðèòà":       "Мастер и Маргарита",
		"Война":                    "Война",
		"ABBA\x00":                 "ABBA",
		string(cp1251(t, "Чехов")): "Чехов", // raw CP1251 bytes
		"a\x01b":                   "",
	} {
		if got := fixTagText(in); got != want {
			t.Errorf("fixTagText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAudiobookChapterNumbersFromZero(t *testing.T) {
	s, root := audiobookScanner(t)
	for _, n := range []string{"00 - Вступление.mp3", "01.mp3", "02.mp3"} {
		junkAudio(t, filepath.Join(root, "Книга", n))
	}
	scanAudiobooksOK(t, s)
	if _, chs := oneAudiobook(t, s.Repo); strings.Join(chapterTitles(chs), "|") != "Вступление|Глава 1|Глава 2" {
		t.Fatalf("chapters %v", chapterTitles(chs))
	}
}

func TestAudiobookSidecarsBeatTagsAndNarratorFilters(t *testing.T) {
	s, root := audiobookScanner(t)
	dir := filepath.Join(root, "Автор", "Книга")
	writeAudio(t, filepath.Join(dir, "01.mp3"), map[string]string{"album": "Книга", "narrator": "Из тега", "comment": "Длинное описание из тега, которое проиграет"}, 1, nil)
	writeBytes(t, filepath.Join(dir, "desc.txt"), []byte("Описание из файла"))
	writeBytes(t, filepath.Join(dir, "reader.txt"), []byte("Чтец из файла"))
	// composer = author is not the narrator; the folder's [Чтец] is; a URL comment is no description
	writeAudio(t, filepath.Join(root, "Лем Станислав - Солярис [Ким Валерий, 2010]", "01.mp3"),
		map[string]string{"album": "Солярис", "artist": "Лем Станислав", "composer": "лем станислав", "comment": "www.abook-club.ru"}, 1, nil)
	scanAudiobooksOK(t, s)
	b, _ := oneAudiobookTitled(t, s.Repo, "Книга")
	if b.Overview != "Описание из файла" || bookMeta(t, b)["narrator"] != "Чтец из файла" {
		t.Fatalf("sidecars lost: %+v", b)
	}
	b2, _ := oneAudiobookTitled(t, s.Repo, "Солярис")
	if b2.Overview != "" || bookMeta(t, b2)["narrator"] != "Ким Валерий" {
		t.Fatalf("narrator/description %+v", b2)
	}
}

func TestAudiobookMixedNamesOneBook(t *testing.T) {
	for _, names := range [][]string{
		{"Пролог.mp3", "Глава первая.mp3", "Эпилог.mp3"},
		{"01 Пролог.mp3", "02 Глава.mp3", "Эпилог.mp3"},
	} {
		s, root := audiobookScanner(t)
		for _, n := range names {
			junkAudio(t, filepath.Join(root, "Мастер и Маргарита", n))
		}
		scanAudiobooksOK(t, s)
		b, chs := oneAudiobook(t, s.Repo)
		if b.Title != "Мастер и Маргарита" || len(chs) != 3 {
			t.Fatalf("%v: %q %v", names, b.Title, chapterTitles(chs))
		}
	}
}
