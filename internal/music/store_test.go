package music

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
)

type env struct {
	t     *testing.T
	s     *Store
	repo  *library.Repo
	users *auth.Store
	dir   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return &env{t: t, s: New(d), repo: library.NewRepo(d), users: auth.NewStore(d), dir: t.TempDir()}
}

func (e *env) user(name string) int64 {
	u, err := e.users.CreateUser(context.Background(), auth.NewUser{Name: name})
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

// album adds an album of n plain tracks and returns its id and track ids.
func (e *env) album(artist, title string, n int, cover string) (int64, []int64) {
	e.t.Helper()
	dir := filepath.Join(e.dir, artist, title)
	var in []library.TrackInput
	for i := 1; i <= n; i++ {
		in = append(in, library.TrackInput{File: library.FileInput{Path: filepath.Join(dir, string(rune('a'+i))+".mp3"), Size: 10},
			AlbumDir: dir, Album: title, Artist: artist, Title: title + " " + string(rune('A'+i)), Track: i, Codec: "mp3", Cover: cover})
	}
	id, err := e.repo.UpsertAlbumTracks(context.Background(), artist, in)
	if err != nil {
		e.t.Fatal(err)
	}
	tr, err := e.repo.AlbumTracks(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	var ids []int64
	for _, t := range tr {
		ids = append(ids, t.ID)
	}
	return id, ids
}

func TestPlaylistCRUDPerUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user("Папа"), e.user("Мама")
	_, tr := e.album("Кино", "Группа крови", 4, "0123456789abcdef0123456789abcdef01234567.jpg")

	p, err := e.s.CreatePlaylist(ctx, a, "Дорога", tr[:2])
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Дорога" || p.TrackCount != 2 || len(p.Covers) != 1 || p.Covers[0] != "/api/images/0123456789abcdef0123456789abcdef01234567.jpg" {
		t.Fatalf("%+v", p)
	}
	name := "В дорогу"
	if err := e.s.UpdatePlaylist(ctx, a, p.ID, PlaylistPatch{Name: &name, Append: []int64{tr[2], tr[3]}}); err != nil {
		t.Fatal(err)
	}
	got, items, err := e.s.Playlist(ctx, a, p.ID)
	if err != nil || got.Name != "В дорогу" || !equal(items, tr) {
		t.Fatalf("%+v %v %v", got, items, err)
	}
	if err := e.s.UpdatePlaylist(ctx, a, p.ID, PlaylistPatch{Remove: []int{0, 2}}); err != nil {
		t.Fatal(err)
	}
	if _, items, _ = e.s.Playlist(ctx, a, p.ID); !equal(items, []int64{tr[1], tr[3]}) {
		t.Fatalf("after remove %v", items)
	}
	if err := e.s.UpdatePlaylist(ctx, a, p.ID, PlaylistPatch{Order: []int64{tr[3], tr[1], tr[0]}}); err != nil {
		t.Fatal(err)
	}
	if _, items, _ = e.s.Playlist(ctx, a, p.ID); !equal(items, []int64{tr[3], tr[1], tr[0]}) {
		t.Fatalf("after order %v", items)
	}

	// someone else's playlist does not exist for b
	if _, _, err := e.s.Playlist(ctx, b, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}
	if err := e.s.UpdatePlaylist(ctx, b, p.ID, PlaylistPatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user update: %v", err)
	}
	if err := e.s.DeletePlaylist(ctx, b, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user delete: %v", err)
	}
	if ls, _ := e.s.Playlists(ctx, b); len(ls) != 0 {
		t.Fatalf("b sees %+v", ls)
	}
	if ls, _ := e.s.Playlists(ctx, a); len(ls) != 1 || ls[0].TrackCount != 3 {
		t.Fatalf("a sees %+v", ls)
	}
	if err := e.s.DeletePlaylist(ctx, a, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.Playlist(ctx, a, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestPlaylistCoversDistinct(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("Папа")
	_, t1 := e.album("A", "One", 2, "1111111111111111111111111111111111111111.jpg")
	_, t2 := e.album("B", "Two", 1, "")
	a3, t3 := e.album("C", "Three", 1, "file:/m/c/cover.jpg")
	p, err := e.s.CreatePlaylist(ctx, u, "Микс", []int64{t1[0], t1[1], t2[0], t3[0]})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/images/1111111111111111111111111111111111111111.jpg", "/api/images/local/" + itoa(a3) + "/poster"}
	if len(p.Covers) != 2 || p.Covers[0] != want[0] || p.Covers[1] != want[1] {
		t.Fatalf("covers %v", p.Covers)
	}
}

func TestPlaysRecentAlbums(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("Папа")
	a, ta := e.album("Кино", "A", 2, "")
	b, tb := e.album("Кино", "B", 1, "")
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []int64{ta[0], ta[1], tb[0]} {
		if err := e.s.Played(ctx, u, id, t0.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.s.RecentAlbums(ctx, u, 10)
	if err != nil || !equal(got, []int64{b, a}) {
		t.Fatalf("%v %v", got, err)
	}
	if n, _ := e.s.PlayCount(ctx, ta[0]); n != 1 {
		t.Fatalf("track plays %d", n)
	}
	if n, _ := e.s.PlayCount(ctx, a); n != 2 {
		t.Fatalf("album plays %d", n)
	}
	top, err := e.s.TopTracks(ctx, ta[0], 10) // not an artist: nothing
	if err != nil || len(top) != 0 {
		t.Fatalf("%v %v", top, err)
	}
}

func TestStars(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("Папа")
	_, tr := e.album("Кино", "A", 2, "")
	for _, id := range tr {
		if err := e.s.Star(ctx, u, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.s.Star(ctx, u, tr[0]); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := e.s.Unstar(ctx, u, tr[1]); err != nil {
		t.Fatal(err)
	}
	ids, err := e.s.Starred(ctx, u)
	if err != nil || !equal(ids, []int64{tr[0]}) {
		t.Fatalf("%v %v", ids, err)
	}
	if ok, _ := e.s.IsStarred(ctx, u, tr[0]); !ok {
		t.Fatal("starred")
	}
	if ok, _ := e.s.IsStarred(ctx, u, tr[1]); ok {
		t.Fatal("unstarred")
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// plays, stars and playlist_items are looked up by item (play counts, top
// tracks, ON DELETE CASCADE from items): never a full scan of plays.
func TestItemIndexesUsed(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"plays_item", "stars_item", "playlist_items_item"} {
		var n int
		if err := e.s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s: %d %v", name, n, err)
		}
	}
	for what, q := range map[string]string{
		"play count": `SELECT (SELECT COUNT(*) FROM plays WHERE item_id=?1) + (SELECT COUNT(*) FROM items t JOIN plays p ON p.item_id=t.id WHERE t.parent_id=?1)`,
		"top tracks": `SELECT p.item_id, COUNT(*) n FROM plays p JOIN items pi ON pi.id=p.item_id JOIN items pa ON pa.id=pi.parent_id WHERE pa.parent_id=?1 GROUP BY p.item_id`,
		"cascade":    `SELECT 1 FROM plays WHERE item_id=?1 UNION ALL SELECT 1 FROM stars WHERE item_id=?1 UNION ALL SELECT 1 FROM playlist_items WHERE item_id=?1`,
	} {
		rows, err := e.s.db.Query(`EXPLAIN QUERY PLAN `+q, 1)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		for _, d := range plan {
			if strings.HasPrefix(d, "SCAN p") || d == "SCAN plays" || d == "SCAN stars" || d == "SCAN playlist_items" {
				t.Fatalf("%s scans: %v", what, plan)
			}
		}
	}
}

func TestStarAtKeepsDate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("Папа")
	_, tr := e.album("Кино", "Группа крови", 2, "")
	t1, t2 := tr[0], tr[1]
	if err := e.s.StarAt(ctx, u, t1, time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := e.s.StarAt(ctx, u, t2, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.Starred(ctx, u)
	if err != nil || !equal(got, []int64{t2, t1}) {
		t.Fatalf("starred %v %v", got, err)
	}
	// a second star keeps the first date: t1 stays older than t2
	if err := e.s.StarAt(ctx, u, t1, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var at string
	if err := e.s.db.QueryRow(`SELECT starred_at FROM stars WHERE user_id=? AND item_id=?`, u, t1).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(at, "2019-05-01") {
		t.Fatalf("starred_at %s", at)
	}
	if got, _ := e.s.Starred(ctx, u); !equal(got, []int64{t2, t1}) {
		t.Fatalf("after restar %v", got)
	}
	// zero time: now
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	e.s.Now = func() time.Time { return now }
	_, more := e.album("Кино", "Ночь", 1, "")
	if err := e.s.StarAt(ctx, u, more[0], time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.Starred(ctx, u); len(got) != 3 || got[0] != more[0] {
		t.Fatalf("zero time %v", got)
	}
}
