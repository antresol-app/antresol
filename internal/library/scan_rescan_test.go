package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

// writeCounter watches a database from a second connection: its
// PRAGMA data_version moves whenever another connection commits a change to
// the file. A statement that sets a row to the values it already has still
// changes the page, so an unchanged value is not hidden.
type writeCounter struct {
	t    *testing.T
	conn *sql.Conn
}

func watchWrites(t *testing.T, path string) *writeCounter {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	c, err := d.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &writeCounter{t, c}
}

func (w *writeCounter) version() int64 {
	w.t.Helper()
	var v int64
	if err := w.conn.QueryRowContext(context.Background(), `PRAGMA data_version`).Scan(&v); err != nil {
		w.t.Fatal(err)
	}
	return v
}

// scanAllOK is one ScanAll that must succeed.
func scanAllOK(t *testing.T, s *Scanner) ScanStats {
	t.Helper()
	st, err := s.ScanAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// videoFixture is a small library: a movie with its nfo and poster, a
// two-part movie, a loose one, and a series with tvshow.nfo, episode nfo and
// poster.
func videoFixture(t *testing.T) (s *Scanner, mv, sr, dbFile string) {
	t.Helper()
	root := t.TempDir()
	dbFile = filepath.Join(root, "v.db")
	d, err := db.Open(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	mv, sr = filepath.Join(root, "movies"), filepath.Join(root, "series")
	s = &Scanner{Repo: NewRepo(d), Movies: mv, Series: sr}
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "Oppenheimer (2023) Bluray-1080p.mkv"), "x")
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "movie.nfo"),
		`<movie><title>Оппенгеймер</title><originaltitle>Oppenheimer</originaltitle><year>2023</year><tmdbid>693134</tmdbid></movie>`)
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "poster.jpg"), "x")
	touch(t, filepath.Join(mv, "Тихий Дон (1957)", "Тихий Дон - cd1.mkv"), "x")
	touch(t, filepath.Join(mv, "Тихий Дон (1957)", "Тихий Дон - cd2.mkv"), "x")
	touch(t, filepath.Join(mv, "Брат 2 (2000) BDRip 1080p.mkv"), "x")
	touch(t, filepath.Join(sr, "Сёгун", "tvshow.nfo"), `<tvshow><title>Сёгун</title><year>2024</year><tvdbid>392573</tvdbid></tvshow>`)
	touch(t, filepath.Join(sr, "Сёгун", "poster.jpg"), "x")
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E01.mkv"), "x")
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E01.nfo"), `<episodedetails><title>Сёгун</title><plot>Начало</plot><season>1</season><episode>1</episode></episodedetails>`)
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E02.mkv"), "x")
	touch(t, filepath.Join(sr, "The.Bear.S03.1080p", "The.Bear.S03E01.mkv"), "x")
	return s, mv, sr, dbFile
}

// The scan of a library that did not change reads no .nfo and writes
// nothing: not a row, not an index entry.
func TestSecondVideoScanWritesNothing(t *testing.T) {
	s, _, _, dbFile := videoFixture(t)
	w := watchWrites(t, dbFile)
	first := scanAllOK(t, s)
	if first.Movies != 3 || first.Episodes != 3 || first.Changed != 6 || !first.Dirty() {
		t.Fatalf("first %+v", first)
	}
	before := w.version()
	second := scanAllOK(t, s)
	if after := w.version(); after != before {
		t.Fatalf("the second scan wrote to the database (data_version %d -> %d)", before, after)
	}
	if second.Changed != 0 || second.Removed != 0 || second.Dirty() {
		t.Fatalf("second %+v", second)
	}
	if second.Movies != first.Movies || second.Episodes != first.Episodes || second.Files != first.Files {
		t.Fatalf("counts changed: first %+v second %+v", first, second)
	}
}

