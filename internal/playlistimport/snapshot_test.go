package playlistimport

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestSnapshotNormalize(t *testing.T) {
	early := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	late := early.Add(48 * time.Hour)
	s := Snapshot{Service: " spotify ", Tracks: []SnapTrack{
		{Key: "a", Artist: " Кино ", Title: "Кукушка", AddedAt: &late},
		{Key: "a", Artist: "Кино", Title: "Кукушка", AddedAt: &early, Liked: true},
		{Key: "b", Artist: "Кино", Title: "  "},
		{Artist: "Кино", Title: "Группа крови", Album: "Группа крови"},
	}, Playlists: []SnapPlaylist{{ID: "p1", Name: " Дорога ", Readable: true, Entries: []string{"a", "zzz", "b", "a"}}}}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if s.Service != "spotify" {
		t.Errorf("service %q", s.Service)
	}
	if len(s.Tracks) != 2 {
		t.Fatalf("tracks %+v", s.Tracks)
	}
	a := s.Tracks[0]
	if a.Key != "a" || a.Artist != "Кино" || !a.Liked || a.AddedAt == nil || !a.AddedAt.Equal(early) {
		t.Errorf("deduped %+v", a)
	}
	if k := s.Tracks[1].Key; k != "кино|группа крови|группа крови" {
		t.Errorf("empty key became %q", k)
	}
	p := s.Playlists[0]
	if p.Name != "Дорога" || len(p.Entries) != 2 || p.Entries[0] != "a" || p.Entries[1] != "a" {
		t.Errorf("playlist %+v", p)
	}

	big := Snapshot{Service: "file"}
	for i := range MaxTracks + 1 {
		big.Tracks = append(big.Tracks, SnapTrack{Key: strconv.Itoa(i), Artist: "a", Title: "t"})
	}
	if err := big.Normalize(); !errors.Is(err, ErrTooLarge) || err.Error() != "Файл слишком большой" {
		t.Errorf("50 001 tracks: %v", err)
	}
	many := Snapshot{Service: "file", Playlists: make([]SnapPlaylist, MaxPlaylists+1)}
	if err := many.Normalize(); !errors.Is(err, ErrTooLarge) {
		t.Errorf("2 001 playlists: %v", err)
	}
	bad := Snapshot{Service: "napster"}
	if err := bad.Normalize(); !errors.Is(err, ErrUnknownService) || err.Error() != "Неизвестный сервис" {
		t.Errorf("napster: %v", err)
	}
}
