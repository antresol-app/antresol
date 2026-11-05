package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
)

// «metallica» names the band: their studio albums oldest first, then their
// EPs, then the other release groups found (albums titled «Metallica» by
// anyone), each once; no song search.
func TestSearchFreeArtistDiscographyFirst(t *testing.T) {
	var rq string
	var discoQ string
	c := artistServer(t, mbFixtures{artists: "artists-metallica.json", discography: "discography-metallica.json",
		groups: "release-groups-metallica.json"}, &rq)
	c.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if q := r.URL.Query().Get("query"); strings.HasPrefix(q, "arid:") {
			discoQ = q
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	gs, err := c.SearchFree(context.Background(), "Metallica")
	if err != nil {
		t.Fatal(err)
	}
	if want := "arid:(" + metallicaID + ") AND primarytype:(album OR ep) AND NOT secondarytype:* AND status:official"; discoQ != want {
		t.Fatalf("discography query %q", discoQ)
	}
	if rq != "" {
		t.Fatalf("song searched: %s", rq)
	}
	got := titles(gs)
	t.Logf("%s", strings.Join(got, "\n"))
	head := []string{"Kill ’Em All", "Ride the Lightning", "Master of Puppets", "…And Justice for All", "Metallica",
		"Load", "Reload", "St. Anger", "Death Magnetic", "Lulu", "Hardwired… to Self‐Destruct", "72 Seasons",
		"The $5.98 E.P.: Garage Days Re‐Revisited", "Creeping Death / Jump in the Fire"}
	for i, w := range head {
		if i >= len(gs) || gs[i].Title != w {
			t.Fatalf("#%d, want %q\n%s", i, w, strings.Join(got, "\n"))
		}
	}
	const discoLen = 20
	seen := map[string]bool{}
	for i, g := range gs {
		if seen[g.ID] {
			t.Fatalf("%s twice", g.ID)
		}
		seen[g.ID] = true
		if mine := i < discoLen; mine != (g.ByArtist != "") {
			t.Fatalf("#%d %s: ByArtist %q", i, got[i], g.ByArtist)
		}
		if i < discoLen && (g.ByArtist != "Metallica" || len(g.SecondaryTypes) > 0) {
			t.Fatalf("#%d %+v", i, g)
		}
	}
	if g := gs[4]; g.Year != 1991 || g.ID != "e8f70201-8899-3f0c-9e07-5d6495bc8046" {
		t.Fatalf("black album %+v", g)
	}
	if g := gs[9]; g.Artist != "Lou Reed & Metallica" {
		t.Fatalf("Lulu %+v", g)
	}
	// then the other matches: Cloutchaser's «Metallica», «Metallica 2»…
	rest := strings.Join(got[discoLen:], "\n")
	for _, w := range []string{"Cloutchaser — Metallica", "Metallica 2 — Metallica 2", "Musical Artizan — String Metallica"} {
		if !strings.Contains(rest, w) {
			t.Errorf("%q lost:\n%s", w, rest)
		}
	}
	if len(gs) != discoLen+9 { // 10 hits, the black album among them
		t.Fatalf("%d results", len(gs))
	}
}

// A query that is not an artist's name («metallica sad but true») is
// searched as before, though the artist search finds Metallica.
func TestSearchFreeNotAnArtistAsBefore(t *testing.T) {
	var rq string
	c := artistServer(t, mbFixtures{artists: "artists-metallica.json", discography: "discography-metallica.json",
		groups: "release-groups-metallica-sad-but-true.json", recordings: "recordings-metallica-sad-but-true.json"}, &rq)
	gs, err := c.SearchFree(context.Background(), "metallica sad but true")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rq, `recording:"Sad but True" AND arid:(`+metallicaID+`)`) {
		t.Fatalf("recording query %s", rq)
	}
	if len(gs) == 0 || gs[0].Title != "Metallica" || gs[0].MatchedTrack != "Sad but True" {
		t.Fatalf("%s", strings.Join(titles(gs), "\n"))
	}
	for _, g := range gs {
		if g.ByArtist != "" {
			t.Fatalf("%+v", g)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQueryArtists(t *testing.T) {
	a := func(id, name, sort string, score int, aliases ...string) rawArtist {
		r := rawArtist{ID: id, Name: name, SortName: sort, Score: flexInt(score)}
		for _, al := range aliases {
			r.Aliases = append(r.Aliases, struct {
				Name     string `json:"name"`
				SortName string `json:"sort-name"`
			}{Name: al})
		}
		return r
	}
	as := []rawArtist{
		a("beatles", "The Beatles", "Beatles, The", 100),
		a("kino", "Кино", "Kino", 100, "Kinoband"),
		a("weak", "Beatles", "Beatles", 70), // a low score
		a(variousArtists, "Various Artists", "Various Artists", 100),
		a("tribute", "The Beatles Revival Band", "Beatles Revival Band, The", 95),
	}
	cases := map[string]string{"beatles": "beatles", "The Beatles": "beatles", "beatles, the": "beatles",
		"кино": "kino", "KINO": "kino", "kinoband": "kino", "various artists": "", "beatles revival": "", "": ""}
	for q, want := range cases {
		got := queryArtists(q, as)
		ids := make([]string, len(got))
		for i, g := range got {
			ids[i] = g.ID
		}
		if strings.Join(ids, ",") != want {
			t.Errorf("%q: %v, want %q", q, ids, want)
		}
	}
}

// Same-named artists: the one with more releases first; a group credited to
// another artist first goes to the named one it credits.
func TestDiscographySameNamedArtists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"release-groups":[
 {"id":"uk1","title":"The Story of Simon Simopath","primary-type":"Album","first-release-date":"1967","count":3,"artist-credit":[{"name":"Nirvana","artist":{"id":"uk","name":"Nirvana"}}]},
 {"id":"us2","title":"Nevermind","primary-type":"Album","first-release-date":"1991-09-24","count":60,"artist-credit":[{"name":"Nirvana","artist":{"id":"us","name":"Nirvana"}}]},
 {"id":"usep","title":"Hormoaning","primary-type":"EP","first-release-date":"1992-02-05","count":5,"artist-credit":[{"name":"Nirvana","artist":{"id":"us","name":"Nirvana"}}]},
 {"id":"us1","title":"Bleach","primary-type":"Album","first-release-date":"1989-06-15","count":30,"artist-credit":[{"name":"Nirvana","artist":{"id":"us","name":"Nirvana"}}]},
 {"id":"live","title":"Unplugged","primary-type":"Album","secondary-types":["Live"],"count":40,"artist-credit":[{"name":"Nirvana","artist":{"id":"us","name":"Nirvana"}}]},
 {"id":"single","title":"Lithium","primary-type":"Single","count":9,"artist-credit":[{"name":"Nirvana","artist":{"id":"us","name":"Nirvana"}}]},
 {"id":"undated","title":"Rarities","primary-type":"Album","count":1,"artist-credit":[{"name":"Nirvana","artist":{"id":"us","name":"Nirvana"}}]},
 {"id":"collab","title":"Split","primary-type":"Album","first-release-date":"1970","count":1,"artist-credit":[{"name":"Other","joinphrase":" & ","artist":{"id":"x","name":"Other"}},{"name":"Nirvana","artist":{"id":"uk","name":"Nirvana"}}]}
]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	gs, pop, err := c.discography(context.Background(), []rawArtist{{ID: "uk", Name: "Nirvana"}, {ID: "us", Name: "Nirvana"}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, g := range gs {
		ids = append(ids, g.ID)
	}
	if strings.Join(ids, ",") != "us1,us2,undated,usep,uk1,collab" || pop != 96 {
		t.Fatalf("%v pop=%d", ids, pop)
	}
	if gs[5].ArtistID != "x" || gs[5].ByArtist != "Nirvana" {
		t.Fatalf("%+v", gs[5])
	}
}

func TestSongOutweighs(t *testing.T) {
	named := []rawArtist{{ID: "band"}}
	songs := []ReleaseGroup{{ID: "a", ArtistID: "band"}, {ID: "b", ArtistID: "radiohead"}}
	if !songOutweighs(songs, map[string]int{"band": 50, "radiohead": 40}, named, 10) {
		t.Fatal("the famous song must outweigh an obscure band")
	}
	if songOutweighs(songs, map[string]int{"band": 50, "radiohead": 40}, named, 40) {
		t.Fatal("as popular a band keeps its discography first")
	}
}
