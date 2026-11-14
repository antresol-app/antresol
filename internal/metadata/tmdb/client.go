// Package tmdb is a minimal TMDB v3 client and the library enricher built on it.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// ErrNotFound is returned for HTTP 404 and for empty /find results.
var ErrNotFound = errors.New("tmdb: not found")

// StatusError is an HTTP error status from TMDB (not 404, which is
// ErrNotFound): 401 for a bad key, 5xx when TMDB itself fails.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// DefaultImageBase is used when Client.ImageBase is empty.
const DefaultImageBase = "https://image.tmdb.org/t/p"

func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

const (
	maxRetries     = 3
	maxConcurrency = 4
	maxCertLookups = 3
	defaultLang    = "ru-RU"
)

type langKey struct{}

// WithLanguage makes the lookups made with ctx use the TMDB language tag
// ("ru-RU", "en-US"): live lookups for a request (search and title pages
// of «Найти и добавить») follow the viewer's language. Lookups without it
// use the client's language (the library's metadata language).
func WithLanguage(ctx context.Context, tag string) context.Context {
	if tag == "" {
		return ctx
	}
	return context.WithValue(ctx, langKey{}, tag)
}

// language is the TMDB language of a lookup: the ctx's (WithLanguage),
// else the client's (SetLanguage), else ru-RU.
func (c *Client) language(ctx context.Context) string {
	if l, ok := ctx.Value(langKey{}).(string); ok && l != "" {
		return l
	}
	if l, ok := c.lang.Load().(string); ok && l != "" {
		return l
	}
	return defaultLang
}

// ContextLanguage is the TMDB language WithLanguage put into ctx ("" = none).
func ContextLanguage(ctx context.Context) string {
	l, _ := ctx.Value(langKey{}).(string)
	return l
}

// SetLanguage sets the language of lookups made without WithLanguage —
// library enrichment and scans («Язык метаданных»): a TMDB tag ("ru-RU",
// "en-US"). Safe while lookups run.
func (c *Client) SetLanguage(tag string) { c.lang.Store(tag) }

// imageLanguages is include_image_language for a lookup language: posters
// and logos in that language first, then the other, then textless.
func imageLanguages(tag string) string {
	if strings.HasPrefix(tag, "en") {
		return "en,ru,null"
	}
	return "ru,en,null"
}

// Client talks to TMDB. Key may be a v3 api_key or (longer than 40 chars) a
// v4 read-access token sent as a Bearer header.
type Client struct {
	Key       string
	BaseURL   string // https://api.themoviedb.org/3
	ImageBase string // https://image.tmdb.org/t/p
	HTTP      *http.Client
	Backoff   time.Duration // base delay after HTTP 429 (default 1s)

	once sync.Once
	sem  chan struct{}
	// certSem: the age-rating lookups of kid profiles' searches
	// (Certification), a budget of their own so library scans and admin
	// searches never queue behind them
	certSem chan struct{}
	lang    atomic.Value // string, see SetLanguage
	genres  genreCache
}

type Result struct {
	ID            int
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	Poster        string // TMDB path ("" = none)
	// Adult: TMDB marks it adult content (never shown in «Найти и добавить»).
	Adult bool
}

type Person struct {
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Profile   string `json:"profile,omitempty"` // TMDB path
}

type Details struct {
	ID            int
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	Genres        []string
	// GenreIDs: TMDB's genre ids, named in any language by GenreNames.
	GenreIDs  []int
	Runtime   int
	Rating    string // RU certification, else US mapped to the RU scale
	Poster    string // TMDB paths
	Backdrop  string
	Logo      string
	AltTitles []string
	Cast      []Person
	Seasons   []SeasonCount // TV: seasons with their episode counts (specials are season 0)
	// ReleaseDate: a movie's release date or a show's first air date
	// ("2024-02-27"; "" = unknown).
	ReleaseDate string
	// IMDbID ("tt1160419"; "" = unknown) and OriginalLanguage ("en") are
	// what online sources search by.
	IMDbID           string
	OriginalLanguage string
	// Adult: TMDB marks it adult content.
	Adult bool
	// Countries: where it was made, ISO 3166-1 («SU» the Soviet Union): a
	// movie's production countries, a show's origin countries.
	Countries []string
	// Keywords: TMDB's keywords (English, lower case: «space», «christmas»).
	Keywords []string
}

// SeasonCount is one season of a TV show in its details.
type SeasonCount struct {
	Number   int
	Episodes int
}

type Episode struct {
	Number   int
	Name     string
	Overview string
	Still    string
	AirDate  string // "2024-02-27"; "" = not announced
}

type SeasonDetails struct {
	Number   int
	AirDate  string // the season's first air date; "" = unknown
	Episodes []Episode
}

