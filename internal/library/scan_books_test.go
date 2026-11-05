package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
	"github.com/antresol-app/antresol/internal/library/bookmeta/bookmetatest"
)

// bookScanner returns a scanner with only an ebooks root (and an ArtDir).
func bookScanner(t *testing.T) (*Scanner, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "books", "ebooks")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Scanner{Repo: newRepo(t), Ebooks: root, ArtDir: filepath.Join(base, "images")}, root
}

func scanOK(t *testing.T, s *Scanner) ScanStats {
	t.Helper()
	st, err := s.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return st
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func coverJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 3)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func bookMeta(t *testing.T, it Item) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
		t.Fatalf("meta_json %q: %v", it.MetaJSON, err)
	}
	return m
}

func authorsOf(t *testing.T, it Item) []string {
	t.Helper()
	var out []string
	if a, ok := bookMeta(t, it)["authors"].([]any); ok {
		for _, v := range a {
			out = append(out, v.(string))
		}
	}
	return out
}

func oneBook(t *testing.T, r *Repo) (Item, []File) {
	t.Helper()
	books := listKind(t, r, KindBook)
	if len(books) != 1 {
		t.Fatalf("books: %v", titles(books))
	}
	files, err := r.FilesOf(context.Background(), books[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return books[0], files
}

func TestParseBookFile(t *testing.T) {
	cases := []struct{ name, author, title string }{
		{"Стругацкие - Пикник на обочине.epub", "Стругацкие", "Пикник на обочине"},
		{"Пикник на обочине.fb2", "", "Пикник на обочине"},
		{"Пикник_на_обочине.401234.fb2.zip", "", "Пикник на обочине"},
		{"Пикник на обочине_.401234.FB2.ZIP", "", "Пикник на обочине"},
		{"Толстой - Война и мир - Том 1.pdf", "Толстой", "Война и мир - Том 1"},
		{"1984.epub", "", "1984"},
	}
	for _, c := range cases {
		a, ti := parseBookFile(c.name)
		if a != c.author || ti != c.title {
			t.Errorf("parseBookFile(%q) = (%q, %q), want (%q, %q)", c.name, a, ti, c.author, c.title)
		}
	}
	for name, want := range map[string]string{"a.FB2.zip": ".fb2.zip", "a.EPUB": ".epub", "a.zip": ".zip", "a.fb2": ".fb2"} {
		if got := bookExt(name); got != want {
			t.Errorf("bookExt(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestScanBrokenBookVisible(t *testing.T) {
	s, root := bookScanner(t)
	writeBytes(t, filepath.Join(root, "Булгаков", "Мастер и Маргарита.epub"), bytes.Repeat([]byte("x"), 100))
	st, err := s.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if st.Books != 1 || st.Files != 1 {
		t.Fatalf("stats %+v", st)
	}
	b, files := oneBook(t, s.Repo)
	if b.Kind != KindBook || b.Title != "Мастер и Маргарита" || len(files) != 1 {
		t.Fatalf("book %+v files %d", b, len(files))
	}
	if a := authorsOf(t, b); !reflect.DeepEqual(a, []string{"Булгаков"}) {
		t.Fatalf("authors %q", a)
	}
	found, err := s.Repo.SearchKinds(context.Background(), "булгаков", []string{KindBook}, 10, "")
	if err != nil || len(found) != 1 {
		t.Fatalf("search by author: %v %v", titles(found), err)
	}
}

func TestScanBookAuthorNotFromRoot(t *testing.T) {
	s, root := bookScanner(t)
	writeBytes(t, filepath.Join(root, "Мастер и Маргарита.pdf"), []byte("%PDF-1.4"))
	scanOK(t, s)
	b, _ := oneBook(t, s.Repo)
	if a := authorsOf(t, b); len(a) != 0 {
		t.Fatalf("authors from the root folder: %q", a)
	}
}

func TestScanBookFormatsMerged(t *testing.T) {
	s, root := bookScanner(t)
	dir := filepath.Join(root, "Фантастика")
	cover := coverJPEG(t)
	epubMeta := bookmeta.Meta{Title: "Пикник на обочине", Authors: []string{"Аркадий Стругацкий", "Борис Стругацкий"}, Lang: "ru"}
	fb2Meta := bookmeta.Meta{Title: "Пикник на обочине (сборник)", Authors: []string{"А. Стругацкий"}, Series: "Миры братьев Стругацких",
		SeriesIndex: 3, ISBN: "5-17-001234-5", Publisher: "АСТ", Year: 1997, Description: "Сталкеры и Зона", Cover: cover, CoverType: "jpg"}
	writeBytes(t, filepath.Join(dir, "Пикник на обочине.epub"), bookmetatest.EPUB(t, epubMeta))
	writeBytes(t, filepath.Join(dir, "Пикник на обочине.fb2"), bookmetatest.FB2(t, fb2Meta, "windows-1251"))
	writeBytes(t, filepath.Join(dir, "Пикник_на_обочине.401234.fb2.zip"),
		bookmetatest.FB2Zip(t, "Пикник_на_обочине.fb2", bookmetatest.FB2(t, fb2Meta, "utf-8")))
	writeBytes(t, filepath.Join(dir, "Другая книга.pdf"), []byte("%PDF"))

	st := scanOK(t, s)
	if st.Books != 2 || st.Files != 4 {
		t.Fatalf("stats %+v", st)
	}
	books := listKind(t, s.Repo, KindBook)
	if len(books) != 2 {
		t.Fatalf("books %v", titles(books))
	}
	var b Item
	for _, it := range books {
		if it.Title == "Пикник на обочине" {
			b = it
		}
	}
	if b.ID == 0 {
		t.Fatalf("no merged book: %v", titles(books))
	}
	files, _ := s.Repo.FilesOf(context.Background(), b.ID)
	if len(files) != 3 {
		t.Fatalf("files %d", len(files))
	}
	if a := authorsOf(t, b); !reflect.DeepEqual(a, []string{"Аркадий Стругацкий", "Борис Стругацкий"}) {
		t.Fatalf("authors %q", a)
	}
	// empty fields of the EPUB are filled from the FB2
	m := bookMeta(t, b)
	if m["series"] != "Миры братьев Стругацких" || m["series_index"] != 3.0 || m["isbn"] != "5-17-001234-5" ||
		m["publisher"] != "АСТ" || m["lang"] != "ru" {
		t.Fatalf("meta %v", m)
	}
	if b.Year != 1997 || b.Overview != "Сталкеры и Зона" {
		t.Fatalf("year/overview %d %q", b.Year, b.Overview)
	}
	if b.Poster != coverName(cover, "jpg") {
		t.Fatalf("poster %q", b.Poster)
	}
	if _, err := os.Stat(filepath.Join(s.ArtDir, b.Poster)); err != nil {
		t.Fatalf("cover not saved: %v", err)
	}
	if k := matchKey(t, s.Repo, b.ID); k != dir+"|пикник на обочине" {
		t.Fatalf("match_key %q", k)
	}
	for _, q := range []string{"Борис Стругацкий", "Миры братьев"} {
		found, err := s.Repo.SearchKinds(context.Background(), q, []string{KindBook}, 10, "")
		if err != nil || len(found) != 1 || found[0].ID != b.ID {
			t.Fatalf("search %q: %v %v", q, titles(found), err)
		}
	}
}

// matchKey reads items.match_key of an item.
func matchKey(t *testing.T, r *Repo, id int64) string {
	t.Helper()
	var k string
	if err := r.db.QueryRow(`SELECT COALESCE(match_key,'') FROM items WHERE id=?`, id).Scan(&k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestBookCoverSidecarOrder(t *testing.T) {
	s, root := bookScanner(t)
	dir := filepath.Join(root, "Автор")
	writeBytes(t, filepath.Join(dir, "X.pdf"), []byte("%PDF"))
	writeBytes(t, filepath.Join(dir, "cover.jpg"), []byte("c"))
	writeBytes(t, filepath.Join(dir, "X.JPG"), []byte("x"))
	writeBytes(t, filepath.Join(dir, "Y.pdf"), []byte("%PDF"))
	scanOK(t, s)
	books := listKind(t, s.Repo, KindBook)
	if len(books) != 2 {
		t.Fatalf("books %v", titles(books))
	}
	want := map[string]string{"X": "file:" + filepath.Join(dir, "X.JPG"), "Y": "file:" + filepath.Join(dir, "cover.jpg")}
	for _, b := range books {
		if b.Poster != want[b.Title] {
			t.Errorf("%s poster %q, want %q", b.Title, b.Poster, want[b.Title])
		}
	}

	// the only picture of a one-book folder
	s2, root2 := bookScanner(t)
	writeBytes(t, filepath.Join(root2, "Один", "Книга.mobi"), []byte("BOOKMOBI"))
	writeBytes(t, filepath.Join(root2, "Один", "scan 001.png"), []byte("p"))
	writeBytes(t, filepath.Join(root2, "Два", "А.mobi"), []byte("BOOKMOBI"))
	writeBytes(t, filepath.Join(root2, "Два", "Б.mobi"), []byte("BOOKMOBI"))
	writeBytes(t, filepath.Join(root2, "Два", "scan.png"), []byte("p"))
	scanOK(t, s2)
	for _, b := range listKind(t, s2.Repo, KindBook) {
		w := ""
		if b.Title == "Книга" {
			w = "file:" + filepath.Join(root2, "Один", "scan 001.png")
		}
		if b.Poster != w {
			t.Errorf("%s poster %q, want %q", b.Title, b.Poster, w)
		}
	}
}

func TestScanBooksIgnoresOther(t *testing.T) {
	s, root := bookScanner(t)
	for _, n := range []string{"a.djvu", "b.cbz", "c.jpg", "metadata.opf", "d.zip", "e.txt", ".hidden.epub", "@eaDir/f.epub"} {
		writeBytes(t, filepath.Join(root, "Папка", n), []byte("x"))
	}
	writeBytes(t, filepath.Join(root, "Папка", "Книга.AZW3"), []byte("x"))
	st := scanOK(t, s)
	if st.Books != 1 || st.Files != 1 {
		t.Fatalf("stats %+v", st)
	}
	b, files := oneBook(t, s.Repo)
	if b.Title != "Книга" || len(files) != 1 || !strings.HasSuffix(files[0].Path, "Книга.AZW3") {
		t.Fatalf("book %q files %v", b.Title, files)
	}
}

func TestRescanBooksIdempotent(t *testing.T) {
	s, root := bookScanner(t)
	cover := coverJPEG(t)
	writeBytes(t, filepath.Join(root, "Толстой", "Война и мир.fb2"), bookmetatest.FB2(t, bookmeta.Meta{
		Title: "Война и мир", Authors: []string{"Лев Николаевич Толстой"}, Cover: cover, CoverType: "jpg"}, "windows-1251"))
	writeBytes(t, filepath.Join(root, "Толстой", "Война и мир.pdf"), []byte("%PDF"))
	writeBytes(t, filepath.Join(root, "Булгаков - Морфий.epub"), []byte("broken"))
	scanOK(t, s)
	snapshot := func() string {
		var rows []string
		q, err := s.Repo.db.Query(`SELECT i.id, i.kind, i.title, i.year, COALESCE(i.poster,''), i.meta_json, COALESCE(i.match_key,''),
		  (SELECT COUNT(*) FROM item_titles WHERE item_id=i.id), (SELECT group_concat(id||path, ';') FROM files WHERE item_id=i.id)
		  FROM items i ORDER BY i.id`)
		if err != nil {
			t.Fatal(err)
		}
		defer q.Close()
		for q.Next() {
			var id, year, nt int64
			var kind, title, poster, meta, key, files string
			if err := q.Scan(&id, &kind, &title, &year, &poster, &meta, &key, &nt, &files); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, fmt.Sprint(id, kind, title, year, poster, meta, key, nt, files))
		}
		sort.Strings(rows)
		return strings.Join(rows, "\n")
	}
	first := snapshot()
	st := scanOK(t, s)
	if st.Books != 2 || st.Files != 3 || st.Removed != 0 {
		t.Fatalf("rescan stats %+v", st)
	}
	if second := snapshot(); second != first {
		t.Fatalf("rescan changed the library:\n%s\n---\n%s", first, second)
	}
	books := listKind(t, s.Repo, KindBook)
	if len(books) != 2 {
		t.Fatalf("books %v", titles(books))
	}
	for _, b := range books {
		if b.Title == "Морфий" && !reflect.DeepEqual(authorsOf(t, b), []string{"Булгаков"}) {
			t.Fatalf("Морфий authors %q", authorsOf(t, b))
		}
	}

	// an unchanged library is not read again
	reads := bookReads.Load()
	scanOK(t, s)
	if n := bookReads.Load() - reads; n != 0 {
		t.Fatalf("unchanged rescan read %d book files", n)
	}
	if third := snapshot(); third != first {
		t.Fatalf("fast rescan changed the library:\n%s\n---\n%s", first, third)
	}
	// a touched file: only its book is read again (all its formats)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "Булгаков - Морфий.epub"), later, later); err != nil {
		t.Fatal(err)
	}
	reads = bookReads.Load()
	scanOK(t, s)
	if n := bookReads.Load() - reads; n != 1 {
		t.Fatalf("one touched book: %d reads", n)
	}
	if err := os.Chtimes(filepath.Join(root, "Толстой", "Война и мир.pdf"), later, later); err != nil {
		t.Fatal(err)
	}
	reads = bookReads.Load()
	scanOK(t, s)
	if n := bookReads.Load() - reads; n != 2 {
		t.Fatalf("one touched format of a two-format book: %d reads", n)
	}
	// a new picture in the folder: the book's cover may change, read again
	writeBytes(t, filepath.Join(root, "Толстой", "Война и мир.jpg"), []byte("j"))
	reads = bookReads.Load()
	scanOK(t, s)
	if n := bookReads.Load() - reads; n != 2 {
		t.Fatalf("new sidecar: %d reads", n)
	}

	// a format removed: the book stays with the other one
	if err := os.Remove(filepath.Join(root, "Толстой", "Война и мир.pdf")); err != nil {
		t.Fatal(err)
	}
	st = scanOK(t, s)
	if st.Books != 2 || st.Removed != 1 {
		t.Fatalf("stats after removal %+v", st)
	}
	if len(listKind(t, s.Repo, KindBook)) != 2 {
		t.Fatal("book lost with one of its formats")
	}
}

func TestScanBooksMissingRootNotPruned(t *testing.T) {
	s, root := bookScanner(t)
	writeBytes(t, filepath.Join(root, "Книга.pdf"), []byte("%PDF"))
	scanOK(t, s)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanAll(context.Background()); err == nil {
		t.Fatal("missing root: no error")
	}
	if len(listKind(t, s.Repo, KindBook)) != 1 {
		t.Fatal("missing root pruned the books")
	}
}

func TestRescanBookDropsStaleScanData(t *testing.T) {
	s, root := bookScanner(t)
	dir := filepath.Join(root, "Автор")
	p := filepath.Join(dir, "Книга.fb2")
	writeBytes(t, p, bookmetatest.FB2(t, bookmeta.Meta{Title: "Книга", Authors: []string{"Иван Петров"}, Series: "Цикл", SeriesIndex: 2,
		ISBN: "5-17-001234-5"}, "utf-8"))
	writeBytes(t, filepath.Join(dir, "Книга.png"), []byte("p"))
	scanOK(t, s)
	b, _ := oneBook(t, s.Repo)
	if b.Poster != "file:"+filepath.Join(dir, "Книга.png") || bookMeta(t, b)["series"] != "Цикл" {
		t.Fatalf("first scan: %q %v", b.Poster, bookMeta(t, b))
	}
	// the file loses its series and ISBN, the sidecar is deleted
	writeBytes(t, p, bookmetatest.FB2(t, bookmeta.Meta{Title: "Книга", Authors: []string{"Иван Петров"}}, "utf-8"))
	later := time.Now().Add(time.Hour)
	os.Chtimes(p, later, later)
	if err := os.Remove(filepath.Join(dir, "Книга.png")); err != nil {
		t.Fatal(err)
	}
	scanOK(t, s)
	b, _ = oneBook(t, s.Repo)
	m := bookMeta(t, b)
	for _, k := range []string{"series", "series_index", "isbn", "scan_cover"} {
		if _, ok := m[k]; ok {
			t.Errorf("stale %s: %v", k, m)
		}
	}
	if b.Poster != "" {
		t.Errorf("stale poster %q", b.Poster)
	}
}
