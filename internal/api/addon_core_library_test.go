package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/releaseprefs"
)

// coreLib is the addon env with a library under real folders: a movie
// (TMDB 603), a series (TMDB 42, two episodes), an album and a book, and
// refs of the addons "lib" and "lib2".
type coreLib struct {
	*addonEnv
	repo                          *library.Repo
	root, movies, series, music   string
	movie, show, album, book      int64
	moviePath, ep1, ep2, tr1, tr2 string
	outside                       string
}

func newCoreLib(t *testing.T) *coreLib {
	t.Helper()
	// resolved: so only the symlinks made here are between a path and its root
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &coreLib{root: base, movies: filepath.Join(base, "movies"), series: filepath.Join(base, "series"),
		music: filepath.Join(base, "music"), outside: filepath.Join(base, "outside")}
	c.moviePath = filepath.Join(c.movies, "Matrix (1999)", "Matrix.mkv")
	c.ep1 = filepath.Join(c.series, "Shogun", "S01", "Shogun.S01E01.mkv")
	c.ep2 = filepath.Join(c.series, "Shogun", "S01", "Shogun.S01E02.mkv")
	albumDir := filepath.Join(c.music, "Кино", "Группа крови")
	c.tr1, c.tr2 = filepath.Join(albumDir, "01.flac"), filepath.Join(albumDir, "02.flac")
	for _, p := range []string{c.moviePath, c.ep1, c.ep2, c.tr1, c.tr2, filepath.Join(c.outside, "secret.mkv")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// a folder inside the movies root that leads outside it
	if err := os.Symlink(c.outside, filepath.Join(c.movies, "escape")); err != nil {
		t.Fatal(err)
	}
	// and one that leads nowhere yet, outside it
	if err := os.Symlink(filepath.Join(c.outside, "later"), filepath.Join(c.movies, "dangling")); err != nil {
		t.Fatal(err)
	}
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		d.Jobs = &library.Jobs{Scanner: &library.Scanner{Movies: c.movies, Series: c.series, Music: c.music}}
	})
	c.repo = library.NewRepo(c.db)
	ctx := context.Background()
	if c.movie, err = c.repo.UpsertMovie(ctx, library.MovieInput{Title: "Матрица", OriginalTitle: "The Matrix", Year: 1999, TmdbID: 603,
		File: library.FileInput{Path: c.moviePath, Size: 10, Mtime: 5}}); err != nil {
		t.Fatal(err)
	}
	f, _ := c.repo.FileByPath(ctx, c.moviePath)
	if err := c.repo.SetProbe(ctx, f.ID, `{"Audio":[{"Title":"Дубляж"}]}`, 8160000); err != nil {
		t.Fatal(err)
	}
	for i, p := range []string{c.ep1, c.ep2} {
		ep, err := c.repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: 1, Episode: i + 1,
			File: library.FileInput{Path: p, Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		c.db.QueryRow(`SELECT p.parent_id FROM items i JOIN items p ON p.id=i.parent_id WHERE i.id=?`, ep).Scan(&c.show)
	}
	f, _ = c.repo.FileByPath(ctx, c.ep1)
	if err := c.repo.SetProbe(ctx, f.ID, `{"Audio":[{"Title":"Оригинал"}]}`, 0); err != nil {
		t.Fatal(err)
	}
	var in []library.TrackInput
	for i, p := range []string{c.tr1, c.tr2} {
		in = append(in, library.TrackInput{File: library.FileInput{Path: p, Size: 1, Mtime: 1}, AlbumDir: albumDir,
			Album: "Группа крови", Title: []string{"Группа крови", "Закрой за мной дверь"}[i], Track: i + 1})
	}
	if c.album, err = c.repo.UpsertAlbumTracks(ctx, "Кино", in); err != nil {
		t.Fatal(err)
	}
	if c.book, err = c.repo.UpsertBook(ctx, library.BookInput{Title: "Мастер и Маргарита", Authors: []string{"Михаил Булгаков"},
		Files: []library.FileInput{{Path: "/b/Булгаков/Мастер и Маргарита.epub", Size: 1}}}); err != nil {
		t.Fatal(err)
	}
	c.bindRef("lib", "r1", c.moviePath)
	c.bindRef("lib", "r1", c.tr1)
	c.bindRef("lib2", "r1", c.tr2) // the same ref string, another addon

	lib := c.fake.installed["lib"]
	lib2 := lib
	lib2.ID, lib2.Manifest.ID = "lib2", "lib2"
	ppl := lib
	ppl.ID, ppl.Manifest.ID, ppl.Manifest.Permissions = "ppl", "ppl", []string{"users.read"}
	both := lib
	both.ID, both.Manifest.ID, both.Manifest.Permissions = "both", "both", []string{"library.read", "users.read"}
	c.fake.mu.Lock()
	c.fake.installed["lib2"], c.fake.installed["ppl"], c.fake.installed["both"] = lib2, ppl, both
	c.fake.tokens["tok-lib2"], c.fake.tokens["tok-ppl"], c.fake.tokens["tok-both"] = "lib2", "ppl", "both"
	c.fake.mu.Unlock()
	return c
}

