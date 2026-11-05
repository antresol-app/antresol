package library

import (
	"archive/zip"
	"context"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/comics"
)

// comicScanner returns a scanner with only a comics root (and an ArtDir).
func comicScanner(t *testing.T) (*Scanner, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "books", "manga")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Scanner{Repo: newRepo(t), Comics: root, ArtDir: filepath.Join(base, "images"),
		ComicsCache: filepath.Join(base, "comics-cache")}, root
}

// writeCBZ writes a CBZ with n JPEG pages (and a ComicInfo.xml if given).
func writeCBZ(t *testing.T, path string, n int, comicInfo string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= n; i++ {
		add(filepath.Base(path)+"-"+strings.Repeat("0", 2)+string(rune('0'+i))+".jpg", testJPEG(t, color.RGBA{uint8(i * 40), 0, 0, 255}))
	}
	if comicInfo != "" {
		add("ComicInfo.xml", []byte(comicInfo))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func comicInfo(fields string) string {
	return `<?xml version="1.0" encoding="utf-8"?><ComicInfo>` + fields + `</ComicInfo>`
}

func metaOf(t *testing.T, it Item) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(it.MetaJSON), &m); err != nil {
		t.Fatalf("meta_json %q: %v", it.MetaJSON, err)
	}
	return m
}

func seriesByTitle(t *testing.T, r *Repo, title string) Item {
	t.Helper()
	items, _, err := r.List(context.Background(), ListQuery{Kind: KindComic})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Title == title {
			return it
		}
	}
	t.Fatalf("no series %q in %v", title, titles(items))
	return Item{}
}