// A changed sidecar, a new file and a removed file are each picked up by the
// next scan, and only they are written.
func TestVideoScanWritesWhatChanged(t *testing.T) {
	s, mv, sr, _ := videoFixture(t)
	ctx := context.Background()
	scanAllOK(t, s)

	// the movie's nfo gets a plot (same names and sizes elsewhere, a newer mtime)
	nfo := filepath.Join(mv, "Oppenheimer (2023)", "movie.nfo")
	touch(t, nfo, `<movie><title>Оппенгеймер</title><originaltitle>Oppenheimer</originaltitle><year>2023</year><plot>Бомба</plot><tmdbid>693134</tmdbid></movie>`)
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(nfo, later, later); err != nil {
		t.Fatal(err)
	}
	st := scanAllOK(t, s)
	if st.Changed != 1 {
		t.Fatalf("edited nfo: %+v", st)
	}
	opp, err := s.Repo.Search(ctx, "oppenheimer", 5)
	if err != nil || len(opp) != 1 || opp[0].Overview != "Бомба" {
		t.Fatalf("the plot of the edited nfo is not stored: %+v %v", opp, err)
	}

	// a new episode: that episode is written, the others are not
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E03.mkv"), "x")
	if st := scanAllOK(t, s); st.Changed != 1 || st.Episodes != 4 {
		t.Fatalf("new episode: %+v", st)
	}

	// a new tvshow.nfo plot reaches the series through its episodes
	show := filepath.Join(sr, "Сёгун", "tvshow.nfo")
	touch(t, show, `<tvshow><title>Сёгун</title><year>2024</year><tvdbid>392573</tvdbid><plot>Япония</plot></tvshow>`)
	if err := os.Chtimes(show, later, later); err != nil {
		t.Fatal(err)
	}
	scanAllOK(t, s)
	series, _, _ := s.Repo.List(ctx, ListQuery{Kind: "series", Sort: "title"})
	var shogun Item
	for _, it := range series {
		if it.Title == "Сёгун" {
			shogun = it
		}
	}
	if shogun.Overview != "Япония" {
		t.Fatalf("series plot %q", shogun.Overview)
	}

	// a removed file counts as a change
	if err := os.Remove(filepath.Join(mv, "Брат 2 (2000) BDRip 1080p.mkv")); err != nil {
		t.Fatal(err)
	}
	if st := scanAllOK(t, s); st.Removed != 1 || !st.Dirty() {
		t.Fatalf("removed file: %+v", st)
	}
	if st := scanAllOK(t, s); st.Dirty() {
		t.Fatalf("nothing changed since: %+v", st)
	}
}

// Rescan makes the next scan read everything again; the one after reads
// only what changed.
func TestRescanReadsEverythingOnce(t *testing.T) {
	s, _, _, dbFile := videoFixture(t)
	w := watchWrites(t, dbFile)
	scanAllOK(t, s)
	if st := scanAllOK(t, s); st.Changed != 0 {
		t.Fatalf("%+v", st)
	}
	before := w.version()
	s.Rescan()
	if st := scanAllOK(t, s); st.Changed != 6 {
		t.Fatalf("rescan: %+v", st)
	}
	if w.version() == before { // the watcher does see rows written with the values they had
		t.Fatal("a scan that read everything again wrote nothing")
	}
	if st := scanAllOK(t, s); st.Changed != 0 {
		t.Fatalf("after the rescan: %+v", st)
	}
}

// A library written before the signature existed is read once more, then
// settles.
func TestVideoScanAdoptsRowsWithoutSignature(t *testing.T) {
	s, _, _, _ := videoFixture(t)
	scanAllOK(t, s)
	if _, err := s.Repo.db.Exec(`UPDATE files SET scan_sig=''`); err != nil {
		t.Fatal(err)
	}
	if st := scanAllOK(t, s); st.Changed != 6 {
		t.Fatalf("no signatures: %+v", st)
	}
	if st := scanAllOK(t, s); st.Changed != 0 {
		t.Fatalf("settled: %+v", st)
	}
}

// A file moved out of a movie folder leaves the folder's other movie alone
// (the signature has "only movie in the folder" in it).
func TestVideoScanSeesAFolderStopBeingSingle(t *testing.T) {
	s, mv, _, _ := videoFixture(t)
	scanAllOK(t, s)
	touch(t, filepath.Join(mv, "Oppenheimer (2023)", "Another Film (2020).mkv"), "x")
	if st := scanAllOK(t, s); st.Changed != 2 { // the new film, and Oppenheimer's folder is not single any more
		t.Fatalf("%+v", st)
	}
}

// Two files that are one episode, or two folders that are one movie (the
// same tmdb id), are each read once and then both left alone.
func TestVideoScanSettlesWhenFilesShareAnItem(t *testing.T) {
	s, mv, sr, _ := videoFixture(t)
	touch(t, filepath.Join(sr, "Сёгун", "Season 1", "Сёгун.S01E01.1080p.mkv"), "xx")
	touch(t, filepath.Join(mv, "Oppenheimer 4K", "Oppenheimer 4K.mkv"), "x")
	touch(t, filepath.Join(mv, "Oppenheimer 4K", "movie.nfo"), `<movie><title>Оппенгеймер</title><tmdbid>693134</tmdbid></movie>`)
	scanAllOK(t, s)
	var films int
	if err := s.Repo.db.QueryRow(`SELECT COUNT(*) FROM items WHERE kind='movie' AND tmdb_id=693134`).Scan(&films); err != nil || films != 1 {
		t.Fatalf("the two folders are not one movie: %d %v", films, err)
	}
	if st := scanAllOK(t, s); st.Changed != 0 {
		t.Fatalf("settled scan wrote: %+v", st)
	}
}
