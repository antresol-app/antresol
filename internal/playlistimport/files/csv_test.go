package files

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, name string, data []byte) playlistimport.Snapshot {
	t.Helper()
	s, err := Parse(name, data)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return s
}

func byKey(s playlistimport.Snapshot) map[string]playlistimport.SnapTrack {
	m := map[string]playlistimport.SnapTrack{}
	for _, t := range s.Tracks {
		m[t.Key] = t
	}
	return m
}

// titles names a playlist's entries by their songs' titles.
func titles(t *testing.T, s playlistimport.Snapshot, playlist string) []string {
	t.Helper()
	tr := byKey(s)
	for _, p := range s.Playlists {
		if p.Name == playlist {
			var out []string
			for _, k := range p.Entries {
				out = append(out, tr[k].Title)
			}
			return out
		}
	}
	t.Fatalf("no playlist %q in %+v", playlist, s.Playlists)
	return nil
}

func liked(s playlistimport.Snapshot) []string {
	var out []string
	for _, t := range s.Tracks {
		if t.Liked {
			out = append(out, t.Artist+" — "+t.Title)
		}
	}
	return out
}

func TestCSVUniversal(t *testing.T) {
	s := parse(t, "universal.csv", fixture(t, "universal.csv"))
	if s.Service != playlistimport.ServiceFile || len(s.Tracks) != 4 {
		t.Fatalf("snapshot %+v", s)
	}
	if l := liked(s); strings.Join(l, ";") != "Кино — Кукушка;Кино — Группа крови" {
		t.Fatalf("liked %v", l)
	}
	if len(s.Playlists) != 1 {
		t.Fatalf("playlists %+v", s.Playlists)
	}
	if got := titles(t, s, "Дорога"); strings.Join(got, ";") != "Звезда по имени Солнце;Город золотой" {
		t.Fatalf("Дорога %v", got)
	}
	tr := s.Tracks[0]
	if tr.Album != "Чёрный альбом" || tr.ISRC != "SUA019000123" || tr.AddedAt == nil ||
		!tr.AddedAt.Equal(time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)) {
		t.Fatalf("first %+v", tr)
	}
	if d := s.Tracks[1].AddedAt; d == nil || !d.Equal(time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date only %v", d)
	}
	if err := s.Normalize(); err != nil || len(s.Tracks) != 4 || len(s.Playlists[0].Entries) != 2 {
		t.Fatalf("normalize %v %+v", err, s)
	}
}

func TestCSVUniversalVariants(t *testing.T) {
	// Excel in Russia: «;», Windows-1251, Russian headers, a BOM-less file
	cp1251 := []byte("\xc8\xf1\xef\xee\xeb\xed\xe8\xf2\xe5\xeb\xfc;\xcd\xe0\xe7\xe2\xe0\xed\xe8\xe5;\xcf\xeb\xe5\xe9\xeb\xe8\xf1\xf2\r\n" +
		"\xca\xe8\xed\xee;\xca\xf3\xea\xf3\xf8\xea\xe0;\xc4\xee\xf0\xee\xe3\xe0\r\n")
	s := parse(t, "export.csv", cp1251)
	if len(s.Tracks) != 1 || s.Tracks[0].Artist != "Кино" || s.Tracks[0].Title != "Кукушка" {
		t.Fatalf("cp1251 %+v", s)
	}
	if got := titles(t, s, "Дорога"); len(got) != 1 {
		t.Fatalf("cp1251 playlist %v", got)
	}
	// UTF-8 BOM, upper-case headers, no playlist column: the file names it
	s = parse(t, "Утро.csv", []byte("\ufeffARTIST,TITLE\nКино,Кукушка\n"))
	if s.Tracks[0].Artist != "Кино" || len(liked(s)) != 0 {
		t.Fatalf("bom %+v", s)
	}
	if got := titles(t, s, "Утро"); len(got) != 1 {
		t.Fatalf("file name playlist %v", got)
	}
	for _, name := range []string{"liked.csv", "Понравившиеся.csv", "liked_songs.csv", "LIKED SONGS.CSV"} {
		s = parse(t, name, []byte("artist,title\nКино,Кукушка\n"))
		if len(s.Playlists) != 0 || len(liked(s)) != 1 {
			t.Fatalf("%s %+v", name, s)
		}
	}
}

func TestCSVExportify(t *testing.T) {
	s := parse(t, "Liked Songs.csv", fixture(t, "exportify-liked.csv"))
	if len(s.Playlists) != 0 || len(s.Tracks) != 2 || len(liked(s)) != 2 {
		t.Fatalf("liked %+v", s)
	}
	acdc := s.Tracks[0]
	if acdc.Key != "spotify:track:2zYzyRzz6pRmhPzyfMEC8s" || acdc.Artist != "AC, DC" || acdc.AlbumArtist != "AC, DC" ||
		acdc.Title != "Highway to Hell" || acdc.Album != "Highway to Hell" || acdc.ISRC != "AUAP07900029" ||
		acdc.DurationMs != 208400 || acdc.AddedAt == nil || !acdc.AddedAt.Equal(time.Date(2023, 11, 5, 18, 22, 41, 0, time.UTC)) {
		t.Fatalf("AC/DC %+v", acdc)
	}

	p := parse(t, "Дорога.csv", fixture(t, "exportify-playlist.csv"))
	if got := titles(t, p, "Дорога"); strings.Join(got, ";") != "Bohemian Rhapsody;Кукушка" {
		t.Fatalf("Дорога %v", got)
	}
	if len(liked(p)) != 0 || p.Tracks[0].DurationMs != 354320 {
		t.Fatalf("playlist songs %+v", p.Tracks)
	}

	// both files in one upload: one song, liked and in «Дорога»
	m, err := Merge([]playlistimport.Snapshot{s, p})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	if m.Service != playlistimport.ServiceFile || len(m.Tracks) != 3 || len(m.Playlists) != 1 || len(m.Playlists[0].Entries) != 2 {
		t.Fatalf("merged %+v", m)
	}
	if k := byKey(m)["spotify:track:3n3Ppam7vgaVa1iaRUc9Lp"]; !k.Liked || k.ISRC != "SUA019000123" {
		t.Fatalf("merged Кукушка %+v", k)
	}
}