func volumeTitles(t *testing.T, r *Repo, seriesID int64) []string {
	t.Helper()
	vols, err := r.comicVolumes(context.Background(), seriesID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range vols {
		out = append(out, v.item.Title)
	}
	return out
}

func TestParseComicNumber(t *testing.T) {
	for _, c := range []struct {
		in       string
		vol, ch  float64
		series   string
		wantName string
	}{
		{"Берсерк v01", 1, 0, "Берсерк", "Том 1"},
		{"Vol.01 Ch.001", 1, 1, "", "Том 1 · Глава 1"},
		{"Том 3", 3, 0, "", "Том 3"},
		{"Глава 12", 0, 12, "", "Глава 12"},
		{"ch.12.5", 0, 12.5, "", "Глава 12.5"},
		{"#12", 0, 12, "", "Глава 12"},
		{"Берсерк 05", 5, 0, "Берсерк", "Том 5"},
		{"One Piece v10 c0987 (2019) [Viz]", 10, 987, "One Piece", "Том 10 · Глава 987"},
		{"Naruto_-_Vol_03", 3, 0, "Naruto", "Том 3"},
		{"Наруто - Том 2 Глава 7", 2, 7, "Наруто", "Том 2 · Глава 7"},
		{"Ван-Пис т.4", 4, 0, "Ван-Пис", "Том 4"},
		{"Ходячий замок", 0, 0, "Ходячий замок", "Ходячий замок"},
		{"Batman #404", 0, 404, "Batman", "Глава 404"},
		{"Akira 1988", 0, 0, "Akira 1988", "Akira 1988"},
		{"Akira v1 1988", 1, 0, "Akira", "Том 1"},
	} {
		vol, ch := parseComicNumber(c.in)
		if vol != c.vol || ch != c.ch {
			t.Errorf("parseComicNumber(%q) = %v, %v; want %v, %v", c.in, vol, ch, c.vol, c.ch)
		}
		series, v2, c2 := parseComicFile(c.in)
		if series != c.series || v2 != c.vol || c2 != c.ch {
			t.Errorf("parseComicFile(%q) = %q, %v, %v; want %q", c.in, series, v2, c2, c.series)
		}
		if got := comicVolumeTitle(vol, ch, c.in); got != c.wantName {
			t.Errorf("title(%q) = %q, want %q", c.in, got, c.wantName)
		}
	}
}

func TestScanKavitaLayout(t *testing.T) {
	s, root := comicScanner(t)
	for _, v := range []string{"Берсерк v10", "Берсерк v02", "Берсерк v01"} {
		writeCBZ(t, filepath.Join(root, "Берсерк", v+".cbz"), 2, "")
	}
	writeCBZ(t, filepath.Join(root, "One Piece", "Vol.01 Ch.001.cbz"), 3, "")
	writeBytes(t, filepath.Join(root, "Берсерк", "cover.jpg"), testJPEG(t, color.RGBA{0, 0, 255, 255}))
	writeBytes(t, filepath.Join(root, "Берсерк", "notes.txt"), []byte("x"))
	st := scanOK(t, s)
	if st.Comics != 4 || st.Files != 4 {
		t.Fatalf("stats %+v", st)
	}
	items, total, err := s.Repo.List(context.Background(), ListQuery{Kind: KindComic})
	if err != nil || total != 2 {
		t.Fatalf("series %v %v", titles(items), err)
	}
	b := seriesByTitle(t, s.Repo, "Берсерк")
	if got := volumeTitles(t, s.Repo, b.ID); strings.Join(got, "|") != "Том 1|Том 2|Том 10" {
		t.Fatalf("order %q", got)
	}
	if m := metaOf(t, b); m["direction"] != "rtl" {
		t.Fatalf("series meta %v", m)
	}
	if mk := matchKey(t, s.Repo, b.ID); mk != filepath.Join(root, "Берсерк") {
		t.Fatalf("match_key %q", mk)
	}
	if b.Poster == "" || b.Rating != "" {
		t.Fatalf("series poster %q rating %q", b.Poster, b.Rating)
	}
	vols, _ := s.Repo.comicVolumes(context.Background(), b.ID)
	if b.Poster != vols[0].item.Poster {
		t.Fatalf("series cover %q is not the first volume's %q", b.Poster, vols[0].item.Poster)
	}
	vm := metaOf(t, vols[0].item)
	if vm["volume"] != 1.0 || vm["chapter"] != 0.0 || vm["pages"] != 2.0 || vm["direction"] != "rtl" {
		t.Fatalf("volume meta %v", vm)
	}
	op := seriesByTitle(t, s.Repo, "One Piece")
	if got := volumeTitles(t, s.Repo, op.ID); strings.Join(got, "|") != "Том 1 · Глава 1" {
		t.Fatalf("one piece %q", got)
	}
	if res, _ := s.Repo.SearchKinds(context.Background(), "берсерк", []string{KindComic}, 10, ""); len(res) != 1 {
		t.Fatalf("search %v", titles(res))
	}
}

func TestComicsInRootAndComicInfo(t *testing.T) {
	s, root := comicScanner(t)
	writeCBZ(t, filepath.Join(root, "Берсерк v01.cbz"), 1, comicInfo(`<Series>Berserk</Series><Manga>No</Manga>
	  <Writer>Кэнтаро Миура</Writer><Summary>Гатс.</Summary><Year>1990</Year><LanguageISO>ru</LanguageISO><Title>Чёрный мечник</Title>`))
	writeCBZ(t, filepath.Join(root, "Берсерк v02.cbz"), 1, "")
	writeCBZ(t, filepath.Join(root, "Акира.cbz"), 1, comicInfo(`<Title>Акира</Title><Manga>Yes</Manga>`))
	scanOK(t, s)
	b := seriesByTitle(t, s.Repo, "Berserk") // ComicInfo.Series of the first volume names the series
	if mk := matchKey(t, s.Repo, b.ID); mk != root+"|"+"берсерк" {
		t.Fatalf("match_key %q", mk)
	}
	m := metaOf(t, b)
	if m["direction"] != "ltr" || m["writer"] != "Кэнтаро Миура" || b.Year != 1990 || b.Overview != "Гатс." {
		t.Fatalf("series %+v %v", b, m)
	}
	vols, _ := s.Repo.comicVolumes(context.Background(), b.ID)
	if len(vols) != 2 || metaOf(t, vols[0].item)["lang"] != "ru" || metaOf(t, vols[1].item)["direction"] != "rtl" {
		t.Fatalf("volumes %+v", vols)
	}
	a := seriesByTitle(t, s.Repo, "Акира")
	if got := volumeTitles(t, s.Repo, a.ID); len(got) != 1 || got[0] != "Акира" {
		t.Fatalf("one-shot %q", got)
	}
	if metaOf(t, a)["direction"] != "rtl" { // Manga=Yes: the default applies
		t.Fatal("direction for Manga=Yes")
	}
	// the volume cover is the first page, saved in ArtDir
	if p := vols[0].item.Poster; p == "" || strings.HasPrefix(p, "file:") {
		t.Fatalf("cover %q", p)
	} else if _, err := os.Stat(filepath.Join(s.ArtDir, p)); err != nil {
		t.Fatal(err)
	}
}

func TestImageFolderVolume(t *testing.T) {
	s, root := comicScanner(t)
	dir := filepath.Join(root, "Берсерк", "Том 1")
	var size int64
	for _, n := range []string{"001.jpg", "002.jpg", "010.png"} {
		b := testJPEG(t, color.RGBA{1, 2, 3, 255})
		writeBytes(t, filepath.Join(dir, n), b)
		size += int64(len(b))
	}
	writeBytes(t, filepath.Join(dir, "Thumbs.db"), []byte("junk"))
	newest := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(dir, "002.jpg"), newest, newest); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"001.jpg", "010.png"} {
		old := newest.Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(dir, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	writeBytes(t, filepath.Join(root, "Берсерк", "cover.jpg"), testJPEG(t, color.RGBA{9, 9, 9, 255})) // series art, not a volume
	writeCBZ(t, filepath.Join(root, "Берсерк", "Том 2.cbz"), 1, "")
	st := scanOK(t, s)
	if st.Comics != 2 {
		t.Fatalf("stats %+v", st)
	}
	b := seriesByTitle(t, s.Repo, "Берсерк")
	vols, err := s.Repo.comicVolumes(context.Background(), b.ID)
	if err != nil || len(vols) != 2 {
		t.Fatalf("%+v %v", vols, err)
	}
	fs, err := s.Repo.FilesOf(context.Background(), vols[0].item.ID)
	if err != nil || len(fs) != 1 {
		t.Fatalf("files %+v %v", fs, err)
	}
	if fs[0].Path != dir || fs[0].Size != size || fs[0].Mtime != newest.Unix() {
		t.Fatalf("folder row %+v (want size %d mtime %d)", fs[0], size, newest.Unix())
	}
	if metaOf(t, vols[0].item)["pages"] != 3.0 || vols[0].item.Title != "Том 1" {
		t.Fatalf("volume %+v", vols[0].item)
	}
	if p := vols[0].item.Poster; p != "file:"+filepath.Join(dir, "001.jpg") {
		t.Fatalf("folder cover %q", p)
	}
	// the background probe never sees the folder
	pending, err := s.Repo.FilesWithoutProbe(context.Background(), 100)
	if err != nil || len(pending) != 0 {
		t.Fatalf("probe %+v %v", pending, err)
	}
	// a page removed: the row follows
	if err := os.Remove(filepath.Join(dir, "010.png")); err != nil {
		t.Fatal(err)
	}
	scanOK(t, s)
	fs, _ = s.Repo.FilesOf(context.Background(), vols[0].item.ID)
	if len(fs) != 1 || fs[0].Size >= size {
		t.Fatalf("after removing a page %+v", fs)
	}
}

func TestComicAgeRatingHiddenFromKid(t *testing.T) {
	s, root := comicScanner(t)
	writeCBZ(t, filepath.Join(root, "Взрослое", "v01.cbz"), 1, comicInfo(`<AgeRating>Teen</AgeRating>`))
	writeCBZ(t, filepath.Join(root, "Взрослое", "v02.cbz"), 1, comicInfo(`<AgeRating>Adults Only 18+</AgeRating>`))
	writeCBZ(t, filepath.Join(root, "Детское", "v01.cbz"), 1, comicInfo(`<AgeRating>Everyone</AgeRating>`))
	scanOK(t, s)
	ctx := context.Background()
	adult := seriesByTitle(t, s.Repo, "Взрослое")
	if adult.Rating != "18+" {
		t.Fatalf("series rating %q (max of volumes)", adult.Rating)
	}
	for _, kind := range []string{KindComic, KindComicVolume} {
		items, _, err := s.Repo.List(ctx, ListQuery{Kind: kind, MaxRating: "12+"})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.ID == adult.ID || it.ParentID == adult.ID {
				t.Fatalf("kid sees %s %q", kind, it.Title)
			}
		}
		if len(items) != 1 {
			t.Fatalf("kid %s: %v", kind, titles(items))
		}
	}
	if res, _ := s.Repo.SearchKinds(ctx, "взрослое", []string{KindComic, KindComicVolume}, 10, "12+"); len(res) != 0 {
		t.Fatalf("kid search %v", titles(res))
	}
	all, _, _ := s.Repo.List(ctx, ListQuery{Kind: KindComicVolume})
	if len(all) != 3 {
		t.Fatalf("admin %v", titles(all))
	}
}

