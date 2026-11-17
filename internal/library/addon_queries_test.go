package library

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

// addRef binds the library file at path to addon's ref (as RefStore.Bind).
func addRef(t *testing.T, r *Repo, addon, ref, path string) int64 {
	t.Helper()
	f, err := r.FileByPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO addon_file_refs(file_id, addon_id, ref) VALUES(?,?,?)`, f.ID, addon, ref); err != nil {
		t.Fatal(err)
	}
	return f.ID
}

func addUser(t *testing.T, r *Repo, name string) int64 {
	t.Helper()
	res, err := r.db.Exec(`INSERT INTO users (name, avatar) VALUES (?, 'grad-1')`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestFilesOfRef(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for _, p := range []string{"/v/b.mkv", "/v/a.mkv", "/v/c.mkv", "/v/gone.mkv"} {
		if _, err := r.UpsertMovie(ctx, MovieInput{Title: p, Year: 2000, File: FileInput{Path: p, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	addRef(t, r, "a", "r1", "/v/b.mkv")
	addRef(t, r, "a", "r1", "/v/a.mkv")
	gone := addRef(t, r, "a", "r1", "/v/gone.mkv")
	addRef(t, r, "b", "r1", "/v/c.mkv") // same ref string, another addon
	if _, err := r.db.Exec(`UPDATE files SET missing_since=1 WHERE id=?`, gone); err != nil {
		t.Fatal(err)
	}
	got, err := r.FilesOfRef(ctx, "a", "r1")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"/v/a.mkv", "/v/b.mkv"}) {
		t.Fatalf("FilesOfRef %v", paths)
	}
	if got, err := r.FilesOfRef(ctx, "a", "nope"); err != nil || len(got) != 0 {
		t.Fatalf("unknown ref %v %v", got, err)
	}
}

func TestTracksOfRef(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	kinoAlbum(t, r)
	addRef(t, r, "a", "r1", "/m/Кино/Группа крови/03.flac")
	addRef(t, r, "a", "r1", "/m/Кино/Группа крови/01.flac")
	addRef(t, r, "b", "r1", "/m/Кино/Группа крови/02.flac")
	got, err := r.TracksOfRef(ctx, "a", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Группа крови" || got[1].Title != "Война" || got[0].AlbumID == 0 {
		t.Fatalf("tracks %+v", got)
	}
	if got, err := r.TracksOfRef(ctx, "a", "r2"); err != nil || len(got) != 0 {
		t.Fatalf("other ref: %v %v", got, err)
	}
}

func TestClaimRefAndClaimFiles(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	mum, dad := addUser(t, r, "Мама"), addUser(t, r, "Папа")
	film, _ := r.UpsertMovie(ctx, MovieInput{Title: "Дюна", Year: 2021, File: FileInput{Path: "/v/Дюна.mkv", Size: 1}})
	other, _ := r.UpsertMovie(ctx, MovieInput{Title: "Брат", Year: 1997, File: FileInput{Path: "/v/Брат.mkv", Size: 1}})
	ep, _ := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Тьма", SeriesYear: 2017, Season: 1, Episode: 1,
		File: FileInput{Path: "/s/Тьма/1x1.mkv", Size: 1}})
	addRef(t, r, "a", "r1", "/v/Дюна.mkv")
	addRef(t, r, "b", "r1", "/v/Брат.mkv")
	if err := r.ClaimRef(ctx, "a", "r1", mum); err != nil {
		t.Fatal(err)
	}
	if o, _ := r.Owners(ctx, film); !reflect.DeepEqual(o, []int64{mum}) {
		t.Fatalf("film owners %v", o)
	}
	if o, _ := r.Owners(ctx, other); len(o) != 0 {
		t.Fatalf("another addon's file claimed: %v", o)
	}
	epFile, _ := r.FileByPath(ctx, "/s/Тьма/1x1.mkv")
	if err := r.ClaimFiles(ctx, []int64{epFile.ID, 999}, dad); err != nil {
		t.Fatal(err)
	}
	if o, _ := r.Owners(ctx, ep); !reflect.DeepEqual(o, []int64{dad}) {
		t.Fatalf("series owners %v", o)
	}
	if err := r.ClaimFiles(ctx, nil, dad); err != nil {
		t.Fatal(err)
	}
	if err := r.ClaimRef(ctx, "a", "r1", 0); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesAudio(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	probe := func(path, titles string) {
		f, err := r.FileByPath(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.SetProbe(ctx, f.ID, `{"Audio":[`+titles+`]}`, 0); err != nil {
			t.Fatal(err)
		}
	}
	var series int64
	for _, c := range []struct{ s, e int }{{2, 1}, {1, 2}, {1, 1}} {
		id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42, Season: c.s, Episode: c.e,
			File: FileInput{Path: "/s/" + itoa(c.s) + "x" + itoa(c.e) + ".mkv", Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		r.db.QueryRow(`SELECT p.parent_id FROM items i JOIN items p ON p.id=i.parent_id WHERE i.id=?`, id).Scan(&series)
	}
	probe("/s/1x1.mkv", `{"Title":"LostFilm"},{"Title":""}`)
	probe("/s/1x2.mkv", `{"Title":"Кубик в кубе"}`)
	// 2x1 unprobed: no titles
	mum := addUser(t, r, "Мама")
	if err := r.RememberSeriesAudio(ctx, mum, series, TrackChoice{Lang: "ru", Title: "LostFilm"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.SeriesAudio(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []addonkit.EpisodeAudio{
		{Season: 1, Episode: 1, Titles: []string{"LostFilm", ""}},
		{Season: 1, Episode: 2, Titles: []string{"Кубик в кубе"}},
		{Season: 2, Episode: 1, Titles: []string{}},
	}
	if !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("files %+v", got.Files)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].UserID != mum || got.Profiles[0].AudioTitle != "LostFilm" ||
		time.Since(got.Profiles[0].UpdatedAt) > time.Hour {
		t.Fatalf("profiles %+v", got.Profiles)
	}
	if got, err := r.SeriesAudio(ctx, 7); err != nil || len(got.Files) != 0 || len(got.Profiles) != 0 || got.Files == nil || got.Profiles == nil {
		t.Fatalf("unknown series %+v %v", got, err)
	}
}

func TestSearchTitles(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	a, _ := r.UpsertMovie(ctx, MovieInput{Title: "Ёлки", Year: 2010, File: FileInput{Path: "/v/1.mkv", Size: 1}})
	b, _ := r.UpsertMovie(ctx, MovieInput{Title: "Елки", Year: 2011, File: FileInput{Path: "/v/2.mkv", Size: 1}})
	gone, _ := r.UpsertMovie(ctx, MovieInput{Title: "ЁЛКИ!", Year: 2012, File: FileInput{Path: "/v/3.mkv", Size: 1}})
	r.UpsertMovie(ctx, MovieInput{Title: "Ёлки 2", Year: 2011, File: FileInput{Path: "/v/4.mkv", Size: 1}})
	r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "Ёлки", Season: 1, Episode: 1, File: FileInput{Path: "/s/1.mkv", Size: 1}})
	if _, err := r.db.Exec(`UPDATE items SET missing_since=1 WHERE id=?`, gone); err != nil {
		t.Fatal(err)
	}
	got, err := r.SearchTitles(ctx, KindMovie, "ёлки", 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, it := range got {
		ids = append(ids, it.ID)
	}
	if !reflect.DeepEqual(ids, []int64{a, b}) {
		t.Fatalf("SearchTitles %v (want %d %d)", ids, a, b)
	}
	if got, _ := r.SearchTitles(ctx, KindMovie, "ёлки", 1); len(got) != 1 {
		t.Fatalf("limit: %d", len(got))
	}
	if got, _ := r.SearchTitles(ctx, KindSeries, "Ёлки", 10); len(got) != 1 || got[0].Kind != KindSeries {
		t.Fatalf("series %+v", got)
	}
	if got, err := r.SearchTitles(ctx, KindMovie, "!!", 10); err != nil || len(got) != 0 {
		t.Fatalf("empty norm %v %v", got, err)
	}
}

func TestFilesUnderNoProbe(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for _, p := range []string{"/v/a/1.mkv", "/v/a/2.mkv", "/v/b/3.mkv"} {
		if _, err := r.UpsertMovie(ctx, MovieInput{Title: p, Year: 2000, File: FileInput{Path: p, Size: 7, Mtime: 3}}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := r.FileByPath(ctx, "/v/a/1.mkv")
	huge := `{"Audio":[{"Title":"` + strings.Repeat("x", 8<<20) + `"}]}`
	if err := r.SetProbe(ctx, f.ID, huge, 9000); err != nil {
		t.Fatal(err)
	}
	got, err := r.FilesUnderNoProbe(ctx, "/v/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "/v/a/1.mkv" || got[1].Path != "/v/a/2.mkv" || got[0].ID != f.ID ||
		got[0].Size != 7 || got[0].Mtime != 3 || got[0].DurationMs != 9000 || got[0].ItemID == 0 {
		t.Fatalf("FilesUnderNoProbe %+v", got)
	}
	for _, f := range got {
		if f.ProbeJSON != "" {
			t.Fatalf("probe read: %d bytes", len(f.ProbeJSON))
		}
	}
	// FilesUnder itself still reads it
	if all, err := r.FilesUnder(ctx, "/v/a"); err != nil || len(all[0].ProbeJSON) != len(huge) {
		t.Fatalf("FilesUnder probe %d %v", len(all[0].ProbeJSON), err)
	}
}