func (c *coreLib) bindRef(addon, ref, path string) {
	c.t.Helper()
	f, err := c.repo.FileByPath(context.Background(), path)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO addon_file_refs(file_id, addon_id, ref) VALUES(?,?,?)`, f.ID, addon, ref); err != nil {
		c.t.Fatal(err)
	}
}

// get calls the core API as the addon of tok; out (if set) gets the JSON.
func (c *coreLib) get(tok, path string, wantCode int, out any) string {
	c.t.Helper()
	w := c.do("GET", "/api/addon-core/v1"+path, "", nil, "Authorization", "Bearer "+tok)
	if w.Code != wantCode {
		c.t.Fatalf("GET %s: %d %s (want %d)", path, w.Code, w.Body, wantCode)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			c.t.Fatalf("GET %s: %v: %s", path, err, w.Body)
		}
	}
	return w.Body.String()
}

func qe(s string) string { return url.QueryEscape(s) }

func TestAddonCoreLibraryItems(t *testing.T) {
	c := newCoreLib(t)
	var it addonkit.Item
	c.get("tok-lib", fmt.Sprintf("/library/items/%d", c.movie), 200, &it)
	if it.ID != c.movie || it.Kind != "movie" || it.Title != "Матрица" || it.OriginalTitle != "The Matrix" || it.Year != 1999 ||
		it.TMDBID != 603 || it.AddedAt.IsZero() {
		t.Fatalf("item %+v", it)
	}
	if body := c.get("tok-lib", fmt.Sprintf("/library/items/%d", c.movie), 200, nil); !strings.Contains(body, `"tmdbId":603`) ||
		strings.Contains(body, "tmdb_id") {
		t.Fatalf("not the contract DTO: %s", body)
	}
	c.get("tok-lib", "/library/items/999999", 404, nil)
	c.get("tok-lib", "/library/items/x", 400, nil)

	var files []addonkit.File
	c.get("tok-lib", fmt.Sprintf("/library/items/%d/files", c.movie), 200, &files)
	if len(files) != 1 || files[0].Path != c.moviePath || files[0].Size != 10 || files[0].Mtime != 5 || files[0].DurationMs != 8160000 ||
		files[0].ItemID != c.movie || string(files[0].Probe) != `{"Audio":[{"Title":"Дубляж"}]}` {
		t.Fatalf("movie files %+v", files)
	}
	// a series: the files of its episodes, with their numbers
	c.get("tok-lib", fmt.Sprintf("/library/items/%d/files", c.show), 200, &files)
	if len(files) != 2 || files[0].Path != c.ep1 || files[0].Season != 1 || files[0].Episode != 1 ||
		files[1].Path != c.ep2 || files[1].Episode != 2 {
		t.Fatalf("series files %+v", files)
	}
	c.get("tok-lib", "/library/items/999999/files", 404, nil)
}

func TestAddonCoreLibraryTMDB(t *testing.T) {
	c := newCoreLib(t)
	var it addonkit.Item
	c.get("tok-lib", "/library/tmdb/movie/603", 200, &it)
	if it.ID != c.movie || it.Title != "Матрица" || it.Kind != "movie" {
		t.Fatalf("tmdb item %+v", it)
	}
	c.get("tok-lib", "/library/tmdb/series/603", 404, nil)

	var ids map[string]int64
	c.get("tok-lib", "/library/tmdb/series?ids=42,603,7", 200, &ids)
	if !reflect.DeepEqual(ids, map[string]int64{"42": c.show}) {
		t.Fatalf("ids %v", ids)
	}
	ids = nil
	c.get("tok-lib", "/library/tmdb/movie?ids=603", 200, &ids)
	if !reflect.DeepEqual(ids, map[string]int64{"603": c.movie}) {
		t.Fatalf("ids %v", ids)
	}
	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprint(i + 1)
	}
	c.get("tok-lib", "/library/tmdb/movie?ids="+strings.Join(many[:500], ","), 200, nil)
	for _, p := range []string{"movie?ids=" + strings.Join(many, ","), "movie?ids=", "movie", "movie?ids=1,x", "movie?ids=-1", "book?ids=1"} {
		c.get("tok-lib", "/library/tmdb/"+p, 400, nil)
	}
}

func TestAddonCoreLibraryFind(t *testing.T) {
	c := newCoreLib(t)
	var it addonkit.Item
	c.get("tok-lib", "/library/find/title?kind=movie&tmdbId=603", 200, &it)
	if it.ID != c.movie {
		t.Fatalf("title movie %+v", it)
	}
	c.get("tok-lib", "/library/find/title?kind=music&title="+qe("Группа крови")+"&creator="+qe("Кино"), 200, &it)
	if it.ID != c.album {
		t.Fatalf("title album %+v", it)
	}
	c.get("tok-lib", "/library/find/title?kind=movie&tmdbId=1", 404, nil)
	c.get("tok-lib", "/library/find/title?kind=movie&tmdbId=x", 400, nil)
	for _, p := range []string{"tmdbId=603", "kind=comic&title=x&creator=y", "kind=manga&tmdbId=603", "kind=movie",
		"kind=series&title=x", "kind=music&title=x", "kind=music&creator=y", "kind=book&creator=y", "kind=audiobook"} {
		c.get("tok-lib", "/library/find/title?"+p, 400, nil)
	}
	c.get("tok-lib", "/library/find/title?kind=book&title="+qe("Нет такой"), 404, nil)

	c.get("tok-lib", "/library/find/album?artist="+qe("кино")+"&album="+qe("группа крови"), 200, &it)
	if it.ID != c.album || it.Kind != "album" {
		t.Fatalf("album %+v", it)
	}
	c.get("tok-lib", "/library/find/album?mbid=5e4b8c3e-0000-0000-0000-000000000000", 404, nil)
	if _, err := c.db.Exec(`UPDATE items SET meta_json=json_set(COALESCE(meta_json,'{}'),'$.mb_id','rg-1') WHERE id=?`, c.album); err != nil {
		t.Fatal(err)
	}
	c.get("tok-lib", "/library/find/album?mbid=rg-1", 200, &it)
	if it.ID != c.album {
		t.Fatalf("album by mbid %+v", it)
	}
	c.get("tok-lib", "/library/find/album?artist="+qe("Кино"), 400, nil)

	c.get("tok-lib", "/library/find/book?kind=book&author="+qe("Булгаков")+"&title="+qe("Мастер и Маргарита"), 200, &it)
	if it.ID != c.book {
		t.Fatalf("book %+v", it)
	}
	c.get("tok-lib", "/library/find/book?kind=audiobook&author="+qe("Булгаков")+"&title="+qe("Мастер и Маргарита"), 404, nil)
	c.get("tok-lib", "/library/find/book?kind=movie&title=x", 400, nil)
}

func TestAddonCoreLibrarySeries(t *testing.T) {
	c := newCoreLib(t)
	var eps [][2]int
	c.get("tok-lib", "/library/series/42/episodes", 200, &eps)
	if !reflect.DeepEqual(eps, [][2]int{{1, 1}, {1, 2}}) {
		t.Fatalf("episodes %v", eps)
	}
	c.get("tok-lib", "/library/series/7/episodes", 404, nil)
	c.get("tok-lib", "/library/series/x/episodes", 400, nil)

	var dir struct{ Path string }
	c.get("tok-lib", "/library/series/42/folder?root="+qe(c.series), 200, &dir)
	if dir.Path != filepath.Join(c.series, "Shogun") {
		t.Fatalf("folder %+v", dir)
	}
	c.get("tok-lib", "/library/series/7/folder?root="+qe(c.series), 404, nil)
	for _, root := range []string{"", c.outside, c.series + "/Shogun", c.root, "series"} {
		c.get("tok-lib", "/library/series/42/folder?root="+qe(root), 400, nil)
	}

	var admin int64
	c.db.QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&admin)
	if err := c.repo.RememberSeriesAudio(context.Background(), admin, c.show, library.TrackChoice{Lang: "ja", Title: "Оригинал"}); err != nil {
		t.Fatal(err)
	}
	var audio addonkit.SeriesAudio
	body := c.get("tok-lib", "/library/series/42/audio", 200, &audio)
	want := []addonkit.EpisodeAudio{{Season: 1, Episode: 1, Titles: []string{"Оригинал"}}, {Season: 1, Episode: 2, Titles: []string{}}}
	// the profiles' choices only with users.read too
	if !reflect.DeepEqual(audio.Files, want) || strings.Contains(body, "profiles") || strings.Contains(body, "userId") {
		t.Fatalf("audio without users.read %s", body)
	}
	audio = addonkit.SeriesAudio{}
	c.get("tok-both", "/library/series/42/audio", 200, &audio)
	if !reflect.DeepEqual(audio.Files, want) || len(audio.Profiles) != 1 || audio.Profiles[0].UserID != admin ||
		audio.Profiles[0].AudioTitle != "Оригинал" {
		t.Fatalf("audio with users.read %+v", audio)
	}
	c.get("tok-lib", "/library/series/7/audio", 404, nil)
}

func TestAddonCoreLibraryAlbums(t *testing.T) {
	c := newCoreLib(t)
	var dir struct{ Path string }
	c.get("tok-lib", fmt.Sprintf("/library/albums/%d/dir", c.album), 200, &dir)
	if dir.Path != filepath.Dir(c.tr1) {
		t.Fatalf("dir %+v", dir)
	}
	c.get("tok-lib", fmt.Sprintf("/library/albums/%d/dir", c.movie), 404, nil)
	c.get("tok-lib", "/library/albums/999999/dir", 404, nil)

	var tracks []addonkit.Track
	c.get("tok-lib", fmt.Sprintf("/library/albums/%d/tracks", c.album), 200, &tracks)
	if len(tracks) != 2 || tracks[0].Title != "Группа крови" || tracks[0].No != 1 || tracks[0].AlbumID != c.album ||
		tracks[0].Artist != "Кино" || tracks[0].Album != "Группа крови" || tracks[0].FileID == 0 || tracks[1].No != 2 {
		t.Fatalf("tracks %+v", tracks)
	}
	c.get("tok-lib", "/library/albums/999999/tracks", 404, nil)
}

func TestAddonCoreLibraryFiles(t *testing.T) {
	c := newCoreLib(t)
	var files []addonkit.File
	c.get("tok-lib", "/library/files?under="+qe(c.series), 200, &files)
	if len(files) != 2 || files[0].Path != c.ep1 || files[1].Path != c.ep2 {
		t.Fatalf("under %+v", files)
	}
	c.get("tok-lib", "/library/files?path="+qe(c.moviePath), 200, &files)
	if len(files) != 1 || files[0].Path != c.moviePath || files[0].ItemID != c.movie || files[0].Probe == nil {
		t.Fatalf("path %+v", files)
	}
	// a folder's listing leaves the probe summaries out (a whole root's are huge)
	files = nil
	c.get("tok-lib", "/library/files?under="+qe(c.movies), 200, &files)
	if len(files) != 1 || files[0].Path != c.moviePath || files[0].Probe != nil || files[0].DurationMs != 8160000 {
		t.Fatalf("under movies %+v", files)
	}
	// inside the roots but not on disk (nor its folder): nothing, not refused
	for _, p := range []string{"path=" + qe(filepath.Join(c.movies, "New (2026)", "New.mkv")), "under=" + qe(filepath.Join(c.movies, "New (2026)"))} {
		if body := c.get("tok-lib", "/library/files?"+p, 200, nil); strings.TrimSpace(body) != "[]" {
			t.Fatalf("%s: %s", p, body)
		}
	}
	for _, p := range []string{
		"path=" + qe(filepath.Join(c.outside, "secret.mkv")),            // outside the roots
		"under=" + qe(c.outside),                                        // outside the roots
		"under=" + qe(c.root),                                           // above them
		"path=" + qe(filepath.Join(c.movies, "escape", "secret.mkv")),   // a symlink out of the root
		"under=" + qe(filepath.Join(c.movies, "escape")),                // the same folder
		"path=" + qe(filepath.Join(c.movies, "escape", "new", "x.mkv")), // not on disk, under the symlink
		"path=" + qe(filepath.Join(c.movies, "dangling", "x.mkv")),      // not on disk, under a dangling symlink
		"path=" + qe(c.movies+"/../outside/secret.mkv"),                 // not clean
		"path=" + qe("movies/Matrix.mkv"),                               // relative
		"path=" + qe(c.moviePath) + "&under=" + qe(c.movies),            // both
		"", // neither
	} {
		c.get("tok-lib", "/library/files?"+p, 400, nil)
	}

	var paths []string
	c.get("tok-lib", "/library/paths", 200, &paths)
	want := []string{"/b/Булгаков/Мастер и Маргарита.epub", c.moviePath, c.tr1, c.tr2, c.ep1, c.ep2}
	if !reflect.DeepEqual(paths, slices.Sorted(slices.Values(want))) {
		t.Fatalf("paths %v", paths)
	}
}

func TestAddonCoreLibrarySearch(t *testing.T) {
	c := newCoreLib(t)
	var items []addonkit.Item
	c.get("tok-lib", "/library/search?kind=movie&title="+qe("МАТРИЦА"), 200, &items)
	if len(items) != 1 || items[0].ID != c.movie {
		t.Fatalf("search %+v", items)
	}
	if body := c.get("tok-lib", "/library/search?kind=series&title="+qe("Матрица")+"&limit=50", 200, nil); strings.TrimSpace(body) != "[]" {
		t.Fatalf("other kind: %s", body)
	}
	for _, p := range []string{"kind=movie&title=x&limit=51", "kind=movie&title=x&limit=x", "title=x", "kind=movie"} {
		c.get("tok-lib", "/library/search?"+p, 400, nil)
	}
}

func TestAddonCoreLibraryRefs(t *testing.T) {
	c := newCoreLib(t)
	var refs []addonkit.RefFile
	c.get("tok-lib", "/library/refs/r1", 200, &refs)
	if len(refs) != 2 || refs[0].Path != c.moviePath || refs[0].ItemID != c.movie || refs[0].FileID == 0 || refs[1].Path != c.tr1 {
		t.Fatalf("lib refs %+v", refs)
	}
	// another addon's file with the same ref string is not lib's, and back
	c.get("tok-lib2", "/library/refs/r1", 200, &refs)
	if len(refs) != 1 || refs[0].Path != c.tr2 {
		t.Fatalf("lib2 refs %+v", refs)
	}
	if body := c.get("tok-lib", "/library/refs/r2", 200, nil); strings.TrimSpace(body) != "[]" {
		t.Fatalf("unknown ref: %s", body)
	}
	// a ref with characters a path segment escapes
	c.bindRef("lib", "a/b c%", c.ep1)
	c.get("tok-lib", "/library/refs/"+url.PathEscape("a/b c%"), 200, &refs)
	if len(refs) != 1 || refs[0].Path != c.ep1 {
		t.Fatalf("escaped ref %+v", refs)
	}
	c.get("tok-lib", "/library/refs/"+strings.Repeat("x", addons.MaxRefLen+1), 400, nil)

	var tracks []addonkit.Track
	c.get("tok-lib", "/library/tracks?ref=r1", 200, &tracks)
	if len(tracks) != 1 || tracks[0].Title != "Группа крови" {
		t.Fatalf("lib tracks %+v", tracks)
	}
	c.get("tok-lib2", "/library/tracks?ref=r1", 200, &tracks)
	if len(tracks) != 1 || tracks[0].Title != "Закрой за мной дверь" {
		t.Fatalf("lib2 tracks %+v", tracks)
	}
	c.get("tok-lib", "/library/tracks", 400, nil)
	c.get("tok-lib", "/library/tracks?ref="+qe("\x01"), 400, nil)
}

func TestAddonCoreLibraryUsers(t *testing.T) {
	c := newCoreLib(t)
	var ids [3]int64
	for i, n := range []string{"", "Маша", "Вася"} {
		if n == "" {
			c.db.QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&ids[i])
		} else {
			c.db.QueryRow(`SELECT id FROM users WHERE name=?`, n).Scan(&ids[i])
		}
	}
	// Маша chose English; Вася's own audio languages start with an English
	// one; the admin has neither: the household's languages (ru first)
	if _, err := c.db.Exec(`INSERT INTO ui_prefs(user_id, lang) VALUES(?, 'en')`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO playback_prefs(user_id, audio_langs) VALUES(?, 'de,en-US,ru')`, ids[2]); err != nil {
		t.Fatal(err)
	}
	var users []addonkit.User
	c.get("tok-ppl", "/users", 200, &users)
	if len(users) != 3 || !users[0].IsAdmin || users[1].Name != "Маша" || users[1].IsAdmin || users[1].IsKid ||
		users[2].Name != "Вася" || !users[2].IsKid || users[0].ID == 0 {
		t.Fatalf("users %+v", users)
	}
	if users[0].Lang != "ru" || users[1].Lang != "en" || users[2].Lang != "en" {
		t.Fatalf("langs %+v", users)
	}
	// uid: each profile's stable key, as the users table keeps it
	for i, u := range users {
		var uid string
		c.db.QueryRow(`SELECT uid FROM users WHERE id=?`, ids[i]).Scan(&uid)
		if len(u.UID) != 32 || u.UID != uid {
			t.Fatalf("user %d uid %q, want %q", u.ID, u.UID, uid)
		}
	}
	// the household's languages, English first
	if _, err := c.db.Exec(`DELETE FROM playback_prefs`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO settings(key, value) VALUES(?, '{"languages":["en","ru"]}')`, appSettingsKey); err != nil {
		t.Fatal(err)
	}
	c.get("tok-ppl", "/users", 200, &users)
	if users[0].Lang != "en" || users[1].Lang != "en" || users[2].Lang != "en" {
		t.Fatalf("household langs %+v", users)
	}
	if body := c.get("tok-ppl", "/users", 200, nil); strings.Contains(body, "password") || strings.Contains(body, "avatar") {
		t.Fatalf("more than the contract: %s", body)
	}
	c.get("tok-lib", "/users", 403, nil)
}

// TestAddonCoreUsers_MaxRating: GET /users carries each profile's rating
// limit as the X-Antresol-User header does (auth.User.RatingLimit): its
// own, a kid without one of its own: 12+, and nothing for an unlimited
// adult.
func TestAddonCoreUsers_MaxRating(t *testing.T) {
	c := newCoreLib(t)
	// Маша gets a limit of her own; the admin and Вася (a kid, whose own
	// limit the fixture sets: cleared) have none
	if _, err := c.db.Exec(`UPDATE users SET max_rating='16+' WHERE name='Маша'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`UPDATE users SET max_rating=NULL WHERE name='Вася'`); err != nil {
		t.Fatal(err)
	}
	var users []map[string]any
	c.get("tok-ppl", "/users", 200, &users)
	got := map[string]any{}
	for _, u := range users {
		got[fmt.Sprint(u["name"])] = u["maxRating"]
	}
	if len(users) != 3 || got["Маша"] != "16+" || got["Вася"] != "12+" {
		t.Fatalf("limits %v", got)
	}
	// an unlimited adult carries no maxRating at all, as in the header
	if v, ok := users[0]["maxRating"]; ok || users[0]["isAdmin"] != true {
		t.Fatalf("admin: maxRating %v (present %v), want absent: %v", v, ok, users[0])
	}
	// a kid's own limit wins over the kid default
	if _, err := c.db.Exec(`UPDATE users SET max_rating='6+' WHERE name='Вася'`); err != nil {
		t.Fatal(err)
	}
	var typed []addonkit.User
	c.get("tok-ppl", "/users", 200, &typed)
	if typed[2].Name != "Вася" || typed[2].MaxRating != "6+" || typed[1].MaxRating != "16+" || typed[0].MaxRating != "" {
		t.Fatalf("typed %+v", typed)
	}
}

