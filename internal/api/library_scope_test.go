package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
)

// meID is the profile of cookie c.
func (e *libEnv) meID(c *http.Cookie) int64 {
	e.t.Helper()
	var u auth.User
	w := e.do("GET", "/api/me", "", c)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &u) != nil {
		e.t.Fatalf("me %d %s", w.Code, w.Body)
	}
	return u.ID
}

// titlesOf are the card titles of a {"items": [...]} page.
func titlesOf(t *testing.T, e *libEnv, path string, c *http.Cookie) map[string]bool {
	t.Helper()
	w := e.do("GET", path, "", c)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var page struct {
		Items []Card `json:"items"`
		Total int    `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range page.Items {
		out[c.Title] = true
	}
	if len(out) != page.Total {
		t.Fatalf("%s: total %d for %v", path, page.Total, out)
	}
	return out
}

// ?scope=mine keeps the titles the profile brought; no scope (the apps)
// is the shared library; a kid always gets the shared library, filtered
// by its rating.
func TestLibraryScopeLists(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	mum := e.login(`{"name":"Мама"}`)
	kid := e.kid("12+")
	e.movie("Дюна", "12+", "0123456789")
	e.movie("Брат", "18+", "0123456789")
	e.movie("Бременские", "0+", "0123456789")
	if err := e.repo.ClaimPaths(ctx, []string{filepath.Join(e.media, "Дюна.mkv")}, e.meID(mum)); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.ClaimPaths(ctx, []string{filepath.Join(e.media, "Бременские.mkv")}, e.meID(kid)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.UpsertBook(ctx, library.BookInput{Title: "Солярис", Files: []library.FileInput{{Path: "/b/Солярис.epub", Size: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.ClaimPaths(ctx, []string{"/b/Солярис.epub"}, e.meID(mum)); err != nil {
		t.Fatal(err)
	}

	if got := titlesOf(t, e, "/api/items?kind=movie", mum); len(got) != 3 {
		t.Fatalf("default is the shared library: %v", got)
	}
	if got := titlesOf(t, e, "/api/items?kind=movie&scope=all", mum); len(got) != 3 {
		t.Fatalf("scope=all: %v", got)
	}
	if got := titlesOf(t, e, "/api/items?kind=movie&scope=mine", mum); len(got) != 1 || !got["Дюна"] {
		t.Fatalf("Мама's movies: %v", got)
	}
	if got := titlesOf(t, e, "/api/items?kind=movie&scope=mine", e.admin); len(got) != 0 {
		t.Fatalf("the admin brought nothing: %v", got)
	}
	if got := titlesOf(t, e, "/api/items?kind=movie&scope=bogus", mum); len(got) != 3 {
		t.Fatalf("an unknown scope is the shared library: %v", got)
	}
	// a kid: the shared library by its rating, scope or not
	if got := titlesOf(t, e, "/api/items?kind=movie&scope=mine", kid); len(got) != 2 || got["Брат"] {
		t.Fatalf("kid with scope=mine: %v", got)
	}
	if got := titlesOf(t, e, "/api/books?kind=book&scope=mine", mum); len(got) != 1 || !got["Солярис"] {
		t.Fatalf("Мама's books: %v", got)
	}
	if got := titlesOf(t, e, "/api/books?kind=book&scope=mine", e.admin); len(got) != 0 {
		t.Fatalf("admin's books: %v", got)
	}

	// /api/home: the hero and «recently added» follow the scope
	shelf := func(c *http.Cookie, q, id string) (hero, cards int) {
		t.Helper()
		w := e.do("GET", "/api/home"+q, "", c)
		var out struct {
			Hero    []Card  `json:"hero"`
			Shelves []shelf `json:"shelves"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("home %d %s", w.Code, w.Body)
		}
		for _, s := range out.Shelves {
			if s.ID == id {
				return len(out.Hero), len(s.Cards)
			}
		}
		t.Fatalf("no shelf %s", id)
		return 0, 0
	}
	if hero, n := shelf(mum, "", "recent-movies"); hero != 3 || n != 3 {
		t.Fatalf("home: hero %d, movies %d", hero, n)
	}
	if hero, n := shelf(mum, "?scope=mine", "recent-movies"); hero != 1 || n != 1 {
		t.Fatalf("my home: hero %d, movies %d", hero, n)
	}
	if _, n := shelf(kid, "?scope=mine", "recent-movies"); n != 2 {
		t.Fatalf("kid's home: movies %d", n)
	}
}

