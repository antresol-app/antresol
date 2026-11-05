package files

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

func TestTakeout(t *testing.T) {
	s := parse(t, "takeout-20240501T000000Z-001.zip", fixture(t, "takeout.zip"))
	if s.Service != playlistimport.ServiceYouTube {
		t.Fatalf("service %q", s.Service)
	}
	if l := liked(s); len(l) != 3 {
		t.Fatalf("liked %v", l)
	}
	if k := byKey(s)["fJ9rUzIMcZQ"]; k.Title != "Bohemian Rhapsody" || k.Artist != "Queen" || k.Album != "A Night at the Opera" {
		t.Fatalf("Queen %+v", k)
	}
	// playlists.csv (the index) is no playlist
	if len(s.Playlists) != 1 || s.Playlists[0].Name != "Дорога" || len(s.Playlists[0].Entries) != 2 {
		t.Fatalf("playlists %+v", s.Playlists)
	}
	if n := Unresolved(s); n != 1 {
		t.Fatalf("unresolved %d", n)
	}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(s.Tracks) != 3 || len(s.Playlists[0].Entries) != 1 || s.Playlists[0].Entries[0] != "fJ9rUzIMcZQ" {
		t.Fatalf("normalized %+v", s)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestTakeoutVariants(t *testing.T) {
	// English folders, an old playlist CSV with a header block, « videos»
	data := zipOf(t, map[string]string{
		"Takeout/YouTube and YouTube Music/music (library and uploads)/music library songs.csv": "Video ID,Song Title,Album Title,Artist Name 1\nid1,Кукушка,Чёрный альбом,Кино\n",
		"Takeout/YouTube and YouTube Music/playlists/Утро videos.csv":                           "Playlist Id,Channel Id,Time Created,Title\nPL1,UC1,2020,Утро\n\nVideo Id,Time Added\nid1,2020-01-01 00:00:00 UTC\n",
		"Takeout/YouTube and YouTube Music/history/watch-history.html":                          "<html></html>",
	})
	s := parse(t, "takeout.zip", data)
	if len(s.Tracks) != 1 || s.Tracks[0].Artist != "Кино" || len(s.Playlists) != 1 || s.Playlists[0].Name != "Утро" ||
		strings.Join(s.Playlists[0].Entries, ",") != "id1" || Unresolved(s) != 0 {
		t.Fatalf("snapshot %+v", s)
	}
	// a zip that is not a Takeout
	if _, err := Parse("photos.zip", zipOf(t, map[string]string{"a.jpg": "x"})); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("not takeout: %v", err)
	}
}

func TestTakeoutZipBombBounded(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("Takeout/YouTube Music/music library songs.csv")
	w.Write([]byte("Video ID,Song Title,Album Title,Artist Name\n"))
	row := bytes.Repeat([]byte("x"), 1<<20)
	for range maxZipFile>>20 + 2 {
		w.Write(row) // compresses to almost nothing
	}
	z.Close()
	if b.Len() > 1<<20 {
		t.Fatalf("zip %d bytes", b.Len())
	}
	if _, err := Parse("takeout.zip", b.Bytes()); !errors.Is(err, playlistimport.ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
}

// a Takeout in two zips: a playlist video of one is in the other's library
func TestTakeoutSplitResolves(t *testing.T) {
	a := parse(t, "takeout-001.zip", zipOf(t, map[string]string{
		"Takeout/YouTube Music/playlists/Дорога-videos.csv": "Video ID,Playlist Video Creation Timestamp\nid1,2024-01-01T00:00:00+00:00\n",
	}))
	b := parse(t, "takeout-002.zip", zipOf(t, map[string]string{
		"Takeout/YouTube Music/music library songs.csv": "Video ID,Song Title,Album Title,Artist Name\nid1,Кукушка,Чёрный альбом,Кино\n",
	}))
	if Unresolved(a) != 1 {
		t.Fatalf("a %+v", a)
	}
	m, err := Merge([]playlistimport.Snapshot{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if m.Service != playlistimport.ServiceYouTube || Unresolved(m) != 0 || len(m.Tracks) != 1 || m.Tracks[0].Title != "Кукушка" || !m.Tracks[0].Liked {
		t.Fatalf("merged %+v", m)
	}
}

// Exportify's «Export All»: a zip of CSVs, not a Takeout
func TestZipOfCSVs(t *testing.T) {
	s := parse(t, "spotify_playlists.zip", zipOf(t, map[string]string{
		"liked_songs.csv":       string(fixture(t, "exportify-liked.csv")),
		"Дорога.csv":            string(fixture(t, "exportify-playlist.csv")),
		"__MACOSX/._Дорога.csv": "\x00\x05",
		"README.txt":            "hello",
		"notes.csv":             "foo,bar\n1,2\n",
	}))
	if s.Service != playlistimport.ServiceFile || len(s.Tracks) != 3 || len(liked(s)) != 2 || len(s.Playlists) != 1 {
		t.Fatalf("snapshot %+v", s)
	}
	if got := titles(t, s, "Дорога"); strings.Join(got, ";") != "Bohemian Rhapsody;Кукушка" {
		t.Fatalf("Дорога %v", got)
	}
}

// stubResolver names the ids it knows; it records what it was asked.
type stubResolver struct {
	known map[string]playlistimport.SnapTrack
	asked []string
	err   error
}

func (s *stubResolver) Resolve(_ context.Context, ids []string) (map[string]playlistimport.SnapTrack, error) {
	s.asked = append(s.asked, ids...)
	out := map[string]playlistimport.SnapTrack{}
	for _, id := range ids {
		if t, ok := s.known[id]; ok {
			out[id] = t
		}
	}
	return out, s.err
}

func TestTakeoutResolveFillsUnknownIds(t *testing.T) {
	s := parse(t, "takeout.zip", zipOf(t, map[string]string{
		"Takeout/YouTube Music/music library songs.csv":     "Video ID,Song Title,Album Title,Artist Name\nid1,Кукушка,Чёрный альбом,Кино\n",
		"Takeout/YouTube Music/playlists/Дорога-videos.csv": "Video ID,Playlist Video Creation Timestamp\nid1,2024\nid2,2024\nid3,2024\n",
	}))
	r := &stubResolver{known: map[string]playlistimport.SnapTrack{"id2": {Artist: "Кино", Title: "Группа крови", Album: "Группа крови", DurationMs: 286000}}}
	got, err := Resolve(context.Background(), s, r)
	if err != nil {
		t.Fatal(err)
	}
	// only the ids the Takeout does not name are asked for
	if strings.Join(r.asked, ",") != "id2,id3" {
		t.Fatalf("asked %v", r.asked)
	}
	by := byKey(got)
	if b := by["id2"]; b.Artist != "Кино" || b.Title != "Группа крови" || b.Album != "Группа крови" || b.DurationMs != 286000 || b.Key != "id2" || b.Liked {
		t.Fatalf("id2 %+v", b)
	}
	if by["id1"].Title != "Кукушка" || Unresolved(got) != 1 || Unresolved(s) != 2 {
		t.Fatalf("got %+v (the input is left as it was)", got)
	}
	// a resolver failing midway: what it named is kept, the error returned
	r = &stubResolver{known: r.known, err: errors.New("oembed: 503")}
	got, err = Resolve(context.Background(), s, r)
	if err == nil || Unresolved(got) != 1 {
		t.Fatalf("partial %v %d", err, Unresolved(got))
	}
	// nothing unknown: the resolver is not asked
	r = &stubResolver{}
	if _, err := Resolve(context.Background(), byKeySnap(by), r); err != nil || len(r.asked) != 0 {
		t.Fatalf("asked %v %v", r.asked, err)
	}
}

func byKeySnap(m map[string]playlistimport.SnapTrack) playlistimport.Snapshot {
	s := playlistimport.Snapshot{Service: playlistimport.ServiceYouTube}
	for _, t := range m {
		if t.Title != "" {
			s.Tracks = append(s.Tracks, t)
		}
	}
	return s
}
