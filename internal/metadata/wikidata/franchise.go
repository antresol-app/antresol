package wikidata

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Properties of a film or a series' place in a franchise.
const (
	propTMDBMovie  = "P4947" // TMDB movie ID
	propTMDBTV     = "P4983" // TMDB TV series ID
	propFranchise  = "P8345" // media franchise
	propUniverse   = "P1434" // takes place in fictional universe
	propSeries     = "P179"  // part of the series
	propOrdinal    = "P1545" // series ordinal (a qualifier of P179)
	propFollows    = "P155"  // follows
	propFollowedBy = "P156"  // followed by
	propPartOf     = "P361"  // part of
	propInstance   = "P31"   // instance of
	propSubclass   = "P279"  // subclass of
)

// FactsVersion is the shape of TitleFacts a cache may keep: a cached value
// of an older version is fetched again (version 2: the anchors).
const FactsVersion = 2

// Anchor ranks: what kind of item ties a title to its franchise, the
// franchise itself first (its name names the franchise best).
const (
	RankFranchise  = 0 // a media or film franchise (Star Wars, Marvel Cinematic Universe)
	RankUniverse   = 1 // a fictional or shared universe (Star Wars universe)
	RankAdaptation = 2 // the screen adaptations of a work (Middle-earth in motion pictures)
)

// anchorClasses are the classes (P31) of the items that make a franchise,
// with their rank; a subclass of one (P279, one level) counts as it.
var anchorClasses = map[string]int{
	"Q196600":    RankFranchise,  // media franchise
	"Q130371093": RankFranchise,  // film franchise
	"Q559618":    RankUniverse,   // fictional universe
	"Q3275581":   RankUniverse,   // shared universe
	"Q133829894": RankAdaptation, // adaptations of works
}

// seriesClasses are the classes of a series of films or episodes. Wikidata
// makes a film series a kind of media franchise, but a series is not one:
// a studio's catalogue («DreamWorks Animation feature films») and a
// trilogy are series, the franchise is what the series is part of. A
// series (or a subclass of one) is passed through, never an anchor.
var seriesClasses = map[string]bool{
	"Q24856":    true, // film series
	"Q13593818": true, // film trilogy
	"Q56884562": true, // animated film series
	"Q5398426":  true, // television series
	"Q7725310":  true, // series of creative works
}

// anchorDepth: how many links up (P8345, P1434, P179, P361) a title's
// anchors are looked for. The Lord of the Rings films reach their
// franchise through their film series (film → trilogy → Middle-earth).
const anchorDepth = 3

// factsBatch: TMDB ids looked up in one search (and one wbgetentities).
const factsBatch = 50

// Ref is an item with its label (the language asked, else English).
type Ref struct {
	QID     string `json:"q"`
	Label   string `json:"l,omitempty"`
	Ordinal string `json:"o,omitempty"` // a series' ordinal of the title (P1545), "" = none
	Rank    int    `json:"r,omitempty"` // an anchor's: RankFranchise…
}

// TitleFacts are what Wikidata says of a film's or a series' franchise:
// the franchise (P8345), the fictional universe (P1434), the series it is
// part of with its ordinal (P179/P1545), and the items it follows and is
// followed by (P155/P156, QIDs). Anchors are the franchise's own items
// the title is tied to (see anchors): titles that share one are of one
// franchise.
type TitleFacts struct {
	Version    int      `json:"v,omitempty"` // FactsVersion
	QID        string   `json:"q"`
	Anchors    []Ref    `json:"a,omitempty"`
	Franchise  []Ref    `json:"f,omitempty"`
	Universe   []Ref    `json:"u,omitempty"`
	Series     []Ref    `json:"s,omitempty"`
	Follows    []string `json:"p,omitempty"`
	FollowedBy []string `json:"n,omitempty"`
}

