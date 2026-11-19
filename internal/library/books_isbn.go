package library

import (
	"context"
	"encoding/json"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
	"github.com/antresol-app/antresol/internal/release"
)

// BookRef names a book or audiobook of the library.
type BookRef struct {
	ID   int64
	Kind string
}

// BooksByISBN indexes the visible books and audiobooks by the ISBN-13 of
// their scanned ISBN (an ISBN-10 or a hyphenated one reads as the same
// number). One query, whatever the library's size: for matching a whole
// reading list at once.
func (r *Repo) BooksByISBN(ctx context.Context) (map[string][]BookRef, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, kind, json_extract(meta_json,'$.isbn') FROM items
	  WHERE kind IN ('book','audiobook') AND missing_since IS NULL AND json_extract(meta_json,'$.isbn') IS NOT NULL
	  ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]BookRef{}
	for rows.Next() {
		var ref BookRef
		var isbn string
		if err := rows.Scan(&ref.ID, &ref.Kind, &isbn); err != nil {
			return nil, err
		}
		if k := bookmeta.ISBN13(isbn); k != "" {
			out[k] = append(out[k], ref)
		}
	}
	return out, rows.Err()
}

// FindBooks lists the visible books and audiobooks called title (their own
// title, not a series or an author named alike) whose author shares a name
// word of 3+ letters with author, in either script, or that have none
// ("Михаил Булгаков" = "Булгаков М." = "Mikhail Bulgakov"). Strict on
// purpose: the caller marks what it finds as read.
func (r *Repo) FindBooks(ctx context.Context, author, title string) ([]BookRef, error) {
	books, err := r.BooksTitled(ctx, title)
	if err != nil {
		return nil, err
	}
	var out []BookRef
	for _, b := range books {
		if len(b.Authors) == 0 || len(nameKeys([]string{author})) == 0 || SameAuthor([]string{author}, b.Authors) {
			out = append(out, b.BookRef)
		}
	}
	return out, nil
}

// TitledBook is a book or audiobook with its authors as the library has them.
type TitledBook struct {
	BookRef
	Authors []string
}

// BooksTitled lists the visible books and audiobooks whose own title (or an
// alias of it) is title once normalised; a series or an author named alike
// does not count.
func (r *Repo) BooksTitled(ctx context.Context, title string) ([]TitledBook, error) {
	t := release.Normalize(title)
	if t == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT i.id, i.kind, i.title, COALESCE(json_extract(i.meta_json,'$.authors'),'[]') FROM items i
	  WHERE i.kind IN ('book','audiobook') AND i.missing_since IS NULL
	    AND i.id IN (SELECT item_id FROM item_titles WHERE norm=?)
	  ORDER BY i.id LIMIT 50`, t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TitledBook
	for rows.Next() {
		var b TitledBook
		var own, raw string
		if err := rows.Scan(&b.ID, &b.Kind, &own, &raw); err != nil {
			return nil, err
		}
		if release.Normalize(own) != t {
			continue // found by its series or its author's name
		}
		json.Unmarshal([]byte(raw), &b.Authors) // not a list: no author known
		out = append(out, b)
	}
	return out, rows.Err()
}

// BooksByWork indexes the visible books and audiobooks by the Open Library
// work the enricher matched them to (meta ol_key, "/works/OL676009W"): every
// edition and translation of a work is one work there.
func (r *Repo) BooksByWork(ctx context.Context) (map[string][]BookRef, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, kind, json_extract(meta_json,'$.ol_key') FROM items
	  WHERE kind IN ('book','audiobook') AND missing_since IS NULL AND json_extract(meta_json,'$.ol_key') IS NOT NULL
	  ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]BookRef{}
	for rows.Next() {
		var ref BookRef
		var key string
		if err := rows.Scan(&ref.ID, &ref.Kind, &key); err != nil {
			return nil, err
		}
		if key != "" {
			out[key] = append(out[key], ref)
		}
	}
	return out, rows.Err()
}
