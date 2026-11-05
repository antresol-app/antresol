package musicbrainz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
)

const metallicaID = "65f4f0c5-ef9e-490c-aee3-909e7ae6b2ab"

// catalogServer answers the release-group search with groupsFile and the
// recording search with recsFile (recorded MusicBrainz answers, testdata;
// release events and track lists trimmed), the artist search with no
// artist; the recording query lands in rq.
func catalogServer(t *testing.T, groupsFile, recsFile string, rq *string) *Client {
	t.Helper()
	return artistServer(t, mbFixtures{groups: groupsFile, recordings: recsFile}, rq)
}

// mbFixtures name the testdata answers of a fake MusicBrainz: "" answers
// with nothing found (artists, discography) or fails the test (groups,
// recordings).
type mbFixtures struct {
	artists, discography, groups, recordings string
}

// artistServer is catalogServer with the artist search and the discography
// (a release-group query on arid:…) answered too.
func artistServer(t *testing.T, f mbFixtures, rq *string) *Client {
	t.Helper()
	read := func(f, none string) []byte {
		if f == "" {
			return []byte(none)
		}
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	artists := read(f.artists, `{"artists":[]}`)
	disco := read(f.discography, `{"release-groups":[]}`)
	groups := read(f.groups, "")
	var recs []byte
	if f.recordings != "" {
		recs = read(f.recordings, "")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/artist":
			w.Write(artists)
		case "/ws/2/release-group":
			if strings.HasPrefix(r.URL.Query().Get("query"), "arid:") {
				w.Write(disco)
				return
			}
			w.Write(groups)
		case "/ws/2/recording":
			if recs == nil {
				t.Errorf("recording search %s", r.URL)
			}
			if r.URL.Query().Get("limit") != "100" {
				t.Errorf("recording limit %s", r.URL)
			}
			*rq = r.URL.Query().Get("query")
			w.Write(recs)
		default:
			t.Errorf("request %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL + "/ws/2", Limiter: &metadata.Limiter{}}
}

func titles(gs []ReleaseGroup) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Artist + " — " + g.Title
		if g.MatchedTrack != "" {
			out[i] += " [" + g.MatchedTrack + "]"
		}
	}
	return out
}

func TestSearchFreeSongTitleFindsAlbum(t *testing.T) {
	var rq string
	c := catalogServer(t, "release-groups-sad-but-true.json", "recordings-sad-but-true.json", &rq)
	gs, err := c.SearchFree(context.Background(), "sad but true")
	if err != nil {
		t.Fatal(err)
	}
	// the artists of same-titled release groups, most releases first
	want := `((recording:"Sad but True" AND arid:(` + metallicaID + ` OR aa62b28e-b6d4-4086-91d4-e5fac1ed56f3 OR 6d2db307-f78f-451d-aaa6-6d53145bcce0))) AND status:official AND primarytype:album AND NOT comment:live`
	if rq != want {
		t.Fatalf("recording query\n%s\nwant\n%s", rq, want)
	}
	got := titles(gs)
	t.Logf("%s", strings.Join(got, "\n"))
	// the famous recording leads: Metallica's album with the song, then
	// Metallica's single, then Royal Blood (more releases of the song); then
	// the albums titled «Sad but True» lead the band of artists with 1–3
	head := []string{"Metallica — Metallica [Sad but True]", "Metallica — Sad but True", "Royal Blood — Sad but True",
		"Tex, Don and Charlie — Sad but True", "Fae August — Sad but True", "The Times — Sad But True"}
	for i, w := range head {
		if i >= len(got) || got[i] != w {
			t.Fatalf("#%d = %q, want %q\n%s", i, got[min(i, len(got)-1)], w, strings.Join(got, "\n"))
		}
	}
	m := gs[0]
	if m.Year != 1991 || m.ArtistID != metallicaID || m.ID == "" {
		t.Fatalf("%+v", m)
	}
	seen := map[string]bool{}
	for _, g := range gs {
		if seen[g.ID] {
			t.Fatalf("%s twice", g.ID)
		}
		seen[g.ID] = true
		if g.MatchedTrack != "" && (g.PrimaryType != "Album" || len(g.SecondaryTypes) > 0) {
			t.Errorf("song album %+v", g)
		}
	}
	// the same-named singles are kept, after the albums
	if !strings.Contains(strings.Join(got, "\n"), "Metallica — Sad but True") {
		t.Errorf("Metallica single lost:\n%s", strings.Join(got, "\n"))
	}
}

func TestSearchFreeArtistAndSong(t *testing.T) {
	var rq string
	c := catalogServer(t, "release-groups-metallica-sad-but-true.json", "recordings-metallica-sad-but-true.json", &rq)
	gs, err := c.SearchFree(context.Background(), "metallica sad but true")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rq, `recording:"Sad but True" AND arid:(`+metallicaID+`)`) {
		t.Fatalf("recording query %s", rq)
	}
	if len(gs) == 0 || gs[0].Title != "Metallica" || gs[0].Year != 1991 || gs[0].MatchedTrack != "Sad but True" {
		t.Fatalf("%s", strings.Join(titles(gs), "\n"))
	}
}