// TitleFacts finds the items of TMDB ids (kind "movie" or "tv") and reads
// their franchise facts, labels in lang ("ru", "en"…). An id no item has is
// not in the map. Two requests per 50 ids, and one for the labels.
func (c *Client) TitleFacts(ctx context.Context, kind string, tmdbIDs []int, lang string) (map[int]TitleFacts, error) {
	prop := propTMDBMovie
	switch kind {
	case "movie":
	case "tv":
		prop = propTMDBTV
	default:
		return nil, fmt.Errorf("wikidata: facts of kind %q", kind)
	}
	out := map[int]TitleFacts{}
	labels := map[string]string{}
	for len(tmdbIDs) > 0 {
		n := min(len(tmdbIDs), factsBatch)
		part := tmdbIDs[:n]
		tmdbIDs = tmdbIDs[n:]
		want := map[string]int{}
		terms := make([]string, 0, len(part))
		for _, id := range part {
			want[strconv.Itoa(id)] = id
			terms = append(terms, prop+"="+strconv.Itoa(id))
		}
		var found struct {
			Query struct {
				Search []struct {
					Title string `json:"title"`
				} `json:"search"`
			} `json:"query"`
		}
		q := url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {"haswbstatement:" + strings.Join(terms, "|")},
			"srprop": {""}, "srlimit": {strconv.Itoa(factsBatch)}}
		if err := c.get(ctx, q, &found); err != nil {
			return nil, err
		}
		var qids []string
		for _, s := range found.Query.Search {
			if strings.HasPrefix(s.Title, "Q") {
				qids = append(qids, s.Title)
			}
		}
		if len(qids) == 0 {
			continue
		}
		ents, err := c.entities(ctx, qids, "claims")
		if err != nil {
			return nil, err
		}
		anchors, err := c.anchors(ctx, qids, ents)
		if err != nil {
			return nil, err
		}
		for _, qid := range qids {
			e, ok := ents[qid]
			if !ok {
				continue
			}
			for _, c := range e.Claims[prop] {
				s, _ := c.Mainsnak.DataValue.Value.(string)
				id, ok := want[s]
				if !ok {
					continue
				}
				if _, dup := out[id]; dup {
					continue // the first item found stands for the id
				}
				f := TitleFacts{Version: FactsVersion, QID: qid, Anchors: anchors[qid], Follows: e.liveItems(propFollows), FollowedBy: e.liveItems(propFollowedBy)}
				for _, x := range e.liveItems(propFranchise) {
					f.Franchise = append(f.Franchise, Ref{QID: x})
				}
				for _, x := range e.liveItems(propUniverse) {
					f.Universe = append(f.Universe, Ref{QID: x})
				}
				for _, cl := range e.Claims[propSeries] {
					if cl.Rank == "deprecated" {
						continue
					}
					if m, ok := cl.Mainsnak.DataValue.Value.(map[string]any); ok {
						if x, _ := m["id"].(string); x != "" {
							r := Ref{QID: x}
							for _, o := range cl.Qualifiers[propOrdinal] {
								if s, ok := o.DataValue.Value.(string); ok {
									r.Ordinal = s
									break
								}
							}
							f.Series = append(f.Series, r)
						}
					}
				}
				for _, rs := range [][]Ref{f.Anchors, f.Franchise, f.Universe, f.Series} {
					for _, r := range rs {
						labels[r.QID] = ""
					}
				}
				out[id] = f
			}
		}
	}
	if len(labels) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(labels))
	for id := range labels {
		ids = append(ids, id)
	}
	slices.Sort(ids) // one request for one set of ids (recorded fixtures)
	ents, err := c.entities(ctx, ids, "labels")
	if err != nil {
		return out, nil // facts without their labels still tell titles apart
	}
	name := func(r *Ref) {
		e := ents[r.QID]
		if r.Label = e.label(lang); r.Label == "" {
			r.Label = e.label("en")
		}
	}
	for id, f := range out {
		for _, rs := range [][]Ref{f.Anchors, f.Franchise, f.Universe, f.Series} {
			for i := range rs {
				name(&rs[i])
			}
		}
		out[id] = f
	}
	return out, nil
}

