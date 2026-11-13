package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// rootsScanner: every root under base, StorageOffline as cmd/vault has it
// (the media root, movies and series must exist).
func rootsScanner(t *testing.T, base string) *Scanner {
	t.Helper()
	s := &Scanner{Repo: newRepo(t), MediaRoot: base,
		Movies: filepath.Join(base, "movies"), Series: filepath.Join(base, "series"), Music: filepath.Join(base, "music"),
		Ebooks: filepath.Join(base, "books", "ebooks"), Audiobooks: filepath.Join(base, "books", "audiobooks"),
		Comics: filepath.Join(base, "books", "manga"), ArtDir: filepath.Join(t.TempDir(), "images")}
	s.StorageOffline = func() string {
		for _, d := range []string{s.MediaRoot, s.Movies, s.Series} {
			if st, err := os.Stat(d); err != nil || !st.IsDir() {
				return "нет папки " + d
			}
		}
		return ""
	}
	return s
}

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func allRoots(s *Scanner) []string {
	return []string{s.Movies, s.Series, s.Music, s.Ebooks, s.Audiobooks, s.Comics}
}

func TestScanAllCreatesMissingRootsOnFreshInstall(t *testing.T) {
	s := rootsScanner(t, t.TempDir())
	if _, err := s.ScanAll(context.Background()); err != nil {
		t.Fatalf("fresh install scan: %v", err)
	}
	for _, d := range allRoots(s) {
		if !isDir(d) {
			t.Fatalf("%s not created", d)
		}
	}
}

func TestScanAllKeepsMissingVideoRootsOfKnownLibrary(t *testing.T) {
	// the disk is unmounted: the mount point is there, its folders are not.
	// Creating movies/ would make the empty disk look online and prune it.
	base := t.TempDir()
	s := rootsScanner(t, base)
	touch(t, filepath.Join(s.Movies, "Брат (1997)", "Брат (1997).mkv"), "x")
	if err := os.MkdirAll(s.Series, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if st, _ := s.ScanAll(ctx); st.Movies != 1 {
		t.Fatalf("first scan %+v", st)
	}
	for _, d := range []string{s.Movies, s.Series, s.Music, s.Ebooks} {
		if err := os.RemoveAll(d); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = s.ScanAll(ctx)
	for _, d := range []string{s.Movies, s.Series, s.Music, s.Ebooks} {
		if isDir(d) {
			t.Fatalf("%s created while the disk looks unmounted", d)
		}
	}
}

func TestScanAllCreatesMissingBookRootsWhileOnline(t *testing.T) {
	base := t.TempDir()
	s := rootsScanner(t, base)
	touch(t, filepath.Join(s.Movies, "Брат (1997)", "Брат (1997).mkv"), "x")
	if err := os.MkdirAll(s.Series, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = s.ScanAll(ctx)
	if err := os.RemoveAll(filepath.Join(base, "books")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanAll(ctx); err != nil {
		t.Fatalf("online scan: %v", err)
	}
	for _, d := range []string{s.Ebooks, s.Audiobooks, s.Comics} {
		if !isDir(d) {
			t.Fatalf("%s not created", d)
		}
	}
}

func TestScanAllNeverCreatesTheMediaRootOrOutsideIt(t *testing.T) {
	parent := t.TempDir()
	s := rootsScanner(t, filepath.Join(parent, "tank", "media"))
	outside := filepath.Join(parent, "elsewhere", "music")
	s.Music = outside
	_, _ = s.ScanAll(context.Background())
	if isDir(s.MediaRoot) || isDir(filepath.Join(parent, "tank")) {
		t.Fatal("media root created")
	}
	s.MediaRoot = parent
	_, _ = s.ScanAll(context.Background())
	if !isDir(filepath.Join(parent, "elsewhere", "music")) {
		// under parent now: allowed
		t.Fatal("root under the media root not created")
	}
	s2 := rootsScanner(t, filepath.Join(parent, "m2"))
	if err := os.MkdirAll(s2.MediaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	far := filepath.Join(t.TempDir(), "far", "music")
	s2.Music = far
	_, _ = s2.ScanAll(context.Background())
	if isDir(far) {
		t.Fatal("root outside the media root created")
	}
}