// TestAddonCoreLibraryPermissions: every route answers 403 to an addon
// without its permission, before it looks at the request.
func TestAddonCoreLibraryPermissions(t *testing.T) {
	c := newCoreLib(t)
	routes := []string{
		"/library/items/1", "/library/items/1/files", "/library/tmdb/movie/603", "/library/tmdb/movie?ids=603",
		"/library/tmdb/movie?ids=x", "/library/find/title?kind=movie&tmdbId=603", "/library/find/album?mbid=x",
		"/library/find/book?kind=book&title=x", "/library/series/42/episodes", "/library/series/42/episode-items?userId=1", "/library/series/42/folder?root=x",
		"/library/series/42/audio", "/library/albums/1/dir", "/library/albums/1/tracks", "/library/files?path=x",
		"/library/paths", "/library/search?kind=movie&title=x&limit=99", "/library/refs/r1", "/library/tracks?ref=r1",
	}
	for _, p := range routes {
		w := c.do("GET", "/api/addon-core/v1"+p, "", nil, "Authorization", "Bearer tok-hello")
		if w.Code != 403 || !strings.Contains(w.Body.String(), msgAddonPermission) {
			t.Errorf("%s without library.read: %d %s", p, w.Code, w.Body)
		}
	}
	if w := c.do("GET", "/api/addon-core/v1/users", "", nil, "Authorization", "Bearer tok-hello"); w.Code != 403 {
		t.Errorf("users without users.read: %d", w.Code)
	}
	if w := c.do("GET", "/api/addon-core/v1/library/paths", "", nil, "Authorization", "Bearer tok-ppl"); w.Code != 403 {
		t.Errorf("library with users.read only: %d", w.Code)
	}
}

