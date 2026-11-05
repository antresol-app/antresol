// Package musictest builds the music library fixture shared by the store
// tests (internal/music) and the API tests (internal/api).
package musictest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

// Shuffle is the shuffle fixture's ids:
//   - «Тестовый артист» / «Первый альбом», genre «Рок», 3 tracks (First);
//   - compilation «Сборник хитов» by «Разные исполнители», genre «Поп»,
//     one track by «Тестовый артист» (CompTest) and one by «Другой
//     артист» (CompOther);
//   - artist item «Другой артист» rated 18+ (its album has no rating) with
//     «Взрослый», genre «Рок», 2 tracks (Adult);
//   - «Без артиста», no artist, rated 18+, genre «Рок», 1 track (NoArtist);
//   - a missing album «Пропавший» of «Тестовый артист», genre «Рок» (Missing).
//
// Visible to all: 8 tracks; «Рок»: 6; to a 12+ kid: First + Comp.
type Shuffle struct {
	First, Comp, Adult, NoArtist, Missing []int64 // track ids
	CompTest, CompOther                   int64
	TestArtist, OtherArtist               int64 // artist item ids
	FirstAlbum, AdultAlbum                int64
}

// GenreAlbum adds an album under dir with one track per artist (files
// written to disk), all of genre, and returns its id and track ids.
func GenreAlbum(t testing.TB, repo *library.Repo, dir, albumArtist, title, genre string, artists ...string) (int64, []int64) {
	t.Helper()
	ad := filepath.Join(dir, albumArtist, title)
	if err := os.MkdirAll(ad, 0o755); err != nil {
		t.Fatal(err)
	}
	var in []library.TrackInput
	for i, a := range artists {
		n := strconv.Itoa(i + 1)
		p := filepath.Join(ad, n+".flac")
		if err := os.WriteFile(p, []byte("audio "+n), 0o644); err != nil {
			t.Fatal(err)
		}
		in = append(in, library.TrackInput{File: library.FileInput{Path: p, Size: int64(len("audio " + n)), Mtime: 1},
			AlbumDir: ad, Album: title, Artist: a, Title: title + " " + n, Disc: 1, Track: i + 1, Codec: "flac", Lossless: true, Genre: genre})
	}
	id, err := repo.UpsertAlbumTracks(context.Background(), albumArtist, in)
	if err != nil {
		t.Fatal(err)
	}
	trs, err := repo.AlbumTracks(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, tr := range trs {
		ids = append(ids, tr.ID)
	}
	return id, ids
}

// Exec runs a fixture statement.
func Exec(t testing.TB, d *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// Artist inserts a bare artist item (no albums) and returns its id.
func Artist(t testing.TB, d *sql.DB, name string) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO items(kind,title,sort_title,match_key,added_at,updated_at) VALUES('artist',?,?,?,?,?)`,
		name, name, name, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func parent(t testing.TB, repo *library.Repo, id int64) int64 {
	t.Helper()
	it, err := repo.Item(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it.ParentID
}

// ShuffleLib builds the shuffle fixture (see Shuffle) with files under dir.
func ShuffleLib(t testing.TB, repo *library.Repo, d *sql.DB, dir string) Shuffle {
	t.Helper()
	var f Shuffle
	const ta, oa = "Тестовый артист", "Другой артист"
	f.FirstAlbum, f.First = GenreAlbum(t, repo, dir, ta, "Первый альбом", "Рок", ta, ta, ta)
	_, f.Comp = GenreAlbum(t, repo, dir, library.VariousArtists, "Сборник хитов", "Поп", ta, oa)
	f.CompTest, f.CompOther = f.Comp[0], f.Comp[1]
	f.AdultAlbum, f.Adult = GenreAlbum(t, repo, dir, oa, "Взрослый", "Рок", oa, oa)
	f.TestArtist, f.OtherArtist = parent(t, repo, f.FirstAlbum), parent(t, repo, f.AdultAlbum)
	Exec(t, d, `UPDATE items SET rating='18+' WHERE id=?`, f.OtherArtist) // the album item has no rating
	na, tr := GenreAlbum(t, repo, dir, "Кто-то", "Без артиста", "Рок", "Кто-то")
	f.NoArtist = tr
	Exec(t, d, `UPDATE items SET parent_id=NULL, rating='18+' WHERE id=?`, na)
	ma, tr := GenreAlbum(t, repo, dir, ta, "Пропавший", "Рок", ta)
	f.Missing = tr
	Exec(t, d, `UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, ma)
	return f
}

// Set is the set of ids of the lists.
func Set(lists ...[]int64) map[int64]bool {
	m := map[int64]bool{}
	for _, l := range lists {
		for _, id := range l {
			m[id] = true
		}
	}
	return m
}

// SameSet: got holds exactly the ids of want, each once.
func SameSet(got []int64, want map[int64]bool) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[int64]bool{}
	for _, id := range got {
		if !want[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
