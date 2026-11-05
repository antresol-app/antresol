package library

import (
	"context"
	"fmt"
	"testing"
)

func TestParseAlbumFolder(t *testing.T) {
	for _, c := range []struct {
		in            string
		artist, album string
		year          int
	}{
		{"Кино - Группа крови (1988) [FLAC]", "Кино", "Группа крови", 1988},
		{"(Rock) Кино - Группа крови - 1988, FLAC (image+.cue), lossless", "Кино", "Группа крови", 1988},
		{"Pink Floyd - 1979 - The Wall", "Pink Floyd", "The Wall", 1979},
		{"The Wall (1979)", "", "The Wall", 1979},
		{"The Wall", "", "The Wall", 0},
		{"Pink Floyd - The Wall (1979) [FLAC]", "Pink Floyd", "The Wall", 1979},
		{"Аквариум - Радио Африка (1983)", "Аквариум", "Радио Африка", 1983},
	} {
		a, al, y := parseAlbumFolder(c.in)
		if a != c.artist || al != c.album || y != c.year {
			t.Errorf("%q: got (%q, %q, %d), want (%q, %q, %d)", c.in, a, al, y, c.artist, c.album, c.year)
		}
	}
}

func TestParseTrackFile(t *testing.T) {
	for _, c := range []struct {
		in    string
		no    int
		title string
	}{
		{"01 - Группа крови", 1, "Группа крови"},
		{"01. Группа крови", 1, "Группа крови"},
		{"1-01 Title", 1, "Title"},
		{"2-05 Another Title", 5, "Another Title"},
		{"Группа крови", 0, "Группа крови"},
		{"07 Звезда по имени Солнце", 7, "Звезда по имени Солнце"},
		{"12_Track", 12, "Track"},
		{"03", 3, ""},
	} {
		no, title := parseTrackFile(c.in)
		if no != c.no || title != c.title {
			t.Errorf("%q: got (%d, %q), want (%d, %q)", c.in, no, title, c.no, c.title)
		}
	}
	if d, no, _ := parseTrackFileDisc("2-05 Another Title"); d != 2 || no != 5 {
		t.Errorf("disc-track: %d %d", d, no)
	}
}

func TestDiscFolderAndAlbumSuffix(t *testing.T) {
	for in, want := range map[string]int{"CD1": 1, "cd 2": 2, "Disc 3": 3, "Диск 1": 1, "Disk2 (Bonus)": 2, "Bonus": 0, "CDs": 0} {
		if got := discFolder(in); got != want {
			t.Errorf("discFolder(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]struct {
		name string
		disc int
	}{
		"The Wall (Disc 1)": {"The Wall", 1},
		"The Wall [CD2]":    {"The Wall", 2},
		"The Wall disc 2":   {"The Wall", 2},
		"The Wall":          {"The Wall", 0},
		"Abcd 2":            {"Abcd 2", 0},
		"MCD 1":             {"MCD 1", 0},
		"Wcd2":              {"Wcd2", 0},
		"Album CD2":         {"Album", 2},
		"Альбом (Диск 2)":   {"Альбом", 2},
		"Album - CD 3":      {"Album", 3},
	} {
		n, d := stripDiscSuffix(in)
		if n != want.name || d != want.disc {
			t.Errorf("stripDiscSuffix(%q) = (%q, %d), want %+v", in, n, d, want)
		}
	}
}

func TestPrimaryArtist(t *testing.T) {
	for in, want := range map[string]string{
		"Eminem":                   "Eminem",
		"Eminem feat. Dido":        "Eminem",
		"Eminem (feat. Rihanna)":   "Eminem",
		"Eminem ft. Nate Dogg":     "Eminem",
		"Eminem featuring Dr. Dre": "Eminem",
		"Кино при участии БГ":      "Кино",
		"Eminem & D12":             "Eminem",
		"Daft Punk":                "Daft Punk",
		"Simon & Garfunkel":        "Simon", // a counting key, never stored
		"Feat":                     "Feat",
	} {
		if got := primaryArtist(in); got != want {
			t.Errorf("primaryArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseYear(t *testing.T) {
	for in, want := range map[string]int{
		"1973": 1973, "1973-03-01": 1973, " 1973-03 ": 1973, "1973-03-01T00:00:00Z": 1973, "2999": 2999, "1000": 1000,
		"": 0, "1": 0, "0001": 0, "0001-01-01": 0, "999": 0, "0999": 0, "3000": 0, "19730": 0, "abcd": 0, "x1973": 0,
	} {
		if got := parseYear(in); got != want {
			t.Errorf("parseYear(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[int]int{-5: 0, 0: 0, 1: 0, 999: 0, 1000: 1000, 1973: 1973, 2999: 2999, 3000: 0, 20240: 0} {
		if got := plausibleYear(in); got != want {
			t.Errorf("plausibleYear(%d) = %d, want %d", in, got, want)
		}
	}
	if got := cueYear("1973-03-01"); got != 1973 {
		t.Errorf("cueYear = %d", got)
	}
}

// Garbage years never reach the album: the most common plausible year wins,
// and a rescan repairs a garbage year stored earlier.
func TestUpsertAlbumTracksYears(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	mk := func(years ...int) []TrackInput {
		var in []TrackInput
		for i, y := range years {
			in = append(in, TrackInput{File: FileInput{Path: fmt.Sprintf("/m/A/Al/%02d.flac", i+1), Size: 1, Mtime: 1},
				AlbumDir: "/m/A/Al", Album: "Al", Title: fmt.Sprintf("t%d", i+1), Track: i + 1, Year: y})
		}
		return in
	}
	year := func(id int64) (int, int) {
		var a, tr int
		if err := r.db.QueryRow(`SELECT year FROM items WHERE id=?`, id).Scan(&a); err != nil {
			t.Fatal(err)
		}
		if err := r.db.QueryRow(`SELECT MAX(year) FROM items WHERE kind='track' AND parent_id=?`, id).Scan(&tr); err != nil {
			t.Fatal(err)
		}
		return a, tr
	}
	id, err := r.UpsertAlbumTracks(ctx, "A", mk(1, 1, 1, 1973, 1974, 1974))
	if err != nil {
		t.Fatal(err)
	}
	if a, tr := year(id); a != 1974 || tr != 1974 {
		t.Fatalf("album %d track max %d, want 1974", a, tr)
	}
	// only garbage: unknown
	id2, err := r.UpsertAlbumTracks(ctx, "B", []TrackInput{{File: FileInput{Path: "/m/B/Al/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/B/Al", Album: "Al", Title: "t", Track: 1, Year: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if a, tr := year(id2); a != 0 || tr != 0 {
		t.Fatalf("garbage year stored: album %d track %d", a, tr)
	}
	// a garbage value stored earlier is repaired on rescan
	if _, err := r.db.Exec(`UPDATE items SET year=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpsertAlbumTracks(ctx, "A", mk(1973, 1973)); err != nil {
		t.Fatal(err)
	}
	if a, _ := year(id); a != 1973 {
		t.Fatalf("rescan left album year %d, want 1973", a)
	}
}