func TestAddonCore_FileByID(t *testing.T) {
	c := newCoreLib(t)
	var byPath []addonkit.File
	c.get("tok-lib", "/library/files?path="+qe(c.moviePath), 200, &byPath)
	if len(byPath) != 1 {
		t.Fatalf("by path %+v", byPath)
	}
	var f addonkit.File
	c.get("tok-lib", fmt.Sprintf("/library/files/%d", byPath[0].ID), 200, &f)
	// the File of the other calls, probe included
	if !reflect.DeepEqual(f, byPath[0]) || f.ItemID != c.movie || string(f.Probe) != `{"Audio":[{"Title":"Дубляж"}]}` {
		t.Fatalf("file %+v\nwant %+v", f, byPath[0])
	}
	if body := c.get("tok-lib", "/library/files/999999", 404, nil); !strings.Contains(body, "Файл не найден") {
		t.Fatalf("404 body %s", body)
	}
	c.get("tok-lib", "/library/files/x", 400, nil)
	c.get("tok-lib", "/library/files/0", 400, nil)
	c.get("tok-ppl", fmt.Sprintf("/library/files/%d", byPath[0].ID), 403, nil)
}

func TestAddonCore_Studios(t *testing.T) {
	c := newCoreLib(t)
	var got struct {
		Studios []string `json:"studios"`
	}
	if body := c.get("tok-lib", "/settings/studios", 200, &got); strings.TrimSpace(body) != `{"studios":[]}` {
		t.Fatalf("none saved: %s", body)
	}
	// what the Settings screen saves («Любимые озвучки»), in its order
	want := []string{"Кубик в Кубе", "LostFilm", "AniDub"}
	if err := (releaseprefs.Store{DB: c.db}).SetStudios(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	c.get("tok-lib", "/settings/studios", 200, &got)
	if !reflect.DeepEqual(got.Studios, want) {
		t.Fatalf("studios %v, want %v", got.Studios, want)
	}
	c.get("tok-ppl", "/settings/studios", 403, nil)
}

// E21: the item, TMDB and search answers carry the library's other titles
// (item_titles minus the title and the original title, as stored); []
// when none.
func TestAddonCoreItem_Aliases(t *testing.T) {
	c := newCoreLib(t)
	ctx := context.Background()
	p := filepath.Join(c.movies, "Billions (2016)", "Billions.mkv")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := c.repo.UpsertMovie(ctx, library.MovieInput{Title: "Миллиарды", OriginalTitle: "Billions", Year: 2016, TmdbID: 62852,
		AltTitles: []string{"Деньги", "миллиарды"}, File: library.FileInput{Path: p, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var item addonkit.Item
	c.get("tok-lib", fmt.Sprintf("/library/items/%d", id), 200, &item)
	if !slices.Equal(item.Aliases, []string{"Деньги"}) {
		t.Fatalf("item aliases %#v", item.Aliases)
	}
	c.get("tok-lib", "/library/tmdb/movie/62852", 200, &item)
	if !slices.Equal(item.Aliases, []string{"Деньги"}) {
		t.Fatalf("tmdb aliases %#v", item.Aliases)
	}
	var items []addonkit.Item
	c.get("tok-lib", "/library/search?kind=movie&title="+qe("деньги"), 200, &items)
	if len(items) != 1 || items[0].ID != id || !slices.Equal(items[0].Aliases, []string{"Деньги"}) {
		t.Fatalf("search %+v", items)
	}
	// none: [] (not null) on all three
	for _, path := range []string{fmt.Sprintf("/library/items/%d", c.movie), "/library/tmdb/movie/603"} {
		if body := c.get("tok-lib", path, 200, nil); !strings.Contains(body, `"aliases":[]`) {
			t.Errorf("%s: %s", path, body)
		}
	}
	if body := c.get("tok-lib", "/library/search?kind=movie&title="+qe("Матрица"), 200, nil); !strings.Contains(body, `"aliases":[]`) {
		t.Errorf("search: %s", body)
	}
}

// episodeItems reads GET /library/series/{tmdbId}/episode-items?userId as
// the addon of tok; the answer must be {"episodes": [...]}, never null.
func (c *coreLib) episodeItems(tok string, tmdb, user int64, want int) []addonkit.EpisodeItem {
	c.t.Helper()
	var out struct {
		Episodes []addonkit.EpisodeItem `json:"episodes"`
	}
	body := c.get(tok, fmt.Sprintf("/library/series/%d/episode-items?userId=%d", tmdb, user), want, &out)
	if want == 200 && (out.Episodes == nil || !strings.HasPrefix(body, `{"episodes":[`)) {
		c.t.Fatalf("episode-items answer %s", body)
	}
	return out.Episodes
}

// addEpisode stores an episode of the series (TMDB 42) with a file of that
// name; the item id.
func (c *coreLib) addEpisode(season, episode int, name string) int64 {
	c.t.Helper()
	id, err := c.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: season, Episode: episode,
		File: library.FileInput{Path: filepath.Join(c.series, "Shogun", name), Size: 1}})
	if err != nil {
		c.t.Fatal(err)
	}
	return id
}

// The ids are the episode items', in season and episode order; a file of
// two episodes gives both the item that holds it. Whoever asks (an
// admin, an adult with a limit the series is under) gets them.
func TestAddonEpisodeItems_Visible(t *testing.T) {
	f := newProgFix(t)
	s2e1 := f.addEpisode(2, 1, "Shogun.S02E01.mkv") // before the others: the order is the numbers', not the ids'
	s1e3 := f.addEpisode(1, 3, "Shogun.S01E03-E04.mkv")
	want := []addonkit.EpisodeItem{{Season: 1, Episode: 1, ItemID: f.eps[0]}, {Season: 1, Episode: 2, ItemID: f.eps[1]},
		{Season: 1, Episode: 3, ItemID: s1e3}, {Season: 1, Episode: 4, ItemID: s1e3}, {Season: 2, Episode: 1, ItemID: s2e1}}
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); !reflect.DeepEqual(got, want) {
		t.Fatalf("Маша: %+v\nwant %+v", got, want)
	}
	if got := f.episodeItems("tok-lib", 42, f.admin, 200); !reflect.DeepEqual(got, want) {
		t.Fatalf("admin: %+v", got)
	}
	// the answer's keys
	body := f.get("tok-lib", fmt.Sprintf("/library/series/42/episode-items?userId=%d", f.masha), 200, nil)
	if !strings.HasPrefix(body, fmt.Sprintf(`{"episodes":[{"season":1,"episode":1,"itemId":%d},`, f.eps[0])) {
		t.Fatalf("body %s", body)
	}
	// the pairs of the pair route are the same
	var pairs [][2]int
	f.get("tok-lib", "/library/series/42/episodes", 200, &pairs)
	if len(pairs) != len(want) {
		t.Fatalf("pairs %v", pairs)
	}
	for i, p := range pairs {
		if p != [2]int{want[i].Season, want[i].Episode} {
			t.Fatalf("pair %d: %v vs %+v", i, p, want[i])
		}
	}
	// under her limit: the same
	f.limit("16+", "16+", "12+")
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); !reflect.DeepEqual(got, want) {
		t.Fatalf("under the limit: %+v", got)
	}
	// an addon with users.read too is no different: the permission is library.read
	if got := f.episodeItems("tok-both", 42, f.masha, 200); !reflect.DeepEqual(got, want) {
		t.Fatalf("both: %+v", got)
	}
}

