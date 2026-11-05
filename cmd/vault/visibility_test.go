package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/api"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/jellyfin"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/progress"
	"github.com/antresol-app/antresol/internal/subsonic"
)

// TestWebAndSubsonicAgreeOnVisibility: for a table of rated albums and
// profiles, the web album page and Subsonic getAlbum show the same albums
// (both use library.Repo.Visible).
func TestWebAndSubsonicAgreeOnVisibility(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	users, repo, ms := auth.NewStore(d), library.NewRepo(d), music.New(d)
	sessions := auth.NewSessions(d)
	web := api.NewRouter(api.Deps{Users: users, Sessions: sessions, Library: repo, Progress: progress.New(d), Music: ms,
		DataDir: t.TempDir(), ComicsCache: t.TempDir()})
	sub := &subsonic.Server{Users: users, Library: repo, Music: ms}
	media := t.TempDir()

	// artist rating, album rating
	table := [][2]string{{"", ""}, {"18+", ""}, {"6+", ""}, {"16+", ""}, {"", "18+"}, {"12+", "18+"}, {"bogus", ""}}
	var albums []int64
	for i, rt := range table {
		artist := "Артист " + strconv.Itoa(i)
		dir := filepath.Join(media, artist)
		os.MkdirAll(dir, 0o755)
		p := filepath.Join(dir, "1.flac")
		os.WriteFile(p, []byte("x"), 0o644)
		id, err := repo.UpsertAlbumTracks(ctx, artist, []library.TrackInput{{File: library.FileInput{Path: p, Size: 1, Mtime: 1},
			AlbumDir: dir, Album: "Альбом", Artist: artist, Title: "Песня", Track: 1, Codec: "flac", Lossless: true}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`UPDATE items SET rating=? WHERE id=(SELECT parent_id FROM items WHERE id=?)`, rt[0], id); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`UPDATE items SET rating=? WHERE id=?`, rt[1], id); err != nil {
			t.Fatal(err)
		}
		albums = append(albums, id)
	}
	profiles := []auth.NewUser{
		{Name: "Папа", Password: "secret", IsAdmin: true},
		{Name: "Вася", IsKid: true},
		{Name: "Петя", IsKid: true, MaxRating: "16+"},
		{Name: "Гость", Password: "1234", MaxRating: "6+"},
	}
	for _, nu := range profiles {
		u, err := users.CreateUser(ctx, nu)
		if err != nil {
			t.Fatal(err)
		}
		app, _ := users.SetAppPassword(ctx, u.ID)
		tok, err := sessions.Create(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i, al := range albums {
			r := httptest.NewRequest("GET", "/api/music/albums/"+strconv.FormatInt(al, 10), nil)
			r.RemoteAddr = "192.168.1.20:50000"
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
			w := httptest.NewRecorder()
			web.ServeHTTP(w, r)
			webOK := w.Code == 200
			if !webOK && w.Code != 404 {
				t.Fatalf("web %d %s", w.Code, w.Body)
			}
			q := url.Values{"u": {u.Name}, "p": {app}, "f": {"json"}, "id": {strconv.FormatInt(al, 10)}}
			r = httptest.NewRequest("GET", "/rest/getAlbum?"+q.Encode(), nil)
			r.RemoteAddr = "192.168.1.20:50000"
			w = httptest.NewRecorder()
			sub.ServeHTTP(w, r)
			var out map[string]map[string]any
			json.Unmarshal(w.Body.Bytes(), &out)
			subOK := out["subsonic-response"]["status"] == "ok"
			if webOK != subOK {
				t.Errorf("%s, artist %q album %q: web %v, subsonic %v (%s)", u.Name, table[i][0], table[i][1], webOK, subOK,
					strings.TrimSpace(w.Body.String()))
			}
		}
	}
}

// TestJellyfinVisibility: the Jellyfin API shows exactly the movies and
// series (and their episodes and art) the web item page shows, for the
// same rated items and profiles (both use library.Repo.Visible).
func TestJellyfinVisibility(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	users, repo, sessions, tokens := auth.NewStore(d), library.NewRepo(d), auth.NewSessions(d), auth.NewAppTokens(d)
	network := &auth.Network{Trusted: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}
	web := api.NewRouter(api.Deps{Users: users, Sessions: sessions, Network: network, Library: repo, Progress: progress.New(d),
		Music: music.New(d), DataDir: t.TempDir(), ComicsCache: t.TempDir()})
	imageDir := t.TempDir()
	jf := &jellyfin.Server{Users: users, Tokens: tokens, Network: network, DB: d, Version: "12.1.0",
		Library: repo, Progress: progress.New(d), ImageDir: imageDir}
	poster := strings.Repeat("b", 40) + ".jpg"
	os.WriteFile(filepath.Join(imageDir, poster), []byte("\xff\xd8\xff\xe0 jpeg"), 0o644)
	media := t.TempDir()

	ratings := []string{"", "0+", "6+", "12+", "16+", "18+", "bogus"}
	type rated struct {
		id     int64
		rating string
		kind   string
	}
	var items []rated
	for i, rt := range ratings {
		p := filepath.Join(media, "m"+strconv.Itoa(i)+".mkv")
		os.WriteFile(p, []byte("x"), 0o644)
		id, err := repo.UpsertMovie(ctx, library.MovieInput{Title: "Фильм " + strconv.Itoa(i), Year: 2000 + i, Poster: poster,
			File: library.FileInput{Path: p, Size: 1, Mtime: 1}})
		if err != nil {
			t.Fatal(err)
		}
		ep := filepath.Join(media, "s"+strconv.Itoa(i), "S01E01.mkv")
		os.MkdirAll(filepath.Dir(ep), 0o755)
		os.WriteFile(ep, []byte("x"), 0o644)
		epID, err := repo.UpsertEpisode(ctx, library.EpisodeInput{SeriesTitle: "Сериал " + strconv.Itoa(i), SeriesPoster: poster,
			Season: 1, Episode: 1, File: library.FileInput{Path: ep, Size: 1, Mtime: 1}})
		if err != nil {
			t.Fatal(err)
		}
		// raw ratings, as the TMDB enricher may leave them
		if _, err := d.Exec(`UPDATE items SET rating=? WHERE id=? OR id=(SELECT s.parent_id FROM items e JOIN items s ON s.id=e.parent_id WHERE e.id=?)`,
			rt, id, epID); err != nil {
			t.Fatal(err)
		}
		ser, _ := repo.Item(ctx, epID)
		season, _ := repo.Item(ctx, ser.ParentID)
		items = append(items, rated{id, rt, "movie"}, rated{season.ParentID, rt, "series"}, rated{season.ID, rt, "season"}, rated{epID, rt, "episode"})
	}
	profiles := []auth.NewUser{
		{Name: "Папа", Password: "secret", IsAdmin: true},
		{Name: "Вася", IsKid: true},
		{Name: "Петя", IsKid: true, MaxRating: "16+"},
		{Name: "Гость", Password: "1234", MaxRating: "6+"},
	}
	send := func(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
		r.RemoteAddr = "192.168.1.20:50000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, nu := range profiles {
		u, err := users.CreateUser(ctx, nu)
		if err != nil {
			t.Fatal(err)
		}
		cookie, err := sessions.Create(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := tokens.Issue(ctx, u.ID, "jellyfin", auth.Device{ID: "dev", Name: "iPhone", Client: "Swiftfin iOS"})
		if err != nil {
			t.Fatal(err)
		}
		jfGet := func(path string) *httptest.ResponseRecorder {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("X-Emby-Token", tok)
			return send(jf, r)
		}
		var list struct {
			Items            []struct{ Id string }
			TotalRecordCount int
		}
		w := jfGet("/Items?includeItemTypes=Movie,Series,Season,Episode&recursive=true")
		json.Unmarshal(w.Body.Bytes(), &list)
		listed := map[string]bool{}
		for _, it := range list.Items {
			listed[it.Id] = true
		}
		if list.TotalRecordCount != len(list.Items) {
			t.Fatalf("%s: total %d, items %d", u.Name, list.TotalRecordCount, len(list.Items))
		}
		seen := 0
		for _, it := range items {
			r := httptest.NewRequest("GET", "/api/items/"+strconv.FormatInt(it.id, 10), nil)
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
			w := send(web, r)
			webOK := w.Code == 200
			if !webOK && w.Code != 404 {
				t.Fatalf("web %d %s", w.Code, w.Body)
			}
			if webOK {
				seen++
			}
			id := jellyfin.ItemID(it.id)
			got := map[string]bool{"item": jfGet("/Items/"+id).Code == 200, "list": listed[id]}
			if it.kind != "season" && it.kind != "episode" {
				got["image"] = jfGet("/Items/"+id+"/Images/Primary").Code == 200
			}
			for what, ok := range got {
				if ok != webOK {
					t.Errorf("%s, %s rated %q: web %v, jellyfin %s %v", u.Name, it.kind, it.rating, webOK, what, ok)
				}
			}
		}
		// the table has something to compare: all for the admin, a part for the kids
		if nu.IsAdmin && seen != len(items) || !nu.IsAdmin && (seen == 0 || seen == len(items)) {
			t.Fatalf("%s sees %d of %d", u.Name, seen, len(items))
		}
	}
}
