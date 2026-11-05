package main

import (
	"github.com/antresol-app/antresol/internal/version"
	"io"
	"log/slog"
	"testing"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
	"github.com/antresol-app/antresol/internal/metadata/openlibrary"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/subtitles"
)

func kinds(src metaSources) (tm, mb, ol int) {
	for _, e := range append(append([]library.Enricher{}, src.Fast...), src.Slow...) {
		switch e.(type) {
		case *tmdb.Enricher:
			tm++
		case *musicbrainz.Enricher:
			mb++
		case *openlibrary.Enricher:
			ol++
		}
	}
	return
}

func TestEnrichersOff(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	im := &metadata.Images{Dir: t.TempDir(), UserAgent: version.UserAgent}

	t.Setenv("VAULT_TMDB_KEY", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MusicBrainzURL != "https://musicbrainz.org/ws/2" || cfg.CoverArtURL != "https://coverartarchive.org" ||
		cfg.OpenLibraryURL != "https://openlibrary.org" || cfg.OpenLibraryCoversURL != "https://covers.openlibrary.org" {
		t.Fatalf("defaults %+v", cfg)
	}
	src := buildEnrichers(cfg, nil, im, log, subtitleDeps{})
	if tm, mb, ol := kinds(src); tm != 0 || mb != 1 || ol != 1 || len(src.Fast) != 0 || len(src.Slow) != 2 || src.TMDB != nil {
		t.Fatalf("default: %d %d %d %+v", tm, mb, ol, src)
	}
	mbe := src.Slow[0].(*musicbrainz.Enricher)
	if mbe.Client != src.MusicBrainz || mbe.Client.UserAgent != version.UserAgent || mbe.Client.Limiter.Interval != musicbrainz.DefaultMinInterval ||
		mbe.Client.CoverBase != "https://coverartarchive.org" || mbe.Images != im {
		t.Fatalf("%+v", mbe.Client)
	}
	if ole := src.Slow[1].(*openlibrary.Enricher); ole.Client != src.OpenLibrary || ole.Client.Limiter.Interval != openlibrary.DefaultMinInterval {
		t.Fatalf("%+v", ole.Client)
	}

	t.Setenv("VAULT_TMDB_KEY", "k")
	t.Setenv("VAULT_MUSICBRAINZ_BASEURL", "off")
	t.Setenv("VAULT_OPENLIBRARY_COVERS_BASEURL", "off")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MusicBrainzURL != "off" {
		t.Fatalf("MusicBrainzURL %q", cfg.MusicBrainzURL)
	}
	src = buildEnrichers(cfg, nil, im, log, subtitleDeps{})
	if tm, mb, ol := kinds(src); tm != 1 || mb != 0 || ol != 1 || src.MusicBrainz != nil {
		t.Fatalf("musicbrainz off: %d %d %d", tm, mb, ol)
	}
	if te, ok := src.Fast[0].(*tmdb.Enricher); !ok || te.Client != src.TMDB || len(src.Fast) != 1 {
		t.Fatal("tmdb runs after every scan, with the shared client")
	}
	if c := src.OpenLibrary; c.CoversBase != "" || c.UserAgent != version.UserAgent {
		t.Fatalf("covers off: %+v", c)
	}

	t.Setenv("VAULT_COVERART_BASEURL", "off")
	t.Setenv("VAULT_MUSICBRAINZ_BASEURL", "")
	t.Setenv("VAULT_OPENLIBRARY_BASEURL", "off")
	if cfg, err = config.Load(); err != nil {
		t.Fatal(err)
	}
	src = buildEnrichers(cfg, nil, im, log, subtitleDeps{})
	if tm, mb, ol := kinds(src); tm != 1 || mb != 1 || ol != 0 || src.OpenLibrary != nil {
		t.Fatalf("open library off: %d %d %d", tm, mb, ol)
	}
	if src.MusicBrainz.CoverBase != "" {
		t.Fatal("cover art off")
	}
}

func TestEnrichersSubtitles(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	im := &metadata.Images{Dir: t.TempDir(), UserAgent: version.UserAgent}
	autos := func(src metaSources) (n int, a *subtitles.Auto) {
		for _, e := range src.Slow {
			if x, ok := e.(*subtitles.Auto); ok {
				n, a = n+1, x
			}
		}
		return
	}
	t.Setenv("VAULT_OPENSUBTITLES_KEY", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenSubtitlesURL != "https://api.opensubtitles.com/api/v1" {
		t.Fatalf("%q", cfg.OpenSubtitlesURL)
	}
	if n, _ := autos(buildEnrichers(cfg, nil, im, log, subtitleDeps{})); n != 0 {
		t.Fatal("no key: no auto subtitles")
	}
	t.Setenv("VAULT_OPENSUBTITLES_KEY", "k")
	t.Setenv("VAULT_OPENSUBTITLES_USER", "u")
	if cfg, err = config.Load(); err != nil {
		t.Fatal(err)
	}
	src := buildEnrichers(cfg, nil, im, log, subtitleDeps{DataDir: "/data"})
	n, a := autos(src)
	if n != 1 || a.Client != src.Subtitles || a.Client.Key != "k" || a.Client.Username != "u" || a.Client.UserAgent != "Vault v0.1" ||
		a.DataDir != "/data" || a.MaxPerRun != 10 || a.Client.Limiter == nil || a.Client.Limiter.Interval != subtitles.MinInterval || src.Slow[len(src.Slow)-1] != a {
		t.Fatalf("%d %+v", n, a)
	}
	t.Setenv("VAULT_OPENSUBTITLES_BASEURL", "off")
	if cfg, err = config.Load(); err != nil {
		t.Fatal(err)
	}
	if src := buildEnrichers(cfg, nil, im, log, subtitleDeps{}); src.Subtitles != nil {
		t.Fatal("off")
	}
}
