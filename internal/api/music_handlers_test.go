package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
)

type tTrack struct {
	disc, no int
	title    string
	file     string // relative to the album folder
	genre    string
}

// album writes the track files (tiny, not real audio) and adds the album.
func (e *libEnv) album(artist, title string, year int, tracks ...tTrack) (albumID int64, trackIDs []int64) {
	e.t.Helper()
	dir := filepath.Join(e.media, "music", artist, title)
	var in []library.TrackInput
	for _, t := range tracks {
		p := filepath.Join(dir, t.file)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("audio "+t.title), 0o644); err != nil {
			e.t.Fatal(err)
		}
		in = append(in, library.TrackInput{File: library.FileInput{Path: p, Size: int64(len("audio " + t.title)), Mtime: 1},
			AlbumDir: dir, Album: title, Artist: artist, Title: t.title, Disc: t.disc, Track: t.no, Year: year,
			Codec: "flac", Lossless: true, Genre: t.genre})
	}
	id, err := e.repo.UpsertAlbumTracks(context.Background(), artist, in)
	if err != nil {
		e.t.Fatal(err)
	}
	trs, err := e.repo.AlbumTracks(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, tr := range trs {
		trackIDs = append(trackIDs, tr.ID)
	}
	return id, trackIDs
}

func (e *libEnv) simpleAlbum(artist, title string, n int) (int64, []int64) {
	var ts []tTrack
	for i := 1; i <= n; i++ {
		ts = append(ts, tTrack{disc: 1, no: i, title: title + " " + itoa(int64(i)), file: itoa(int64(i)) + ".flac"})
	}
	return e.album(artist, title, 1990, ts...)
}

func decodeJSON[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return v
}

type shelvesOut struct {
	Shelves []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Cards []Card `json:"cards"`
	} `json:"shelves"`
}

