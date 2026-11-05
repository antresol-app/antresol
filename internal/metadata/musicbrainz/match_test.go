package musicbrainz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
)

// recordingServer answers every recording search with answer(query)
// (a testdata file name, "" = no recordings) and records the queries.
func recordingServer(t *testing.T, answer func(q string) string) (*Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/2/recording" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("request %s", r.URL)
		}
		q := r.URL.Query().Get("query")
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		f := answer(q)
		if f == "" {
			w.Write([]byte(`{"count":0,"recordings":[]}`))
			return
		}
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Error(err)
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL + "/ws/2", Limiter: &metadata.Limiter{}}, &queries
}

func TestMatchByISRC(t *testing.T) {
	c, queries := recordingServer(t, func(string) string { return "recordings-isrc-USEE10001993.json" })
	cs, err := c.MatchCandidates(context.Background(), TrackQuery{ISRC: "USEE10001993", Artist: "Metallica", Title: "Sad But True"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*queries, []string{"isrc:USEE10001993"}) {
		t.Fatalf("queries %q", *queries)
	}
	if len(cs) == 0 {
		t.Fatal("no candidates")
	}
	var studio *TrackCandidate
	seen := map[string]bool{}
	for i, x := range cs {
		if x.RecordingID != "a105dba3-a35b-464f-8f46-b18c21c13809" || x.Title != "Sad but True" || x.Artist != "Metallica" ||
			x.ArtistID != metallicaID || x.DurationMs != 324666 || x.Live || x.Group.MatchedTrack != "Sad but True" {
			t.Fatalf("candidate %+v", x)
		}
		if seen[x.Group.ID] {
			t.Fatalf("group %s twice", x.Group.Title)
		}
		seen[x.Group.ID] = true
		if i > 0 && cs[i-1].Date != "" && x.Date != "" && x.Date < cs[i-1].Date {
			t.Fatalf("not in date order: %s after %s", x.Date, cs[i-1].Date)
		}
		if x.Group.Title == "Metallica" && x.Group.PrimaryType == "Album" && len(x.Group.SecondaryTypes) == 0 && studio == nil {
			studio = &cs[i]
		}
	}
	if studio == nil || studio.Group.Year != 1991 || studio.Group.ArtistID != metallicaID {
		t.Fatalf("studio album %+v", studio)
	}
	// every official release's group, types from the release-group subobject
	var guitarHero bool
	for _, x := range cs {
		guitarHero = guitarHero || strings.HasPrefix(x.Group.Title, "Guitar Hero")
	}
	if guitarHero {
		t.Fatal("a bootleg release's group is a candidate")
	}
}

// The album narrows the first request; when no recording of it has the
// title, the song is searched again without the album.
func TestMatchCandidatesAlbumFirst(t *testing.T) {
	c, queries := recordingServer(t, func(q string) string {
		if strings.Contains(q, "release:") && !strings.Contains(q, `release:"Metallica"`) {
			return ""
		}
		return "recordings-sad-but-true-match.json"
	})
	ctx := context.Background()
	cs, err := c.MatchCandidates(ctx, TrackQuery{Artist: "Metallica", Title: "Sad But True (Remastered 2021)", Album: "Metallica (Remastered)"})
	if err != nil || len(cs) == 0 {
		t.Fatalf("%d %v", len(cs), err)
	}
	want := []string{`recording:"Sad But True" AND artist:"Metallica" AND release:"Metallica"`}
	if !slices.Equal(*queries, want) {
		t.Fatalf("queries %q", *queries)
	}
	var live bool
	for _, x := range cs {
		if x.Disambiguation != "" && strings.HasPrefix(x.Disambiguation, "live") {
			live = true
			if !x.Live {
				t.Fatalf("live take not Live: %+v", x)
			}
		}
	}
	if !live {
		t.Fatal("no live take in the fixture")
	}

	*queries = nil
	if _, err := c.MatchCandidates(ctx, TrackQuery{Artist: "Metallica", Title: "Sad But True", Album: "Greatest Hits"}); err != nil {
		t.Fatal(err)
	}
	want = []string{`recording:"Sad But True" AND artist:"Metallica" AND release:"Greatest Hits"`,
		`recording:"Sad But True" AND artist:"Metallica"`}
	if !slices.Equal(*queries, want) {
		t.Fatalf("queries %q", *queries)
	}
}

func TestStripEditions(t *testing.T) {
	for in, want := range map[string]string{
		"Sad But True (Remastered 2011)":     "Sad But True",
		"Sad But True - 2011 Remaster":       "Sad But True",
		"Metallica (Deluxe Edition)":         "Metallica",
		"Sad But True [Explicit]":            "Sad But True",
		"Song (Deluxe) [Explicit]":           "Song",
		"Sad But True (Live at Wembley)":     "Sad But True (Live at Wembley)",
		"Группа крови":                       "Группа крови",
		"Hey Jude - Remastered 2015":         "Hey Jude",
		"Help! (Remastered 2009) - Live":     "Help! (Remastered 2009) - Live",
		"Wish You Were Here - 2011 Remaster": "Wish You Were Here",
		// a suffix naming a version stays, edition words or not
		"Sad But True - Live / Remastered 2021": "Sad But True - Live / Remastered 2021",
		"Sad But True (Live - 2021 Remaster)":   "Sad But True (Live - 2021 Remaster)",
		"Sad But True - Acoustic / Remastered":  "Sad But True - Acoustic / Remastered",
		"Song (Remastered Live Version)":        "Song (Remastered Live Version)",
		"Song (Live in Stereo)":                 "Song (Live in Stereo)",
		"Song (Unplugged Edition)":              "Song (Unplugged Edition)",
		"Песня (Концерт) [Remastered]":          "Песня (Концерт)",
		"Песня (Акустика, ремастер)":            "Песня (Акустика, ремастер)",
	} {
		if got := StripEditions(in); got != want {
			t.Errorf("StripEditions(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionWords(t *testing.T) {
	for in, want := range map[string][]string{
		"live, 1993-07-04: Festivalpark Werchter": {"live"},
		"Sad But True (Karaoke Version)":          {"karaoke"},
		"Группа крови (концерт)":                  {"концерт"},
		"Discover":              nil,
		"Sad but True":          nil,
		"demo: 1990-08-13":      {"demo"},
		"MTV Unplugged session": {"unplugged", "session"},
		"Живое выступление, ремикс": {"живое", "ремикс"},
	} {
		if got := VersionWords(in); !slices.Equal(got, want) {
			t.Errorf("VersionWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionTagged(t *testing.T) {
	for in, want := range map[string]bool{
		"Sad But True (Live at Wembley)":        true,
		"Sad But True - Live / Remastered 2021": true,
		"Sad But True (Live) [Remastered]":      true,
		"Sad But True (Remastered 2021)":        false,
		"Live and Let Die":                      false,
		"Live Forever - Remastered":             false,
		"Группа крови (концерт)":                true,
	} {
		if got := VersionTagged(in); got != want {
			t.Errorf("VersionTagged(%q) = %v, want %v", in, got, want)
		}
	}
}