func TestScanBrokenComicVisible(t *testing.T) {
	s, root := comicScanner(t)
	writeBytes(t, filepath.Join(root, "Битое", "Битое v03.cbr"), []byte("not a rar"))
	writeBytes(t, filepath.Join(root, "Битое", "Битое v04.cbz"), []byte("PK\x03\x04 broken"))
	st := scanOK(t, s)
	if st.Comics != 2 {
		t.Fatalf("stats %+v", st)
	}
	b := seriesByTitle(t, s.Repo, "Битое")
	if got := volumeTitles(t, s.Repo, b.ID); strings.Join(got, "|") != "Том 3|Том 4" {
		t.Fatalf("%q", got)
	}
	vols, _ := s.Repo.comicVolumes(context.Background(), b.ID)
	if m := metaOf(t, vols[0].item); m["pages"] != 0.0 || m["direction"] != "rtl" || vols[0].item.Poster != "" {
		t.Fatalf("broken volume %v %q", m, vols[0].item.Poster)
	}
}

func TestScanComicsFixtures(t *testing.T) {
	s, root := comicScanner(t)
	for src, dst := range map[string]string{"rar5.cbr": "Серия/v01.cbr", "three.cb7": "Серия/v02.cb7"} {
		b, err := os.ReadFile(filepath.Join("..", "comics", "testdata", src))
		if err != nil {
			t.Fatal(err)
		}
		writeBytes(t, filepath.Join(root, dst), b)
	}
	scanOK(t, s)
	b := seriesByTitle(t, s.Repo, "Серия")
	vols, _ := s.Repo.comicVolumes(context.Background(), b.ID)
	if len(vols) != 2 || metaOf(t, vols[0].item)["pages"] != 3.0 || metaOf(t, vols[1].item)["pages"] != 3.0 || vols[1].item.Poster == "" {
		t.Fatalf("%+v", vols)
	}
	if es, _ := os.ReadDir(s.ComicsCache); len(es) != 0 {
		t.Fatal("the scanner must not unpack archives")
	}
}

