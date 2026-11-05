package library

import (
	"context"
	"encoding/json"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"github.com/antresol-app/antresol/internal/media"
)

// musicScanner returns a scanner with only a music root (and an ArtDir).
func musicScanner(t *testing.T) (*Scanner, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "music")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Scanner{Repo: newRepo(t), Music: root, ArtDir: filepath.Join(base, "images")}, root
}

func scanMusicOK(t *testing.T, s *Scanner) ScanStats {
	t.Helper()
	st, err := s.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return st
}

func listKind(t *testing.T, r *Repo, kind string) []Item {
	t.Helper()
	items, _, err := r.List(context.Background(), ListQuery{Kind: kind, Sort: "title"})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func oneAlbum(t *testing.T, r *Repo) (Item, []Track) {
	t.Helper()
	albums := listKind(t, r, KindAlbum)
	if len(albums) != 1 {
		t.Fatalf("albums: %v", titles(albums))
	}
	tracks, err := r.AlbumTracks(context.Background(), albums[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return albums[0], tracks
}

func TestScanAlbum(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино", "Группа крови (1988)")
	names := []string{"Группа крови", "Закрой за мной дверь", "Война"}
	for i, n := range names {
		writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i+1)+".flac"), map[string]string{
			"artist": "Кино", "album": "Группа крови", "date": "1988", "track": strconv.Itoa(i + 1), "title": n}, 1, nil)
	}
	st := scanMusicOK(t, s)
	if st.Albums != 1 || st.Tracks != 3 {
		t.Fatalf("stats %+v", st)
	}
	artists := listKind(t, s.Repo, KindArtist)
	if len(artists) != 1 || artists[0].Title != "Кино" {
		t.Fatalf("artists %v", titles(artists))
	}
	album, tracks := oneAlbum(t, s.Repo)
	if album.Title != "Группа крови" || album.Year != 1988 || album.ParentID != artists[0].ID {
		t.Fatalf("album %+v", album)
	}
	if len(tracks) != 3 {
		t.Fatalf("tracks %d", len(tracks))
	}
	for i, tr := range tracks {
		if tr.Title != names[i] || tr.No != i+1 || !tr.Lossless || tr.Codec != "flac" || tr.Artist != "Кино" || tr.Disc != 1 {
			t.Fatalf("track %d: %+v", i, tr)
		}
	}
	tr, err := s.Repo.TrackByID(context.Background(), tracks[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if tr.No != 2 || tr.ArtistID != artists[0].ID || tr.AlbumID != album.ID || tr.Album != "Группа крови" || tr.Kind != KindTrack {
		t.Fatalf("TrackByID %+v", tr)
	}
	if _, err := s.Repo.TrackByID(context.Background(), album.ID); err != ErrNotFound {
		t.Fatalf("TrackByID(album) = %v", err)
	}
}

func TestScanCompilationOneAlbum(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Хиты 80-х")
	artists := []string{"Кино", "Аквариум", "Алиса", "ДДТ"}
	for i, a := range artists {
		writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i+1)+".mp3"), map[string]string{
			"artist": a, "album": "Хиты 80-х", "track": strconv.Itoa(i + 1), "title": "Песня " + a}, 1, nil)
	}
	scanMusicOK(t, s)
	album, tracks := oneAlbum(t, s.Repo)
	ar := listKind(t, s.Repo, KindArtist)
	if len(ar) != 1 || ar[0].Title != "Разные исполнители" || album.ParentID != ar[0].ID {
		t.Fatalf("artists %v, album parent %d", titles(ar), album.ParentID)
	}
	found, err := s.Repo.SearchKinds(context.Background(), "various artists", []string{KindArtist}, 10, "")
	if err != nil || len(found) != 1 || found[0].ID != ar[0].ID {
		t.Fatalf("search: %v %v", titles(found), err)
	}
	if len(tracks) != 4 {
		t.Fatalf("tracks %d", len(tracks))
	}
	for i, tr := range tracks {
		if tr.Artist != artists[i] || tr.Lossless || tr.Codec != "mp3" {
			t.Fatalf("track %d: %+v", i, tr)
		}
	}
	if !regexp.MustCompile(`"compilation":true`).MatchString(album.MetaJSON) {
		t.Fatalf("meta %s", album.MetaJSON)
	}
}