// liveItems are the item ids of p, deprecated statements left out.
func (e entity) liveItems(p string) []string {
	var out []string
	for _, c := range e.Claims[p] {
		if c.Rank == "deprecated" {
			continue
		}
		if m, ok := c.Mainsnak.DataValue.Value.(map[string]any); ok {
			if id, _ := m["id"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// up are the items a title or a series is part of: its franchise,
// universe, series and what it is part of (P8345, P1434, P179, P361).
func (e entity) up() []string {
	var out []string
	for _, p := range []string{propFranchise, propUniverse, propSeries, propPartOf} {
		for _, x := range e.liveItems(p) {
			if !slices.Contains(out, x) {
				out = append(out, x)
			}
		}
	}
	return out
}

// anchors are the franchise items of each title (qids, their entities in
// titles): the items of an anchor class nearest to it going up its
// franchise, universe, series and part-of links, at most anchorDepth links
// away. The nearest ones only: Shrek's universe is part of a «DreamWorks
// universe» that Madagascar's is part of too, and the two are not one
// franchise; a list the title is on (National Film Registry, a critics'
// top 100) or a studio's catalogue (a film series of no franchise) is
// passed through, never an anchor. P155/P156 (follows) are not followed:
// Planet Earth II follows Planet Earth, a documentary is no franchise.
func (c *Client) anchors(ctx context.Context, qids []string, titles map[string]entity) (map[string][]Ref, error) {
	nodes := map[string]entity{}
	rank := map[string]int{} // a class → its anchor rank, -1: none
	for k, v := range anchorClasses {
		rank[k] = v
	}
	for k := range seriesClasses {
		rank[k] = -1
	}
	// fetch reads the items not read yet, and the classes of theirs not
	// known yet with their superclasses
	fetch := func(ids []string) error {
		var need []string
		for _, id := range ids {
			if _, ok := nodes[id]; !ok && !slices.Contains(need, id) {
				need = append(need, id)
			}
		}
		if len(need) == 0 {
			return nil
		}
		slices.Sort(need)
		got, err := c.entities(ctx, need, "claims")
		if err != nil {
			return err
		}
		var classes []string
		for _, id := range need {
			nodes[id] = got[id] // a missing item stays an empty one
			for _, k := range got[id].liveItems(propInstance) {
				if _, known := rank[k]; !known && !slices.Contains(classes, k) {
					classes = append(classes, k)
				}
			}
		}
		if len(classes) == 0 {
			return nil
		}
		slices.Sort(classes)
		cls, err := c.entities(ctx, classes, "claims")
		if err != nil {
			return err
		}
		for _, k := range classes {
			rank[k] = -1
			sups := cls[k].liveItems(propSubclass)
			if slices.ContainsFunc(sups, func(s string) bool { return seriesClasses[s] }) {
				continue
			}
			for _, sup := range sups {
				if r, ok := anchorClasses[sup]; ok && (rank[k] < 0 || r < rank[k]) {
					rank[k] = r
				}
			}
		}
		return nil
	}
	rankOf := func(id string) int {
		best := -1
		for _, k := range nodes[id].liveItems(propInstance) {
			if seriesClasses[k] {
				return -1 // a series that is a «brand» too is still a series
			}
			if r := rank[k]; r >= 0 && (best < 0 || r < best) {
				best = r
			}
		}
		return best
	}
	out := map[string][]Ref{}
	frontier := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, q := range qids {
		frontier[q] = titles[q].up()
		seen[q] = map[string]bool{q: true}
	}
	for depth := 0; depth < anchorDepth; depth++ {
		var want []string
		for _, q := range qids {
			want = append(want, frontier[q]...)
		}
		if len(want) == 0 {
			break
		}
		if err := fetch(want); err != nil {
			return nil, err
		}
		for _, q := range qids {
			var found []Ref
			var next []string
			for _, id := range frontier[q] {
				if seen[q][id] {
					continue
				}
				seen[q][id] = true
				if r := rankOf(id); r >= 0 {
					found = append(found, Ref{QID: id, Rank: r})
					continue
				}
				next = append(next, nodes[id].up()...)
			}
			if len(found) > 0 {
				slices.SortFunc(found, func(a, b Ref) int { return cmp.Or(cmp.Compare(a.Rank, b.Rank), cmp.Compare(a.QID, b.QID)) })
				out[q] = found
				next = nil // the nearest anchors only
			}
			frontier[q] = next
		}
	}
	return out, nil
}
