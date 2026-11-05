package library

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A page of artists gets the same album counts and covers in two queries
// as artist by artist: the newest album's cover (year, then added, then
// id), hidden albums and albums without one left out.
func TestArtistRowsMatchOneByOne(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	for _, q := range []string{
		`INSERT INTO items (id, kind, title) VALUES (1, 'artist', 'А'), (2, 'artist', 'Б'), (3, 'artist', 'В'), (4, 'artist', 'Г')`,
		`INSERT INTO items (id, kind, parent_id, title, year, poster, added_at) VALUES
		  (10, 'album', 1, 'a', 1990, 'aa.jpg', '2026-01-01T00:00:00Z'),
		  (11, 'album', 1, 'b', 2001, 'bb.jpg', '2026-01-01T00:00:00Z'),
		  (12, 'album', 1, 'c', 2001, 'cc.jpg', '2026-02-01T00:00:00Z'),
		  (13, 'album', 1, 'd', 2005, NULL, '2026-03-01T00:00:00Z'),
		  (20, 'album', 2, 'e', 2000, '', '2026-01-01T00:00:00Z'),
		  (30, 'album', 3, 'f', 2000, 'ff.jpg', '2026-01-01T00:00:00Z'),
		  (31, 'album', 3, 'g', 2000, 'gg.jpg', '2026-01-01T00:00:00Z'),
		  (32, 'album', 3, 'h', 2010, 'hh.jpg', '2026-01-01T00:00:00Z')`,
		`UPDATE items SET missing_since='2026-01-01' WHERE id=32`,
	} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var items []Item
	for _, id := range []int64{1, 2, 3, 4} {
		it, err := r.Item(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, it)
	}
	rows, err := r.artistRows(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range items {
		one, err := r.artistRow(ctx, it)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(one) != fmt.Sprint(rows[i]) {
			t.Fatalf("artist %d: %+v, one by one %+v", it.ID, rows[i], one)
		}
	}
	if rows[0].AlbumCount != 4 || rows[2].AlbumCount != 2 || rows[3].AlbumCount != 0 {
		t.Fatalf("counts %d %d %d", rows[0].AlbumCount, rows[2].AlbumCount, rows[3].AlbumCount)
	}
}

// A page of songs (or of artists, or titles by name) is read in title
// order from an index and stops at the page: no sort of every track.
func TestTitleOrderFromIndex(t *testing.T) {
	r := newRepo(t)
	for _, q := range []string{
		`SELECT i.id FROM items i JOIN tracks t ON t.item_id=i.id JOIN files f ON f.id=t.file_id LEFT JOIN items al ON al.id=i.parent_id
		  LEFT JOIN items ar ON ar.id=al.parent_id WHERE i.kind='track' AND i.missing_since IS NULL ORDER BY i.sort_title, i.id LIMIT 200`,
		`SELECT i.id FROM items i WHERE i.kind='artist' AND i.missing_since IS NULL ORDER BY i.sort_title, i.id LIMIT 120`,
	} {
		rows, err := r.db.Query(`EXPLAIN QUERY PLAN ` + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			rows.Scan(&id, &parent, &notused, &detail)
			plan = append(plan, detail)
		}
		rows.Close()
		if p := fmt.Sprint(plan); !strings.Contains(p, "items_kind_sort") || strings.Contains(p, "TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("plan %s", p)
		}
	}
}

// FilesOfItems and ItemsByID answer as FilesOf and Item one by one would:
// shown files by path, shown items; hidden ones left out.
func TestBatchLookupsMatchOneByOne(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := r.UpsertMovie(ctx, MovieInput{Title: fmt.Sprint("Фильм ", i), Year: 2000 + i,
			Files: []FileInput{{Path: fmt.Sprintf("/m/%d/b.mkv", i)}, {Path: fmt.Sprintf("/m/%d/a.mkv", i)}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	ep, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сериал", Season: 1, Episode: 1, File: FileInput{Path: "/s/e1.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, ep)
	// a hidden item and a hidden file
	if _, err := r.db.Exec(`UPDATE items SET missing_since='2026-01-01' WHERE id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE files SET missing_since='2026-01-01' WHERE path='/m/2/a.mkv'`); err != nil {
		t.Fatal(err)
	}
	ask := append([]int64{999999}, ids...)
	files, err := r.FilesOfItems(ctx, ask)
	if err != nil {
		t.Fatal(err)
	}
	items, err := r.ItemsByID(ctx, ask)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ask {
		one, err := r.FilesOf(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(one) != fmt.Sprint(files[id]) {
			t.Fatalf("item %d: files %v, one by one %v", id, files[id], one)
		}
		it, err := r.Item(ctx, id)
		got, ok := items[id]
		if (err == nil) != ok || err == nil && fmt.Sprint(it) != fmt.Sprint(got) {
			t.Fatalf("item %d: %v %v, one by one %v %v", id, got, ok, it, err)
		}
	}
	if len(items) != 3 { // the hidden movie and the unknown id are left out
		t.Fatalf("items %d", len(items))
	}
}
