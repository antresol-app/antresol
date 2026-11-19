package library

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

// The typo-tolerant pass of a search: when titles and the full text find
// nothing at all, each title (and alias) whose words are each within a few
// edits of a query word answers instead («Джентельмены» finds
// «Джентльмены», «Гари Потер» «Гарри Поттер», «Interstelar»
// «Interstellar»). Words are compared after release.Normalize (case, ё=е,
// punctuation); a query word may also be the start of a title word, so a
// half-typed one still finds it.

// fuzzyEdits is how many edits a query word of n letters may carry: none
// for short words (too many neighbours: «кот», «пот»), one up to seven
// letters, two from eight.
func fuzzyEdits(n int) int {
	switch {
	case n < 4:
		return 0
	case n < 8:
		return 1
	default:
		return 2
	}
}

// fuzzyScore is the edits a title needs to answer the query words (all of
// them must match some word of the title); ok is false when one does not.
// Both sides are normalized words (release.Normalize, split on spaces).
func fuzzyScore(query []string, title string) (score int, ok bool) {
	words := strings.Fields(title)
	if len(words) == 0 || len(query) == 0 {
		return 0, false
	}
	for _, q := range query {
		qr := []rune(q)
		limit := fuzzyEdits(len(qr))
		best := limit + 1
		for _, w := range words {
			if d := wordEdits(qr, []rune(w), limit); d < best {
				best = d
				if d == 0 {
					break
				}
			}
		}
		if best > limit {
			return 0, false
		}
		score += best
	}
	return score, true
}

// wordEdits is the edits between query word q and title word w, or a
// prefix of w about q's length (the word still being typed); above limit
// it is limit+1.
func wordEdits(q, w []rune, limit int) int {
	best := limit + 1
	if len(w) < len(q)-limit {
		return best
	}
	// the whole word, then its prefixes within limit of q's length
	for n := len(q) - limit; n <= len(q)+limit; n++ {
		if n < 1 || n > len(w) {
			continue
		}
		if n < len(w) && n < len(q) {
			continue // a prefix shorter than the query is not «still typing»
		}
		if d := osa(q, w[:n], best-1); d < best {
			best = d
		}
	}
	return best
}

// osa is the optimal-string-alignment distance (insertions, deletions,
// substitutions, swaps of neighbours) between a and b, cut short above
// max (it then answers max+1).
func osa(a, b []rune, max int) int {
	if d := len(a) - len(b); d > max || -d > max {
		return max + 1
	}
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		low := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
			low = min(low, v)
		}
		if low > max {
			return max + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	if prev[len(b)] > max {
		return max + 1
	}
	return prev[len(b)]
}

// fuzzyFragments are the LIKE patterns a matching title surely holds one
// of: the first or the last two letters of the longest query word (a
// typo rarely touches both ends). Nil when no word is long enough to
// carry a typo.
func fuzzyFragments(query []string) []any {
	long := ""
	for _, q := range query {
		if utf8.RuneCountInString(q) > utf8.RuneCountInString(long) {
			long = q
		}
	}
	r := []rune(long)
	if fuzzyEdits(len(r)) == 0 {
		return nil
	}
	return []any{"%" + string(r[:2]) + "%", "%" + string(r[len(r)-2:]) + "%"}
}

// searchFuzzy answers the sections by fuzzyScore over the titles of their
// kinds (the same conditions as searchSections): fewest edits first, then
// by sort title.
func (r *Repo) searchFuzzy(ctx context.Context, norm string, secs []SearchSection, limits []int, secOf map[string]int, kindIn string, kindArgs []any, joins, rate string, rateArgs []any) ([][]Item, error) {
	out := make([][]Item, len(secs))
	query := strings.Fields(norm)
	frags := fuzzyFragments(query)
	if frags == nil {
		return out, nil
	}
	args := append(append([]any{}, kindArgs...), frags...)
	args = append(args, rateArgs...)
	rows, err := r.db.QueryContext(ctx, `SELECT i.id, i.kind, i.sort_title, t.norm FROM items i JOIN item_titles t ON t.item_id=i.id`+joins+`
	  WHERE `+kindIn+` AND i.missing_since IS NULL AND (t.norm LIKE ? OR t.norm LIKE ?) AND `+rate, args...)
	if err != nil {
		return nil, err
	}
	type hit struct {
		id    int64
		kind  string
		sort  string
		score int
	}
	best := map[int64]*hit{}
	for rows.Next() {
		var h hit
		var title string
		if err := rows.Scan(&h.id, &h.kind, &h.sort, &title); err != nil {
			rows.Close()
			return nil, err
		}
		s, ok := fuzzyScore(query, title)
		if !ok {
			continue
		}
		if b := best[h.id]; b == nil || s < b.score {
			h.score = s
			best[h.id] = &h
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(best) == 0 {
		return out, nil
	}
	hits := make([]*hit, 0, len(best))
	for _, h := range best {
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if a.sort != b.sort {
			return a.sort < b.sort
		}
		return a.id < b.id
	})
	var ids []any
	for _, h := range hits {
		if s := secOf[h.kind]; len(out[s]) < limits[s] {
			out[s] = append(out[s], Item{ID: h.id}) // placeholder, filled below
			ids = append(ids, h.id)
		}
	}
	items, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i WHERE i.id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, ids...)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	for s := range out {
		filled := out[s][:0]
		for _, p := range out[s] {
			if it, ok := byID[p.ID]; ok {
				filled = append(filled, it)
			}
		}
		out[s] = filled
	}
	return out, nil
}