func TestScanMultiDiscFolders(t *testing.T) {
	t.Run("folders", func(t *testing.T) {
		s, root := musicScanner(t)
		dir := filepath.Join(root, "Pink Floyd - The Wall (1979) [FLAC]")
		writeAudio(t, filepath.Join(dir, "CD1", "01.flac"), map[string]string{"artist": "Pink Floyd", "title": "In the Flesh?"}, 1, nil)
		writeAudio(t, filepath.Join(dir, "CD2", "01.flac"), map[string]string{"artist": "Pink Floyd", "title": "Hey You"}, 1, nil)
		scanMusicOK(t, s)
		album, tracks := oneAlbum(t, s.Repo)
		if album.Title != "The Wall" || album.Year != 1979 {
			t.Fatalf("album %+v", album)
		}
		if len(tracks) != 2 || tracks[0].Disc != 1 || tracks[1].Disc != 2 || tracks[0].Title != "In the Flesh?" || tracks[1].No != 1 {
			t.Fatalf("tracks %+v", tracks)
		}
		if ar := listKind(t, s.Repo, KindArtist); len(ar) != 1 || ar[0].Title != "Pink Floyd" {
			t.Fatalf("artists %v", titles(ar))
		}
	})
	t.Run("album tags", func(t *testing.T) {
		s, root := musicScanner(t)
		dir := filepath.Join(root, "Pink Floyd - The Wall")
		writeAudio(t, filepath.Join(dir, "a.flac"), map[string]string{"artist": "Pink Floyd", "album": "The Wall (Disc 2)", "track": "1", "title": "Hey You"}, 1, nil)
		writeAudio(t, filepath.Join(dir, "b.flac"), map[string]string{"artist": "Pink Floyd", "album": "The Wall (Disc 1)", "track": "1", "title": "In the Flesh?"}, 1, nil)
		scanMusicOK(t, s)
		album, tracks := oneAlbum(t, s.Repo)
		if album.Title != "The Wall" {
			t.Fatalf("album %+v", album)
		}
		if len(tracks) != 2 || tracks[0].Disc != 1 || tracks[0].Title != "In the Flesh?" || tracks[1].Disc != 2 {
			t.Fatalf("tracks %+v", tracks)
		}
	})
}

func TestScanCueImageCP1251(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988) [FLAC]")
	img := filepath.Join(dir, "Кино - Группа крови.flac")
	writeAudio(t, img, nil, 6, nil)
	cp, err := charmap.Windows1251.NewEncoder().String(cueText)
	if err != nil {
		t.Fatal(err)
	}
	cuePath := filepath.Join(dir, "Кино - Группа крови.cue")
	touch(t, cuePath, cp)
	st := scanMusicOK(t, s)
	if st.Albums != 1 || st.Tracks != 3 {
		t.Fatalf("stats %+v", st)
	}
	album, tracks := oneAlbum(t, s.Repo)
	if album.Title != "Группа крови" || album.Year != 1988 {
		t.Fatalf("album %+v", album)
	}
	if ar := listKind(t, s.Repo, KindArtist); len(ar) != 1 || ar[0].Title != "Кино" {
		t.Fatalf("artists %v", titles(ar))
	}
	want := []string{"Группа крови", "Закрой за мной дверь, я ухожу", "Война"}
	if len(tracks) != 3 {
		t.Fatalf("tracks %+v", tracks)
	}
	for i, tr := range tracks {
		if tr.Title != want[i] || tr.No != i+1 || tr.StartMs != int64(i)*2000 || tr.FileID != tracks[0].FileID {
			t.Fatalf("track %d: %+v", i, tr)
		}
		if i < 2 && (tr.EndMs == nil || *tr.EndMs != int64(i+1)*2000) {
			t.Fatalf("track %d end %v", i, tr.EndMs)
		}
	}
	if tracks[2].EndMs != nil {
		t.Fatalf("last end %v", *tracks[2].EndMs)
	}
	f, err := s.Repo.FileByID(context.Background(), tracks[0].FileID)
	if err != nil || f.Path != img || f.ItemID != album.ID {
		t.Fatalf("image file %+v %v", f, err)
	}
	if _, err := s.Repo.FileByPath(context.Background(), cuePath); err != ErrNotFound {
		t.Fatalf("cue indexed: %v", err)
	}
	// rescan keeps everything
	scanMusicOK(t, s)
	if _, again := oneAlbum(t, s.Repo); len(again) != 3 || again[1].ID != tracks[1].ID {
		t.Fatalf("rescan %+v", again)
	}
}

