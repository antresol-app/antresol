package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/iptv"
)

// Categories of the viewer's channel list (GET /api/channels,
// /api/channels/epg): each channel carries the ids of its categories, the
// list carries every category once with its name and how many channels it
// has, so the web and the apps filter a big list by one of them.
//
//	ai:<code>    a topic of iptv.CategoryList, localized: what the AI helper
//	             answered for the channel, and what its playlist groups mean
//	             (iptv.GroupCodes: «RU | Кино HD», «Фильмы» and «Movies» are
//	             all «Кино»), and an own generated channel's kind
//	ai:adult     the adult channels: exclusive (an adult channel has no
//	             other category, and is in neither «Все» nor «Избранное»),
//	             always last; a profile with an age limit has none
//	source:<id>  the external source it comes from (only when there are two or more)
//	group:<name> a group of none of the topics (iptv.CleanGroup's spelling),
//	             only for a channel without a topic
//
// Topics and groups are one set of tags: the MaxTags most common are the
// categories, the rest are dropped from the channels (they stay in «Все»).
// The order: the tags by count (then name, as a person reads it: «Группа 2»
// before «Группа 10», i18n.Collator), the sources by their position, then
// «Для взрослых».

// MaxTags: the most topic and group categories a list has.
const MaxTags = 15

type categoryJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // ai | source | group
	Count int    `json:"count"`
	// Exclusive: its channels are in no other category, nor in «Все» and
	// «Избранное» (the adult one).
	Exclusive bool `json:"exclusive,omitempty"`
}

var adultCat = aiCat(iptv.AdultCode)

func groupCat(g string) string  { return "group:" + g }
func sourceCat(id int64) string { return "source:" + strconv.FormatInt(id, 10) }
func aiCat(code string) string  { return "ai:" + code }

// ownKinds: the topic of a generated channel by its key's kind.
var ownKinds = map[string][]string{
	"movies": {"movies"}, "genre": {"movies"}, "decade": {"movies"},
	"series": {"series"}, "toons": {"kids", "series"},
	"kids": {"kids"}, "docs": {"documentary"},
}

// ownCats: the category ids of an own channel (category: its group in the
// viewer's language): a generated one's topic by its kind, else what its
// group means, else its group.
func ownCats(c channels.Channel, category string) []string {
	if c.AutoKey != "" {
		k, _, _ := strings.Cut(c.AutoKey, ":")
		if codes, ok := ownKinds[k]; ok {
			return codeCats(codes)
		}
	}
	return groupCats([]string{category})
}

func codeCats(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if c != iptv.AdultCode && !slices.Contains(out, aiCat(c)) {
			out = append(out, aiCat(c))
		}
	}
	return out
}

// groupCats: the topics groups mean; none: their cleaned spellings.
func groupCats(groups []string) []string {
	var out, own []string
	for _, g := range groups {
		for _, id := range codeCats(iptv.GroupCodes(g)) {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
		if cg := iptv.CleanGroup(g); cg != "" && !slices.Contains(own, groupCat(cg)) {
			own = append(own, groupCat(cg))
		}
	}
	if len(out) > 0 {
		return out
	}
	return own
}

// extAdult: an external channel is an adult one (its name or a group says
// so, or the AI helper does).
func extAdult(x iptv.ExtChannel) bool {
	if slices.Contains(x.AI.Cats, iptv.AdultCode) || iptv.IsAdult(x.Channel.Name, "") {
		return true
	}
	for _, g := range x.Groups {
		if iptv.IsAdult("", g) {
			return true
		}
	}
	return false
}

// extCats: the category ids of an external channel (sources: the
// server's sources count, a source category only when there are two).
func extCats(x iptv.ExtChannel, sources int) []string {
	if extAdult(x) {
		return []string{adultCat}
	}
	out := codeCats(x.AI.Cats)
	for _, id := range groupCats(x.Groups) {
		if strings.HasPrefix(id, "group:") && len(out) > 0 {
			continue // a channel with a topic needs no group of its own
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if sources > 1 {
		for _, id := range x.SourceIDs {
			out = append(out, sourceCat(id))
		}
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

func isTag(id string) bool {
	return id != adultCat && (strings.HasPrefix(id, "ai:") || strings.HasPrefix(id, "group:"))
}

// categoriesOf counts the categories of a list's channels (by their ids),
// keeps the MaxTags most common tags and drops the others from lists (in
// place).
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
		case id == adultCat:
			c.Kind, c.Name, c.Exclusive = "ai", i18n.Localize(ctx, iptv.CategoryName(iptv.AdultCode)), true
		case strings.HasPrefix(id, "ai:"):
			c.Kind, c.Name = "ai", i18n.Localize(ctx, iptv.CategoryName(id[3:]))
		case strings.HasPrefix(id, "source:"):
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
	rank := func(c categoryJSON) int {
		switch {
		case c.Exclusive:
			return 2
		case c.Kind == "source":
			return 1
		}
		return 0
	}
	coll := i18n.Collator(ctx)
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if rx, ry := rank(x), rank(y); rx != ry {
			return rx < ry
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
	// the most common tags only
	kept := map[string]bool{}
	n := 0
	out = slices.DeleteFunc(out, func(c categoryJSON) bool {
		if !isTag(c.ID) {
			kept[c.ID] = true
			return false
		}
		if n == MaxTags {
			return true
		}
		n++
		kept[c.ID] = true
		return false
	})
	for i, l := range lists {
		lists[i] = slices.DeleteFunc(l, func(id string) bool { return !kept[id] })
	}
	return slices.Clip(out)
}