// A series over the profile's limit (or one with no rating, which a
// limited profile does not see) has no episodes for it: an empty list, not
// an error; a profile with no limit still gets them.
func TestAddonEpisodeItems_HiddenOverLimitOmitted(t *testing.T) {
	f := newProgFix(t)
	f.limit("12+", "12+", "18+")
	if body := f.get("tok-lib", fmt.Sprintf("/library/series/42/episode-items?userId=%d", f.masha), 200, nil); strings.TrimSpace(body) != `{"episodes":[]}` {
		t.Fatalf("over the limit: %s", body)
	}
	f.limit("12+", "12+", "") // unrated
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); len(got) != 0 {
		t.Fatalf("unrated: %+v", got)
	}
	if got := f.episodeItems("tok-lib", 42, f.admin, 200); len(got) != 2 {
		t.Fatalf("admin: %+v", got)
	}
	// the limit lifted, or the series rated under it
	f.limit("12+", "12+", "6+")
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); len(got) != 2 {
		t.Fatalf("under: %+v", got)
	}
	f.limit("", "", "18+")
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); len(got) != 2 {
		t.Fatalf("no limit: %+v", got)
	}
}

// A kid profile is refused (400), as a missing or unknown one is.
func TestAddonEpisodeItems_KidRefused(t *testing.T) {
	f := newProgFix(t)
	for _, c := range []struct {
		path string
		msg  string
	}{
		{fmt.Sprintf("/library/series/42/episode-items?userId=%d", f.vasya), msgAddonKidProfile},
		{"/library/series/42/episode-items", msgAddonNoUser},
		{"/library/series/42/episode-items?userId=", msgAddonNoUser},
		{"/library/series/42/episode-items?userId=0", msgAddonNoUser},
		{"/library/series/42/episode-items?userId=x", msgAddonNoUser},
		{"/library/series/42/episode-items?userId=99999", msgAddonNoUser},
	} {
		body := f.get("tok-lib", c.path, 400, nil)
		if got := progErrText(t, body); got != c.msg {
			t.Errorf("%s: %q, want %q", c.path, got, c.msg)
		}
	}
	// a kid is refused before the series is looked at
	f.get("tok-lib", fmt.Sprintf("/library/series/7/episode-items?userId=%d", f.vasya), 400, nil)
}

