package files

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

func TestAppleLibraryXML(t *testing.T) {
	s := parse(t, "Library.xml", fixture(t, "Library.xml"))
	if s.Service != playlistimport.ServiceApple {
		t.Fatalf("service %q", s.Service)
	}
	if len(s.Tracks) != 3 {
		t.Fatalf("tracks %+v", s.Tracks)
	}
	tr := byKey(s)
	k := tr["A1B2C3D4E5F60001"]
	if k.Title != "Кукушка" || k.Artist != "Кино" || k.AlbumArtist != "Кино" || k.Album != "Чёрный альбом" ||
		k.DurationMs != 401000 || !k.Liked || k.AddedAt == nil || !k.AddedAt.Equal(time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)) {
		t.Fatalf("Кукушка %+v", k)
	}
	// «Любимые песни» makes its songs liked and is not a playlist
	if !tr["A1B2C3D4E5F60002"].Liked || tr["A1B2C3D4E5F60003"].Liked {
		t.Fatalf("liked %+v", s.Tracks)
	}
	if len(s.Playlists) != 1 || s.Playlists[0].ID != "B000000000000006" {
		t.Fatalf("playlists %+v", s.Playlists)
	}
	// the podcast in «Дорога» is dropped, the order is kept
	if got := titles(t, s, "Дорога"); strings.Join(got, ";") != "Город золотой;Кукушка" {
		t.Fatalf("Дорога %v", got)
	}
}

func TestAppleLibraryXMLSkipsKinds(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Tracks</key><dict>
	<key>1</key><dict><key>Track ID</key><integer>1</integer><key>Name</key><string>Глава 1</string><key>Artist</key><string>Автор</string><key>Kind</key><string>Защищенная аудиокнига</string></dict>
	<key>2</key><dict><key>Track ID</key><integer>2</integer><key>Name</key><string>Ch 1</string><key>Artist</key><string>Author</string><key>Kind</key><string>Audiobook file</string></dict>
	<key>3</key><dict><key>Track ID</key><integer>3</integer><key>Name</key><string>Фильм</string><key>Artist</key><string>Студия</string><key>Movie</key><true/></dict>
	<key>4</key><dict><key>Track ID</key><integer>4</integer><key>Name</key><string>Серия</string><key>Artist</key><string>Канал</string><key>TV Show</key><true/></dict>
	<key>5</key><dict><key>Track ID</key><integer>5</integer><key>Name</key><string>Песня</string><key>Artist</key><string>Певец</string><key>Favorited</key><true/></dict>
	</dict></dict></plist>`
	s := parse(t, "Медиатека.xml", []byte(doc))
	if len(s.Tracks) != 1 || s.Tracks[0].Title != "Песня" || !s.Tracks[0].Liked || s.Tracks[0].Key != "5" {
		t.Fatalf("tracks %+v", s.Tracks)
	}
}

// encoding/xml never expands a DTD's entities (internal or external):
// in strict mode an entity it does not know is a syntax error.
func TestAppleLibraryXMLEntitiesRefused(t *testing.T) {
	laughs := `<?xml version="1.0"?><!DOCTYPE plist [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">` +
		`<!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;"><!ENTITY d "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;">]>` +
		`<plist version="1.0"><dict><key>Tracks</key><dict><key>1</key><dict><key>Name</key><string>&d;</string><key>Artist</key><string>x</string></dict></dict></dict></plist>`
	external := `<?xml version="1.0"?><!DOCTYPE plist [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>` +
		`<plist version="1.0"><dict><key>Tracks</key><dict><key>1</key><dict><key>Name</key><string>&xxe;</string><key>Artist</key><string>x</string></dict></dict></dict></plist>`
	for name, doc := range map[string]string{"laughs": laughs, "external": external} {
		start := time.Now()
		if _, err := Parse("Library.xml", []byte(doc)); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: %v", name, err)
		}
		if time.Since(start) > time.Second {
			t.Errorf("%s took %v", name, time.Since(start))
		}
	}
}

func TestAppleLibraryXMLDepthBounded(t *testing.T) {
	doc := `<?xml version="1.0"?><plist version="1.0"><dict><key>Tracks</key><dict><key>1</key>` +
		strings.Repeat("<array>", 100000) + strings.Repeat("</array>", 100000) + `</dict></dict></plist>`
	if _, err := Parse("Library.xml", []byte(doc)); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("err %v", err)
	}
}
