package library

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Sort "name" is the order the Jellyfin list had when it loaded every film
// and series in title order, then sorted them by name in memory (stable, by
// sort_title or the lower-cased title): films before series on a tie, an
// item with no sort_title by its title. The sort_title index serves the
// plain order; a library with an empty sort_title takes the expression.
func TestListSortName(t *testing.T) {
	for _, withEmpty := range []bool{false, true} {
		t.Run(fmt.Sprint("empty sort_title: ", withEmpty), func(t *testing.T) {
			r := newRepo(t)
			ctx := context.Background()
			movie := func(title string) {
				t.Helper()
				if _, err := r.UpsertMovie(ctx, MovieInput{Title: title, File: FileInput{Path: "/m/" + title + ".mkv"}}); err != nil {
					t.Fatal(err)
				}
			}
			series := func(title string) {
				t.Helper()
				if _, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: title, Season: 1, Episode: 1, File: FileInput{Path: "/tv/" + title + ".mkv"}}); err != nil {
					t.Fatal(err)
				}
			}
			series("Dune") // the series is older than the film of its name
			movie("Dune")
			movie("Альфа")
			series("Альфа")
			movie("Яблоко")
			series("Берег")
			movie("Zeta")
			if withEmpty {
				movie("…") // no letters or digits: sort_title is ""
				series("!?")
				movie("Ωmega")
			}
			var want []string
			for _, kind := range []string{KindMovie, KindSeries} { // what the old path collected
				items, _, err := r.List(ctx, ListQuery{Kind: kind, Sort: "title"})
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range items {
					want = append(want, it.Kind+":"+it.Title)
				}
			}
			all, _, _ := r.List(ctx, ListQuery{Sort: "title"})
			key := map[string]string{}
			for _, it := range all {
				k := it.SortTitle
				if k == "" {
					k = strings.ToLower(it.Title)
				}
				key[it.Kind+":"+it.Title] = k
			}
			sort.SliceStable(want, func(i, j int) bool { return key[want[i]] < key[want[j]] })

			for _, c := range []struct {
				kind          string
				limit, offset int
			}{{"", 0, 0}, {"", 4, 0}, {"", 4, 3}, {"", 100, 2}, {KindMovie, 0, 0}, {KindSeries, 2, 1}} {
				got, total, err := r.List(ctx, ListQuery{Kind: c.kind, Sort: "name", Limit: c.limit, Offset: c.offset})
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, it := range got {
					ids = append(ids, it.Kind+":"+it.Title)
				}
				exp := slices.Clone(want)
				if c.kind != "" {
					exp = slices.DeleteFunc(exp, func(s string) bool { return !strings.HasPrefix(s, c.kind+":") })
				}
				if total != len(exp) {
					t.Errorf("%+v: total %d, want %d", c, total, len(exp))
				}
				exp = exp[min(c.offset, len(exp)):]
				if c.limit > 0 && c.limit < len(exp) {
					exp = exp[:c.limit]
				}
				if !slices.Equal(ids, exp) {
					t.Errorf("%+v:\n got %v\nwant %v", c, ids, exp)
				}
			}
		})
	}
}
