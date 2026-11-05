package tmdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// Enrichment downloads the cast's photos into the image cache, so the
// page shows them with TMDB unreachable; a title enriched before photos
// were kept gets them on the next run, without asking TMDB's API again.
func TestEnrichCachesCastPhotos(t *testing.T) {
	f := newFake(t)
	e, repo := setup(t, f.srv.URL)
	ctx := context.Background()
	id, _ := repo.UpsertMovie(ctx, library.MovieInput{Title: "Interstellar", Year: 2014, TmdbID: 157336, File: library.FileInput{Path: "/m/i.mkv"}})
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("/t/p/w185/m.jpg") != 1 {
		t.Fatalf("photo not fetched: %v", f.hits)
	}
	p := &Photos{Client: e.Client, Images: e.Images}
	name, ok := p.Cached("/m.jpg")
	if !ok {
		t.Fatal("photo not in the cache")
	}
	if b, err := os.ReadFile(filepath.Join(e.Images.Dir, name)); err != nil || string(b) != "IMG" {
		t.Fatalf("%q %v", b, err)
	}
	it, _ := repo.Item(ctx, id)
	if !strings.Contains(it.MetaJSON, `"cast_photos_at"`) {
		t.Fatalf("not marked: %s", it.MetaJSON)
	}

	// an older enrichment: photos not kept yet
	if err := os.Remove(filepath.Join(e.Images.Dir, name)); err != nil {
		t.Fatal(err)
	}
	meta := parseMeta(it.MetaJSON)
	delete(meta, "cast_photos_at")
	b, _ := json.Marshal(meta)
	if err := repo.SetMetadata(ctx, id, library.Metadata{MetaJSON: string(b)}); err != nil {
		t.Fatal(err)
	}
	api := f.count("/3/movie/157336")
	if err := e.EnrichPending(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Cached("/m.jpg"); !ok || f.count("/3/movie/157336") != api {
		t.Fatalf("backfill: cached %v, api hits %d → %d", ok, api, f.count("/3/movie/157336"))
	}
	// done: the next run fetches nothing
	n := len(f.hits)
	if err := e.EnrichPending(ctx); err != nil || len(f.hits) != n {
		t.Fatalf("again: %v %v", err, f.hits[n:])
	}
}