// A series the library lacks is 404 «Не найдено», as on the pair route; a
// bad id is 400.
func TestAddonEpisodeItems_UnknownSeries404(t *testing.T) {
	f := newProgFix(t)
	for _, tmdb := range []string{"7", "603"} { // 603 is a movie
		for _, route := range []string{"episodes", "episode-items"} {
			body := f.get("tok-lib", fmt.Sprintf("/library/series/%s/%s?userId=%d", tmdb, route, f.masha), 404, nil)
			if progErrText(t, body) != msgAddonNotFoundItem {
				t.Errorf("%s %s: %s", tmdb, route, body)
			}
		}
	}
	for _, tmdb := range []string{"x", "0", "-1"} {
		f.get("tok-lib", fmt.Sprintf("/library/series/%s/episode-items?userId=%d", tmdb, f.masha), 400, nil)
	}
	// a series gone from disk is unknown too
	if _, err := f.db.Exec(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, f.show); err != nil {
		t.Fatal(err)
	}
	f.get("tok-lib", "/library/series/42/episodes", 404, nil)
	f.get("tok-lib", fmt.Sprintf("/library/series/42/episode-items?userId=%d", f.masha), 404, nil)
}

// An episode with no file left, a missing episode item or season is not
// listed; the rest still is.
func TestAddonEpisodeItems_MissingFileOmitted(t *testing.T) {
	f := newProgFix(t)
	s2e1 := f.addEpisode(2, 1, "Shogun.S02E01.mkv")
	s3e1 := f.addEpisode(3, 1, "Shogun.S03E01.mkv")
	if len(f.episodeItems("tok-lib", 42, f.masha, 200)) != 4 {
		t.Fatal("the fixture is not four episodes")
	}
	missing := func(q string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	missing(`UPDATE files SET missing_since='2026-01-01T00:00:00Z' WHERE item_id=?`, f.eps[1])
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); len(got) != 3 || got[1].ItemID != s2e1 {
		t.Fatalf("missing file: %+v", got)
	}
	missing(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, f.eps[0])
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); len(got) != 2 || got[0].ItemID != s2e1 {
		t.Fatalf("missing episode: %+v", got)
	}
	missing(`UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=(SELECT parent_id FROM items WHERE id=?)`, s3e1)
	if got := f.episodeItems("tok-lib", 42, f.masha, 200); len(got) != 1 || got[0].ItemID != s2e1 {
		t.Fatalf("missing season: %+v", got)
	}
	// nothing left: an empty list, and the series is still there
	missing(`UPDATE files SET missing_since='2026-01-01T00:00:00Z'`)
	if body := f.get("tok-lib", fmt.Sprintf("/library/series/42/episode-items?userId=%d", f.masha), 200, nil); strings.TrimSpace(body) != `{"episodes":[]}` {
		t.Fatalf("no files: %s", body)
	}
}

// The route is library.read's: an addon without it is refused before the
// request is looked at, whatever else it may do.
func TestAddonEpisodeItems_WithoutPermission403(t *testing.T) {
	f := newProgFix(t)
	for _, tok := range []string{"tok-hello", "tok-prog", "tok-ppl"} { // no permissions, progress only, users.read only
		for _, p := range []string{fmt.Sprintf("/library/series/42/episode-items?userId=%d", f.masha), "/library/series/x/episode-items"} {
			body := f.get(tok, p, 403, nil)
			if !strings.Contains(body, msgAddonPermission) {
				t.Errorf("%s %s: %s", tok, p, body)
			}
		}
	}
	// no token at all
	if w := f.do("GET", "/api/addon-core/v1/library/series/42/episode-items?userId=1", "", nil); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
}
