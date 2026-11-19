package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
	"github.com/antresol-app/antresol/internal/library/bookmeta/bookmetatest"
)

// a kind with several folders: each is scanned, a missing one does not stop
// the others, and pruning stays inside each folder
func TestScanSeveralFoldersOfAKind(t *testing.T) {
	ctx := context.Background()
	s, mv, sr := newScanner(t)
	more := filepath.Join(filepath.Dir(mv), "more-movies")
	gone := filepath.Join(filepath.Dir(mv), "not-mounted")
	moreSeries := filepath.Join(filepath.Dir(mv), "anime")
	s.More = map[string][]string{KindFolderMovies: {more, gone}, KindFolderSeries: {moreSeries}}
	touch(t, filepath.Join(mv, "A (2001)", "A (2001).mkv"), "")
	touch(t, filepath.Join(more, "B (2002)", "B (2002).mkv"), "")
	touch(t, filepath.Join(sr, "Show", "Show s01e01.mkv"), "")
	touch(t, filepath.Join(moreSeries, "Other", "Other s01e01.mkv"), "")
	st, err := s.ScanVideo(ctx)
	if err == nil {
		t.Fatal("a missing folder is not an error")
	}
	if st.Movies != 2 || st.Episodes != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	items, total, _ := s.Repo.List(ctx, ListQuery{Kind: KindMovie, Sort: "title"})
	if total != 2 || items[0].Title != "A" || items[1].Title != "B" {
		t.Fatalf("%+v", items)
	}
	// B leaves its folder: the walk of «more» prunes it; A, in the other
	// folder, stays
	if err := os.RemoveAll(filepath.Join(more, "B (2002)")); err != nil {
		t.Fatal(err)
	}
	s.More[KindFolderMovies] = []string{more}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	items, total, _ = s.Repo.List(ctx, ListQuery{Kind: KindMovie})
	if total != 1 || items[0].Title != "A" {
		t.Fatalf("%+v", items)
	}
	if got := s.Roots(KindFolderMovies); len(got) != 2 || got[0] != mv || got[1] != more {
		t.Fatalf("%v", got)
	}
	if got := len(s.AllRoots()); got != 4 {
		t.Fatalf("all roots %d", got)
	}
}

// the extra scanners (music, books…) walk each folder of their kind too
func TestScanAllWalksEveryBookFolder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, b := filepath.Join(root, "ebooks"), filepath.Join(root, "adult-books")
	writeBytes(t, filepath.Join(a, "Author", "One.fb2"), bookmetatest.FB2(t, bookmeta.Meta{Title: "One", Authors: []string{"Author"}}, "utf-8"))
	writeBytes(t, filepath.Join(b, "Author", "Two.fb2"), bookmetatest.FB2(t, bookmeta.Meta{Title: "Two", Authors: []string{"Author"}}, "utf-8"))
	s := &Scanner{Repo: newRepo(t), Ebooks: a, More: map[string][]string{KindFolderEbooks: {b}}}
	st, err := s.ScanAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Books != 2 {
		t.Fatalf("%+v", st)
	}
}

