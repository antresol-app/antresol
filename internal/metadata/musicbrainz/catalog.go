package musicbrainz

import (
	"context"
	"errors"
	"math/bits"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/release"
)

// The catalog search («На сервер» → music) runs its requests one after the
// other through the client's limiter: the artists named by the query (and,
// for one, their discography), release groups for the query, then
// recordings, so that a song title finds the album it is on.
const (
	groupLimit     = 10
	recordingLimit = 100 // the most MusicBrainz returns at once
	// recordingTimeout bounds the recording search; when it runs out (or
	// fails) the release groups are returned alone.
	recordingTimeout = 10 * time.Second
	maxSongArtists   = 3  // artists of same-titled release groups searched for the song
	maxSongAlbums    = 10 // albums holding the song, at most
	variousArtists   = "89ad4ac3-39f7-470e-963a-56509c546377"
)

// SearchFree searches the catalog for the user's query. A query naming an
// artist («metallica») gets the artist's studio albums and EPs first
// (ByArtist set, see discography), then the other release groups found.
// Otherwise (and when the artist is obscure next to a song of that name):
// release groups (free text) and the albums holding a recording titled like
// the query (MatchedTrack set), the artist's popularity first (see
// mergeCatalog), an exact album title only breaking ties within a band.
func (c *Client) SearchFree(ctx context.Context, q string) ([]ReleaseGroup, error) {
	as, err := c.searchArtists(ctx, q)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// MusicBrainz down (5xx after the retries): the title search would wait as long again
		if he := (*HTTPError)(nil); errors.As(err, &he) && he.Status >= 500 {
			return nil, err
		}
		// the artist block is extra: the title search still answers on its own
		if c.Log != nil {
			c.Log.Warn("musicbrainz: artist search failed, titles only", "err", err)
		}
		as = nil
	}
	var disco []ReleaseGroup
	discoPop := 0
	named := queryArtists(q, as)
	if len(named) > 0 {
		if disco, discoPop, err = c.discography(ctx, named); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if c.Log != nil {
				c.Log.Warn("musicbrainz: discography search failed", "err", err)
			}
		}
	}
	groups, err := c.search(ctx, q, groupLimit)
	if err != nil {
		return nil, err
	}
	if len(disco) > 0 && discoPop >= strongDiscography {
		return artistFirst(disco, mergeCatalog(q, groups, nil, nil)), nil // a band, not a song
	}
	rctx, cancel := context.WithTimeout(ctx, recordingTimeout)
	defer cancel()
	recs, err := c.searchRecordings(rctx, recordingQuery(q, songArtists(q, groups)))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if c.Log != nil {
			c.Log.Warn("musicbrainz: recording search failed, release groups only", "err", err)
		}
		return artistFirst(disco, mergeCatalog(q, groups, nil, nil)), nil // the release groups are an answer on their own
	}
	songs, recW := songAlbums(q, recs)
	if len(disco) > 0 && songOutweighs(songs, recW, named, discoPop) {
		// «creep»: Radiohead's song, not an obscure band of that name — the
		// band's albums rank among the rest
		return mergeCatalog(q, appendNew(groups, plain(disco)), songs, recW), nil
	}
	return artistFirst(disco, mergeCatalog(q, groups, songs, recW)), nil
}

// artistFirst puts the discography before the other results, each release
// group once.
func artistFirst(disco, rest []ReleaseGroup) []ReleaseGroup {
	if len(disco) == 0 {
		return rest
	}
	return appendNew(disco, rest)
}

// appendNew appends to gs the groups of more not already in it.
func appendNew(gs, more []ReleaseGroup) []ReleaseGroup {
	out := append([]ReleaseGroup(nil), gs...)
	seen := map[string]bool{}
	for _, g := range gs {
		seen[g.ID] = true
	}
	for _, g := range more {
		if !seen[g.ID] {
			seen[g.ID] = true
			out = append(out, g)
		}
	}
	return out
}

// plain drops ByArtist: the groups are ranked as ordinary search hits.
func plain(gs []ReleaseGroup) []ReleaseGroup {
	out := make([]ReleaseGroup, len(gs))
	for i, g := range gs {
		g.ByArtist = ""
		out[i] = g
	}
	return out
}

// songOutweighs: a song titled like the query, by an artist other than the
// named ones, has more releases than the named artist's discography.
func songOutweighs(songs []ReleaseGroup, recW map[string]int, named []rawArtist, discoPop int) bool {
	ours := map[string]bool{}
	for _, a := range named {
		ours[a.ID] = true
	}
	for _, s := range songs {
		if !ours[s.ArtistID] && recW[s.ArtistID] > discoPop {
			return true
		}
	}
	return false
}

type rawCredit []struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