func TestCSVTuneMyMusic(t *testing.T) {
	s := parse(t, "My Spotify Library.csv", fixture(t, "tunemymusic.csv"))
	if len(s.Tracks) != 3 || len(liked(s)) != 0 || len(s.Playlists) != 2 {
		t.Fatalf("snapshot %+v", s)
	}
	if got := titles(t, s, "Дорога"); strings.Join(got, ";") != "Кукушка;Bohemian Rhapsody" {
		t.Fatalf("Дорога %v", got)
	}
	if got := titles(t, s, "Утро"); strings.Join(got, ";") != "Город золотой" {
		t.Fatalf("Утро %v", got)
	}
	if s.Tracks[0].Album != "Чёрный альбом" || s.Tracks[0].ISRC != "SUA019000123" {
		t.Fatalf("first %+v", s.Tracks[0])
	}
}

func TestCSVMissingColumns(t *testing.T) {
	_, err := Parse("export.csv", []byte("foo,bar\n1,2\n"))
	if !errors.Is(err, ErrNoColumns) || err.Error() != "Не удалось прочитать CSV: нет колонок «Исполнитель» и «Название»" {
		t.Fatalf("err %v", err)
	}
}

func TestCSVTooManyRows(t *testing.T) {
	var b strings.Builder
	b.WriteString("artist,title\n")
	for i := 0; i <= playlistimport.MaxTracks; i++ {
		b.WriteString("a,t" + strconv.Itoa(i) + "\n")
	}
	if _, err := Parse("x.csv", []byte(b.String())); !errors.Is(err, playlistimport.ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
}

func TestSplitArtists(t *testing.T) {
	for in, want := range map[string]string{
		`AC\, DC,Guest`:       "AC, DC",
		`Queen`:               "Queen",
		` A , B `:             "A",
		`Earth\, Wind & Fire`: "Earth, Wind & Fire",
	} {
		if got := firstArtist(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestParseUnreadable(t *testing.T) {
	for name, data := range map[string][]byte{
		"x.bin":       {0x00, 0x01, 0x02, 0xff},
		"empty.csv":   {},
		"Library.xml": []byte("<?xml version=\"1.0\"?><html><body/></html>"),
		"x.zip":       []byte("PK\x03\x04garbage"),
		"bin.csv":     []byte("artist,title\n\x00\x00\x00"),
	} {
		if _, err := Parse(name, data); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCSVRowsNeedArtistAndTitle(t *testing.T) {
	s := parse(t, "liked.csv", []byte("artist,title\nКино,\n,Кукушка\nКино,Кукушка\n"))
	if len(s.Tracks) != 1 || Unresolved(s) != 0 {
		t.Fatalf("tracks %+v", s.Tracks)
	}
	// «.csv» has no name: its songs are a playlist, not liked songs
	s = parse(t, ".csv", []byte("artist,title\nКино,Кукушка\n"))
	if len(liked(s)) != 0 || len(s.Playlists) != 1 || s.Playlists[0].Name != "CSV" {
		t.Fatalf("no name %+v", s)
	}
}

// the song limit counts unique songs: Exportify's liked songs and the
// playlists that repeat them
func TestMergeCountsUniqueSongs(t *testing.T) {
	n := playlistimport.MaxTracks/2 + 1000
	var b strings.Builder
	b.WriteString("artist,title\n")
	for i := range n {
		b.WriteString("a,t" + strconv.Itoa(i) + "\n")
	}
	liked := parse(t, "liked.csv", []byte(b.String()))
	road := parse(t, "Дорога.csv", []byte(b.String()))
	m, err := Merge([]playlistimport.Snapshot{liked, road, road})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tracks) != n || len(m.Playlists) != 1 || len(m.Playlists[0].Entries) != 2*n || !m.Tracks[0].Liked {
		t.Fatalf("merged %d songs %d playlists", len(m.Tracks), len(m.Playlists))
	}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	// unique songs over the limit
	b.Reset()
	b.WriteString("artist,title\n")
	for i := range n {
		b.WriteString("b,t" + strconv.Itoa(i) + "\n")
	}
	other := parse(t, "Утро.csv", []byte(b.String()))
	if _, err := Merge([]playlistimport.Snapshot{liked, other}); !errors.Is(err, playlistimport.ErrTooLarge) {
		t.Fatalf("unique over the limit: %v", err)
	}
}

func TestDecodeTextEdges(t *testing.T) {
	// a UTF-8 BOM before Windows-1251 bytes (an editor's mistake)
	if s, ok := decodeText([]byte("\xef\xbb\xbf\xca\xe8\xed\xee")); !ok || string(s) != "Кино" {
		t.Fatalf("bom+cp1251 %q", s)
	}
	// Windows-1251 «яю…» starts like a UTF-16 BOM
	if s, ok := decodeText([]byte("\xff\xfe\xeb\xff")); !ok || string(s) != "яюля" {
		t.Fatalf("яю %q", s)
	}
	// real UTF-16 LE
	if s, ok := decodeText([]byte("\xff\xfea\x00,\x00b\x00\n\x00")); !ok || string(s) != "a,b\n" {
		t.Fatalf("utf-16 %q", s)
	}
}
