package wikidata

import (
	"context"
	"fmt"
	"net/url"
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
)

// factsBatch: TMDB ids looked up in one search (and one wbgetentities).
const factsBatch = 50

// Ref is an item with its label (the language asked, else English).
type Ref struct {
	QID     string `json:"q"`
	Label   string `json:"l,omitempty"`
	Ordinal string `json:"o,omitempty"` // a series' ordinal of the title (P1545), "" = none
}

// TitleFacts are what Wikidata says of a film's or a series' franchise:
// the franchise (P8345), the fictional universe (P1434), the series it is
// part of with its ordinal (P179/P1545), and the items it follows and is
// followed by (P155/P156, QIDs).
type TitleFacts struct {
	QID        string   `json:"q"`
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
				f := TitleFacts{QID: qid, Follows: e.liveItems(propFollows), FollowedBy: e.liveItems(propFollowedBy)}
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
				for _, rs := range [][]Ref{f.Franchise, f.Universe, f.Series} {
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
		for _, rs := range [][]Ref{f.Franchise, f.Universe, f.Series} {
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
