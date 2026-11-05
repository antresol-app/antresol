package library

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// insItem inserts an item row directly (parent 0 = none).
func insItem(t *testing.T, r *Repo, kind string, parent int64, title, rating string) int64 {
	t.Helper()
	res, err := r.db.Exec(`INSERT INTO items(kind,parent_id,title,sort_title,rating) VALUES(?,?,?,?,?)`,
		kind, nullInt(parent), title, sortTitle(title), rating)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := r.AddTitles(context.Background(), id, []string{title}); err != nil {
		t.Fatal(err)
	}
	return id
}

func insFile(t *testing.T, r *Repo, itemID int64, path string) int64 {
	t.Helper()
	id, err := upsertFileID(context.Background(), r.db, itemID, FileInput{Path: path, Size: 1, Mtime: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insTrack(t *testing.T, r *Repo, itemID, fileID, startMs int64) {
	t.Helper()
	if _, err := r.db.Exec(`INSERT INTO tracks(item_id,file_id,start_ms) VALUES(?,?,?)`, itemID, fileID, startMs); err != nil {
		t.Fatal(err)
	}
}

// state returns "" (shown), "missing" or "deleted" for each item.
func states(t *testing.T, r *Repo, ids ...int64) []string {
	t.Helper()
	var out []string
	for _, id := range ids {
		var ms *string
		err := r.db.QueryRow(`SELECT missing_since FROM items WHERE id=?`, id).Scan(&ms)
		switch {
		case err != nil:
			out = append(out, "deleted")
		case ms != nil:
			out = append(out, "missing")
		default:
			out = append(out, "")
		}
	}
	return out
}

func allState(t *testing.T, got []string, want string) {
	t.Helper()
	for i, g := range got {
		if g != want {
			t.Fatalf("item %d: %q, want %q (all %q)", i, g, want, got)
		}
	}
}

func TestUpsertFileIDReturnsID(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	it := insItem(t, r, KindBook, 0, "Война и мир", "")
	a, err := upsertFileID(ctx, r.db, it, FileInput{Path: "/m/b/x.epub", Size: 1})
	if err != nil || a == 0 {
		t.Fatal(a, err)
	}
	b, err := upsertFileID(ctx, r.db, it, FileInput{Path: "/m/b/x.epub", Size: 2})
	if err != nil || b != a {
		t.Fatalf("second id %d (first %d) %v", b, a, err)
	}
	var size int64
	r.db.QueryRow(`SELECT size FROM files WHERE id=?`, a).Scan(&size)
	if size != 2 {
		t.Fatalf("size %d", size)
	}
}

func withGrace(t *testing.T, d time.Duration) {
	old := MissingGrace
	MissingGrace = d
	t.Cleanup(func() { MissingGrace = old })
}

func TestRemoveMissingMusicKinds(t *testing.T) {
	ctx := context.Background()
	t.Run("tracks with own files", func(t *testing.T) {
		r := newRepo(t)
		root := filepath.Join(t.TempDir(), "music")
		artist := insItem(t, r, KindArtist, 0, "Кино", "")
		album := insItem(t, r, KindAlbum, artist, "Группа крови", "")
		ids := []int64{artist, album}
		seen := map[string]bool{}
		for _, name := range []string{"01.flac", "02.flac"} {
			tr := insItem(t, r, KindTrack, album, name, "")
			p := filepath.Join(root, "Кино", "Группа крови", name)
			insTrack(t, r, tr, insFile(t, r, tr, p), 0)
			ids = append(ids, tr)
			seen[p] = true
		}
		if _, err := r.removeMissing(ctx, root, seen); err != nil {
			t.Fatal(err)
		}
		allState(t, states(t, r, ids...), "")
		n, err := r.removeMissing(ctx, root, map[string]bool{})
		if err != nil || n != 2 {
			t.Fatalf("removed %d %v", n, err)
		}
		allState(t, states(t, r, ids...), "missing")
		withGrace(t, -time.Hour)
		if _, err := r.removeMissing(ctx, root, map[string]bool{}); err != nil {
			t.Fatal(err)
		}
		allState(t, states(t, r, ids...), "deleted")
	})
	t.Run("cue image", func(t *testing.T) {
		r := newRepo(t)
		root := filepath.Join(t.TempDir(), "music")
		artist := insItem(t, r, KindArtist, 0, "Аквариум", "")
		album := insItem(t, r, KindAlbum, artist, "Радио Африка", "")
		img := filepath.Join(root, "Аквариум", "Радио Африка", "image.flac")
		fid := insFile(t, r, album, img)
		var tracks []int64
		for i := range 3 {
			tr := insItem(t, r, KindTrack, album, "Трек", "")
			insTrack(t, r, tr, fid, int64(i)*60000)
			tracks = append(tracks, tr)
		}
		if _, err := r.removeMissing(ctx, root, map[string]bool{img: true}); err != nil {
			t.Fatal(err)
		}
		allState(t, states(t, r, append(tracks, album, artist)...), "")
		if _, err := r.removeMissing(ctx, root, map[string]bool{}); err != nil {
			t.Fatal(err)
		}
		allState(t, states(t, r, append(tracks, album)...), "missing")
		withGrace(t, -time.Hour)
		if _, err := r.removeMissing(ctx, root, map[string]bool{}); err != nil {
			t.Fatal(err)
		}
		allState(t, states(t, r, append(tracks, album, artist)...), "deleted")
	})
}

func TestKidSeesUnratedMusicAndBooks(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	album := insItem(t, r, KindAlbum, 0, "Группа крови", "")
	book := insItem(t, r, KindBook, 0, "Незнайка", "")
	adult := insItem(t, r, KindComic, 0, "Берсерк", "18+")
	adultVol := insItem(t, r, KindComicVolume, adult, "Том 1", "")
	kids := insItem(t, r, KindComic, 0, "Чиби", "")
	kidsVol := insItem(t, r, KindComicVolume, kids, "Том 1", "")
	insItem(t, r, KindMovie, 0, "Без рейтинга", "") // unrated video stays hidden

	ids := func(kind string) map[int64]bool {
		items, _, err := r.List(ctx, ListQuery{Kind: kind, MaxRating: "12+"})
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64]bool{}
		for _, it := range items {
			m[it.ID] = true
		}
		return m
	}
	if !ids(KindAlbum)[album] || !ids(KindBook)[book] {
		t.Fatal("unrated album/book hidden from kid")
	}
	if c := ids(KindComic); c[adult] || !c[kids] {
		t.Fatalf("comics %v", c)
	}
	if v := ids(KindComicVolume); v[adultVol] || !v[kidsVol] {
		t.Fatalf("volumes %v", v)
	}
	if m := ids(""); len(m) != 0 {
		t.Fatalf("unrated movie shown %v", m)
	}
}

func TestSearchKindsExcludesChildren(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	album := insItem(t, r, KindAlbum, 0, "Кукушка", "")
	insItem(t, r, KindTrack, album, "Кукушка", "")
	got, err := r.SearchKinds(ctx, "кукушка", []string{KindAlbum}, 10, "")
	if err != nil || len(got) != 1 || got[0].ID != album {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := r.SearchKinds(ctx, "кукушка", []string{KindAlbum}, 10, "12+"); err != nil || len(got) != 1 {
		t.Fatalf("kid %+v %v", got, err)
	}
	if got, err := r.Search(ctx, "кукушка", 10); err != nil || len(got) != 0 {
		t.Fatalf("search %+v %v", got, err)
	}
}

func TestIsUnratedKind(t *testing.T) {
	for _, k := range []string{KindArtist, KindAlbum, KindTrack, KindBook, KindAudiobook, KindComic, KindComicVolume} {
		if !IsUnratedKind(k) {
			t.Fatal(k)
		}
	}
	for _, k := range []string{KindMovie, KindSeries, KindSeason, KindEpisode, ""} {
		if IsUnratedKind(k) {
			t.Fatal(k)
		}
	}
}
