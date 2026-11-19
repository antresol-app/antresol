package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/iptv"
)

// Categories of the viewer's channel list (GET /api/channels,
// /api/channels/epg): each channel carries the ids of its categories, the
// list carries every category once with its name and how many channels it
// has, so the web and the apps filter a big list by one of them.
//
//	ai:<code>    the AI helper's category (iptv.CategoryList), localized
//	source:<id>  the external source it comes from (only when there are two or more)
//	group:<name> the playlist's group-title, or an own channel's category
//
// The list's order: the AI's categories (by count), the sources (by their
// position), the groups (by count, then name). Names compare as a person
// reads them (i18n.Collator: «Группа 2» before «Группа 10»), so the web
// and the apps show one order.

type categoryJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // ai | source | group
	Count int    `json:"count"`
}

func groupCat(g string) string  { return "group:" + g }
func sourceCat(id int64) string { return "source:" + strconv.FormatInt(id, 10) }
func aiCat(code string) string  { return "ai:" + code }
func ownCats(category string) []string {
	if category == "" {
		return nil
	}
	return []string{groupCat(category)}
}

// extCats: the category ids of an external channel (sources: the
// server's sources count, a source category only when there are two).
func extCats(x iptv.ExtChannel, sources int) []string {
	var out []string
	for _, c := range x.AI.Cats {
		out = append(out, aiCat(c))
	}
	if sources > 1 {
		for _, id := range x.SourceIDs {
			out = append(out, sourceCat(id))
		}
	}
	for _, g := range x.Groups {
		out = append(out, groupCat(g))
	}
	return out
}

// sourceNames: the external sources by id, and their positions.
func (a *channelsAPI) sourceNames(ctx context.Context) (map[int64]string, map[int64]int) {
	names, pos := map[int64]string{}, map[int64]int{}
	if a.ext == nil {
		return names, pos
	}
	srcs, err := a.ext.Store.Sources(ctx)
	if err != nil {
		return names, pos
	}
	for i, s := range srcs {
		names[s.ID], pos[s.ID] = s.Name, i
	}
	return names, pos
}

// categoriesOf counts the categories of a list's channels (by their ids).
func (a *channelsAPI) categoriesOf(r *http.Request, lists [][]string, names map[int64]string, pos map[int64]int) []categoryJSON {
	ctx := r.Context()
	count := map[string]int{}
	var ids []string
	for _, l := range lists {
		for _, id := range l {
			if count[id] == 0 {
				ids = append(ids, id)
			}
			count[id]++
		}
	}
	out := make([]categoryJSON, 0, len(ids))
	for _, id := range ids {
		c := categoryJSON{ID: id, Count: count[id]}
		switch {
		case len(id) > 3 && id[:3] == "ai:":
			c.Kind, c.Name = "ai", i18n.Localize(ctx, iptv.CategoryName(id[3:]))
		case len(id) > 7 && id[:7] == "source:":
			n, _ := strconv.ParseInt(id[7:], 10, 64)
			c.Kind, c.Name = "source", names[n]
		default:
			c.Kind, c.Name = "group", id[len("group:"):]
		}
		if c.Name == "" {
			continue
		}
		out = append(out, c)
	}
	rank := map[string]int{"ai": 0, "source": 1, "group": 2}
	coll := i18n.Collator(ctx)
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Kind != y.Kind {
			return rank[x.Kind] < rank[y.Kind]
		}
		if x.Kind == "source" {
			a, _ := strconv.ParseInt(x.ID[7:], 10, 64)
			b, _ := strconv.ParseInt(y.ID[7:], 10, 64)
			return pos[a] < pos[b]
		}
		if x.Count != y.Count {
			return x.Count > y.Count
		}
		if c := coll.CompareString(x.Name, y.Name); c != 0 {
			return c < 0
		}
		return x.Name < y.Name
	})
	return slices.Clip(out)
}