// a title from an adults-only folder is judged 18+: kids never see it,
// unlimited profiles do; an unlisted one is off the shelves; the marks
// follow the folders' at each scan, and parents carry their children's
func TestFolderMarks(t *testing.T) {
	ctx := context.Background()
	s, mv, sr := newScanner(t)
	adult := filepath.Join(filepath.Dir(mv), "adult")
	s.More = map[string][]string{KindFolderMovies: {adult}}
	flags := FolderFlags{Adult: []string{adult}, Unlisted: []string{adult, sr}}
	s.Flags = func() FolderFlags { return flags }
	touch(t, filepath.Join(mv, "Kids (2001)", "Kids (2001).mkv"), "")
	touch(t, filepath.Join(adult, "Grown (2002)", "Grown (2002).mkv"), "")
	touch(t, filepath.Join(sr, "Show", "Show s01e01.mkv"), "")
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	all, _, _ := s.Repo.List(ctx, ListQuery{Sort: "title"})
	by := map[string]Item{}
	for _, it := range all {
		by[it.Title] = it
		s.Repo.db.ExecContext(ctx, `UPDATE items SET rating='0+' WHERE id=?`, it.ID)
	}
	if !by["Grown"].Adult || by["Kids"].Adult || !by["Grown"].Unlisted || !by["Show"].Unlisted || by["Show"].Adult {
		t.Fatalf("%+v", by)
	}
	// rated 0+, still hidden from a kid: the folder makes it 18+
	kid, _, _ := s.Repo.List(ctx, ListQuery{MaxRating: "12+", Sort: "title"})
	if len(kid) != 2 || kid[0].Title != "Kids" {
		t.Fatalf("kid sees %+v", kid)
	}
	if hits, _ := s.Repo.SearchRated(ctx, "Grown", 10, "16+"); len(hits) != 0 {
		t.Fatalf("search %+v", hits)
	}
	if hits, _ := s.Repo.SearchRated(ctx, "Grown", 10, "18+"); len(hits) != 1 {
		t.Fatalf("an 18+ profile: %+v", hits)
	}
	grown, _ := s.Repo.Item(ctx, by["Grown"].ID)
	if ok, _ := s.Repo.Visible(ctx, "12+", grown); ok {
		t.Fatal("Visible lets a kid open it")
	}
	if ok, _ := s.Repo.Visible(ctx, "", grown); !ok {
		t.Fatal("an unlimited profile cannot open it")
	}
	// the episode, its season and its series are all unlisted; so is
	// none of them a shelf's
	show := by["Show"]
	if seasons, _ := s.Repo.Children(ctx, show.ID); len(seasons) != 1 || !seasons[0].Unlisted {
		t.Fatalf("%+v", seasons)
	}
	if rs, _ := s.Repo.RecentSeries(ctx, 10, ""); len(rs) != 0 {
		t.Fatalf("recent series %+v", rs)
	}
	if shelf, _, _ := s.Repo.List(ctx, ListQuery{Sort: "added", Listed: true}); len(shelf) != 1 || shelf[0].Title != "Kids" {
		t.Fatalf("shelf %+v", shelf)
	}
	// the marks taken off: the next scan clears them
	flags = FolderFlags{}
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	grown, _ = s.Repo.Item(ctx, by["Grown"].ID)
	if grown.Adult || grown.Unlisted {
		t.Fatalf("%+v", grown)
	}
	// and given again without a scan (the settings' live change)
	s.Repo.SetFolderFlags(FolderFlags{Adult: []string{adult}})
	if err := s.Repo.SyncFolderFlags(ctx); err != nil {
		t.Fatal(err)
	}
	if grown, _ = s.Repo.Item(ctx, by["Grown"].ID); !grown.Adult {
		t.Fatal("not marked again")
	}
}

// a file stored in a marked folder marks its title at once, before the scan
// ends (no window where a kid sees it)
func TestFolderMarkAtUpsert(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	r.SetFolderFlags(FolderFlags{Adult: []string{"/m/adult"}})
	id, err := r.UpsertEpisode(ctx, EpisodeInput{SeriesTitle: "S", Season: 1, Episode: 1, File: FileInput{Path: "/m/adult/S/S s01e01.mkv"}})
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := r.Item(ctx, id)
	season, _ := r.Item(ctx, ep.ParentID)
	series, _ := r.Item(ctx, season.ParentID)
	if !ep.Adult || !season.Adult || !series.Adult {
		t.Fatalf("%+v %+v %+v", ep, season, series)
	}
	// a folder whose name only starts the same is another folder
	id, _ = r.UpsertMovie(ctx, MovieInput{Title: "M", Files: []FileInput{{Path: "/m/adult-free/M.mkv"}}})
	if m, _ := r.Item(ctx, id); m.Adult {
		t.Fatal("a look-alike folder is marked")
	}
}

// taking a folder out of the library hides what came from it (the files
// stay), and nothing of the other folders
func TestForgetFolder(t *testing.T) {
	ctx := context.Background()
	s, mv, _ := newScanner(t)
	more := filepath.Join(filepath.Dir(mv), "more")
	s.More = map[string][]string{KindFolderMovies: {more}}
	touch(t, filepath.Join(mv, "A (2001)", "A (2001).mkv"), "")
	touch(t, filepath.Join(more, "B (2002)", "B (2002).mkv"), "")
	if _, err := s.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Repo.FolderCount(ctx, more); n != 1 {
		t.Fatalf("count %d", n)
	}
	if _, err := s.Repo.ForgetFolder(ctx, more); err != nil {
		t.Fatal(err)
	}
	items, total, _ := s.Repo.List(ctx, ListQuery{Kind: KindMovie})
	if total != 1 || items[0].Title != "A" {
		t.Fatalf("%+v", items)
	}
	if _, err := os.Stat(filepath.Join(more, "B (2002)", "B (2002).mkv")); err != nil {
		t.Fatal("the file went")
	}
	// the root itself is never forgotten whole
	if n, _ := s.Repo.ForgetFolder(ctx, "/"); n != 0 {
		t.Fatal(n)
	}
}