func TestCueIgnoredForSplitTracks(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "01 - Группа крови.flac"), nil, 1, nil)
	writeAudio(t, filepath.Join(dir, "02 - Война.flac"), nil, 1, nil)
	touch(t, filepath.Join(dir, "album.cue"), "PERFORMER \"Кино\"\nTITLE \"Группа крови\"\nFILE \"01 - Группа крови.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"X\"\n    INDEX 01 00:00:00\n"+
		"FILE \"02 - Война.flac\" WAVE\n  TRACK 02 AUDIO\n    TITLE \"Y\"\n    INDEX 01 00:00:00\n")
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if len(tracks) != 2 || tracks[0].Title != "Группа крови" || tracks[1].Title != "Война" || tracks[0].FileID == tracks[1].FileID {
		t.Fatalf("tracks %+v", tracks)
	}
}

// A track-per-file rip whose sheet appends each pregap to the previous file
// is not an image: one track per file, none cut short, no pregap "tracks".
func TestCueGapsAppendedKeepsFiles(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Metallica - Metallica (1991)")
	writeAudio(t, filepath.Join(dir, "01. Enter Sandman.flac"), map[string]string{"title": "Enter Sandman", "track": "1"}, 2, nil)
	writeAudio(t, filepath.Join(dir, "02. Sad But True.flac"), map[string]string{"title": "Sad But True", "track": "2"}, 2, nil)
	writeAudio(t, filepath.Join(dir, "03. Holier Than Thou.flac"), map[string]string{"title": "Holier Than Thou", "track": "3"}, 2, nil)
	touch(t, filepath.Join(dir, "Metallica - Metallica.cue"), strings.NewReplacer("05:30:29", "00:01:29", "05:24:03", "00:01:03").Replace(cueGapsAppended))
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if len(tracks) != 3 {
		t.Fatalf("tracks %+v", tracks)
	}
	for i, tr := range tracks {
		if tr.StartMs != 0 || tr.EndMs != nil {
			t.Fatalf("track %d cut: %+v", i, tr)
		}
	}
}

func TestScanUntaggedFallsBackToNames(t *testing.T) {
	s, root := musicScanner(t)
	touch(t, filepath.Join(root, "Аквариум - Радио Африка (1983)", "02. Капитан Африка.mp3"), "\x00\x01garbage, not an mp3 at all")
	scanMusicOK(t, s)
	album, tracks := oneAlbum(t, s.Repo)
	if album.Title != "Радио Африка" || album.Year != 1983 {
		t.Fatalf("album %+v", album)
	}
	if ar := listKind(t, s.Repo, KindArtist); len(ar) != 1 || ar[0].Title != "Аквариум" {
		t.Fatalf("artists %v", titles(ar))
	}
	if len(tracks) != 1 || tracks[0].No != 2 || tracks[0].Title != "Капитан Африка" || tracks[0].Artist != "Аквариум" || tracks[0].Codec != "mp3" {
		t.Fatalf("tracks %+v", tracks)
	}
}

