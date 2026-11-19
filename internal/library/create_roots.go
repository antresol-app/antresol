package library

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// createMissingRoots makes the library folders that do not exist yet, empty
// and with their parents, so a new install needs no mkdir and the scan has
// no "no such file" to report. Only under an existing MediaRoot: the media
// root itself is never created (a host path in a container, or a disk that
// is not there, must stay an error).
//
// Missing movies and series folders are also how an unmounted disk is told
// from an empty one (StorageOffline): creating them on a known library
// would make an empty mount point look online and prune everything. So
// video roots are created only while the library has no file at all, and
// the other roots only while the storage is online.
func (s *Scanner) createMissingRoots(ctx context.Context) {
	if s.MediaRoot == "" || !dirExists(s.MediaRoot) {
		return
	}
	video := append(s.Roots(KindFolderMovies), s.Roots(KindFolderSeries)...)
	var other []string
	for _, k := range []string{KindFolderMusic, KindFolderEbooks, KindFolderAudiobooks, KindFolderComics} {
		other = append(other, s.Roots(k)...)
	}
	if s.libraryEmpty(ctx) {
		s.mkRoots(video)
	}
	if s.StorageOffline == nil || s.StorageOffline() == "" {
		s.mkRoots(other)
	}
}

func (s *Scanner) mkRoots(roots []string) {
	base := filepath.Clean(s.MediaRoot)
	for _, r := range roots {
		if r == "" || dirExists(r) {
			continue
		}
		rel, err := filepath.Rel(base, filepath.Clean(r))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if err := os.MkdirAll(r, 0o755); err != nil {
			log.Printf("library: create %s: %v", r, err)
			continue
		}
		log.Printf("library: created the empty folder %s", r)
	}
}

// libraryEmpty: no file of any kind is indexed, so nothing can be pruned.
// An unreadable database counts as not empty.
func (s *Scanner) libraryEmpty(ctx context.Context) bool {
	if s.Repo == nil {
		return false
	}
	return LibraryEmpty(ctx, s.Repo.db)
}

// LibraryEmpty is the scanner's rule for when a missing video folder may be
// made (and when the settings may make one): no file of any kind is
// indexed. An unreadable database counts as not empty.
func LibraryEmpty(ctx context.Context, db *sql.DB) bool {
	if db == nil {
		return false
	}
	var any bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM files)`).Scan(&any); err != nil {
		return false
	}
	return !any
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
