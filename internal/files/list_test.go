package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

func names(es []Entry) string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return strings.Join(out, ",")
}

func TestListSortsAndKinds(t *testing.T) {
	s, mr := testSandbox(t)
	dir := filepath.Join(mr, "media/movies/Брат 2 (2000)")
	write(t, filepath.Join(dir, "Брат 2 (2000) BDRip 1080p.mkv"), 300)
	write(t, filepath.Join(dir, "Брат 2.srt"), 10)
	write(t, filepath.Join(dir, "poster.jpg"), 100)
	write(t, filepath.Join(dir, "movie.nfo"), 5)
	write(t, filepath.Join(dir, ".hidden"), 1)
	write(t, filepath.Join(dir, "Extras/a.mkv"), 1)
	write(t, filepath.Join(dir, "Extras/b.mkv"), 1)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(dir, "movie.nfo"), old, old)

	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside-link")); err != nil {
		t.Fatal(err)
	}
	b := &Browser{Sandbox: s}
	l, err := b.List(context.Background(), "movies", "Брат 2 (2000)", ListOptions{Sort: "size", Order: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(l.Entries); got != "Extras,Брат 2 (2000) BDRip 1080p.mkv,poster.jpg,Брат 2.srt,movie.nfo" {
		t.Fatalf("size desc: %s", got)
	}
	kinds := map[string]string{}
	for _, e := range l.Entries {
		kinds[e.Name] = e.Kind
		if strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, mr) {
			t.Fatalf("absolute path leaked: %q", e.Path)
		}
	}
	want := map[string]string{"Extras": "folder", "Брат 2 (2000) BDRip 1080p.mkv": "video", "Брат 2.srt": "subtitle", "poster.jpg": "image", "movie.nfo": "text"}
	for n, k := range want {
		if kinds[n] != k {
			t.Errorf("kind %s = %q want %q", n, kinds[n], k)
		}
	}
	if e := l.Entries[0]; !e.IsDir || e.ChildCount == nil || *e.ChildCount != 2 || e.Path != "Брат 2 (2000)/Extras" {
		t.Fatalf("folder entry %+v", e)
	}
	if l.Path != "Брат 2 (2000)" || len(l.Breadcrumbs) != 2 || l.Breadcrumbs[0].Name != "Фильмы" || l.Breadcrumbs[1].Path != "Брат 2 (2000)" {
		t.Fatalf("breadcrumbs %+v", l.Breadcrumbs)
	}

	l, _ = b.List(context.Background(), "movies", "Брат 2 (2000)", ListOptions{Sort: "mtime", Order: "asc"})
	if l.Entries[1].Name != "movie.nfo" {
		t.Fatalf("mtime asc: %s", names(l.Entries))
	}
	l, _ = b.List(context.Background(), "movies", "Брат 2 (2000)", ListOptions{})
	if got := names(l.Entries); got != "Extras,movie.nfo,poster.jpg,Брат 2 (2000) BDRip 1080p.mkv,Брат 2.srt" {
		t.Fatalf("name asc: %s", got)
	}

	// search in subtree
	l, err = b.List(context.Background(), "movies", "", ListOptions{Q: "b.MKV"})
	if err != nil || names(l.Entries) != "b.mkv" || l.Entries[0].Path != "Брат 2 (2000)/Extras/b.mkv" {
		t.Fatalf("search: %v %+v", err, l.Entries)
	}

	if _, err := b.List(context.Background(), "movies", "Брат 2 (2000)/movie.nfo", ListOptions{}); err != ErrNotDir {
		t.Fatalf("list of a file: %v", err)
	}
	if _, err := b.List(context.Background(), "movies", "nope", ListOptions{}); err != ErrNotFound {
		t.Fatalf("missing dir: %v", err)
	}
}

