// Package musicbrainz is a minimal MusicBrainz web service v2 client (release
// groups, recordings, Cover Art Archive URLs) and the album enricher built on it.
package musicbrainz

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// DefaultMinInterval is the MusicBrainz rate limit: one request per second.
const DefaultMinInterval = time.Second

const (
	maxRetries = 3 // after HTTP 503
	maxGenres  = 5
)

// retryDelay is the pause before repeating a request MusicBrainz answered
// with 503 (its rate limiter). A var for tests.
var retryDelay = 2 * time.Second

// ErrNotFound is returned for HTTP 404 (an unknown or deleted MBID).
var ErrNotFound = metadata.ErrNotFound

// HTTPError is an answer other than 200 and 404 (after the 503 retries).
type HTTPError = metadata.HTTPError

// Client talks to MusicBrainz. All its requests share Limiter (nil: one is
// created with DefaultMinInterval); UserAgent identifies the application as
// MusicBrainz requires.
type Client struct {
	BaseURL   string // https://musicbrainz.org/ws/2
	CoverBase string // https://coverartarchive.org ("" = no covers)
	UserAgent string
	HTTP      *http.Client
	Limiter   *metadata.Limiter
	Log       *slog.Logger // nil: no log (a failed recording search of SearchFree)

	once sync.Once
	lim  *metadata.Limiter
}

// ReleaseGroup is a MusicBrainz release group (an album across editions).
// Artist is the full artist credit; Genres come with a lookup only (search
// results carry none), most voted first.
type ReleaseGroup struct {
	ID       string
	Title    string
	Artist   string
	ArtistID string // the first credited artist
	Year     int
	Genres   []string
	Score    int // search relevance 0..100 (0 for a lookup)
	// Disambiguation tells same-named groups apart («Blue Album», but also
	// «live», «compilation»): a note, not a name.
	Disambiguation string
	// Aliases are other names of the group (release-group aliases).
	Aliases []string
	// PrimaryType («Album», «Single», «EP»…) and SecondaryTypes («Live»,
	// «Compilation»…) come with a search hit.
	PrimaryType    string
	SecondaryTypes []string
	// Releases is the number of releases (editions) in the group, a
	// popularity hint of a search hit.
	Releases int
	// MatchedTrack is the song of the query the album holds when the album
	// was found by a recording search (SearchFree), "" otherwise.
	MatchedTrack string
	// ByArtist is the artist the query names when the group is from their
	// discography (SearchFree puts it first), "" otherwise.
	ByArtist string
}

func (c *Client) limiter() *metadata.Limiter {
	c.once.Do(func() {
		c.lim = c.Limiter
		if c.lim == nil {
			c.lim = &metadata.Limiter{Interval: DefaultMinInterval}
		}
	})
	return c.lim
}

func (c *Client) get(ctx context.Context, p string, q url.Values, out any) error {
	q.Set("fmt", "json")
	g := metadata.Getter{Service: "musicbrainz", HTTP: c.HTTP, Limiter: c.limiter(), UserAgent: c.UserAgent,
		Retry: func(s int) bool { return s == http.StatusServiceUnavailable }, RetryDelay: retryDelay, MaxRetries: maxRetries}
	return g.JSON(ctx, strings.TrimRight(c.BaseURL, "/")+p+"?"+q.Encode(), p, out)
}

// flexInt reads a number that older servers send as a string ("100").
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	*f = flexInt(n)
	return err
}

type rawGroup struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Score          flexInt  `json:"score"`
	FirstRelease   string   `json:"first-release-date"`
	Disambiguation string   `json:"disambiguation"`
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
	Count          flexInt  `json:"count"`
	Aliases        []struct {
		Name string `json:"name"`
	} `json:"aliases"`
	ArtistCredit []struct {
		Name       string `json:"name"`
		JoinPhrase string `json:"joinphrase"`
		Artist     struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit"`
	Genres []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"genres"`
}

func (r rawGroup) group() ReleaseGroup {
	g := ReleaseGroup{ID: r.ID, Title: r.Title, Score: int(r.Score), Disambiguation: r.Disambiguation,
		PrimaryType: r.PrimaryType, SecondaryTypes: r.SecondaryTypes, Releases: int(r.Count)}
	for _, a := range r.Aliases {
		if n := strings.TrimSpace(a.Name); n != "" {
			g.Aliases = append(g.Aliases, n)
		}
	}
	if len(r.FirstRelease) >= 4 {
		g.Year, _ = strconv.Atoi(r.FirstRelease[:4])
	}
	var b strings.Builder
	for i, ac := range r.ArtistCredit {
		name := ac.Name
		if name == "" {
			name = ac.Artist.Name
		}
		b.WriteString(name + ac.JoinPhrase)
		if i == 0 {
			g.ArtistID = ac.Artist.ID
		}
	}
	g.Artist = strings.TrimSpace(b.String())
	gs := r.Genres
	sort.SliceStable(gs, func(i, j int) bool { return gs[i].Count > gs[j].Count })
	for _, x := range gs {
		if len(g.Genres) == maxGenres {
			break
		}
		if x.Name != "" {
			g.Genres = append(g.Genres, x.Name)
		}
	}
	return g
}

// phrase quotes s as a Lucene phrase.
func phrase(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(strings.TrimSpace(s))
	return `"` + s + `"`
}

func (c *Client) search(ctx context.Context, query string, limit int) ([]ReleaseGroup, error) {
	var raw struct {
		Groups []rawGroup `json:"release-groups"`
	}
	q := url.Values{"query": {query}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "/release-group", q, &raw); err != nil {
		return nil, err
	}
	out := make([]ReleaseGroup, 0, len(raw.Groups))
	for _, r := range raw.Groups {
		out = append(out, r.group())
	}
	return out, nil
}

// SearchReleaseGroups looks an album up by its title and artist. Results
// keep the server's relevance order, except that those released in year
// (when given) come first.
func (c *Client) SearchReleaseGroups(ctx context.Context, artist, album string, year int) ([]ReleaseGroup, error) {
	rs, err := c.search(ctx, "releasegroup:"+phrase(album)+" AND artist:"+phrase(artist), 5)
	if err != nil {
		return nil, err
	}
	if year > 0 {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Year == year && rs[j].Year != year })
	}
	return rs, nil
}

// ReleaseGroup looks up one release group with its artist credit and genres.
func (c *Client) ReleaseGroup(ctx context.Context, id string) (ReleaseGroup, error) {
	var raw rawGroup
	if err := c.get(ctx, "/release-group/"+url.PathEscape(id), url.Values{"inc": {"artist-credits genres"}}, &raw); err != nil {
		return ReleaseGroup{}, err
	}
	return raw.group(), nil
}

// CoverURL is the Cover Art Archive front cover (500 px) of a release group,
// "" when the client has no CoverBase.
func (c *Client) CoverURL(id string) string {
	if c.CoverBase == "" {
		return ""
	}
	return strings.TrimRight(c.CoverBase, "/") + "/release-group/" + url.PathEscape(id) + "/front-500"
}