func TestSongAlbumsMapping(t *testing.T) {
	const other = "a2"
	type rel = struct {
		status, date, rg, title, primary string
		secondary                        []string
		artist                           string
	}
	mk := func(title, artist string, rels ...rel) rawRecording {
		r := rawRecording{Title: title}
		r.ArtistCredit = rawCredit{{Name: "Band"}}
		r.ArtistCredit[0].Artist.ID = artist
		for _, x := range rels {
			var e = struct {
				ID           string    `json:"id"`
				Status       string    `json:"status"`
				Date         string    `json:"date"`
				ArtistCredit rawCredit `json:"artist-credit"`
				ReleaseGroup struct {
					ID             string   `json:"id"`
					Title          string   `json:"title"`
					PrimaryType    string   `json:"primary-type"`
					SecondaryTypes []string `json:"secondary-types"`
				} `json:"release-group"`
			}{Status: x.status, Date: x.date}
			e.ReleaseGroup.ID, e.ReleaseGroup.Title, e.ReleaseGroup.PrimaryType, e.ReleaseGroup.SecondaryTypes = x.rg, x.title, x.primary, x.secondary
			if x.artist != "" {
				e.ArtistCredit = rawCredit{{Name: "VA"}}
				e.ArtistCredit[0].Artist.ID = x.artist
			}
			r.Releases = append(r.Releases, e)
		}
		return r
	}
	recs := []rawRecording{
		mk("Song", "a1",
			rel{"Official", "2005", "best", "Best Of", "Album", []string{"Compilation"}, ""},
			rel{"Official", "1999-02-01", "live", "Alive", "Album", []string{"Live"}, ""},
			rel{"Bootleg", "1990", "boot", "Boot", "Album", nil, ""},
			rel{"Official", "2001", "remaster", "Debut", "Album", nil, ""},
			rel{"Official", "1998-05-01", "debut", "Debut", "Album", nil, ""},
			rel{"Official", "1997", "va", "Hits 97", "Album", nil, variousArtists},
			rel{"Official", "1996", "single", "Song", "Single", nil, ""}),
		// another take on the same album: one entry, the oldest date kept
		mk("Song", "a1", rel{"Official", "", "debut", "Debut", "Album", nil, ""}),
		mk("Other Song", "a1", rel{"Official", "1990", "x", "X", "Album", nil, ""}), // another title
		mk("song", other, rel{"Official", "2010", "o", "Cover", "Album", nil, ""}),
		mk("Song", other, rel{"Official", "", "only-live", "Live", "Album", []string{"Live"}, ""}), // no album: none
	}
	got, w := songAlbums("SONG", recs)
	if w["a1"] != 8 || w[other] != 2 {
		t.Fatalf("weights %v", w)
	}
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if g := got[0]; g.ID != "debut" || g.Title != "Debut" || g.Year != 1998 || g.ArtistID != "a1" || g.MatchedTrack != "Song" {
		t.Fatalf("%+v", g)
	}
	if g := got[1]; g.ID != "o" || g.Year != 2010 || g.MatchedTrack != "song" {
		t.Fatalf("%+v", g)
	}
}

