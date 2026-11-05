package files

import (
	"strings"
	"testing"
)

func TestM3U(t *testing.T) {
	s := parse(t, "Дорога.m3u8", fixture(t, "playlist.m3u8"))
	if len(s.Tracks) != 3 || len(s.Playlists) != 1 || len(liked(s)) != 0 {
		t.Fatalf("snapshot %+v", s)
	}
	if got := titles(t, s, "Дорога"); strings.Join(got, ";") != "Кукушка;Bohemian Rhapsody;Город золотой" {
		t.Fatalf("order %v", got)
	}
	want := []struct {
		artist string
		ms     int64
	}{{"Кино", 401000}, {"Queen", 354000}, {"Аквариум", 0}}
	for i, w := range want {
		if tr := s.Tracks[i]; tr.Artist != w.artist || tr.DurationMs != w.ms {
			t.Fatalf("track %d %+v", i, tr)
		}
	}
}

func TestM3UPathsAndPlaylistName(t *testing.T) {
	text := "#EXTM3U\n#PLAYLIST:Утро\nКино/Группа крови/01 Группа крови.mp3\n" +
		"C:\\Music\\Аквариум\\Десять стрел\\03 - Город золотой.flac\nQueen - Bohemian Rhapsody.mp3\nlonely.mp3\n"
	s := parse(t, "whatever.m3u", []byte(text))
	if len(s.Playlists) != 1 || s.Playlists[0].Name != "Утро" {
		t.Fatalf("playlists %+v", s.Playlists)
	}
	got := []string{}
	for _, tr := range s.Tracks {
		got = append(got, tr.Artist+"|"+tr.Album+"|"+tr.Title)
	}
	if strings.Join(got, ";") != "Кино|Группа крови|Группа крови;Аквариум|Десять стрел|Город золотой;Queen||Bohemian Rhapsody" {
		t.Fatalf("tracks %v", got)
	}
}

func TestM3UFileNameBeforeFolders(t *testing.T) {
	text := "Music/Unsorted/Queen - Bohemian Rhapsody.mp3\nGreen Day/American Idiot/21 Guns.mp3\nКино/Группа крови/01 Группа крови.mp3\n"
	s := parse(t, "mix.m3u", []byte(text))
	got := []string{}
	for _, tr := range s.Tracks {
		got = append(got, tr.Artist+"|"+tr.Album+"|"+tr.Title)
	}
	if strings.Join(got, ";") != "Queen||Bohemian Rhapsody;Green Day|American Idiot|21 Guns;Кино|Группа крови|Группа крови" {
		t.Fatalf("tracks %v", got)
	}
}
