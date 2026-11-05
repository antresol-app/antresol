package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

func TestGenreNamesPerLanguageCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/3/genre/movie/list" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("language") == "en-US" {
			w.Write([]byte(`{"genres":[{"id":18,"name":"Drama"},{"id":80,"name":"Crime"}]}`))
		} else {
			w.Write([]byte(`{"genres":[{"id":18,"name":"драма"},{"id":80,"name":"криминал"}]}`))
		}
	}))
	defer srv.Close()
	c := &Client{Key: "k", BaseURL: srv.URL + "/3"}
	ctx := context.Background()
	ru, err := c.GenreNames(ctx, "movie")
	if err != nil || ru[18] != "драма" {
		t.Fatal(ru, err)
	}
	en, err := c.GenreNames(WithLanguage(ctx, "en-US"), "movie")
	if err != nil || en[80] != "Crime" {
		t.Fatal(en, err)
	}
	if _, err := c.GenreNames(WithLanguage(ctx, "en-US"), "movie"); err != nil || hits.Load() != 2 {
		t.Fatalf("not cached: %d hits, %v", hits.Load(), err)
	}
	if _, err := c.GenreNames(ctx, "person"); err == nil {
		t.Fatal("only movie and tv")
	}
}

// Enrichment keeps the genre ids; an item enriched before they were kept
// is enriched once more, then left alone.
func TestEnrichKeepsGenreIDsAndBackfills(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, TmdbID: 157336, File: library.FileInput{Path: "/m/i.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	it, _ := repo.Item(ctx, id)
	if !strings.Contains(it.MetaJSON, `"genreIds":[`) {
		t.Fatalf("no genre ids: %s", it.MetaJSON)
	}
	// an older enrichment: no genreIds yet
	meta := parseMeta(it.MetaJSON)
	delete(meta, "genreIds")
	meta["tmdb_at"] = time.Now().UTC().Format(time.RFC3339)
	if !pending(meta, it) {
		t.Fatal("an item without genre ids must be enriched again")
	}
	meta["genreIds"] = []int{}
	if pending(meta, it) {
		t.Fatal("an item with genre ids (even none) is done")
	}
}
