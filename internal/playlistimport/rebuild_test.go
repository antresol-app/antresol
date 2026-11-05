package playlistimport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
)

type rebuildEnv struct {
	t     *testing.T
	st    *Store
	m     *music.Store
	lib   *library.Repo
	uid   int64
	items map[string]int64 // library track item by title
}

// newRebuildEnv puts the songs a…e of «Кино/Сборник» into the library.
func newRebuildEnv(t *testing.T) *rebuildEnv {
	t.Helper()
	st, uid := newTestStore(t)
	e := &rebuildEnv{t: t, st: st, m: music.New(st.DB), lib: library.NewRepo(st.DB), uid: uid, items: map[string]int64{}}
	var in []library.TrackInput
	for i, title := range []string{"a", "b", "c", "d", "e"} {
		in = append(in, library.TrackInput{File: library.FileInput{Path: fmt.Sprintf("/m/Кино/Сборник/%02d.flac", i+1), Size: 1, Mtime: 1},
			AlbumDir: "/m/Кино/Сборник", Album: "Сборник", Title: title, Track: i + 1})
	}
	ctx := context.Background()
	id, err := e.lib.UpsertAlbumTracks(ctx, "Кино", in)
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := e.lib.AlbumTracks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range tracks {
		e.items[tr.Title] = tr.ID
	}
	return e
}