func TestAlbumCoverSidecarThenEmbedded(t *testing.T) {
	s, root := musicScanner(t)
	a := filepath.Join(root, "Кино - Звезда по имени Солнце (1989)")
	writeAudio(t, filepath.Join(a, "01.flac"), map[string]string{"artist": "Кино", "album": "Звезда по имени Солнце", "track": "1"}, 1,
		testJPEG(t, color.RGBA{255, 0, 0, 255}))
	sidecar := filepath.Join(a, "Folder.JPG")
	touch(t, sidecar, string(testJPEG(t, color.RGBA{0, 0, 255, 255})))
	b := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(b, "02.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "2"}, 1,
		testJPEG(t, color.RGBA{0, 255, 0, 255}))
	writeAudio(t, filepath.Join(b, "01.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1,
		testJPEG(t, color.RGBA{255, 255, 0, 255}))
	scanMusicOK(t, s)
	albums := listKind(t, s.Repo, KindAlbum)
	if len(albums) != 2 {
		t.Fatalf("albums %v", titles(albums))
	}
	byTitle := map[string]Item{}
	for _, al := range albums {
		byTitle[al.Title] = al
	}
	if p := byTitle["Звезда по имени Солнце"].Poster; p != "file:"+sidecar {
		t.Fatalf("sidecar poster %q", p)
	}
	p := byTitle["Группа крови"].Poster
	if !regexp.MustCompile(`^[0-9a-f]{40}\.jpg$`).MatchString(p) {
		t.Fatalf("embedded poster %q", p)
	}
	if _, err := os.Stat(filepath.Join(s.ArtDir, p)); err != nil {
		t.Fatal(err)
	}
	_, tracks := func() (Item, []Track) {
		tr, err := s.Repo.AlbumTracks(context.Background(), byTitle["Группа крови"].ID)
		if err != nil {
			t.Fatal(err)
		}
		return Item{}, tr
	}()
	for _, tr := range tracks {
		if tr.Poster != "" {
			t.Fatalf("track poster %q", tr.Poster)
		}
	}
	// the cover is track 1's (disc, track order), not that of 02.flac
	if want := coverName(testJPEG(t, color.RGBA{255, 255, 0, 255}), "jpg"); p != want {
		t.Fatalf("poster %s, want track 1's cover %s", p, want)
	}
}

func TestLyricsFromLRC(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "01 - Группа крови.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1", "lyrics": "из тега"}, 1, nil)
	lrc := "[00:01.00]Тёплое место, но улицы ждут\n[00:05.50]Отпечатков наших ног\n"
	touch(t, filepath.Join(dir, "01 - Группа крови.lrc"), lrc)
	writeAudio(t, filepath.Join(dir, "02 - Война.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "2", "lyrics": "Между землёй и небом"}, 1, nil)
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if len(tracks) != 2 || tracks[0].Lyrics != lrc || tracks[1].Lyrics != "Между землёй и небом" {
		t.Fatalf("lyrics %q / %q", tracks[0].Lyrics, tracks[len(tracks)-1].Lyrics)
	}
}

func TestRescanMusicIdempotent(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино", "Группа крови (1988)")
	for i := 1; i <= 3; i++ {
		writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i)+".flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": strconv.Itoa(i)}, 1, nil)
	}
	scanMusicOK(t, s)
	a1, t1 := oneAlbum(t, s.Repo)
	ar1 := listKind(t, s.Repo, KindArtist)
	st := scanMusicOK(t, s)
	a2, t2 := oneAlbum(t, s.Repo)
	ar2 := listKind(t, s.Repo, KindArtist)
	if a1.ID != a2.ID || len(ar1) != 1 || len(ar2) != 1 || ar1[0].ID != ar2[0].ID || st.Removed != 0 {
		t.Fatalf("ids changed: album %d/%d artists %v/%v stats %+v", a1.ID, a2.ID, ar1, ar2, st)
	}
	if len(t1) != 3 || len(t2) != 3 {
		t.Fatalf("tracks %d/%d", len(t1), len(t2))
	}
	for i := range t1 {
		if t1[i].ID != t2[i].ID || t1[i].FileID != t2[i].FileID {
			t.Fatalf("track %d id changed", i)
		}
	}
	var n int
	if err := s.Repo.db.QueryRow(`SELECT COUNT(*) FROM items WHERE kind IN ('track','album','artist')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("items %d", n)
	}
}

func TestScanMusicSkipsJunk(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "01.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1, nil)
	writeAudio(t, filepath.Join(dir, "Scans", "02.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "2"}, 1, nil)
	touch(t, filepath.Join(dir, "__MACOSX", "._01.flac"), "junk")
	touch(t, filepath.Join(dir, "._01.flac"), "junk")
	touch(t, filepath.Join(dir, ".DS_Store"), "junk")
	touch(t, filepath.Join(dir, "Scans", "01.jpg"), "junk")
	touch(t, filepath.Join(dir, "rip.log"), "junk")
	touch(t, filepath.Join(dir, "playlist.m3u"), "01.flac")
	touch(t, filepath.Join(dir, "@eaDir", "01.flac"), "junk")
	touch(t, filepath.Join(root, "Книга.m4b"), "audiobook")
	st := scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if len(tracks) != 1 || st.Tracks != 1 || st.Files != 1 {
		t.Fatalf("tracks %+v stats %+v", tracks, st)
	}
	var n int
	if err := s.Repo.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("files %d", n)
	}
}

func TestMissingMusicRootNotPruned(t *testing.T) {
	s, root := musicScanner(t)
	writeAudio(t, filepath.Join(root, "Кино - Группа крови (1988)", "01.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1, nil)
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	s.ScanAll(context.Background()) // the missing root is an error, not an empty library
	allState(t, states(t, s.Repo, tracks[0].ID, tracks[0].AlbumID, tracks[0].ArtistID), "")
	var ms *string
	if err := s.Repo.db.QueryRow(`SELECT missing_since FROM files WHERE id=?`, tracks[0].FileID).Scan(&ms); err != nil {
		t.Fatal(err)
	}
	if ms != nil {
		t.Fatalf("file marked missing %s", *ms)
	}
}

func TestRescanSkipsUnchangedFolders(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино", "Группа крови (1988)")
	for i := 1; i <= 3; i++ {
		writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i)+".flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": strconv.Itoa(i)}, 1,
			testJPEG(t, color.RGBA{uint8(i * 50), 0, 0, 255}))
	}
	other := filepath.Join(root, "Аквариум - Радио Африка (1983)")
	writeAudio(t, filepath.Join(other, "01.flac"), map[string]string{"artist": "Аквариум", "album": "Радио Африка", "track": "1"}, 1, nil)
	first := scanMusicOK(t, s)
	_, before := oneAlbumIn(t, s.Repo, "Группа крови")

	tagReads.Store(0)
	st := scanMusicOK(t, s)
	if n := tagReads.Load(); n != 0 {
		t.Fatalf("unchanged rescan read tags %d times", n)
	}
	if st.Albums != first.Albums || st.Tracks != first.Tracks || st.Files != first.Files || st.Removed != 0 {
		t.Fatalf("stats %+v, first %+v", st, first)
	}
	_, after := oneAlbumIn(t, s.Repo, "Группа крови")
	if len(after) != 3 || after[0].ID != before[0].ID || after[0].UpdatedAt != before[0].UpdatedAt {
		t.Fatalf("tracks changed: %+v", after)
	}

	// a new .lrc changes the folder: only that folder is read again
	touch(t, filepath.Join(dir, "01.lrc"), "[00:00.50]Тёплое место")
	tagReads.Store(0)
	scanMusicOK(t, s)
	if n := tagReads.Load(); n != 3 {
		t.Fatalf("changed folder: %d tag reads, want 3", n)
	}
	if _, tr := oneAlbumIn(t, s.Repo, "Группа крови"); tr[0].Lyrics != "[00:00.50]Тёплое место" {
		t.Fatalf("lyrics %q", tr[0].Lyrics)
	}
	// a replaced file (other size/mtime) is read again too
	writeAudio(t, filepath.Join(other, "01.flac"), map[string]string{"artist": "Аквариум", "album": "Радио Африка", "track": "1", "title": "Капитан Африка"}, 2, nil)
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(other, "01.flac"), future, future)
	tagReads.Store(0)
	scanMusicOK(t, s)
	if n := tagReads.Load(); n != 1 {
		t.Fatalf("replaced file: %d tag reads, want 1", n)
	}
	if _, tr := oneAlbumIn(t, s.Repo, "Радио Африка"); tr[0].Title != "Капитан Африка" {
		t.Fatalf("title %q", tr[0].Title)
	}
}

// oneAlbumIn returns the album with this title and its tracks.
func oneAlbumIn(t *testing.T, r *Repo, title string) (Item, []Track) {
	t.Helper()
	for _, al := range listKind(t, r, KindAlbum) {
		if al.Title == title {
			tr, err := r.AlbumTracks(context.Background(), al.ID)
			if err != nil {
				t.Fatal(err)
			}
			return al, tr
		}
	}
	t.Fatalf("no album %q", title)
	return Item{}, nil
}

func TestProbeCodecALAC(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	p := filepath.Join(dir, "01.m4a")
	writeAudio(t, p, map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1, nil)
	scanMusicOK(t, s)
	ctx := context.Background()
	_, tracks := oneAlbum(t, s.Repo)
	pr, err := media.Probe(ctx, ffprobe, p)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(pr)
	if err := s.Repo.SetProbe(ctx, tracks[0].FileID, string(js), pr.DurationMs); err != nil {
		t.Fatal(err)
	}
	tr, err := s.Repo.TrackByID(ctx, tracks[0].ID)
	if err != nil || tr.Codec != "alac" || !tr.Lossless || tr.DurationMs < 900 {
		t.Fatalf("after probe %+v %v", tr, err)
	}
	// a rescan of the changed folder keeps the probed codec
	writeAudio(t, filepath.Join(dir, "02.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "2"}, 1, nil)
	scanMusicOK(t, s)
	if tr, _ := s.Repo.TrackByID(ctx, tracks[0].ID); tr.Codec != "alac" || !tr.Lossless {
		t.Fatalf("after rescan %+v", tr)
	}
}

func TestScanFeaturedArtistsStayWithArtist(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Eminem - The Eminem Show (2002)")
	for i, a := range []string{"Eminem", "Eminem feat. Dido", "Eminem ft. Nate Dogg", "Eminem & D12"} {
		writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i+1)+".mp3"), map[string]string{
			"artist": a, "album": "The Eminem Show", "track": strconv.Itoa(i + 1)}, 1, nil)
	}
	scanMusicOK(t, s)
	album, tracks := oneAlbum(t, s.Repo)
	ar := listKind(t, s.Repo, KindArtist)
	if len(ar) != 1 || ar[0].Title != "Eminem" || album.ParentID != ar[0].ID {
		t.Fatalf("artists %v", titles(ar))
	}
	if strings.Contains(album.MetaJSON, `"compilation":true`) || tracks[1].Artist != "Eminem feat. Dido" {
		t.Fatalf("meta %s tracks %+v", album.MetaJSON, tracks)
	}
}

func TestScanCompilationFlagAndVATag(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		s, root := musicScanner(t)
		dir := filepath.Join(root, "Рок-н-ролл 2")
		for i, a := range []string{"Кино", "Алиса"} {
			writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i+1)+".flac"), map[string]string{
				"artist": a, "album": "Рок-н-ролл 2", "track": strconv.Itoa(i + 1), "compilation": "1"}, 1, nil)
		}
		scanMusicOK(t, s)
		album, _ := oneAlbum(t, s.Repo)
		if ar := listKind(t, s.Repo, KindArtist); len(ar) != 1 || ar[0].Title != VariousArtists || !strings.Contains(album.MetaJSON, `"compilation":true`) {
			t.Fatalf("artists %v meta %s", titles(ar), album.MetaJSON)
		}
	})
	t.Run("albumartist", func(t *testing.T) {
		s, root := musicScanner(t)
		dir := filepath.Join(root, "Сборник")
		for i, a := range []string{"Кино", "Алиса"} {
			writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i+1)+".mp3"), map[string]string{
				"artist": a, "album_artist": "Сборник", "album": "Лучшее", "track": strconv.Itoa(i + 1)}, 1, nil)
		}
		scanMusicOK(t, s)
		oneAlbum(t, s.Repo)
		if ar := listKind(t, s.Repo, KindArtist); len(ar) != 1 || ar[0].Title != VariousArtists {
			t.Fatalf("artists %v", titles(ar))
		}
	})
}

func TestRescanEditedCueSheet(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "image.flac"), nil, 6, nil)
	// the FILE name differs from the image: found by the sheet's own stem; UTF-8 with a BOM
	cue := filepath.Join(dir, "image.cue")
	touch(t, cue, "\xEF\xBB\xBF"+strings.Replace(cueText, "Кино - Группа крови.wav", "Range.wav", 1))
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if len(tracks) != 3 || tracks[2].Title != "Война" {
		t.Fatalf("tracks %+v", tracks)
	}
	// the edited sheet drops track 3
	two := cueText[:strings.Index(cueText, "  TRACK 03")]
	touch(t, cue, two)
	future := time.Now().Add(time.Hour)
	os.Chtimes(cue, future, future)
	scanMusicOK(t, s)
	_, again := oneAlbum(t, s.Repo)
	if len(again) != 2 || again[0].ID != tracks[0].ID || again[1].EndMs != nil {
		t.Fatalf("edited %+v", again)
	}
	if st := states(t, s.Repo, tracks[2].ID); st[0] != "deleted" {
		t.Fatalf("dropped track: %v", st)
	}
}

func TestStaleSidecarCoverCleared(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино - Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "01.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1, nil)
	cover := filepath.Join(dir, "cover.jpg")
	touch(t, cover, string(testJPEG(t, color.RGBA{0, 0, 255, 255})))
	scanMusicOK(t, s)
	if al, _ := oneAlbum(t, s.Repo); al.Poster != "file:"+cover {
		t.Fatalf("poster %q", al.Poster)
	}
	os.Remove(cover)
	scanMusicOK(t, s)
	if al, _ := oneAlbum(t, s.Repo); al.Poster != "" || strings.Contains(al.MetaJSON, "scan_cover") {
		t.Fatalf("stale poster %q meta %s", al.Poster, al.MetaJSON)
	}
}

func TestScanKeepsBandNamesWithAmpersand(t *testing.T) {
	s, root := musicScanner(t)
	sg := filepath.Join(root, "Simon & Garfunkel - Bookends (1968)")
	for i := 1; i <= 3; i++ {
		writeAudio(t, filepath.Join(sg, "0"+strconv.Itoa(i)+".mp3"), map[string]string{
			"artist": "Simon & Garfunkel", "album": "Bookends", "track": strconv.Itoa(i)}, 1, nil)
	}
	ewf := filepath.Join(root, "Earth, Wind & Fire - All 'n All (1977)")
	for i, a := range []string{"Earth, Wind & Fire", "Earth, Wind & Fire feat. The Emotions", "Earth, Wind & Fire"} {
		writeAudio(t, filepath.Join(ewf, "0"+strconv.Itoa(i+1)+".mp3"), map[string]string{
			"artist": a, "album": "All 'n All", "track": strconv.Itoa(i + 1)}, 1, nil)
	}
	scanMusicOK(t, s)
	ar := listKind(t, s.Repo, KindArtist)
	if len(ar) != 2 || ar[0].Title != "Earth, Wind & Fire" || ar[1].Title != "Simon & Garfunkel" {
		t.Fatalf("artists %v", titles(ar))
	}
	b, tracks := oneAlbumIn(t, s.Repo, "Bookends")
	if b.ParentID != ar[1].ID || tracks[0].Artist != "Simon & Garfunkel" {
		t.Fatalf("Bookends %+v %+v", b, tracks)
	}
	e, tracks := oneAlbumIn(t, s.Repo, "All 'n All")
	if e.ParentID != ar[0].ID || tracks[1].Artist != "Earth, Wind & Fire feat. The Emotions" || strings.Contains(e.MetaJSON, `"compilation":true`) {
		t.Fatalf("All 'n All %+v %+v", e, tracks)
	}
}

// A partial album (tracks 01 and 03 of five chosen in «Выбрать релиз…»)
// next to its rip's CUE sheet, which lists every track as its own FILE.
func TestPartialAlbumScansWithCue(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино", "Группа крови (1988)")
	writeAudio(t, filepath.Join(dir, "01 - Группа крови.flac"), map[string]string{"title": "Группа крови", "track": "1", "album": "Группа крови", "artist": "Кино"}, 1, nil)
	writeAudio(t, filepath.Join(dir, "03 - Война.flac"), map[string]string{"title": "Война", "track": "3", "album": "Группа крови", "artist": "Кино"}, 1, nil)
	cue := "PERFORMER \"Кино\"\nTITLE \"Группа крови\"\n"
	for i, name := range []string{"01 - Группа крови", "02 - Закрой за мной дверь, я ухожу", "03 - Война", "04 - Спокойная ночь", "05 - Мама, мы все сошли с ума"} {
		n := strconv.Itoa(i + 1)
		cue += "FILE \"" + name + ".flac\" WAVE\n  TRACK 0" + n + " AUDIO\n    TITLE \"" + name[5:] + "\"\n    INDEX 01 00:00:00\n"
	}
	touch(t, filepath.Join(dir, "album.cue"), cue)
	st := scanMusicOK(t, s)
	if st.Albums != 1 || st.Tracks != 2 {
		t.Fatalf("stats %+v", st)
	}
	album, tracks := oneAlbum(t, s.Repo)
	if album.Title != "Группа крови" || len(tracks) != 2 || tracks[0].Title != "Группа крови" || tracks[1].Title != "Война" || tracks[0].No != 1 || tracks[1].No != 3 {
		t.Fatalf("album %+v tracks %+v", album, tracks)
	}
	for _, tr := range tracks {
		var ms *string
		if err := s.Repo.db.QueryRow(`SELECT missing_since FROM files WHERE id=?`, tr.FileID).Scan(&ms); err != nil || ms != nil {
			t.Fatalf("track %q file missing since %v: %v", tr.Title, ms, err)
		}
	}
}

// A track added to an album folder whose tag names an edition of the album
// («Группа крови (Remastered)») joins the album already there: one item,
// keeping its title and id, even when the new file sorts first.
func TestScanFoldsEditionTagIntoExistingAlbum(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "Кино", "Группа крови")
	for i, n := range []string{"Группа крови", "Закрой за мной дверь"} {
		writeAudio(t, filepath.Join(dir, "0"+strconv.Itoa(i+1)+".flac"), map[string]string{
			"artist": "Кино", "album": "Группа крови", "track": strconv.Itoa(i + 1), "title": n}, 1, nil)
	}
	scanMusicOK(t, s)
	before, _ := oneAlbum(t, s.Repo)
	writeAudio(t, filepath.Join(dir, "00 - Бошетунмай.flac"), map[string]string{
		"artist": "Кино", "album": "Группа крови (Remastered 2012)", "track": "5", "title": "Бошетунмай"}, 1, nil)
	scanMusicOK(t, s)
	after, tracks := oneAlbum(t, s.Repo)
	if after.ID != before.ID || after.Title != "Группа крови" || len(tracks) != 3 {
		t.Fatalf("album %+v (was %d), %d tracks", after, before.ID, len(tracks))
	}

	// a new folder with both tags at once: one album
	dir2 := filepath.Join(root, "Кино", "Ночь")
	writeAudio(t, filepath.Join(dir2, "01.flac"), map[string]string{"artist": "Кино", "album": "Ночь", "title": "Ночь"}, 1, nil)
	writeAudio(t, filepath.Join(dir2, "02.flac"), map[string]string{"artist": "Кино", "album": "Ночь [Deluxe Edition]", "title": "Мама анархия"}, 1, nil)
	scanMusicOK(t, s)
	if albums := listKind(t, s.Repo, KindAlbum); len(albums) != 2 {
		t.Fatalf("albums %v", titles(albums))
	}
	// a version is not an edition: «… (Live)» stays its own album
	writeAudio(t, filepath.Join(dir2, "03.flac"), map[string]string{"artist": "Кино", "album": "Ночь (Live)", "title": "Ночь"}, 1, nil)
	scanMusicOK(t, s)
	if albums := listKind(t, s.Repo, KindAlbum); len(albums) != 3 {
		t.Fatalf("albums with a live one %v", titles(albums))
	}
}

// A track number the tag carried into the title goes; a title that only
// starts with a number stays.
func TestCleanTagTitle(t *testing.T) {
	for _, c := range []struct {
		in   string
		no   int
		want string
	}{
		{"01. Ain't My Bitch", 1, "Ain't My Bitch"},
		{"03 - Cure", 3, "Cure"},
		{"1979", 5, "1979"},
		{"99 Problems", 99, "99 Problems"},
		{"02. Wrong Number", 7, "02. Wrong Number"},
		{"01. Song", 0, "01. Song"},
	} {
		if got := cleanTagTitle(c.in, c.no); got != c.want {
			t.Errorf("%q #%d: %q, want %q", c.in, c.no, got, c.want)
		}
	}
}
