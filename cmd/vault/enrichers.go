package main

import (
	"context"
	"database/sql"
	"github.com/antresol-app/antresol/internal/version"
	"log/slog"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/antresol-app/antresol/internal/metadata/musicbrainz"
	"github.com/antresol-app/antresol/internal/metadata/openlibrary"
	"github.com/antresol-app/antresol/internal/metadata/ratings"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/metadata/wikidata"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// off disables a metadata source (VAULT_*_BASEURL=off).
const off = "off"

func source(u string) string {
	if u == off {
		return ""
	}
	return u
}

// metaSources are the metadata clients and the enrichers built on them.
// The clients are shared: one MusicBrainz client (and so one 1 req/s
// limiter) serves both the enricher and the sources build's searches.
type metaSources struct {
	Fast []library.Enricher // Jobs.Enrichers: run after every scan (TMDB)
	Slow []library.Enricher // Jobs.SlowEnrichers: rate-limited, own worker

	TMDB        *tmdb.Client        // nil without a key
	MusicBrainz *musicbrainz.Client // nil when off
	OpenLibrary *openlibrary.Client // nil when off
	Subtitles   *subtitles.Client   // nil without a key or when off
	// Wikidata: Rotten Tomatoes scores, and the sources build's book search
	// (one client, one limiter); nil when off.
	Wikidata *wikidata.Client
}

// subtitleDeps are what automatic subtitles need besides the config: the
// attempts table and the «Субтитры» settings (languages, on/off).
type subtitleDeps struct {
	DB       *sql.DB
	Settings func(ctx context.Context) (langs []string, enabled bool)
	DataDir  string
}

// buildEnrichers builds the metadata sources: TMDB (with a key) runs after
// every scan; MusicBrainz then Open Library (unless off) run in the slow
// worker, so their 1 req/s passes never hold back a scan; automatic
// subtitles (with an OpenSubtitles key) run last there.
func buildEnrichers(cfg config.Config, repo *library.Repo, images *metadata.Images, log *slog.Logger, subs subtitleDeps) metaSources {
	var out metaSources
	if cfg.TMDBKey != "" {
		out.TMDB = &tmdb.Client{Key: cfg.TMDBKey, BaseURL: cfg.TMDBBaseURL, ImageBase: cfg.TMDBImageBase}
		out.Fast = append(out.Fast, &tmdb.Enricher{Client: out.TMDB, Repo: repo, Images: images, Log: log})
	} else {
		log.Info("VAULT_TMDB_KEY is not set: movie and series metadata comes from local files only")
	}
	if u := source(cfg.MusicBrainzURL); u != "" {
		out.MusicBrainz = &musicbrainz.Client{BaseURL: u, CoverBase: source(cfg.CoverArtURL), UserAgent: version.UserAgent,
			Limiter: &metadata.Limiter{Interval: musicbrainz.DefaultMinInterval}, Log: log}
		out.Slow = append(out.Slow, &musicbrainz.Enricher{Client: out.MusicBrainz, Repo: repo, Images: images, Log: log})
	} else {
		log.Info("VAULT_MUSICBRAINZ_BASEURL=off: album metadata comes from tags only")
	}
	if u := source(cfg.OpenLibraryURL); u != "" {
		out.OpenLibrary = &openlibrary.Client{BaseURL: u, CoversBase: source(cfg.OpenLibraryCoversURL), UserAgent: version.UserAgent,
			Limiter: &metadata.Limiter{Interval: openlibrary.DefaultMinInterval}}
		out.Slow = append(out.Slow, &openlibrary.Enricher{Client: out.OpenLibrary, Repo: repo, Images: images, Log: log})
	} else {
		log.Info("VAULT_OPENLIBRARY_BASEURL=off: book metadata comes from the files only")
	}
	if u := source(cfg.WikidataURL); u != "" {
		out.Wikidata = &wikidata.Client{BaseURL: u, UserAgent: version.UserAgent, Limiter: &metadata.Limiter{Interval: wikidata.DefaultMinInterval}}
	}
	if u := source(cfg.OpenSubtitlesURL); u != "" && cfg.OpenSubtitlesKey != "" {
		out.Subtitles = &subtitles.Client{BaseURL: u, Key: cfg.OpenSubtitlesKey, Username: cfg.OpenSubtitlesUser,
			Password: cfg.OpenSubtitlesPassword, UserAgent: subtitles.UserAgent,
			Limiter: &metadata.Limiter{Interval: subtitles.MinInterval}}
		out.Slow = append(out.Slow, &subtitles.Auto{Client: out.Subtitles, Repo: repo, DB: subs.DB, Settings: subs.Settings,
			DataDir: subs.DataDir, MaxPerRun: 10, Log: log})
	} else {
		log.Info("VAULT_OPENSUBTITLES_KEY is not set: no automatic subtitles")
	}
	return out
}

// buildRatings is the IMDb ratings dataset (VAULT_IMDB_RATINGS_URL, kept in
// DataDir and downloaded when a title page first asks) and the Rotten
// Tomatoes scores of Wikidata; nil when both are off.
func buildRatings(cfg config.Config, database *sql.DB, wd *wikidata.Client, log *slog.Logger) *ratings.Service {
	s := &ratings.Service{DB: database, Log: log}
	if cfg.IMDbRatingsURL != "" {
		s.IMDb = &ratings.Dataset{URL: cfg.IMDbRatingsURL, Dir: cfg.DataDir, UserAgent: version.UserAgent, Log: log}
	} else {
		log.Info("VAULT_IMDB_RATINGS_URL is off: no IMDb ratings")
	}
	if wd != nil {
		s.RT = wd
	}
	if s.IMDb == nil && s.RT == nil {
		return nil
	}
	return s
}