func (c *Client) init() {
	c.once.Do(func() {
		c.sem = make(chan struct{}, maxConcurrency)
		c.certSem = make(chan struct{}, maxCertLookups)
	})
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	c.init()
	return c.getWith(ctx, c.sem, p, q, out)
}

// getWith is get holding a slot of sem for the request.
func (c *Client) getWith(ctx context.Context, sem chan struct{}, p string, q url.Values, out any) error {
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if q == nil {
		q = url.Values{}
	}
	bearer := len(c.Key) > 40
	if !bearer && c.Key != "" {
		q.Set("api_key", c.Key)
	}
	u := strings.TrimRight(c.BaseURL, "/") + p + "?" + q.Encode()
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("tmdb %s: %s", p, redact(err.Error(), c.Key))
		}
		req.Header.Set("Accept", "application/json")
		if bearer {
			req.Header.Set("Authorization", "Bearer "+c.Key)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// *url.Error embeds the full URL including api_key: drop it.
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return fmt.Errorf("tmdb %s: %s", p, redact(err.Error(), c.Key))
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			if attempt >= maxRetries {
				return fmt.Errorf("tmdb %s: rate limited", p)
			}
			d := backoff << attempt
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
				d = min(time.Duration(s)*time.Second, 30*time.Second)
			}
			select {
			case <-time.After(d):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("tmdb %s: %w", p, &StatusError{Code: resp.StatusCode})
		}
		if rerr != nil {
			return rerr
		}
		return json.Unmarshal(body, out)
	}
}

type rawResults struct {
	Results []struct {
		ID            int    `json:"id"`
		Title         string `json:"title"`
		Name          string `json:"name"`
		OriginalTitle string `json:"original_title"`
		OriginalName  string `json:"original_name"`
		ReleaseDate   string `json:"release_date"`
		FirstAirDate  string `json:"first_air_date"`
		Overview      string `json:"overview"`
		PosterPath    string `json:"poster_path"`
		Adult         bool   `json:"adult"`
	} `json:"results"`
}

func yearOf(d string) int {
	if len(d) >= 4 {
		n, _ := strconv.Atoi(d[:4])
		return n
	}
	return 0
}

func (c *Client) search(ctx context.Context, kind, q string, year int) ([]Result, error) {
	v := url.Values{"query": {q}, "language": {c.language(ctx)}, "include_adult": {"false"}}
	if year > 0 {
		if kind == "movie" {
			v.Set("year", strconv.Itoa(year))
		} else {
			v.Set("first_air_date_year", strconv.Itoa(year))
		}
	}
	var raw rawResults
	if err := c.get(ctx, "/search/"+kind, v, &raw); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(raw.Results))
	for _, r := range raw.Results {
		out = append(out, Result{
			ID: r.ID, Title: first(r.Title, r.Name), OriginalTitle: first(r.OriginalTitle, r.OriginalName),
			Year: yearOf(first(r.ReleaseDate, r.FirstAirDate)), Overview: r.Overview, Poster: r.PosterPath, Adult: r.Adult,
		})
	}
	return out, nil
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (c *Client) SearchMovie(ctx context.Context, q string, year int) ([]Result, error) {
	return c.search(ctx, "movie", q, year)
}

func (c *Client) SearchTV(ctx context.Context, q string, year int) ([]Result, error) {
	return c.search(ctx, "tv", q, year)
}

