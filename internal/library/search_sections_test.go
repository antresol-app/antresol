package library

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/antresol-app/antresol/internal/release"
)

// searchBefore is searchWith as it was before the sections shared one
// pass: one kind list at a time. Its answer is the reference.
func (r *Repo) searchBefore(ctx context.Context, text string, kinds []string, limit int, joins, rate string, rateArgs []any) ([]Item, error) {
	norm := release.Normalize(text)
	if norm == "" || len(kinds) == 0 {
		return nil, nil
	}
	kindIn := "i.kind IN (?" + strings.Repeat(",?", len(kinds)-1) + ")"
	kindArgs := make([]any, len(kinds))
	for i, k := range kinds {
		kindArgs[i] = k
	}
	if limit <= 0 {
		limit = 50
	}
	args := append([]any{}, kindArgs...)
	args = append(args, "%"+norm+"%")
	args = append(args, rateArgs...)
	args = append(args, norm, norm+"%", limit)
	out, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items i JOIN item_titles t ON t.item_id=i.id`+joins+`
	  WHERE `+kindIn+` AND i.missing_since IS NULL AND t.norm LIKE ? AND `+rate+`
	  GROUP BY i.id ORDER BY MAX(t.norm=?) DESC, MAX(t.norm LIKE ?) DESC, i.sort_title, i.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	if len(out) >= limit {
		return out, nil
	}
	seen := map[int64]bool{}
	for _, it := range out {
		seen[it.ID] = true
	}
	var toks []string
	for _, f := range strings.FieldsFunc(norm, func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) }) {
		toks = append(toks, `"`+f+`"*`)
	}
	fargs := []any{strings.Join(toks, " ")}
	fargs = append(fargs, kindArgs...)
	fargs = append(fargs, rateArgs...)
	fargs = append(fargs, limit)
	ftsRows, err := r.queryItems(ctx, `SELECT `+itemCols+` FROM items_fts f JOIN items i ON i.id=f.rowid`+joins+`
	  WHERE items_fts MATCH ? AND `+kindIn+` AND i.missing_since IS NULL AND `+rate+` ORDER BY f.rank LIMIT ?`, fargs...)
	if err != nil {
		return nil, err
	}
	for _, it := range ftsRows {
		if !seen[it.ID] && len(out) < limit {
			out = append(out, it)
		}
	}
	return out, nil
}

// One pass for all sections answers each as its own search did: exact
// titles, then prefixes, then by sort title (ties on it by id), aliases,
// the full-text fill of a short section, ratings, missing items.
func TestSearchSectionsSameAsBefore(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	kinds := []string{KindMovie, KindSeries, KindArtist, KindAlbum, KindBook, KindAudiobook, KindComic, KindTrack}
	ratings := []string{"", "6+", "12+", "18+"}
	words := []string{"Филин", "филин", "Филиппины", "Ночной филин", "Профиль", "Фил", "Сова", "Ёж и филин", "Кинофильм"}
	for i := range 160 {
		kind := kinds[i%len(kinds)]
		title := words[i%len(words)]
		if i%5 == 0 {
			title += fmt.Sprintf(" %d", i%4) // sort-title ties across items
		}
		var parent any
		var missing any
		if i%23 == 0 {
			missing = "2026-01-01T00:00:00Z"
		}
		res, err := r.db.Exec(`INSERT INTO items(kind,parent_id,title,sort_title,overview,rating,added_at,updated_at,missing_since)
			VALUES(?,?,?,?,?,?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',?)`,
			kind, parent, title, strings.ToLower(title), []string{"", "про филина и сову", "филиал"}[i%3], ratings[i%len(ratings)], missing)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		titles := []string{title}
		if i%7 == 0 {
			titles = append(titles, "Owl "+title, "филин") // an alias that is exact
		}
		if err := r.AddTitles(ctx, id, titles); err != nil {
			t.Fatal(err)
		}
	}
	secs := []SearchSection{{[]string{KindMovie, KindSeries}, 50}, {[]string{KindArtist}, 4}, {[]string{KindAlbum}, 3},
		{[]string{KindBook}, 20}, {[]string{KindAudiobook}, 2}, {[]string{KindComic}, 20}, {[]string{KindTrack}, 10}}
	for _, q := range []string{"фил", "филин", "Филин", "ФИЛ", "сов", "филиал", "про", "owl", "zzz", "", "ё"} {
		for _, rating := range []string{"", "12+"} {
			got, err := r.SearchSections(ctx, q, secs, rating)
			if err != nil {
				t.Fatal(err)
			}
			rate, rateArgs := "1", []any(nil)
			if rating != "" {
				rate, rateArgs = allowedRatings(rating)
			}
			for s, sec := range secs {
				want, err := r.searchBefore(ctx, q, sec.Kinds, sec.Limit, "", rate, rateArgs)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got[s], want) {
					t.Fatalf("%q %q section %v:\n got %v\nwant %v", q, rating, sec.Kinds, titles(got[s]), titles(want))
				}
			}
			// the music search (Subsonic): its own joins and rating
			mrate, margs := MusicRootAllowed(musicRootExpr, rating)
			for _, k := range []string{KindArtist, KindAlbum, KindTrack} {
				gotM, err := r.searchWith(ctx, q, []string{k}, 7, musicRootJoins, mrate, margs)
				if err != nil {
					t.Fatal(err)
				}
				wantM, err := r.searchBefore(ctx, q, []string{k}, 7, musicRootJoins, mrate, margs)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotM, wantM) {
					t.Fatalf("music %q %q %s:\n got %v\nwant %v", q, rating, k, titles(gotM), titles(wantM))
				}
			}
		}
	}
}
