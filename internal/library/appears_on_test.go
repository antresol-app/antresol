package library

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// appearsOnBefore is AppearsOn's query before it was driven from the
// tracks: its answer is the reference.
const appearsOnBefore = `SELECT ` + albumCols + ` FROM items i LEFT JOIN items ar ON ar.id=i.parent_id
  WHERE i.kind='album' AND i.missing_since IS NULL AND COALESCE(ar.title,'') <> ?1 COLLATE NOCASE
    AND EXISTS (SELECT 1 FROM items ti JOIN tracks t ON t.item_id=ti.id WHERE ti.parent_id=i.id AND ti.kind='track'
      AND ti.missing_since IS NULL AND t.artist = ?1 COLLATE NOCASE)
  ORDER BY i.year DESC, i.sort_title, i.id`

// AppearsOn reads the artist's tracks through an index and answers as the
// query over every album did: compilations, case-insensitive, not the
// artist's own albums, not missing tracks or albums.
func TestAppearsOnSameAsBefore(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	n := 0
	ins := func(kind string, parent int64, title string, year int, missing bool) int64 {
		t.Helper()
		var p, m any
		if parent != 0 {
			p = parent
		}
		if missing {
			m = "2026-01-01T00:00:00Z"
		}
		res, err := r.db.Exec(`INSERT INTO items(kind,parent_id,title,sort_title,year,added_at,updated_at,missing_since) VALUES(?,?,?,?,?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',?)`,
			kind, p, title, strings.ToLower(title), year, m)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	track := func(album int64, artist string, missing bool) {
		t.Helper()
		n++
		id := ins("track", album, "t", 0, missing)
		res, err := r.db.Exec(`INSERT INTO files(item_id,path,size,mtime) VALUES(?,?,1,1)`, id, "/m/"+strings.Repeat("x", n))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := res.LastInsertId()
		if _, err := r.db.Exec(`INSERT INTO tracks(item_id,file_id,artist) VALUES(?,?,?)`, id, f, artist); err != nil {
			t.Fatal(err)
		}
	}
	kino := ins("artist", 0, "Кино", 0, false)
	va := ins("artist", 0, "Various Artists", 0, false)
	own := ins("album", kino, "Группа крови", 1988, false)
	track(own, "Кино", false)
	for i, c := range []struct {
		artist  string
		year    int
		missing bool
		tMiss   bool
	}{{"Кино", 1990, false, false}, {"KINO", 1990, false, false}, {"кино", 2001, false, false},
		{"Кино", 1995, true, false}, {"Кино", 1996, false, true}, {"Аквариум", 1997, false, false}, {"Кино feat. X", 1998, false, false}} {
		al := ins("album", va, "Сборник "+string(rune('A'+i)), c.year, c.missing)
		track(al, c.artist, c.tMiss)
		track(al, "Другой", false)
	}
	none := ins("album", 0, "Без артиста", 2002, false)
	track(none, "кИнО", false)
	for _, name := range []string{"Кино", "кино", "KINO", "Аквариум", "Никто"} {
		got, err := r.AppearsOn(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		want, err := r.queryAlbums(ctx, appearsOnBefore, name)
		if err != nil {
			t.Fatal(err)
		}
		if len(want) == 0 && name != "Никто" {
			t.Fatalf("%s: empty reference", name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", name, got, want)
		}
	}
	if plan := queryPlan(t, r, appearsOnSQL, "Кино"); !strings.Contains(plan, "tracks_artist_nocase") || strings.Contains(plan, "SCAN i") {
		t.Fatalf("plan:\n%s", plan)
	}
}