// An unchanged comics root is not read again: no archive is opened and the
// rows stay as they are. A changed volume is read again, a removed one
// moves the series cover to the next volume.
func TestRescanComicsFastPath(t *testing.T) {
	s, root := comicScanner(t)
	var peeks atomic.Int64
	orig := peekComic
	peekComic = func(p string) ([]string, comics.ComicInfo, bool, []byte, error) {
		peeks.Add(1)
		return orig(p)
	}
	t.Cleanup(func() { peekComic = orig })
	for _, v := range []string{"v01", "v02", "v03"} {
		writeCBZ(t, filepath.Join(root, "Берсерк", v+".cbz"), 1, "")
	}
	writeCBZ(t, filepath.Join(root, "Акира", "Акира.cbz"), 2, "")
	scanOK(t, s)
	if peeks.Load() != 4 {
		t.Fatalf("first scan peeks %d", peeks.Load())
	}
	snap := func() string {
		var sb strings.Builder
		rows, err := s.Repo.db.Query(`SELECT id, kind, COALESCE(parent_id,0), title, rating, COALESCE(poster,''), meta_json, updated_at FROM items ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, c, d, e, f, g, h string
			if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g, &h); err != nil {
				t.Fatal(err)
			}
			sb.WriteString(strings.Join([]string{a, b, c, d, e, f, g, h}, "|") + "\n")
		}
		return sb.String()
	}
	before := snap()
	peeks.Store(0)
	st := scanOK(t, s)
	if peeks.Load() != 0 || st.Comics != 4 || st.Removed != 0 {
		t.Fatalf("unchanged rescan: peeks %d stats %+v", peeks.Load(), st)
	}
	if after := snap(); after != before {
		t.Fatalf("rows changed:\n%s\n%s", before, after)
	}
	// one volume replaced: only it is read again
	p := filepath.Join(root, "Берсерк", "v02.cbz")
	writeCBZ(t, p, 4, "")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	peeks.Store(0)
	scanOK(t, s)
	b := seriesByTitle(t, s.Repo, "Берсерк")
	vols, _ := s.Repo.comicVolumes(context.Background(), b.ID)
	if peeks.Load() != 1 || len(vols) != 3 || metaOf(t, vols[1].item)["pages"] != 4.0 {
		t.Fatalf("changed volume: peeks %d vols %+v", peeks.Load(), vols)
	}
	// the first volume removed: the series cover is now volume 2's
	if err := os.Remove(filepath.Join(root, "Берсерк", "v01.cbz")); err != nil {
		t.Fatal(err)
	}
	peeks.Store(0)
	st = scanOK(t, s)
	b = seriesByTitle(t, s.Repo, "Берсерк")
	vols, _ = s.Repo.comicVolumes(context.Background(), b.ID)
	if peeks.Load() != 0 || st.Removed != 1 || len(vols) != 2 || b.Poster != vols[0].item.Poster || vols[0].item.Title != "Том 2" {
		t.Fatalf("removed volume: peeks %d stats %+v poster %q vols %+v", peeks.Load(), st, b.Poster, vols)
	}
	// a volume moved to another series folder follows it
	if err := os.Rename(filepath.Join(root, "Берсерк", "v03.cbz"), filepath.Join(root, "Акира", "v03.cbz")); err != nil {
		t.Fatal(err)
	}
	scanOK(t, s)
	a := seriesByTitle(t, s.Repo, "Акира")
	if got := volumeTitles(t, s.Repo, a.ID); len(got) != 2 {
		t.Fatalf("moved %q", got)
	}
}

func TestComicsMissingRootNotPruned(t *testing.T) {
	s, root := comicScanner(t)
	writeCBZ(t, filepath.Join(root, "Берсерк", "v01.cbz"), 1, "")
	scanOK(t, s)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanAll(context.Background()); err == nil {
		t.Fatal("a missing root is an error")
	}
	if items, _, _ := s.Repo.List(context.Background(), ListQuery{Kind: KindComicVolume}); len(items) != 1 {
		t.Fatalf("pruned %v", titles(items))
	}
}

// A volume follows its series in the rating filter, whatever its own
// items.rating says (an enricher or an edit may set one).
func TestComicVolumeRatingFollowsSeries(t *testing.T) {
	s, root := comicScanner(t)
	writeCBZ(t, filepath.Join(root, "Взрослое", "v01.cbz"), 1, comicInfo(`<AgeRating>Adults Only 18+</AgeRating>`))
	writeCBZ(t, filepath.Join(root, "Детское", "v01.cbz"), 1, "")
	scanOK(t, s)
	adult := seriesByTitle(t, s.Repo, "Взрослое")
	kids := seriesByTitle(t, s.Repo, "Детское")
	if _, err := s.Repo.db.Exec(`UPDATE items SET rating='12+' WHERE parent_id=?`, adult.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Repo.db.Exec(`UPDATE items SET rating='18+' WHERE parent_id=?`, kids.ID); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.Repo.List(context.Background(), ListQuery{Kind: KindComicVolume, MaxRating: "12+"})
	if err != nil || len(items) != 1 || items[0].ParentID != kids.ID {
		t.Fatalf("kid volumes %+v %v", items, err)
	}
}

// The scanner never follows symlinks: a linked archive or page is not a
// volume or a page.
func TestScanComicsSkipsSymlinks(t *testing.T) {
	s, root := comicScanner(t)
	outside := t.TempDir()
	writeCBZ(t, filepath.Join(outside, "real.cbz"), 1, "")
	writeBytes(t, filepath.Join(outside, "page.jpg"), testJPEG(t, color.RGBA{1, 1, 1, 255}))
	writeCBZ(t, filepath.Join(root, "Серия", "v01.cbz"), 1, "")
	if err := os.Symlink(filepath.Join(outside, "real.cbz"), filepath.Join(root, "Серия", "v02.cbz")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Серия", "Том 3")
	writeBytes(t, filepath.Join(dir, "01.jpg"), testJPEG(t, color.RGBA{2, 2, 2, 255}))
	if err := os.Symlink(filepath.Join(outside, "page.jpg"), filepath.Join(dir, "02.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "Ссылка")); err != nil {
		t.Fatal(err)
	}
	st := scanOK(t, s)
	if st.Comics != 2 {
		t.Fatalf("stats %+v", st)
	}
	b := seriesByTitle(t, s.Repo, "Серия")
	vols, _ := s.Repo.comicVolumes(context.Background(), b.ID)
	if len(vols) != 2 || metaOf(t, vols[1].item)["pages"] != 1.0 {
		t.Fatalf("%+v", vols)
	}
}

// Series of a sibling folder that shares the root's prefix (manga-old) are
// not the root's.
func TestComicSeriesUnderPrefix(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base := t.TempDir()
	for _, d := range []string{"manga", "manga-old"} {
		if _, err := r.UpsertComicVolume(ctx, ComicInput{File: FileInput{Path: filepath.Join(base, d, "A", "v1.cbz"), Size: 1, Mtime: 1},
			SeriesDir: filepath.Join(base, d, "A"), Series: "A", Volume: 1}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.comicSeriesUnder(ctx, filepath.Join(base, "manga"))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if _, ok := got[filepath.Join(base, "manga", "A")]; !ok {
		t.Fatalf("%v", got)
	}
}