func TestMusicHomeShape(t *testing.T) {
	e := newLibEnv(t)
	_, tr := e.simpleAlbum("Кино", "Группа крови", 2)
	if w := e.do("POST", "/api/music/plays", `{"trackId":`+itoa(tr[0])+`}`, e.admin); w.Code != 204 {
		t.Fatalf("play %d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/api/music/home", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	out := decodeJSON[shelvesOut](t, w.Body.Bytes())
	want := [][2]string{{"recent-albums", "Недавно добавленные"}, {"recently-played", "Слушали недавно"}, {"artists", "Артисты"}}
	if len(out.Shelves) != len(want) {
		t.Fatalf("no playlists → no shelf: %s", w.Body)
	}
	for i, s := range out.Shelves {
		if s.ID != want[i][0] || s.Title != want[i][1] || len(s.Cards) != 1 {
			t.Fatalf("shelf %d: %+v", i, s)
		}
	}
	if c := out.Shelves[0].Cards[0]; c.Kind != "album" || c.Subtitle != "Кино" || c.Title != "Группа крови" {
		t.Fatalf("album card %+v", c)
	}
	if w := e.do("POST", "/api/music/playlists", `{"name":"Дорога","trackIds":[`+itoa(tr[0])+`,`+itoa(tr[1])+`]}`, e.admin); w.Code != 200 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	out = decodeJSON[shelvesOut](t, e.do("GET", "/api/music/home", "", e.admin).Body.Bytes())
	last := out.Shelves[len(out.Shelves)-1]
	if last.ID != "playlists" || last.Title != "Плейлисты" || len(last.Cards) != 1 || last.Cards[0].Kind != "playlist" || last.Cards[0].Subtitle != "2 песни" {
		t.Fatalf("playlists shelf %+v", last)
	}
}

type albumDetailOut struct {
	Card   Card `json:"card"`
	Artist struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
	Year       int        `json:"year"`
	Genres     []string   `json:"genres"`
	DurationMs int64      `json:"durationMs"`
	TrackCount int        `json:"trackCount"`
	Quality    string     `json:"quality"`
	Tracks     []TrackDTO `json:"tracks"`
	Starred    bool       `json:"starred"`
}

func TestAlbumDetailOrdersByDiscTrack(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.album("Аквариум", "Радио Африка", 1983,
		tTrack{2, 1, "Второй диск", "CD2/01.flac", "Рок"},
		tTrack{1, 10, "Десятая", "CD1/10.flac", ""},
		tTrack{1, 2, "Вторая", "CD1/02.flac", ""})
	w := e.do("GET", "/api/music/albums/"+itoa(id), "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d := decodeJSON[albumDetailOut](t, w.Body.Bytes())
	var got []string
	for _, tr := range d.Tracks {
		got = append(got, tr.Title)
	}
	if strings.Join(got, "|") != "Вторая|Десятая|Второй диск" {
		t.Fatalf("order %v", got)
	}
	if d.Artist.Name != "Аквариум" || d.TrackCount != 3 || d.Year != 1983 || len(d.Genres) != 1 || d.Card.Title != "Радио Африка" {
		t.Fatalf("%+v", d)
	}
	if tr := d.Tracks[2]; tr.DiscNo != 2 || tr.TrackNo != 1 || tr.FileID == 0 || tr.AlbumID != id || tr.Artist != "Аквариум" || !tr.Lossless {
		t.Fatalf("track %+v", tr)
	}
	if !strings.Contains(w.Body.String(), `"card":{`) {
		t.Fatalf("card must be nested: %s", w.Body)
	}
	if w := e.do("GET", "/api/music/albums/"+itoa(d.Tracks[0].ID), "", e.admin); w.Code != 404 {
		t.Fatalf("a track is not an album: %d", w.Code)
	}
}

func TestAlbumQualityLabel(t *testing.T) {
	for _, c := range []struct {
		probe, codec string
		lossless     bool
		want         string
	}{
		{`{"Audio":[{"Codec":"flac","SampleRate":96000,"BitDepth":24}]}`, "flac", true, "Lossless · FLAC 24 бит / 96 кГц"},
		{`{"Audio":[{"Codec":"flac","SampleRate":44100,"BitDepth":16}]}`, "flac", true, "Lossless · FLAC"},
		{`{"Audio":[{"Codec":"flac","SampleRate":88200,"BitDepth":24}]}`, "flac", true, "Lossless · FLAC 24 бит / 88,2 кГц"},
		{`{"Audio":[{"Codec":"mp3","SampleRate":44100,"BitRate":320000}]}`, "mp3", false, "MP3 320 кбит/с"},
		{`{"Audio":[{"Codec":"aac","SampleRate":44100,"BitRate":256874}]}`, "aac", false, "AAC 256 кбит/с"},
		{``, "mp3", false, "MP3"},
	} {
		if got := qualityLabel(i18n.RU, c.probe, c.codec, c.lossless); got != c.want {
			t.Errorf("%s: %q, want %q", c.probe, got, c.want)
		}
	}
	e := newLibEnv(t)
	id, _ := e.simpleAlbum("Кино", "Ночь", 1)
	tr, _ := e.repo.AlbumTracks(context.Background(), id)
	e.probeJSON(tr[0].FileID, `{"Audio":[{"Codec":"flac","SampleRate":96000,"BitDepth":24}]}`)
	d := decodeJSON[albumDetailOut](t, e.do("GET", "/api/music/albums/"+itoa(id), "", e.admin).Body.Bytes())
	if d.Quality != "Lossless · FLAC 24 бит / 96 кГц" {
		t.Fatalf("quality %q", d.Quality)
	}
}

func TestAlbumZip(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.album("Кино", "Звезда по имени Солнце", 1989,
		tTrack{1, 1, "Песня без слов", "01.flac", ""}, tTrack{2, 1, "Звезда", "CD2/01.flac", ""})
	dir := filepath.Join(e.media, "music", "Кино", "Звезда по имени Солнце")
	for name, body := range map[string]string{"album.cue": "FILE", "folder.jpg": "jpg", "rip.log": "log", "notes.txt": "skip",
		"CD2/cd2.cue": "FILE2", "Scans/back.jpg": "skip"} {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(t.TempDir(), "secret.jpg")
	os.WriteFile(secret, []byte("secret"), 0o644)
	if err := os.Symlink(secret, filepath.Join(dir, "cover.jpg")); err != nil { // never followed out of the folder
		t.Fatal(err)
	}
	w := e.do("GET", "/api/music/albums/"+itoa(id)+"/zip", "", e.admin)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	_, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if err != nil || params["filename"] != "Кино - Звезда по имени Солнце.zip" {
		t.Fatalf("disposition %q %v", w.Header().Get("Content-Disposition"), err)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Fatalf("%s compressed", f.Name)
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}
	for _, n := range []string{"01.flac", "CD2/01.flac", "album.cue", "folder.jpg", "rip.log", "CD2/cd2.cue"} {
		if _, ok := got[n]; !ok {
			t.Fatalf("missing %s in %v", n, got)
		}
	}
	if len(got) != 6 || got["folder.jpg"] != "jpg" {
		t.Fatalf("entries %v", got)
	}
}

func TestAlbumsSortFrequentStarredGenre(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	a, ta := e.album("Кино", "А", 1985, tTrack{1, 1, "a1", "1.flac", "Рок"})
	b, tb := e.album("Кино", "Б", 1987, tTrack{1, 1, "b1", "1.flac", "Поп"})
	c, _ := e.album("Кино", "В", 1990, tTrack{1, 1, "c1", "1.flac", "рок"})
	st := music.New(e.db)
	guest, err := e.store.CreateUser(ctx, auth.NewUser{Name: "Гость"})
	if err != nil {
		t.Fatal(err)
	}
	other := guest.ID
	now := time.Now()
	for i := 0; i < 3; i++ {
		st.Played(ctx, 1, tb[0], now.Add(time.Duration(i)*time.Second))
	}
	st.Played(ctx, 1, ta[0], now.Add(time.Minute))
	for i := 0; i < 5; i++ {
		st.Played(ctx, other, ta[0], now)
	}
	got, total, err := e.repo.Albums(ctx, library.AlbumQuery{Sort: "frequent", UserID: 1})
	if err != nil || total != 2 || len(got) != 2 || got[0].ID != b || got[1].ID != a {
		t.Fatalf("frequent %v %d %v", ids(got), total, err)
	}
	if got, _, _ = e.repo.Albums(ctx, library.AlbumQuery{Sort: "recent", UserID: 1}); len(got) != 2 || got[0].ID != a {
		t.Fatalf("recent %v", ids(got))
	}
	st.Star(ctx, 1, c)
	st.Star(ctx, other, a)
	if got, total, _ = e.repo.Albums(ctx, library.AlbumQuery{Starred: true, UserID: 1}); total != 1 || got[0].ID != c {
		t.Fatalf("starred %v", ids(got))
	}
	if got, _, _ = e.repo.Albums(ctx, library.AlbumQuery{Genre: "Рок", Sort: "year"}); len(got) != 2 || got[0].ID != c || got[1].ID != a {
		t.Fatalf("genre %v", ids(got))
	}
	if got, _, _ = e.repo.Albums(ctx, library.AlbumQuery{FromYear: 1990, ToYear: 1985}); len(got) != 3 || got[0].ID != c || got[2].ID != a {
		t.Fatalf("years desc %v", ids(got))
	}
	if got, _, _ = e.repo.Albums(ctx, library.AlbumQuery{FromYear: 1986, ToYear: 1995}); len(got) != 2 || got[0].ID != b {
		t.Fatalf("years %v", ids(got))
	}
	w := e.do("GET", "/api/music/albums?sort=frequent", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func ids(as []library.Album) []int64 {
	var out []int64
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

type playlistOut struct {
	Playlist PlaylistDTO     `json:"playlist"`
	Tracks   []PlaylistTrack `json:"tracks"`
}

func TestPlaylistCRUDPerUserAPI(t *testing.T) {
	e := newLibEnv(t)
	_, tr := e.simpleAlbum("Кино", "Группа крови", 3)
	if w := e.do("POST", "/api/music/playlists", `{"name":"  ","trackIds":[]}`, e.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "Введите название плейлиста") {
		t.Fatalf("empty name %d %s", w.Code, w.Body)
	}
	w := e.do("POST", "/api/music/playlists", `{"name":"Дорога","trackIds":[`+itoa(tr[0])+`]}`, e.admin)
	p := decodeJSON[PlaylistDTO](t, w.Body.Bytes())
	if w.Code != 200 || p.Name != "Дорога" || p.TrackCount != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	pu := "/api/music/playlists/" + itoa(p.ID)
	w = e.do("PATCH", pu, `{"append":[`+itoa(tr[1])+`,`+itoa(tr[2])+`]}`, e.admin)
	if w.Code != 200 || decodeJSON[PlaylistDTO](t, w.Body.Bytes()).TrackCount != 3 {
		t.Fatalf("append %d %s", w.Code, w.Body)
	}
	e.do("PATCH", pu, `{"remove":[1]}`, e.admin)
	e.do("PATCH", pu, `{"order":[1,0]}`, e.admin)
	out := decodeJSON[playlistOut](t, e.do("GET", pu, "", e.admin).Body.Bytes())
	if len(out.Tracks) != 2 || out.Tracks[0].ID != tr[2] || out.Tracks[1].ID != tr[0] || out.Playlist.TrackCount != 2 ||
		out.Tracks[0].Idx != 0 || out.Tracks[1].Idx != 1 {
		t.Fatalf("%+v", out)
	}
	if w := e.do("PATCH", pu, `{"append":[999999]}`, e.admin); w.Code != 400 {
		t.Fatalf("unknown track %d", w.Code)
	}
	other := e.login(`{"name":"Мама"}`)
	for _, m := range []string{"GET", "PATCH", "DELETE"} {
		if w := e.do(m, pu, `{"name":"x"}`, other); w.Code != 404 {
			t.Fatalf("other %s %d", m, w.Code)
		}
	}
	if w := e.do("GET", "/api/music/playlists", "", other); !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("other list %s", w.Body)
	}
	if w := e.do("DELETE", pu, "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := e.do("GET", pu, "", e.admin); w.Code != 404 {
		t.Fatalf("deleted %d", w.Code)
	}
}

func TestStarAndLyrics(t *testing.T) {
	e := newLibEnv(t)
	id, tr := e.simpleAlbum("Кино", "Группа крови", 1)
	if w := e.do("POST", "/api/music/star/"+itoa(tr[0]), "", e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d := decodeJSON[albumDetailOut](t, e.do("GET", "/api/music/albums/"+itoa(id), "", e.admin).Body.Bytes())
	if !d.Tracks[0].Starred || d.Starred {
		t.Fatalf("%+v", d)
	}
	if w := e.do("DELETE", "/api/music/star/"+itoa(tr[0]), "", e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	w := e.do("GET", "/api/music/tracks/"+itoa(tr[0])+"/lyrics", "", e.admin)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Текста нет") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	e.db.Exec(`UPDATE tracks SET lyrics=? WHERE item_id=?`, "[00:01.00]Красная-красная кровь\n", tr[0])
	w = e.do("GET", "/api/music/tracks/"+itoa(tr[0])+"/lyrics", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"synced":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestArtistDetailAndSongs(t *testing.T) {
	e := newLibEnv(t)
	a1, tr := e.simpleAlbum("Кино", "Группа крови", 2)
	e.album("Разные исполнители", "Рок-н-ролл", 1995, tTrack{1, 1, "Кукушка", "1.flac", ""})
	e.db.Exec(`UPDATE tracks SET artist='Кино' WHERE item_id IN (SELECT id FROM items WHERE title='Кукушка')`)
	e.db.Exec(`UPDATE items SET poster='1111111111111111111111111111111111111111.jpg' WHERE id=?`, a1)
	music.New(e.db).Played(context.Background(), 1, tr[1], time.Now())
	al, _ := e.repo.Item(context.Background(), a1)
	w := e.do("GET", "/api/music/artists/"+itoa(al.ParentID), "", e.admin)
	var d struct {
		Card      Card       `json:"card"`
		Albums    []Card     `json:"albums"`
		TopTracks []TrackDTO `json:"topTracks"`
		AppearsOn []Card     `json:"appearsOn"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if d.Card.Title != "Кино" || d.Card.Poster != "/api/images/1111111111111111111111111111111111111111.jpg" || len(d.Albums) != 1 ||
		len(d.TopTracks) != 2 || d.TopTracks[0].ID != tr[1] || len(d.AppearsOn) != 1 || d.AppearsOn[0].Title != "Рок-н-ролл" {
		t.Fatalf("%+v", d)
	}
	w = e.do("GET", "/api/music/artists", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/music/songs?limit=2", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":3`) || !strings.Contains(w.Body.String(), `"fileId"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestKidSeesMusicNotAdultManga(t *testing.T) {
	e := newLibEnv(t)
	album, tr := e.simpleAlbum("Кино", "Группа крови", 1)
	_, vols := e.comic("Взрослое", "18+", 1)
	kid := e.kid("")
	for _, u := range []string{"/api/music/albums/" + itoa(album), "/api/music/tracks/" + itoa(tr[0]) + "/lyrics"} {
		if w := e.do("GET", u, "", kid); w.Code == 403 || (w.Code != 200 && !strings.Contains(w.Body.String(), "Текста нет")) {
			t.Fatalf("kid %s: %d %s", u, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/music/albums", "", kid); !strings.Contains(w.Body.String(), "Группа крови") {
		t.Fatalf("kid albums %s", w.Body)
	}
	for _, u := range []string{"/api/books/" + itoa(vols[0].id), "/api/comics/" + itoa(vols[0].file) + "/pages",
		"/api/comics/" + itoa(vols[0].file) + "/page/0"} {
		if w := e.do("GET", u, "", kid); w.Code != 404 {
			t.Fatalf("kid %s: %d", u, w.Code)
		}
		if w := e.do("GET", u, "", e.admin); w.Code != 200 {
			t.Fatalf("admin %s: %d %s", u, w.Code, w.Body)
		}
	}
	if w := e.do("PUT", "/api/progress/"+itoa(vols[0].id), `{"locator":"page:1","fraction":0.1}`, kid); w.Code != 404 {
		t.Fatalf("kid progress %d", w.Code)
	}
	if w := e.do("GET", "/api/books?kind=comic", "", kid); strings.Contains(w.Body.String(), "Взрослое") {
		t.Fatalf("kid comics %s", w.Body)
	}
}

// Positions are stored positions: a hidden entry (missing file) is left
// out of the list but keeps its place through remove and order.
func TestPlaylistPositionsSkipHidden(t *testing.T) {
	e := newLibEnv(t)
	_, tr := e.simpleAlbum("Кино", "Группа крови", 4)
	w := e.do("POST", "/api/music/playlists", `{"name":"Дорога","trackIds":[`+itoa(tr[0])+`,`+itoa(tr[1])+`,`+itoa(tr[2])+`,`+itoa(tr[3])+`]}`, e.admin)
	pu := "/api/music/playlists/" + itoa(decodeJSON[PlaylistDTO](t, w.Body.Bytes()).ID)
	if _, err := e.db.Exec(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, tr[0]); err != nil {
		t.Fatal(err)
	}
	out := decodeJSON[playlistOut](t, e.do("GET", pu, "", e.admin).Body.Bytes())
	if len(out.Tracks) != 3 || out.Tracks[0].ID != tr[1] || out.Tracks[0].Idx != 1 || out.Tracks[2].Idx != 3 {
		t.Fatalf("%+v", out.Tracks)
	}
	if w := e.do("PATCH", pu, `{"remove":[1]}`, e.admin); w.Code != 200 { // t1, the one shown first
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", pu, `{"remove":[0]}`, e.admin); w.Code != 400 { // the hidden entry
		t.Fatalf("hidden position %d", w.Code)
	}
	// stored now [t0 t2 t3]: reverse the visible ones
	if w := e.do("PATCH", pu, `{"order":[2,1]}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	e.db.Exec(`UPDATE items SET missing_since=NULL WHERE id=?`, tr[0])
	out = decodeJSON[playlistOut](t, e.do("GET", pu, "", e.admin).Body.Bytes())
	var got []int64
	for _, x := range out.Tracks {
		got = append(got, x.ID)
	}
	if len(got) != 3 || got[0] != tr[0] || got[1] != tr[3] || got[2] != tr[2] {
		t.Fatalf("stored %v, want t0 t3 t2", got)
	}
}

func TestWatchlistKinds(t *testing.T) {
	e := newLibEnv(t)
	album, _ := e.simpleAlbum("Кино", "Группа крови", 1)
	b := e.book("Идиот", ".epub")
	if w := e.do("POST", "/api/watchlist/"+itoa(album), "", e.admin); w.Code != 400 {
		t.Fatalf("album %d", w.Code)
	}
	if w := e.do("POST", "/api/watchlist/"+itoa(b), "", e.admin); w.Code != 204 {
		t.Fatalf("book %d", w.Code)
	}
}

// TestCueSlice: only tracks cut from a bigger file are CUE slices (their
// «Скачать песню» would download the whole image).
func TestCueSlice(t *testing.T) {
	end := int64(180_000)
	for _, c := range []struct {
		start int64
		end   *int64
		want  bool
	}{{0, nil, false}, {0, &end, true}, {200_000, nil, true}, {200_000, &end, true}} {
		if got := cueSlice(library.Track{StartMs: c.start, EndMs: c.end}); got != c.want {
			t.Errorf("start %d end %v: %v, want %v", c.start, c.end, got, c.want)
		}
	}
}

type songsOut struct {
	Items []TrackDTO `json:"items"`
	Total int        `json:"total"`
}

func songIDs(s songsOut) []int64 {
	var out []int64
	for _, t := range s.Items {
		out = append(out, t.ID)
	}
	return out
}

func TestStarredSongs(t *testing.T) {
	e := newLibEnv(t)
	f := e.shuffleLib()
	t1, t3 := f.First[0], f.First[2]
	for _, id := range []int64{t3, t1} {
		if w := e.do("POST", "/api/music/star/"+itoa(id), "", e.admin); w.Code != 204 {
			t.Fatalf("star %d %s", w.Code, w.Body)
		}
	}
	got := decodeJSON[songsOut](t, e.do("GET", "/api/music/songs?starred=1", "", e.admin).Body.Bytes())
	if ids := songIDs(got); len(ids) != 2 || ids[0] != t1 || ids[1] != t3 || got.Total != 2 || !got.Items[0].Starred {
		t.Fatalf("%+v", got)
	}
	got = decodeJSON[songsOut](t, e.do("GET", "/api/music/songs?starred=1&limit=1&offset=1", "", e.admin).Body.Bytes())
	if ids := songIDs(got); len(ids) != 1 || ids[0] != t3 || got.Total != 2 {
		t.Fatalf("page %+v", got)
	}
	if got = decodeJSON[songsOut](t, e.do("GET", "/api/music/songs?starred=1&offset=5", "", e.admin).Body.Bytes()); len(got.Items) != 0 || got.Total != 2 ||
		!strings.Contains(e.do("GET", "/api/music/songs?starred=1&offset=5", "", e.admin).Body.String(), `"items":[]`) {
		t.Fatalf("past the end %+v", got)
	}
	kid := e.kid("")
	var kidID int64
	if err := e.db.QueryRow(`SELECT id FROM users WHERE name='Вася'`).Scan(&kidID); err != nil {
		t.Fatal(err)
	}
	st := music.New(e.db)
	for _, id := range []int64{f.Adult[0], t1} {
		if err := st.Star(context.Background(), kidID, id); err != nil {
			t.Fatal(err)
		}
	}
	got = decodeJSON[songsOut](t, e.do("GET", "/api/music/songs?starred=1", "", kid).Body.Bytes())
	if ids := songIDs(got); len(ids) != 1 || ids[0] != t1 || got.Total != 1 {
		t.Fatalf("kid %+v", got)
	}
}

func TestTrackDetail(t *testing.T) {
	e := newLibEnv(t)
	f := e.shuffleLib()
	e.mustExec(`UPDATE items SET poster='1111111111111111111111111111111111111111.jpg' WHERE id=?`, f.FirstAlbum)
	e.do("POST", "/api/music/star/"+itoa(f.First[0]), "", e.admin)
	w := e.do("GET", "/api/music/tracks/"+itoa(f.First[0]), "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d := decodeJSON[TrackDTO](t, w.Body.Bytes())
	if d.ID != f.First[0] || d.AlbumID != f.FirstAlbum || d.Cover != "/api/images/1111111111111111111111111111111111111111.jpg" || !d.Starred || d.Album != "Первый альбом" {
		t.Fatalf("%+v", d)
	}
	kid := e.kid("")
	for _, c := range []struct {
		u   string
		who *http.Cookie
	}{
		{"/api/music/tracks/" + itoa(f.FirstAlbum), e.admin},
		{"/api/music/tracks/999999", e.admin},
		{"/api/music/tracks/" + itoa(f.Missing[0]), e.admin},
		{"/api/music/tracks/" + itoa(f.Adult[0]), kid},
		{"/api/music/tracks/" + itoa(f.NoArtist[0]), kid},
	} {
		if w := e.do("GET", c.u, "", c.who); w.Code != 404 || strings.TrimSpace(w.Body.String()) != `{"error":"Не найдено"}` {
			t.Fatalf("%s: %d %s", c.u, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/music/tracks/"+itoa(f.First[0]), "", kid); w.Code != 200 {
		t.Fatalf("kid visible %d", w.Code)
	}
}