func TestMergeCatalogOrder(t *testing.T) {
	groups := []ReleaseGroup{
		{ID: "single", Title: "Song", PrimaryType: "Single", ArtistID: "famous", Releases: 12},
		{ID: "comp", Title: "Song", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}, ArtistID: "b", Releases: 1},
		{ID: "debut", Title: "Debut", PrimaryType: "Album", Aliases: []string{"First"}, ArtistID: "famous", Releases: 3},
		{ID: "album", Title: "Song!", PrimaryType: "Album", ArtistID: "obscure", Releases: 1},
		{ID: "other", Title: "Song", PrimaryType: "Single", ArtistID: "c", Releases: 3},
	}
	songs := []ReleaseGroup{
		{ID: "debut", Title: "Debut", Year: 1998, MatchedTrack: "Song", ArtistID: "famous"},
		{ID: "x", Title: "X", MatchedTrack: "Song", ArtistID: "obscure"},
	}
	got := mergeCatalog("song", groups, songs, map[string]int{"famous": 90, "obscure": 1})
	var ids []string
	for _, g := range got {
		ids = append(ids, g.ID)
	}
	// famous (105): the album with the song, then the single; then the band
	// 1–3: the exact album title, the album with the song, the rest (more
	// releases first)
	if strings.Join(ids, ",") != "debut,single,album,x,other,comp" {
		t.Fatal(ids)
	}
	if d := got[0]; d.MatchedTrack != "Song" || len(d.Aliases) != 1 || d.Year != 1998 {
		t.Fatalf("%+v", d)
	}
	if got[1].MatchedTrack != "" || got[2].MatchedTrack != "" {
		t.Fatalf("%+v", got)
	}
	for p, b := range map[int]int{0: 0, 1: 1, 3: 1, 4: 2, 15: 2, 16: 3, 100: 4} {
		if popBand(p) != b {
			t.Errorf("popBand(%d) = %d, want %d", p, popBand(p), b)
		}
	}
}

func TestTitleMatches(t *testing.T) {
	for _, c := range []struct {
		q, title, artist string
		ok               bool
	}{
		{"sad but true", "Sad, but True", "Metallica", true},
		{"Metallica  sad but true", "Sad but True", "Metallica", true},
		{"sad but true metallica", "Sad but True", "Metallica", true},
		{"sad but true", "Sad but True (live)", "Metallica", false},
		{"sad", "Sad but True", "Metallica", false},
		{"", "", "", false},
	} {
		if titleMatches(c.q, c.title, c.artist) != c.ok {
			t.Errorf("%+v", c)
		}
	}
}

func TestRecordingQueryFallback(t *testing.T) {
	q := recordingQuery(`ac/dc: "back"`, nil)
	if q != `(recording:"ac/dc: \"back\"" OR (ac\/dc\: \"back\")) AND status:official AND primarytype:album AND NOT comment:live` {
		t.Fatal(q)
	}
}

// A recording search that fails leaves the release groups.
func TestSearchFreeRecordingFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/recording" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(surferRosa))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	gs, err := c.SearchFree(context.Background(), "surfer rosa")
	if err != nil || len(gs) != 2 || gs[0].Title != "Surfer Rosa" {
		t.Fatalf("%v %+v", err, gs)
	}
	// a cancelled request is an error, not an empty answer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.SearchFree(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// The artist lookup failing (an old mirror, an outage of that endpoint)
// leaves the title search to answer on its own.
func TestSearchFreeArtistSearchFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/artist" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/recording" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(surferRosa))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Limiter: &metadata.Limiter{}}
	gs, err := c.SearchFree(context.Background(), "surfer rosa")
	if err != nil || len(gs) != 2 || gs[0].Title != "Surfer Rosa" {
		t.Fatalf("%v %+v", err, gs)
	}
}