func TestListLinksLibraryItem(t *testing.T) {
	s, mr := testSandbox(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	repo := library.NewRepo(d)
	movie := filepath.Join(mr, "media/movies/Дюна (2024)/Дюна.mkv")
	write(t, movie, 10)
	write(t, filepath.Join(mr, "media/movies/Дюна (2024)/poster.jpg"), 10)
	write(t, filepath.Join(mr, "media/movies/Чужое.mkv"), 10)
	id, err := repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Дюна", Year: 2024, File: library.FileInput{Path: movie, Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	b := &Browser{Sandbox: s, Library: repo, Seeds: fakeSeeds{movie: "abc"},
		ThumbURL: func(root, rel string, v int64) string { return "/t/" + root + "/" + rel }}

	l, err := b.List(context.Background(), "movies", "", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 2 {
		t.Fatalf("%+v", l.Entries)
	}
	dir := l.Entries[0]
	if dir.Item == nil || dir.Item.ID != id || dir.Item.Title != "Дюна" || dir.Item.Kind != "movie" {
		t.Fatalf("dir item %+v", dir.Item)
	}
	if dir.Torrent == nil || dir.Torrent.Infohash != "abc" {
		t.Fatalf("dir torrent %+v", dir.Torrent)
	}
	if dir.Thumb != "/t/movies/Дюна (2024)" {
		t.Fatalf("dir thumb %q", dir.Thumb)
	}
	if l.Entries[1].Item != nil || l.Entries[1].Torrent != nil {
		t.Fatalf("unknown file linked: %+v", l.Entries[1])
	}

	l, _ = b.List(context.Background(), "movies", "Дюна (2024)", ListOptions{})
	f := l.Entries[1]
	if f.Name != "Дюна.mkv" || f.Item == nil || f.Item.ID != id || f.Torrent == nil || !f.Torrent.Seeding {
		t.Fatalf("file entry %+v", f)
	}
	e, err := b.Stat(context.Background(), "movies", "Дюна (2024)/Дюна.mkv")
	if err != nil || e.Item == nil || e.Item.ID != id {
		t.Fatalf("stat %+v %v", e, err)
	}
}

func TestListLinksTrackToItsAlbum(t *testing.T) {
	s, mr := testSandbox(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	repo := library.NewRepo(d)
	dir := filepath.Join(mr, "media/movies/Кино - Группа крови")
	song := filepath.Join(dir, "01 Группа крови.flac")
	write(t, song, 10)
	album, err := repo.UpsertAlbumTracks(context.Background(), "Кино", []library.TrackInput{{
		File: library.FileInput{Path: song, Size: 10}, AlbumDir: dir, Album: "Группа крови", Artist: "Кино", Title: "Группа крови", Track: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	b := &Browser{Sandbox: s, Library: repo, Seeds: fakeSeeds{}, ThumbURL: func(root, rel string, v int64) string { return "" }}
	l, err := b.List(context.Background(), "movies", "Кино - Группа крови", ListOptions{})
	if err != nil || len(l.Entries) != 1 {
		t.Fatalf("%+v %v", l.Entries, err)
	}
	// «Показать в медиатеке» opens the album, not a page for the bare track
	if it := l.Entries[0].Item; it == nil || it.ID != album || it.Kind != "album" || it.Title != "Группа крови" {
		t.Fatalf("track item %+v, album %d", it, album)
	}
}

func TestListCapsEntriesAndHonoursContext(t *testing.T) {
	s, mr := testSandbox(t)
	for _, n := range []string{"a.mkv", "b.mkv", "c.mkv", "d.mkv"} {
		write(t, filepath.Join(mr, "media/movies", n), 1)
	}
	old := listLimit
	listLimit = 3
	t.Cleanup(func() { listLimit = old })
	b := &Browser{Sandbox: s}
	l, err := b.List(context.Background(), "movies", "", ListOptions{})
	if err != nil || len(l.Entries) != 3 || !l.Truncated {
		t.Fatalf("%d entries truncated=%v %v", len(l.Entries), l.Truncated, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.List(ctx, "movies", "", ListOptions{}); err == nil {
		t.Fatal("cancelled listing must fail")
	}
}
