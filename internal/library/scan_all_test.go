package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanAllNoDeadlock(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.ScanAll(ctx); done <- err }()
	select {
	case err := <-done:
		if errors.Is(err, context.DeadlineExceeded) || err != nil {
			t.Fatalf("ScanAll: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ScanAll deadlocked")
	}
	if _, err := s.ScanVideo(ctx); err != nil { // the lock was released
		t.Fatal(err)
	}
}

func TestScanAllEmptyRealRoots(t *testing.T) {
	root := t.TempDir()
	dir := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	s := &Scanner{Repo: newRepo(t), Movies: dir("movies"), Series: dir("series"), Music: dir("music"),
		Ebooks: dir("books/ebooks"), Audiobooks: dir("books/audiobooks"), Comics: dir("books/manga"),
		ArtDir: dir("data/images"), ComicsCache: dir("data/comics-cache")}
	movie := filepath.Join(s.Movies, "Брат (1997)", "Брат (1997).mkv")
	touch(t, movie, "x")
	ctx := context.Background()
	for run := 0; run < 2; run++ {
		st, err := s.ScanAll(ctx)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if st.Movies != 1 || st.Albums+st.Tracks+st.Books+st.Audiobooks+st.Comics != 0 {
			t.Fatalf("run %d: %+v", run, st)
		}
		if _, _, err := s.Repo.ItemByPath(ctx, movie); err != nil {
			t.Fatalf("run %d: movie hidden: %v", run, err)
		}
	}
}

// The media disk is unmounted: its mount point is an empty folder, so the
// walks of the music and books roots succeed with nothing found. While the
// download engine reports the storage offline, ScanAll must not mark any
// track or book missing; once the disk is back online the prune runs again.
func TestScanAllNoCleanupWhenStorageOffline(t *testing.T) {
	base := t.TempDir()
	music, ebooks := filepath.Join(base, "music"), filepath.Join(base, "books", "ebooks")
	offline := "нет папки " + music
	s := &Scanner{Repo: newRepo(t), Music: music, Ebooks: ebooks, ArtDir: filepath.Join(base, "images"),
		StorageOffline: func() string { return offline }}
	writeAudio(t, filepath.Join(music, "Кино - Группа крови (1988)", "01.flac"), map[string]string{"artist": "Кино", "album": "Группа крови", "track": "1"}, 1, nil)
	writeBytes(t, filepath.Join(ebooks, "Книга.pdf"), []byte("%PDF"))
	ctx := context.Background()
	offline = ""
	if st, err := s.ScanAll(ctx); err != nil || st.Tracks != 1 || st.Books != 1 {
		t.Fatalf("first scan %+v: %v", st, err)
	}
	for _, root := range []string{music, ebooks} {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	missing := func() (files, items int) {
		t.Helper()
		if err := s.Repo.db.QueryRow(`SELECT (SELECT COUNT(*) FROM files WHERE missing_since IS NOT NULL),
			(SELECT COUNT(*) FROM items WHERE missing_since IS NOT NULL)`).Scan(&files, &items); err != nil {
			t.Fatal(err)
		}
		return
	}
	offline = "нет папки " + music
	if _, err := s.ScanAll(ctx); err != nil {
		t.Fatalf("offline scan: %v", err)
	}
	if f, i := missing(); f != 0 || i != 0 {
		t.Fatalf("storage offline: %d files and %d items marked missing", f, i)
	}
	offline = ""
	if _, err := s.ScanAll(ctx); err != nil {
		t.Fatalf("online scan: %v", err)
	}
	if f, i := missing(); f != 2 || i == 0 {
		t.Fatalf("storage online: %d files and %d items marked missing, want 2 files and the items", f, i)
	}
}
