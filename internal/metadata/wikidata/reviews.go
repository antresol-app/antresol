package wikidata

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Wikidata items and properties of review scores.
const (
	propIMDb        = "P345"       // IMDb ID
	propReviewScore = "P444"       // review score: "92%", "8.0/10"
	propScoreBy     = "P447"       // review score by
	propMethod      = "P459"       // determination method
	propPointInTime = "P585"       // when the score was read
	rottenTomatoes  = "Q105584"    // Rotten Tomatoes
	tomatometer     = "Q108403393" // Tomatometer score
	popcornmeter    = "Q131100566" // Popcornmeter (audience) score
)

// imdbItems is how many items of one IMDb id are looked at (a film and,
// rarely, a game or an edition share one).
const imdbItems = 3

// ReviewScores are a title's Rotten Tomatoes scores in percent (0 = none).
type ReviewScores struct {
	Tomatometer int // critics
	Audience    int // the Popcornmeter, formerly the audience score
}

var percent = regexp.MustCompile(`^\s*(\d{1,3})\s*%\s*$`)

// RottenTomatoes finds the item of an IMDb id ("tt1160419", P345) and reads
// its Rotten Tomatoes review scores (P444 qualified by P447 = Q105584):
// the latest statement of each kind, a preferred one first. A score without
// a determination method that reads "NN%" is the Tomatometer (most of the
// older statements). An id no item has, or an item without scores, gives
// zero scores and no error.
func (c *Client) RottenTomatoes(ctx context.Context, imdbID string) (ReviewScores, error) {
	var found struct {
		Query struct {
			Search []struct {
				Title string `json:"title"` // "Q60834962"
			} `json:"search"`
		} `json:"query"`
	}
	q := url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {"haswbstatement:" + propIMDb + "=" + imdbID},
		"srprop": {""}, "srlimit": {strconv.Itoa(imdbItems)}}
	if err := c.get(ctx, q, &found); err != nil {
		return ReviewScores{}, err
	}
	var ids []string
	for _, s := range found.Query.Search {
		if strings.HasPrefix(s.Title, "Q") {
			ids = append(ids, s.Title)
		}
	}
	if len(ids) == 0 {
		return ReviewScores{}, nil
	}
	ents, err := c.entities(ctx, ids, "claims")
	if err != nil {
		return ReviewScores{}, err
	}
	for _, id := range ids { // search order: the best match first
		e, ok := ents[id]
		if !ok || !e.hasString(propIMDb, imdbID) {
			continue
		}
		if s := e.rottenTomatoes(); s != (ReviewScores{}) {
			return s, nil
		}
	}
	return ReviewScores{}, nil
}

// hasString reports whether p has the string value v.
func (e entity) hasString(p, v string) bool {
	for _, c := range e.Claims[p] {
		if s, _ := c.Mainsnak.DataValue.Value.(string); s == v {
			return true
		}
	}
	return false
}

func (e entity) rottenTomatoes() ReviewScores {
	type pick struct {
		pct       int
		preferred bool
		at        string // "+2024-05-31T00:00:00Z": compares as text
	}
	var critics, audience pick
	better := func(cur *pick, p pick) {
		if cur.pct == 0 || p.preferred && !cur.preferred || p.preferred == cur.preferred && p.at > cur.at {
			*cur = p
		}
	}
	for _, c := range e.Claims[propReviewScore] {
		if c.Rank == "deprecated" || !qualifiedBy(c, propScoreBy, rottenTomatoes) {
			continue
		}
		s, _ := c.Mainsnak.DataValue.Value.(string)
		m := percent.FindStringSubmatch(s)
		if m == nil {
			continue // "7.6/10": the average rating
		}
		pct, _ := strconv.Atoi(m[1])
		if pct < 0 || pct > 100 {
			continue
		}
		p := pick{pct: pct, preferred: c.Rank == "preferred", at: qualifierTime(c, propPointInTime)}
		switch {
		case qualifiedBy(c, propMethod, popcornmeter):
			better(&audience, p)
		case qualifiedBy(c, propMethod, tomatometer) || len(c.Qualifiers[propMethod]) == 0:
			better(&critics, p)
		}
	}
	// a score of 0% is real but cannot be told from "none" here: shown as none
	return ReviewScores{Tomatometer: critics.pct, Audience: audience.pct}
}

// qualifiedBy reports whether a qualifier p of the claim is item id.
func qualifiedBy(c claim, p, id string) bool {
	for _, q := range c.Qualifiers[p] {
		if m, ok := q.DataValue.Value.(map[string]any); ok {
			if v, _ := m["id"].(string); v == id {
				return true
			}
		}
	}
	return false
}

func qualifierTime(c claim, p string) string {
	for _, q := range c.Qualifiers[p] {
		if m, ok := q.DataValue.Value.(map[string]any); ok {
			if t, _ := m["time"].(string); t != "" {
				return t
			}
		}
	}
	return ""
}