// PUT /api/me/library-scope keeps the choice on the profile (any browser
// gets it with /api/me); a kid cannot leave the shared library.
func TestLibraryScopePreference(t *testing.T) {
	e := newLibEnv(t)
	mum := e.login(`{"name":"Мама"}`)
	kid := e.kid("")
	scope := func(c *http.Cookie) string {
		t.Helper()
		var u auth.User
		if w := e.do("GET", "/api/me", "", c); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &u) != nil {
			t.Fatalf("me %d %s", w.Code, w.Body)
		}
		return u.LibraryScope
	}
	if s := scope(mum); s != "all" {
		t.Fatalf("default %q", s)
	}
	w := e.do("PUT", "/api/me/library-scope", `{"scope":"mine"}`, mum)
	var u auth.User
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &u) != nil || u.LibraryScope != "mine" || u.Name != "Мама" {
		t.Fatalf("put %d %s", w.Code, w.Body)
	}
	if s := scope(mum); s != "mine" {
		t.Fatalf("kept %q", s)
	}
	if s := scope(e.admin); s != "all" {
		t.Fatalf("another profile's %q", s)
	}
	// the stored choice filters nothing by itself: the apps see everything
	e.movie("Брат", "18+", "0123456789")
	if got := titlesOf(t, e, "/api/items?kind=movie", mum); len(got) != 1 {
		t.Fatalf("no scope param: %v", got)
	}
	if w := e.do("PUT", "/api/me/library-scope", `{"scope":"theirs"}`, mum); w.Code != http.StatusBadRequest {
		t.Fatalf("bad scope %d", w.Code)
	}
	if w := e.do("PUT", "/api/me/library-scope", `{"scope":"mine"}`, kid); w.Code != http.StatusForbidden {
		t.Fatalf("kid %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/me/library-scope", `{"scope":"all"}`, kid); w.Code != 200 {
		t.Fatalf("kid to all %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/me/library-scope", `{"scope":"all"}`, mum); w.Code != 200 || scope(mum) != "all" {
		t.Fatalf("back to all %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/me/library-scope", `{"scope":"mine"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous %d", w.Code)
	}
}

// Music: an album the profile brought, its artist; the others' are not.
func TestLibraryScopeMusic(t *testing.T) {
	e := newLibEnv(t)
	ctx := context.Background()
	mum := e.login(`{"name":"Мама"}`)
	for _, a := range []struct{ artist, album string }{{"Кино", "Ночь"}, {"Аквариум", "Табу"}} {
		p := "/m/" + a.artist + "/" + a.album + "/01.flac"
		if _, err := e.repo.UpsertAlbumTracks(ctx, a.artist, []library.TrackInput{{File: library.FileInput{Path: p, Size: 1, Mtime: 1},
			AlbumDir: filepath.Dir(p), Album: a.album, AlbumArtist: a.artist, Artist: a.artist, Title: a.album, Track: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.repo.ClaimPaths(ctx, []string{"/m/Кино/Ночь/01.flac"}, e.meID(mum)); err != nil {
		t.Fatal(err)
	}
	if got := titlesOf(t, e, "/api/music/albums", mum); len(got) != 2 {
		t.Fatalf("albums %v", got)
	}
	if got := titlesOf(t, e, "/api/music/albums?scope=mine", mum); len(got) != 1 || !got["Ночь"] {
		t.Fatalf("my albums %v", got)
	}
	if got := titlesOf(t, e, "/api/music/artists?scope=mine", mum); len(got) != 1 || !got["Кино"] {
		t.Fatalf("my artists %v", got)
	}
	if got := titlesOf(t, e, "/api/music/artists", mum); len(got) != 2 {
		t.Fatalf("artists %v", got)
	}
	w := e.do("GET", "/api/music/home?scope=mine", "", mum)
	var home struct {
		Shelves []shelf `json:"shelves"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &home) != nil {
		t.Fatalf("music home %d %s", w.Code, w.Body)
	}
	for _, s := range home.Shelves {
		if (s.ID == "recent-albums" || s.ID == "artists") && len(s.Cards) != 1 {
			t.Fatalf("my %s: %+v", s.ID, s.Cards)
		}
	}
}
