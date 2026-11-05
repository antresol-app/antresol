package musicbrainz

import (
	"context"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A query naming an artist («metallica») is answered with that artist's
// discography first: the artist search finds who the query names, one
// release-group search their studio albums and EPs.
const (
	artistLimit       = 5  // artists asked for the query
	minArtistScore    = 90 // MusicBrainz relevance of an artist the query names
	maxQueryArtists   = 3  // same-named artists whose discographies are asked
	discographyLimit  = 100
	maxDiscography    = 40 // albums and EPs of the artists, at most
	strongDiscography = 20 // releases of the discography: no song search needed
)

type rawArtist struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	SortName string  `json:"sort-name"`
	Score    flexInt `json:"score"`
	Aliases  []struct {
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	} `json:"aliases"`
}

func (c *Client) searchArtists(ctx context.Context, q string) ([]rawArtist, error) {
	var raw struct {
		Artists []rawArtist `json:"artists"`
	}
	v := url.Values{"query": {luceneEscape(strings.TrimSpace(q))}, "limit": {strconv.Itoa(artistLimit)}}
	if err := c.get(ctx, "/artist", v, &raw); err != nil {
		return nil, err
	}
	return raw.Artists, nil
}

// nameKey folds an artist name for comparison: norm without a leading or
// trailing «the» («The Beatles», «Beatles, The» = «beatles»).
func nameKey(s string) string {
	n := norm(s)
	n = strings.TrimPrefix(n, "the ")
	n = strings.TrimSuffix(n, " the")
	return n
}

// named: the query is the artist's name, sort name or one of its aliases.
func (a rawArtist) named(q string) bool {
	k := nameKey(q)
	if k == "" || a.ID == "" || a.ID == variousArtists {
		return false
	}
	names := []string{a.Name, a.SortName}
	for _, al := range a.Aliases {
		names = append(names, al.Name, al.SortName)
	}
	for _, n := range names {
		if nameKey(n) == k {
			return true
		}
	}
	return false
}

// queryArtists are the artists the query names, in relevance order: a
// high score and the name (or an alias) equal to the query.
func queryArtists(q string, as []rawArtist) []rawArtist {
	var out []rawArtist
	for _, a := range as {
		if len(out) == maxQueryArtists {
			break
		}
		if int(a.Score) >= minArtistScore && a.named(q) {
			out = append(out, a)
		}
	}
	return out
}

// discography asks for the official studio albums and EPs (no secondary
// type: no live, compilation, remix, demo…) of the artists. Order: the
// artist with most releases first (a popularity hint, «Nirvana» the US band
// before the others of that name); per artist the albums, then the EPs,
// each oldest first. ByArtist is set to the artist's name. The second
// result is the releases of the first artist's groups.
func (c *Client) discography(ctx context.Context, artists []rawArtist) ([]ReleaseGroup, int, error) {
	ids := make([]string, len(artists))
	for i, a := range artists {
		ids[i] = a.ID
	}
	var raw struct {
		Groups []rawGroup `json:"release-groups"`
	}
	query := "arid:(" + strings.Join(ids, " OR ") + ") AND primarytype:(album OR ep) AND NOT secondarytype:* AND status:official"
	v := url.Values{"query": {query}, "limit": {strconv.Itoa(discographyLimit)}}
	if err := c.get(ctx, "/release-group", v, &raw); err != nil {
		return nil, 0, err
	}
	type entry struct {
		g      ReleaseGroup
		date   string
		artist int // index in artists
	}
	pop := make([]int, len(artists))
	var es []entry
	seen := map[string]bool{}
	for _, r := range raw.Groups {
		g := r.group()
		if g.ID == "" || seen[g.ID] || len(g.SecondaryTypes) > 0 || (g.PrimaryType != "Album" && g.PrimaryType != "EP") {
			continue
		}
		seen[g.ID] = true
		ai := 0 // a group credited to another artist first («Lou Reed & Metallica»)
		for _, ac := range r.ArtistCredit {
			if k := slices.Index(ids, ac.Artist.ID); k >= 0 {
				ai = k
				break
			}
		}
		g.ByArtist = artists[ai].Name
		d := r.FirstRelease
		if d == "" {
			d = "9999"
		}
		pop[ai] += g.Releases
		es = append(es, entry{g, d, ai})
	}
	rank := make([]int, len(artists)) // artist index → place, most releases first
	order := make([]int, len(artists))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return pop[order[i]] > pop[order[j]] })
	for place, ai := range order {
		rank[ai] = place
	}
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.artist != b.artist {
			return rank[a.artist] < rank[b.artist]
		}
		if ea, eb := a.g.PrimaryType == "EP", b.g.PrimaryType == "EP"; ea != eb {
			return eb
		}
		return a.date < b.date
	})
	out := make([]ReleaseGroup, 0, min(len(es), maxDiscography))
	for _, e := range es {
		if len(out) == maxDiscography {
			break
		}
		out = append(out, e.g)
	}
	top := 0
	if len(order) > 0 {
		top = pop[order[0]]
	}
	return out, top, nil
}
