package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParse(t *testing.T) {
	l := Parse("[offset:500]\n[ar:x]\n[00:01.00][00:10.5]Hi\n[00:05]\n")
	if len(l) != 3 || l[0].T != 0.5 || l[0].Text != "Hi" || l[1].Text != "" || l[2].T != 10 {
		t.Fatalf("%+v", l)
	}
	if Parse("plain words") != nil {
		t.Fatal("plain")
	}
}

func fake(t *testing.T, hits *int32) *Client {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.Header.Get("User-Agent") != "ua/1" {
			t.Errorf("ua %q", r.Header.Get("User-Agent"))
		}
		switch {
		case r.URL.Path == "/api/get" && r.URL.Query().Get("track_name") == "Known":
			w.Write([]byte(`{"duration":200,"syncedLyrics":"[00:01.00]a","plainLyrics":"a"}`))
		case r.URL.Path == "/api/search" && r.URL.Query().Get("track_name") == "Fuzzy":
			w.Write([]byte(`[{"duration":300,"plainLyrics":"far"},{"duration":201,"plainLyrics":"p"},{"duration":199,"syncedLyrics":"[00:02]s","plainLyrics":"s"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return &Client{Base: s.URL, HTTP: s.Client(), UA: "ua/1"}
}

func TestFind(t *testing.T) {
	var hits int32
	c := fake(t, &hits)
	ctx := context.Background()
	r, err := c.Find(ctx, Query{Artist: "A", Title: "Known", DurationSec: 200})
	if err != nil || r == nil || r.Synced != "[00:01.00]a" {
		t.Fatalf("%+v %v", r, err)
	}
	c.Find(ctx, Query{Artist: "A", Title: "Known", DurationSec: 200})
	if hits != 1 {
		t.Fatalf("cache: %d", hits)
	}
	r, _ = c.Find(ctx, Query{Artist: "A", Title: "Fuzzy", DurationSec: 200})
	if r == nil || r.Synced != "[00:02]s" {
		t.Fatalf("%+v", r)
	}
	if r, _ = c.Find(ctx, Query{Title: "Nope"}); r != nil {
		t.Fatal(r)
	}
	var nilc *Client
	if r, err := nilc.Find(ctx, Query{Title: "x"}); r != nil || err != nil {
		t.Fatal()
	}
}

func TestLeadArtist(t *testing.T) {
	for in, want := range map[string]string{
		"Stevie Ray Vaughan And Double Trouble": "Stevie Ray Vaughan",
		"Simon & Garfunkel":                     "Simon",
		"Drake feat. Rihanna":                   "Drake",
		"Metallica":                             "Metallica",
	} {
		if got := leadArtist(in); got != want {
			t.Errorf("leadArtist(%q) = %q, want %q", in, got, want)
		}
	}
}
