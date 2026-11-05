package subsonic

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"strings"
	"testing"
)

// TestGetAlbumJSONMatchesNavidromeShape: the album and song keys include
// every key of Navidrome's getAlbum (testdata/navidrome_getAlbum.json is
// written by hand after the Navidrome 0.5x response; no server with music
// was available to capture one).
func TestGetAlbumJSONMatchesNavidromeShape(t *testing.T) {
	e := newEnv(t)
	album, _ := e.album("Кино", "Группа крови", 1988, "Рок", tTrack{1, "Группа крови"}, tTrack{2, "Закрой за мной дверь"})
	e.poster(album) // coverArt is only sent for an album with a cover
	got := e.mustOK(e.call("getAlbum", "id", itoa(album)))
	b, err := os.ReadFile("testdata/navidrome_getAlbum.json")
	if err != nil {
		t.Fatal(err)
	}
	var nd map[string]map[string]any
	if err := json.Unmarshal(b, &nd); err != nil {
		t.Fatal(err)
	}
	want := nd["subsonic-response"]["album"].(map[string]any)
	have := got["album"].(map[string]any)
	for k := range keys(want) {
		if !keys(have)[k] {
			t.Errorf("album lacks %q: %v", k, have)
		}
	}
	wantSong := want["song"].([]any)[0].(map[string]any)
	songs := have["song"].([]any)
	if len(songs) != 2 {
		t.Fatalf("songs %v", songs)
	}
	haveSong := songs[0].(map[string]any)
	for k := range keys(wantSong) {
		if !keys(haveSong)[k] {
			t.Errorf("song lacks %q: %v", k, haveSong)
		}
	}
	if haveSong["coverArt"] != "al-"+itoa(album) || haveSong["path"] != "Кино/Группа крови/1 - Группа крови.flac" ||
		haveSong["contentType"] != "audio/flac" || haveSong["duration"] != 120.0 || haveSong["genre"] != "Рок" ||
		haveSong["isDir"] != false || haveSong["type"] != "music" || haveSong["albumId"] != itoa(album) {
		t.Fatalf("song %v", haveSong)
	}
	if have["songCount"] != 2.0 || have["duration"] != 240.0 || have["year"] != 1988.0 {
		t.Fatalf("album %v", have)
	}
}