// Find resolves an external id (source: "tvdb_id" or "imdb_id") to a TMDB id
// of the given kind ("movie" or "tv"). ErrNotFound when there is no match.
func (c *Client) Find(ctx context.Context, kind, source, extID string) (int, error) {
	var raw struct {
		Movie []struct{ ID int } `json:"movie_results"`
		TV    []struct{ ID int } `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(extID), url.Values{"external_source": {source}, "language": {c.language(ctx)}}, &raw); err != nil {
		return 0, err
	}
	if kind == "movie" && len(raw.Movie) > 0 {
		return raw.Movie[0].ID, nil
	}
	if kind != "movie" && len(raw.TV) > 0 {
		return raw.TV[0].ID, nil
	}
	return 0, ErrNotFound
}

type rawDetails struct {
	ID            int    `json:"id"`
	Adult         bool   `json:"adult"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	OriginalLang  string `json:"original_language"`
	// a movie's production countries; a show's origin_country (and its production_countries)
	ProductionCountries []struct {
		Code string `json:"iso_3166_1"`
	} `json:"production_countries"`
	OriginCountry []string `json:"origin_country"`
	// keywords: a movie's under "keywords", a show's under "results"
	KeywordsRaw struct {
		Keywords []struct {
			Name string `json:"name"`
		} `json:"keywords"`
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	} `json:"keywords"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
	Overview     string `json:"overview"`
	Genres       []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
	Runtime        int    `json:"runtime"`
	EpisodeRunTime []int  `json:"episode_run_time"`
	PosterPath     string `json:"poster_path"`
	BackdropPath   string `json:"backdrop_path"`
	AltTitles      struct {
		Titles  []struct{ Title string } `json:"titles"`
		Results []struct{ Title string } `json:"results"`
	} `json:"alternative_titles"`
	Translations struct {
		Translations []struct {
			Data struct {
				Title string `json:"title"`
				Name  string `json:"name"`
			} `json:"data"`
		} `json:"translations"`
	} `json:"translations"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Cert string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	Credits struct {
		Cast []struct {
			Name      string `json:"name"`
			Character string `json:"character"`
			Profile   string `json:"profile_path"`
		} `json:"cast"`
	} `json:"credits"`
	Seasons []struct {
		Number   int `json:"season_number"`
		Episodes int `json:"episode_count"`
	} `json:"seasons"`
	Images struct {
		Logos   []rawImage `json:"logos"`
		Posters []rawImage `json:"posters"`
	} `json:"images"`
	IMDbID      string `json:"imdb_id"` // movies only
	ExternalIDs struct {
		IMDbID string `json:"imdb_id"`
	} `json:"external_ids"`
}

// rawImage is one of a title's images (images.logos, images.posters);
// Lang "" is textless (iso_639_1 null).
type rawImage struct {
	Path string `json:"file_path"`
	Lang string `json:"iso_639_1"`
}

const maxCast = 15

func (c *Client) details(ctx context.Context, kind string, id int) (Details, error) {
	lng := c.language(ctx)
	v := url.Values{
		"language":               {lng},
		"append_to_response":     {"alternative_titles,translations,release_dates,content_ratings,credits,images,external_ids,keywords"},
		"include_image_language": {imageLanguages(lng)},
	}
	var r rawDetails
	if err := c.get(ctx, fmt.Sprintf("/%s/%d", kind, id), v, &r); err != nil {
		return Details{}, err
	}
	d := Details{
		ID: r.ID, Title: first(r.Title, r.Name), OriginalTitle: first(r.OriginalTitle, r.OriginalName),
		Year: yearOf(first(r.ReleaseDate, r.FirstAirDate)), Overview: r.Overview, Runtime: r.Runtime,
		Poster: r.PosterPath, Backdrop: r.BackdropPath, ReleaseDate: first(r.ReleaseDate, r.FirstAirDate),
		IMDbID: first(r.ExternalIDs.IMDbID, r.IMDbID), OriginalLanguage: r.OriginalLang,
	}
	if d.Runtime == 0 && len(r.EpisodeRunTime) > 0 {
		d.Runtime = r.EpisodeRunTime[0]
	}
	for _, g := range r.Genres {
		d.Genres = append(d.Genres, g.Name)
		d.GenreIDs = append(d.GenreIDs, g.ID)
	}
	for _, se := range r.Seasons {
		d.Seasons = append(d.Seasons, SeasonCount{Number: se.Number, Episodes: se.Episodes})
	}
	d.Countries = countriesOf(r)
	for _, k := range append(r.KeywordsRaw.Keywords, r.KeywordsRaw.Results...) {
		if n := strings.ToLower(strings.TrimSpace(k.Name)); n != "" && !slices.Contains(d.Keywords, n) {
			d.Keywords = append(d.Keywords, n)
		}
	}
	seen := map[string]bool{}
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			d.AltTitles = append(d.AltTitles, t)
		}
	}
	add(d.Title)
	add(d.OriginalTitle)
	for _, t := range r.AltTitles.Titles {
		add(t.Title)
	}
	for _, t := range r.AltTitles.Results {
		add(t.Title)
	}
	for _, t := range r.Translations.Translations {
		add(first(t.Data.Title, t.Data.Name))
	}
	for i, p := range r.Credits.Cast {
		if i >= maxCast {
			break
		}
		d.Cast = append(d.Cast, Person{Name: p.Name, Character: p.Character, Profile: p.Profile})
	}
	d.Logo = pickLogo(r, lng)
	d.Rating = pickRating(r)
	d.Adult = r.Adult
	return d, nil
}

// pickLogo prefers a logo in the lookup language, then the other one,
// then a textless one.
func pickLogo(r rawDetails, tag string) string {
	order := []string{"ru", "en", ""}
	if strings.HasPrefix(tag, "en") {
		order = []string{"en", "ru", ""}
	}
	for _, want := range order {
		for _, l := range r.Images.Logos {
			if l.Lang == want {
				return l.Path
			}
		}
	}
	if len(r.Images.Logos) > 0 {
		return r.Images.Logos[0].Path
	}
	return ""
}