func day(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

// job stores a snapshot and moves it to downloading.
func (e *rebuildEnv) job(snap Snapshot) int64 {
	e.t.Helper()
	id, err := e.st.CreateJob(context.Background(), e.uid, snap)
	if err != nil {
		e.t.Fatal(err)
	}
	mustExec(e.t, e.st.DB, `UPDATE import_jobs SET status = 'downloading' WHERE id = ?`, id)
	return id
}

// arrive links the job's songs (by key) to the library tracks of their
// titles, as the queue does when an album is organized.
func (e *rebuildEnv) arrive(jobID int64, keys ...string) {
	e.t.Helper()
	for _, k := range keys {
		var title string
		if err := e.st.DB.QueryRow(`SELECT title FROM import_tracks WHERE job_id = ? AND key = ?`, jobID, k).Scan(&title); err != nil {
			e.t.Fatalf("%s: %v", k, err)
		}
		mustExec(e.t, e.st.DB, `UPDATE import_tracks SET state = 'done', item_id = ? WHERE job_id = ? AND key = ?`, e.items[title], jobID, k)
	}
}

func (e *rebuildEnv) rebuild(jobID int64) {
	e.t.Helper()
	if err := Rebuild(context.Background(), e.st, e.m, jobID); err != nil {
		e.t.Fatal(err)
	}
}

// playlist returns the Vault playlist an import playlist is rebuilt into.
func (e *rebuildEnv) playlist(external string) (int64, string, []int64) {
	e.t.Helper()
	var pid int64
	if err := e.st.DB.QueryRow(`SELECT COALESCE(playlist_id, 0) FROM import_playlists WHERE external_id = ?`, external).Scan(&pid); err != nil {
		e.t.Fatal(err)
	}
	if pid == 0 {
		return 0, "", nil
	}
	p, ids, err := e.m.Playlist(context.Background(), e.uid, pid)
	if err != nil {
		e.t.Fatal(err)
	}
	return pid, p.Name, ids
}

func (e *rebuildEnv) otherUser() int64 {
	e.t.Helper()
	u, err := auth.NewStore(e.st.DB).CreateUser(context.Background(), auth.NewUser{Name: "Мама"})
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (e *rebuildEnv) ids(titles ...string) []int64 {
	out := []int64{}
	for _, t := range titles {
		out = append(out, e.items[t])
	}
	return out
}

func (e *rebuildEnv) starred() []int64 {
	e.t.Helper()
	ids, err := e.m.Starred(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

func (e *rebuildEnv) starredAt(item int64) string {
	e.t.Helper()
	var at string
	if err := e.st.DB.QueryRow(`SELECT starred_at FROM stars WHERE user_id = ? AND item_id = ?`, e.uid, item).Scan(&at); err != nil {
		e.t.Fatalf("star %d: %v", item, err)
	}
	return at
}

func songs(titles ...string) []SnapTrack {
	var out []SnapTrack
	for _, t := range titles {
		out = append(out, SnapTrack{Key: t, Artist: "Кино", Title: t, Album: "Сборник"})
	}
	return out
}

func TestRebuildOrderAndPending(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	job := e.job(Snapshot{Service: "spotify", Tracks: songs("a", "b", "c"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b", "c"}}}})
	e.arrive(job, "a", "c")
	e.rebuild(job)
	pid, name, items := e.playlist("p1")
	if pid == 0 || name != "Дорога" || !slices.Equal(items, e.ids("a", "c")) {
		t.Fatalf("playlist %d %q %v", pid, name, items)
	}
	pending, err := e.st.PlaylistPending(ctx, e.uid, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Position != 1 || pending[0].Title != "b" || pending[0].Artist != "Кино" || pending[0].State != TrackPending {
		t.Fatalf("pending %+v", pending)
	}

	e.arrive(job, "b")
	e.rebuild(job)
	if p2, _, items := e.playlist("p1"); p2 != pid || !slices.Equal(items, e.ids("a", "b", "c")) {
		t.Fatalf("after b: %d %v", p2, items)
	}
	if pending, err := e.st.PlaylistPending(ctx, e.uid, pid); err != nil || len(pending) != 0 || pending == nil {
		t.Fatalf("pending after b %v %v", pending, err)
	}
	// idempotent: the same rebuild again changes nothing (not even the date)
	var before, after string
	e.st.DB.QueryRow(`SELECT updated_at FROM playlists WHERE id = ?`, pid).Scan(&before)
	e.m.Now = func() time.Time { return time.Now().Add(time.Hour) }
	e.rebuild(job)
	e.st.DB.QueryRow(`SELECT updated_at FROM playlists WHERE id = ?`, pid).Scan(&after)
	if before != after {
		t.Fatalf("unchanged playlist rewritten: %s → %s", before, after)
	}
	if _, _, items := e.playlist("p1"); !slices.Equal(items, e.ids("a", "b", "c")) {
		t.Fatalf("rerun %v", items)
	}
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM playlists`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d playlists", n)
	}
}

func TestRebuildKeepsServiceDuplicates(t *testing.T) {
	e := newRebuildEnv(t)
	job := e.job(Snapshot{Service: "spotify", Tracks: songs("a", "b"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b", "a"}}}})
	e.arrive(job, "a", "b")
	e.rebuild(job)
	if _, _, items := e.playlist("p1"); !slices.Equal(items, e.ids("a", "b", "a")) {
		t.Fatalf("%v", items)
	}
}

func TestRerunUpdatesSamePlaylist(t *testing.T) {
	e := newRebuildEnv(t)
	first := songs("a", "b", "c")
	first[0].Liked, first[0].AddedAt = true, day("2019-05-01")
	j1 := e.job(Snapshot{Service: "spotify", Tracks: first,
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b", "c"}}}})
	e.arrive(j1, "a", "b", "c")
	e.rebuild(j1)
	pid, _, _ := e.playlist("p1")
	if pid == 0 {
		t.Fatal("no playlist")
	}
	oldStar := e.starredAt(e.items["a"])
	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, j1)

	// the same library again: renamed, reordered, 2 new liked songs
	second := songs("a", "b", "c", "d", "e")
	second[0].Liked, second[0].AddedAt = true, day("2023-01-01") // the service's date moved; the star keeps its own
	second[3].Liked, second[3].AddedAt = true, day("2024-02-01")
	second[4].Liked, second[4].AddedAt = true, day("2024-03-01")
	j2 := e.job(Snapshot{Service: "spotify", Tracks: second,
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога 2", Entries: []string{"c", "a"}}}})
	e.arrive(j2, "a", "b", "c", "d", "e")
	e.rebuild(j2)

	p2, name, items := e.playlist("p1")
	if p2 != pid || name != "Дорога 2" || !slices.Equal(items, e.ids("c", "a")) {
		t.Fatalf("rerun: %d (want %d) %q %v", p2, pid, name, items)
	}
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM playlists`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d playlists", n)
	}
	if got := e.starred(); !slices.Equal(got, e.ids("e", "d", "a")) {
		t.Fatalf("starred %v want %v", got, e.ids("e", "d", "a"))
	}
	if at := e.starredAt(e.items["a"]); at != oldStar || !strings.HasPrefix(at, "2019-05-01") {
		t.Fatalf("old star %s (was %s)", at, oldStar)
	}
	if at := e.starredAt(e.items["d"]); !strings.HasPrefix(at, "2024-02-01") {
		t.Fatalf("new star %s", at)
	}
}

// nextJob finishes job and imports the same playlist again (a re-run).
func (e *rebuildEnv) nextJob(prev int64, snap Snapshot) int64 {
	e.t.Helper()
	mustExec(e.t, e.st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, prev)
	return e.job(snap)
}

func TestRebuildRecreatesDeletedPlaylist(t *testing.T) {
	// deleted during the job that built it: the user's choice for the rest
	// of that job; the next job of the service brings it back
	e := newRebuildEnv(t)
	ctx := context.Background()
	snap := Snapshot{Service: "spotify", Tracks: songs("a", "b"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b"}}}}
	job := e.job(snap)
	e.arrive(job, "a")
	e.rebuild(job)
	pid, _, _ := e.playlist("p1")
	if err := e.m.DeletePlaylist(ctx, e.uid, pid); err != nil {
		t.Fatal(err)
	}
	e.arrive(job, "b")
	e.rebuild(job)
	if p, _, _ := e.playlist("p1"); p != 0 {
		t.Fatalf("deleted playlist recreated mid-job: %d", p)
	}
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM playlists`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d playlists", n)
	}

	j2 := e.nextJob(job, snap)
	e.arrive(j2, "a", "b")
	e.rebuild(j2)
	p2, name, items := e.playlist("p1")
	if p2 == 0 || name != "Дорога" || !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("next job: %d %q %v", p2, name, items)
	}

	// an id that is not this profile's playlist (ErrNotFound) is replaced
	// by the next job, and the other playlist is not touched
	ou := e.otherUser()
	other, err := e.m.CreatePlaylist(ctx, ou, "Чужой", nil)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.st.DB, `UPDATE import_playlists SET playlist_id = ? WHERE external_id = 'p1'`, other.ID)
	j3 := e.nextJob(j2, snap)
	e.arrive(j3, "a", "b")
	e.rebuild(j3)
	if p3, name, items := e.playlist("p1"); p3 == 0 || p3 == other.ID || name != "Дорога" || !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("not ours: %d %q %v", p3, name, items)
	}
	if p, _, err := e.m.Playlist(ctx, ou, other.ID); err != nil || p.Name != "Чужой" {
		t.Fatalf("other playlist touched %+v %v", p, err)
	}
}

func TestPlaylistEditKeptMidJob(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	job := e.job(Snapshot{Service: "spotify", Tracks: songs("a", "b", "c"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b", "c"}}}})
	e.arrive(job, "a")
	e.rebuild(job)
	e.arrive(job, "b")
	e.rebuild(job) // still the import's: b is added
	pid, _, items := e.playlist("p1")
	if !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("before the edit %v", items)
	}
	// the user renames it and puts d first
	name := "Моя дорога"
	if err := e.m.UpdatePlaylist(ctx, e.uid, pid, music.PlaylistPatch{Name: &name, Order: e.ids("d", "b", "a")}); err != nil {
		t.Fatal(err)
	}
	e.arrive(job, "c")
	e.rebuild(job)
	if p, got, items := e.playlist("p1"); p != pid || got != "Моя дорога" || !slices.Equal(items, e.ids("d", "b", "a")) {
		t.Fatalf("edit lost: %d %q %v", p, got, items)
	}
	// a rename alone is an edit too
	e2 := newRebuildEnv(t)
	j := e2.job(Snapshot{Service: "spotify", Tracks: songs("a", "b"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b"}}}})
	e2.arrive(j, "a")
	e2.rebuild(j)
	p, _, _ := e2.playlist("p1")
	if err := e2.m.EditPlaylist(ctx, e2.uid, p, &name, nil); err != nil {
		t.Fatal(err)
	}
	e2.arrive(j, "b")
	e2.rebuild(j)
	if _, got, items := e2.playlist("p1"); got != "Моя дорога" || !slices.Equal(items, e2.ids("a")) {
		t.Fatalf("rename lost: %q %v", got, items)
	}
}

func TestNewJobReappliesServiceOrder(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	snap := Snapshot{Service: "spotify", Tracks: songs("a", "b", "c"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b", "c"}}}}
	j1 := e.job(snap)
	e.arrive(j1, "a", "b", "c")
	e.rebuild(j1)
	pid, _, _ := e.playlist("p1")
	name := "Моя дорога"
	if err := e.m.UpdatePlaylist(ctx, e.uid, pid, music.PlaylistPatch{Name: &name, Order: e.ids("c")}); err != nil {
		t.Fatal(err)
	}
	e.rebuild(j1) // the same job keeps the edit
	if _, got, items := e.playlist("p1"); got != "Моя дорога" || !slices.Equal(items, e.ids("c")) {
		t.Fatalf("same job: %q %v", got, items)
	}
	j2 := e.nextJob(j1, snap)
	e.arrive(j2, "a", "b", "c")
	e.rebuild(j2)
	if p, got, items := e.playlist("p1"); p != pid || got != "Дорога" || !slices.Equal(items, e.ids("a", "b", "c")) {
		t.Fatalf("re-run: %d (want %d) %q %v", p, pid, got, items)
	}
}

func TestUnstarDuringImportSurvives(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	tracks := songs("a", "b")
	tracks[0].Liked, tracks[1].Liked = true, true
	job := e.job(Snapshot{Service: "spotify", Tracks: tracks,
		Albums: []SnapAlbum{{Artist: "Кино", Title: "Сборник", AddedAt: day("2018-03-01")}}})
	album, _, _ := e.lib.FindAlbum(ctx, "Кино", "Сборник")
	e.arrive(job, "a")
	e.rebuild(job)
	if got := e.starred(); !slices.Equal(got, []int64{e.items["a"], album.ID}) && !slices.Equal(got, []int64{album.ID, e.items["a"]}) {
		t.Fatalf("starred %v", got)
	}
	// the user unstars the song and the album while the import runs
	if err := e.m.Unstar(ctx, e.uid, e.items["a"]); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Unstar(ctx, e.uid, album.ID); err != nil {
		t.Fatal(err)
	}
	e.arrive(job, "b")
	e.rebuild(job)
	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'done' WHERE id = ?`, job)
	e.rebuild(job) // the job's last rebuild
	if got := e.starred(); !slices.Equal(got, e.ids("b")) {
		t.Fatalf("starred %v, want only b %v", got, e.ids("b"))
	}
	var marked int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM import_tracks WHERE job_id = ? AND starred = 1`, job).Scan(&marked)
	if marked != 2 {
		t.Fatalf("%d songs marked", marked)
	}
}

func TestRebuildSkipsUntickedPlaylistAndInactiveJob(t *testing.T) {
	e := newRebuildEnv(t)
	tracks := songs("a", "b")
	tracks[0].Liked = true
	job := e.job(Snapshot{Service: "spotify", Tracks: tracks, Playlists: []SnapPlaylist{
		{ID: "p1", Name: "Дорога", Entries: []string{"a"}},
		{ID: "p2", Name: "Не надо", Entries: []string{"b"}}}})
	mustExec(t, e.st.DB, `UPDATE import_playlists SET selected = 0 WHERE external_id = 'p2'`)
	e.arrive(job, "a", "b")

	// a job cancelled just before its rebuild: nothing is written
	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'cancelled' WHERE id = ?`, job)
	e.rebuild(job)
	if pid, _, _ := e.playlist("p1"); pid != 0 {
		t.Fatal("cancelled job built a playlist")
	}
	if got := e.starred(); len(got) != 0 {
		t.Fatalf("cancelled job starred %v", got)
	}

	mustExec(t, e.st.DB, `UPDATE import_jobs SET status = 'downloading' WHERE id = ?`, job)
	e.rebuild(job)
	if pid, _, _ := e.playlist("p1"); pid == 0 {
		t.Fatal("no playlist")
	}
	if pid, _, _ := e.playlist("p2"); pid != 0 {
		t.Fatal("unticked playlist built")
	}
	if err := Rebuild(context.Background(), e.st, e.m, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestFavoritesOffNoStars(t *testing.T) {
	e := newRebuildEnv(t)
	tracks := songs("a", "b")
	tracks[0].Liked, tracks[1].Liked = true, true
	job := e.job(Snapshot{Service: "spotify", Tracks: tracks,
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b"}}},
		Albums:    []SnapAlbum{{Artist: "Кино", Title: "Сборник", AddedAt: day("2020-01-01")}}})
	mustExec(t, e.st.DB, `UPDATE import_jobs SET favorites = 0 WHERE id = ?`, job)
	e.arrive(job, "a", "b")
	e.rebuild(job)
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM stars`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d star rows", n)
	}
	if _, _, items := e.playlist("p1"); !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("playlist %v", items)
	}
}

func TestSavedAlbumsStarredWhenPresent(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	// «Ночь» is in the library under its MusicBrainz id only (another title)
	night, err := e.lib.UpsertAlbumTracks(ctx, "Кино", []library.TrackInput{{File: library.FileInput{Path: "/m/Кино/Night/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Night", Album: "Night", Title: "Ночь", Track: 1, MBReleaseGroupID: "rg-night"}})
	if err != nil {
		t.Fatal(err)
	}
	collection, ok, err := e.lib.FindAlbum(ctx, "Кино", "Сборник")
	if err != nil || !ok {
		t.Fatal("no Сборник", err)
	}
	tracks := songs("a")
	tracks = append(tracks, SnapTrack{Key: "n", Artist: "Кино", Title: "Ночь", Album: "Ночь"})
	job := e.job(Snapshot{Service: "spotify", Tracks: tracks, Albums: []SnapAlbum{
		{Artist: "Кино", Title: "Сборник", AddedAt: day("2018-03-01")},
		{Artist: "Кино", Title: "Ночь", AddedAt: day("2017-06-01")},
		{Artist: "Кино", Title: "Звезда по имени Солнце", AddedAt: day("2016-01-01")},
	}})
	// a song of the job matched «Ночь» to its release group
	mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'matched', mb_release_group = 'rg-night', mb_album = 'Ночь', mb_artist = 'Кино'
		WHERE job_id = ? AND key = 'n'`, job)
	e.rebuild(job)

	if got := e.starred(); !slices.Equal(got, []int64{collection.ID, night}) {
		t.Fatalf("starred %v want [%d %d]", got, collection.ID, night)
	}
	if at := e.starredAt(collection.ID); !strings.HasPrefix(at, "2018-03-01") {
		t.Fatalf("date %s", at)
	}
	j, err := e.st.Job(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if j.Report.SavedAlbumsMissing != 1 {
		t.Fatalf("report %+v", j.Report)
	}
	var linked int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM import_albums WHERE job_id = ? AND item_id IS NOT NULL`, job).Scan(&linked)
	if linked != 2 {
		t.Fatalf("%d albums linked", linked)
	}
	// idempotent: the report does not grow
	e.rebuild(job)
	if j, _ := e.st.Job(ctx, job); j.Report.SavedAlbumsMissing != 1 {
		t.Fatalf("rerun report %+v", j.Report)
	}
}

// A re-run keeps each song of the playlist until its song is linked again
// or definitively skipped or not found: the playlist never shrinks while
// the job downloads, and a cancel leaves it whole.
func TestRerunKeepsUnresolvedEntries(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	snap := Snapshot{Service: "spotify", Tracks: songs("a", "b", "c", "d"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b", "c", "d"}}}}
	j1 := e.job(snap)
	e.arrive(j1, "a", "b", "c", "d")
	e.rebuild(j1)
	pid, _, _ := e.playlist("p1")
	// the user removed d from the playlist between the jobs
	if err := e.m.UpdatePlaylist(ctx, e.uid, pid, music.PlaylistPatch{Order: e.ids("a", "b", "c")}); err != nil {
		t.Fatal(err)
	}

	// the re-run: a arrived, b is still downloading, c was not found, d is
	// waiting (but no longer in the playlist)
	j2 := e.nextJob(j1, snap)
	e.arrive(j2, "a")
	mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'downloading' WHERE job_id = ? AND key = 'b'`, j2)
	mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'not_found' WHERE job_id = ? AND key = 'c'`, j2)
	mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'matched' WHERE job_id = ? AND key = 'd'`, j2)
	e.rebuild(j2)
	if p, _, items := e.playlist("p1"); p != pid || !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("mid-download: %d %v", p, items)
	}
	// b is shown by its old entry, not as a pending row; c and d are pending
	pending, err := e.st.PlaylistPending(ctx, e.uid, pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].Title != "c" || pending[1].Title != "d" {
		t.Fatalf("pending %+v", pending)
	}
	// a cancel leaves it as it is
	if err := e.st.CancelJob(ctx, j2); err != nil {
		t.Fatal(err)
	}
	e.rebuild(j2)
	if _, _, items := e.playlist("p1"); !slices.Equal(items, e.ids("a", "b")) {
		t.Fatalf("after cancel %v", items)
	}

	// the next re-run links b and d: the service's order again
	j3 := e.job(snap)
	e.arrive(j3, "a", "b", "d")
	mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'downloading' WHERE job_id = ? AND key = 'c'`, j3)
	e.rebuild(j3)
	if _, _, items := e.playlist("p1"); !slices.Equal(items, e.ids("a", "b", "d")) {
		t.Fatalf("third run %v", items)
	}
}

// A pending song whose album waits for another download says which one.
func TestPendingNamesTheAwaitedDownload(t *testing.T) {
	e := newRebuildEnv(t)
	ctx := context.Background()
	job := e.job(Snapshot{Service: "spotify", Tracks: songs("a", "b"),
		Playlists: []SnapPlaylist{{ID: "p1", Name: "Дорога", Entries: []string{"a", "b"}}}})
	e.arrive(job, "a")
	e.rebuild(job)
	pid, _, _ := e.playlist("p1")
	mustExec(t, e.st.DB, `INSERT INTO import_groups(job_id, release_group, artist, album, state, wait_title, updated_at)
		VALUES (?, 'rg', 'Кино', 'Сборник', 'waiting_download', 'Кино - Сборник FLAC', '')`, job)
	mustExec(t, e.st.DB, `UPDATE import_tracks SET state = 'matched', group_id = (SELECT id FROM import_groups) WHERE key = 'b'`)
	pending, err := e.st.PlaylistPending(ctx, e.uid, pid)
	if err != nil || len(pending) != 1 || pending[0].State != GroupWaitingDownload || pending[0].WaitingFor != "Кино - Сборник FLAC" {
		t.Fatalf("pending %+v %v", pending, err)
	}
}