func (ac rawCredit) text() string {
	var b strings.Builder
	for _, a := range ac {
		name := a.Name
		if name == "" {
			name = a.Artist.Name
		}
		b.WriteString(name + a.JoinPhrase)
	}
	return strings.TrimSpace(b.String())
}

func (ac rawCredit) firstID() string {
	if len(ac) == 0 {
		return ""
	}
	return ac[0].Artist.ID
}

type rawRecording struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Score          flexInt   `json:"score"`
	Length         flexInt   `json:"length"`         // ms, 0 unknown (MatchCandidates)
	Disambiguation string    `json:"disambiguation"` // «live, 1993-…», «demo» (MatchCandidates)
	ArtistCredit   rawCredit `json:"artist-credit"`
	Releases       []struct {
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
	} `json:"releases"`
}

func (c *Client) searchRecordings(ctx context.Context, query string) ([]rawRecording, error) {
	var raw struct {
		Recordings []rawRecording `json:"recordings"`
	}
	q := url.Values{"query": {query}, "limit": {strconv.Itoa(recordingLimit)}}
	if err := c.get(ctx, "/recording", q, &raw); err != nil {
		return nil, err
	}
	return raw.Recordings, nil
}

// norm folds a title for comparison: lower case, letters and digits only,
// single spaces («Sad, but True» = «sad but true»), Latin letters without
// diacritics («JAŸ-Z» = «jay z»).
func norm(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(release.FoldMarks(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

// titleMatches: the query is the title alone, or the artist and the title
// in either order («metallica sad but true»).
func titleMatches(q, title, artist string) bool {
	nq, nt := norm(q), norm(title)
	if nq == "" || nt == "" {
		return false
	}
	if nq == nt {
		return true
	}
	na := norm(artist)
	return na != "" && (nq == na+" "+nt || nq == nt+" "+na)
}

// songArtist is an artist who has a release group titled like the song
// the query names: their recordings of that title are searched for.
type songArtist struct {
	title, artistID string
}

// songArtists picks, from the release groups found, the artists of those
// titled like the query (a single «Sad But True»), most releases first.
func songArtists(q string, groups []ReleaseGroup) []songArtist {
	type hit struct {
		songArtist
		releases int
	}
	var hits []hit
	seen := map[string]bool{}
	for _, g := range groups {
		if g.ArtistID == "" || g.ArtistID == variousArtists || seen[g.ArtistID] || !titleMatches(q, g.Title, g.Artist) {
			continue
		}
		seen[g.ArtistID] = true
		hits = append(hits, hit{songArtist{g.Title, g.ArtistID}, g.Releases})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].releases > hits[j].releases })
	var out []songArtist
	for _, h := range hits {
		if len(out) == maxSongArtists {
			break
		}
		out = append(out, h.songArtist)
	}
	return out
}

// luceneEscape makes free text a run of plain Lucene terms.
func luceneEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\/`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// recordingQuery searches the song in official albums, live takes left out:
// by the artists found (recording:… AND arid:…), else the title phrase or
// the query's words (which also match the artist: «metallica sad but true»).
func recordingQuery(q string, artists []songArtist) string {
	const filter = " AND status:official AND primarytype:album AND NOT comment:live"
	if len(artists) == 0 {
		return "(recording:" + phrase(q) + " OR (" + luceneEscape(strings.TrimSpace(q)) + "))" + filter
	}
	var order []string
	byTitle := map[string][]string{}
	titles := map[string]string{}
	for _, a := range artists {
		k := norm(a.title)
		if _, ok := byTitle[k]; !ok {
			order = append(order, k)
			titles[k] = a.title
		}
		byTitle[k] = append(byTitle[k], a.artistID)
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, "(recording:"+phrase(titles[k])+" AND arid:("+strings.Join(byTitle[k], " OR ")+"))")
	}
	return "(" + strings.Join(parts, " OR ") + ")" + filter
}

// songAlbums maps recordings titled like the query to the albums they first
// came out on: per recording the earliest official release of the artist's
// own album (primary type Album, no secondary type such as Live or
// Compilation); one entry per artist and release group. Order: the artists
// whose matching recordings have most releases (a popularity hint), then
// the album with most of them, then the oldest. The second result is that
// popularity hint by artist ID: releases of their matching recordings.
func songAlbums(q string, recs []rawRecording) ([]ReleaseGroup, map[string]int) {
	type entry struct {
		g              ReleaseGroup
		date           string
		weight, trackW int
	}
	byKey := map[string]*entry{}
	var order []*entry
	artistW := map[string]int{}
	for _, r := range recs {
		artist := r.ArtistCredit.text()
		aid := r.ArtistCredit.firstID()
		if aid == "" || !titleMatches(q, r.Title, artist) {
			continue
		}
		artistW[aid] += len(r.Releases)
		best := -1
		bestDate := ""
		for i, rel := range r.Releases {
			rg := rel.ReleaseGroup
			if rg.ID == "" || rel.Status != "Official" || rg.PrimaryType != "Album" || len(rg.SecondaryTypes) > 0 {
				continue
			}
			if id := rel.ArtistCredit.firstID(); id != "" && id != aid {
				continue // a various-artists or another artist's album
			}
			d := rel.Date
			if d == "" {
				d = "9999"
			}
			if best < 0 || d < bestDate {
				best, bestDate = i, d
			}
		}
		if best < 0 {
			continue
		}
		rel := r.Releases[best]
		key := aid + "|" + rel.ReleaseGroup.ID
		e := byKey[key]
		if e == nil {
			credit := rel.ArtistCredit.text()
			if credit == "" {
				credit = artist
			}
			e = &entry{g: ReleaseGroup{ID: rel.ReleaseGroup.ID, Title: rel.ReleaseGroup.Title, Artist: credit, ArtistID: aid,
				PrimaryType: "Album", Score: int(r.Score)}, date: bestDate}
			byKey[key] = e
			order = append(order, e)
		}
		e.weight += len(r.Releases)
		if bestDate < e.date {
			e.date = bestDate
		}
		if len(r.Releases) > e.trackW { // the most released take names the song
			e.g.MatchedTrack, e.trackW = r.Title, len(r.Releases)
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if wa, wb := artistW[a.g.ArtistID], artistW[b.g.ArtistID]; wa != wb {
			return wa > wb
		}
		if a.weight != b.weight {
			return a.weight > b.weight
		}
		return a.date < b.date
	})
	out := make([]ReleaseGroup, 0, min(len(order), maxSongAlbums))
	for _, e := range order {
		if len(out) == maxSongAlbums {
			break
		}
		if len(e.date) >= 4 && e.date != "9999" {
			e.g.Year, _ = strconv.Atoi(e.date[:4])
		}
		out = append(out, e.g)
	}
	return out, artistW
}

// mergeCatalog orders the catalog answer by the popularity of the artist
// (artistPopularity), in bands a factor of 4 wide; within a band the albums
// titled exactly like the query come first, then the albums holding the
// song, then the other release groups, each more popular first, then in
// relevance order. «sad but true»: Metallica's «Metallica» (1991) leads,
// not an obscure album of that name. A release group found both ways
// appears once, as the album with the song.
func mergeCatalog(q string, groups, songs []ReleaseGroup, recW map[string]int) []ReleaseGroup {
	nq := norm(q)
	byID := map[string]int{}
	for i, g := range groups {
		byID[g.ID] = i
	}
	song := map[string]string{} // release group ID → matched track
	for _, s := range songs {
		song[s.ID] = s.MatchedTrack
	}
	pop := artistPopularity(groups, recW)
	type item struct {
		g         ReleaseGroup
		tier, pop int
	}
	items := make([]item, 0, len(groups)+len(songs))
	used := map[string]bool{}
	add := func(g ReleaseGroup, tier int) {
		if t, ok := song[g.ID]; ok && g.MatchedTrack == "" {
			g.MatchedTrack = t
		}
		used[g.ID] = true
		p := pop[g.ArtistID]
		if g.ArtistID == "" {
			p = g.Releases
		}
		items = append(items, item{g, tier, p})
	}
	for _, g := range groups {
		if norm(g.Title) == nq && g.PrimaryType == "Album" && len(g.SecondaryTypes) == 0 {
			add(g, 0)
		}
	}
	for _, s := range songs {
		if used[s.ID] {
			continue
		}
		if i, ok := byID[s.ID]; ok { // the search hit carries aliases and the disambiguation
			s = mergeHit(groups[i], s)
		}
		add(s, 1)
	}
	for _, g := range groups {
		if !used[g.ID] {
			add(g, 2)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ba, bb := popBand(a.pop), popBand(b.pop); ba != bb {
			return ba > bb
		}
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		return a.pop > b.pop
	})
	out := make([]ReleaseGroup, len(items))
	for i, it := range items {
		out[i] = it.g
	}
	return out
}

// artistPopularity is the popularity proxy of an artist (by ID): the
// releases of their release groups found plus the releases of their
// recordings titled like the query.
func artistPopularity(groups []ReleaseGroup, recW map[string]int) map[string]int {
	pop := map[string]int{}
	for _, g := range groups {
		if g.ArtistID != "" {
			pop[g.ArtistID] += g.Releases
		}
	}
	for id, w := range recW {
		pop[id] += w
	}
	return pop
}

// popBand groups popularities a factor of 4 apart: 0, 1–3, 4–15, 16–63…
func popBand(p int) int {
	if p <= 0 {
		return 0
	}
	return (bits.Len(uint(p)) + 1) / 2
}

func mergeHit(hit, song ReleaseGroup) ReleaseGroup {
	hit.MatchedTrack = song.MatchedTrack
	if hit.Year == 0 {
		hit.Year = song.Year
	}
	return hit
}