// pickIn is the first image in the language (an ISO 639-1 code), else the
// first textless one; "" when there is neither (an image with text in
// another language is no better than the stored one).
func pickIn(images []rawImage, lang string) string {
	for _, want := range []string{lang, ""} {
		for _, im := range images {
			if im.Lang == want && im.Path != "" {
				return im.Path
			}
		}
	}
	return ""
}

// pickRating prefers the RU certification; otherwise the US one, which
// library.NormalizeRating maps onto the RU scale (PG-13 -> 12+, TV-MA -> 18+).
func pickRating(r rawDetails) string {
	var ru, us string
	for _, c := range r.ReleaseDates.Results {
		for _, d := range c.Dates {
			if d.Cert == "" {
				continue
			}
			switch c.Country {
			case "RU":
				if ru == "" {
					ru = d.Cert
				}
			case "US":
				if us == "" {
					us = d.Cert
				}
			}
		}
	}
	for _, c := range r.ContentRatings.Results {
		if c.Rating == "" {
			continue
		}
		switch c.Country {
		case "RU":
			if ru == "" {
				ru = c.Rating
			}
		case "US":
			if us == "" {
				us = c.Rating
			}
		}
	}
	if ru != "" {
		ru = strings.TrimSpace(ru)
		if _, err := strconv.Atoi(ru); err == nil {
			ru += "+" // "16" -> "16+"
		}
		if _, ok := library.RatingRank(ru); ok {
			return library.NormalizeRating(ru)
		}
	}
	if _, ok := library.RatingRank(us); ok {
		return library.NormalizeRating(us)
	}
	return ""
}

// Certification is the age rating of a movie or show ("" = none), as
// Details.Rating, from its release dates or content ratings alone (one
// small request, on a budget of its own: certSem). kind: "movie" or "tv".
func (c *Client) Certification(ctx context.Context, kind string, id int) (string, error) {
	c.init()
	var r rawDetails
	var err error
	if kind == "movie" {
		err = c.getWith(ctx, c.certSem, "/movie/"+strconv.Itoa(id)+"/release_dates", nil, &r.ReleaseDates)
	} else {
		err = c.getWith(ctx, c.certSem, "/tv/"+strconv.Itoa(id)+"/content_ratings", nil, &r.ContentRatings)
	}
	if err != nil {
		return "", err
	}
	return pickRating(r), nil
}

func (c *Client) Movie(ctx context.Context, id int) (Details, error) {
	return c.details(ctx, "movie", id)
}

func (c *Client) TV(ctx context.Context, id int) (Details, error) {
	return c.details(ctx, "tv", id)
}

func (c *Client) Season(ctx context.Context, tvID, n int) (SeasonDetails, error) {
	var raw struct {
		Number   int    `json:"season_number"`
		AirDate  string `json:"air_date"`
		Episodes []struct {
			Number   int    `json:"episode_number"`
			Name     string `json:"name"`
			Overview string `json:"overview"`
			Still    string `json:"still_path"`
			AirDate  string `json:"air_date"`
		} `json:"episodes"`
	}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tvID, n), url.Values{"language": {c.language(ctx)}}, &raw); err != nil {
		return SeasonDetails{}, err
	}
	s := SeasonDetails{Number: raw.Number, AirDate: raw.AirDate}
	for _, e := range raw.Episodes {
		s.Episodes = append(s.Episodes, Episode{Number: e.Number, Name: e.Name, Overview: e.Overview, Still: e.Still, AirDate: e.AirDate})
	}
	return s, nil
}

// ImageURL builds a full image URL for a TMDB path at the given size ("w500").
func (c *Client) ImageURL(size, p string) string {
	if p == "" {
		return ""
	}
	base := c.ImageBase
	if base == "" {
		base = DefaultImageBase
	}
	return strings.TrimRight(base, "/") + "/" + size + p
}

// imageRoot is the URL probed to tell "image host down" from "one bad image".
func (c *Client) imageRoot() string {
	if c.ImageBase == "" {
		return DefaultImageBase
	}
	return c.ImageBase
}

// countriesOf: a show's origin countries first, then the production
// countries (a movie has only those); upper case, once each.
func countriesOf(r rawDetails) []string {
	out := []string{}
	add := func(c string) {
		c = strings.ToUpper(strings.TrimSpace(c))
		if len(c) == 2 && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, c := range r.OriginCountry {
		add(c)
	}
	for _, c := range r.ProductionCountries {
		add(c.Code)
	}
	return out
}