func TestGetAlbumXML(t *testing.T) {
	e := newEnv(t)
	album, _ := e.album("Кино", "Группа крови", 1988, "Рок", tTrack{1, "Группа крови"}, tTrack{2, "Закрой за мной дверь"})
	p := e.creds()
	p.Set("id", itoa(album))
	w := e.req("getAlbum", "", p)
	var out struct {
		XMLName xml.Name `xml:"http://subsonic.org/restapi subsonic-response"`
		Status  string   `xml:"status,attr"`
		Album   struct {
			Name string `xml:"name,attr"`
			Song []struct {
				Title    string `xml:"title,attr"`
				Track    int    `xml:"track,attr"`
				Duration int    `xml:"duration,attr"`
			} `xml:"http://subsonic.org/restapi song"`
		} `xml:"http://subsonic.org/restapi album"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
	if out.Status != "ok" || out.Album.Name != "Группа крови" || len(out.Album.Song) != 2 ||
		out.Album.Song[1].Track != 2 || out.Album.Song[1].Duration != 120 || out.Album.Song[1].Title != "Закрой за мной дверь" {
		t.Fatalf("%+v\n%s", out, w.Body)
	}
}

func TestAlbumList2Newest(t *testing.T) {
	e := newEnv(t)
	var ids []int64
	for i, name := range []string{"Первый", "Второй", "Третий", "Четвёртый"} {
		id, _ := e.album("Кино", name, 1980+i, "", tTrack{1, name})
		if _, err := e.db.Exec(`UPDATE items SET added_at=? WHERE id=?`, "2024-01-0"+itoa(int64(i+1))+"T00:00:00Z", id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	got := e.mustOK(e.call("getAlbumList2", "type", "newest", "size", "2", "offset", "1"))
	list := got["albumList2"].(map[string]any)["album"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["id"] != itoa(ids[2]) || list[1].(map[string]any)["id"] != itoa(ids[1]) {
		t.Fatalf("newest size 2 offset 1: %v", list)
	}
	// byYear needs both years
	if code, _ := errCode(e.call("getAlbumList2", "type", "byYear", "fromYear", "1980")); code != 10 {
		t.Fatalf("byYear without toYear: %d", code)
	}
	got = e.mustOK(e.call("getAlbumList", "type", "byYear", "fromYear", "1981", "toYear", "1982"))
	dirs := got["albumList"].(map[string]any)["album"].([]any)
	if len(dirs) != 2 || dirs[0].(map[string]any)["isDir"] != true || dirs[0].(map[string]any)["title"] != "Второй" {
		t.Fatalf("byYear: %v", dirs)
	}
	// fromYear > toYear lists newest first
	got = e.mustOK(e.call("getAlbumList2", "type", "byYear", "fromYear", "1983", "toYear", "1981"))
	list = got["albumList2"].(map[string]any)["album"].([]any)
	if len(list) != 3 || list[0].(map[string]any)["year"] != 1983.0 || list[2].(map[string]any)["year"] != 1981.0 {
		t.Fatalf("byYear descending: %v", list)
	}
}

func TestGetArtistsIndexCyrillic(t *testing.T) {
	e := newEnv(t)
	e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Группа крови"})
	e.album("The Animals", "Animals", 1964, "", tTrack{1, "House of the Rising Sun"})
	e.album("ABBA", "Arrival", 1976, "", tTrack{1, "Dancing Queen"})
	e.album("Ария", "Герой асфальта", 1987, "", tTrack{1, "Улица роз"})
	e.album("Ёлка", "Город обмана", 2008, "", tTrack{1, "Девочка"})
	e.album("Ева Польна", "Поёт", 2004, "", tTrack{1, "Песня"})
	got := e.mustOK(e.call("getArtists"))
	arts := got["artists"].(map[string]any)
	if arts["ignoredArticles"] != "The" {
		t.Fatalf("ignoredArticles %v", arts["ignoredArticles"])
	}
	idx := arts["index"].([]any)
	var names []string
	byName := map[string][]string{}
	for _, x := range idx {
		m := x.(map[string]any)
		n := m["name"].(string)
		names = append(names, n)
		for _, a := range m["artist"].([]any) {
			byName[n] = append(byName[n], a.(map[string]any)["name"].(string))
		}
	}
	if strings.Join(names, ",") != "A,А,Е,К" {
		t.Fatalf("indexes %v (Latin A, then Cyrillic А, Е with Ё, К)", names)
	}
	if strings.Join(byName["A"], ",") != "ABBA,The Animals" || strings.Join(byName["К"], ",") != "Кино" ||
		strings.Join(byName["Е"], ",") != "Ева Польна,Ёлка" {
		t.Fatalf("artists %v", byName)
	}
	ix := e.mustOK(e.call("getIndexes"))["indexes"].(map[string]any)
	if ix["lastModified"].(float64) <= 0 || ix["ignoredArticles"] != "The" || len(ix["index"].([]any)) != 4 {
		t.Fatalf("getIndexes %v", ix)
	}
}

func TestGetArtistAndSearch3(t *testing.T) {
	e := newEnv(t)
	al, tr := e.album("Кино", "Группа крови", 1988, "Рок", tTrack{1, "Группа крови"}, tTrack{2, "Звезда по имени Солнце"})
	got := e.mustOK(e.call("getArtists"))
	artistID := got["artists"].(map[string]any)["index"].([]any)[0].(map[string]any)["artist"].([]any)[0].(map[string]any)["id"].(string)
	a := e.mustOK(e.call("getArtist", "id", artistID))["artist"].(map[string]any)
	if a["name"] != "Кино" || a["albumCount"] != 1.0 || a["album"].([]any)[0].(map[string]any)["id"] != itoa(al) {
		t.Fatalf("artist %v", a)
	}
	s := e.mustOK(e.call("search3", "query", "звезда"))["searchResult3"].(map[string]any)
	if songs := s["song"].([]any); len(songs) != 1 || songs[0].(map[string]any)["id"] != itoa(tr[1]) {
		t.Fatalf("search3 songs %v", s)
	}
	// an empty query lists everything (Amperfy syncs this way)
	s = e.mustOK(e.call("search3", "query", `""`, "songCount", "1", "songOffset", "1"))["searchResult3"].(map[string]any)
	if songs := s["song"].([]any); len(songs) != 1 || len(s["album"].([]any)) != 1 || len(s["artist"].([]any)) != 1 {
		t.Fatalf("search3 all %v", s)
	}
	g := e.mustOK(e.call("getGenres"))["genres"].(map[string]any)["genre"].([]any)
	if len(g) != 1 || g[0].(map[string]any)["value"] != "Рок" || g[0].(map[string]any)["songCount"] != 2.0 {
		t.Fatalf("genres %v", g)
	}
	r := e.mustOK(e.call("getRandomSongs", "size", "5", "genre", "рок"))["randomSongs"].(map[string]any)["song"].([]any)
	if len(r) != 2 {
		t.Fatalf("random %v", r)
	}
}

func TestMusicDirectoryWalk(t *testing.T) {
	e := newEnv(t)
	al, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Группа крови"}, tTrack{2, "Спокойная ночь"})
	idx := e.mustOK(e.call("getIndexes"))["indexes"].(map[string]any)["index"].([]any)
	artist := idx[0].(map[string]any)["artist"].([]any)[0].(map[string]any)["id"].(string)
	d := e.mustOK(e.call("getMusicDirectory", "id", artist))["directory"].(map[string]any)
	kids := d["child"].([]any)
	if d["name"] != "Кино" || len(kids) != 1 || kids[0].(map[string]any)["isDir"] != true || kids[0].(map[string]any)["id"] != itoa(al) {
		t.Fatalf("artist directory %v", d)
	}
	d = e.mustOK(e.call("getMusicDirectory", "id", kids[0].(map[string]any)["id"].(string)))["directory"].(map[string]any)
	songs := d["child"].([]any)
	if d["parent"] != artist || len(songs) != 2 || songs[1].(map[string]any)["id"] != itoa(tr[1]) || songs[1].(map[string]any)["isDir"] != false {
		t.Fatalf("album directory %v", d)
	}
	if code, _ := errCode(e.call("getMusicDirectory", "id", itoa(tr[0]))); code != 70 {
		t.Fatalf("a song is not a directory: %d", code)
	}
}

// TestKidFullPages: a rated artist does not shorten a limited profile's
// pages (sync clients stop at the first short page).
func TestKidFullPages(t *testing.T) {
	e := newEnv(t)
	bad, _ := e.album("Аааа взрослые", "Аааа", 2000, "", tTrack{1, "Аа 1"}, tTrack{2, "Аа 2"}, tTrack{3, "Аа 3"})
	e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Бб 1"}, tTrack{2, "Бб 2"}, tTrack{3, "Бб 3"})
	e.album("Ария", "Герой", 1987, "", tTrack{1, "Вв 1"})
	if _, err := e.db.Exec(`UPDATE items SET rating='18+' WHERE id=(SELECT parent_id FROM items WHERE id=?)`, bad); err != nil {
		t.Fatal(err)
	}
	e.asKid()
	s := e.mustOK(e.call("search3", "query", "", "songCount", "3", "artistCount", "1", "albumCount", "1"))["searchResult3"].(map[string]any)
	if n := len(s["song"].([]any)); n != 3 {
		t.Fatalf("songs on page 0: %d %v", n, s["song"])
	}
	if a := s["artist"].([]any); len(a) != 1 || a[0].(map[string]any)["name"] == "Аааа взрослые" {
		t.Fatalf("artists %v", a)
	}
	if a := s["album"].([]any); len(a) != 1 || a[0].(map[string]any)["name"] == "Аааа" {
		t.Fatalf("albums %v", a)
	}
	s = e.mustOK(e.call("search3", "query", "аа", "songCount", "3"))["searchResult3"].(map[string]any)
	if n := len(s["song"].([]any)); n != 0 {
		t.Fatalf("text search shows rated songs: %v", s["song"])
	}
	l := e.mustOK(e.call("getAlbumList2", "type", "alphabeticalByName", "size", "1"))["albumList2"].(map[string]any)["album"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["name"] == "Аааа" {
		t.Fatalf("album list page %v", l)
	}
	if g := e.mustOK(e.call("getRandomSongs", "size", "10"))["randomSongs"].(map[string]any)["song"].([]any); len(g) != 4 {
		t.Fatalf("random songs %d", len(g))
	}
}

func TestRenderErrorIsNotSwallowed(t *testing.T) {
	e := newEnv(t)
	al, _ := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"})
	if _, err := e.db.Exec(`DROP TABLE stars`); err != nil {
		t.Fatal(err)
	}
	if code, _ := errCode(e.call("getAlbum", "id", itoa(al))); code != 0 {
		t.Fatalf("a failed star lookup must fail the reply, got code %d", code)
	}
}

func TestStubMethods(t *testing.T) {
	e := newEnv(t)
	e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"}, tTrack{2, "Два"})
	if s := e.mustOK(e.call("getScanStatus"))["scanStatus"].(map[string]any); s["scanning"] != false || s["count"] != 2.0 {
		t.Fatalf("scanStatus %v", s)
	}
	for _, m := range []string{"getNowPlaying", "getArtistInfo2", "getAlbumInfo2", "getTopSongs", "getSimilarSongs2",
		"getPlayQueue", "savePlayQueue", "getBookmarks", "getPodcasts", "getInternetRadioStations"} {
		e.mustOK(e.call(m, "id", "1", "artist", "Кино"))
	}
}
