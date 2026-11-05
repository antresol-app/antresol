package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/antresol-app/antresol/internal/config"
)

// storageDirs lists the folders whose presence tells the real media disk
// from an empty mount point: the media root, movies and series. Scans stop
// pruning (and another build's engine goes offline) while any is
// missing. Music
// and book folders are deliberately left out: they may be empty, trashed or
// never created, and must not stop the engine; their scanners do not prune
// a missing root, and nothing is pruned while the disk is offline.
// (Folders are never created here: on an unmounted disk that would write to
// the root disk.)
func storageDirs(cfg config.Config) []string {
	return []string{cfg.MediaRoot, cfg.Abs(cfg.MoviesDir), cfg.Abs(cfg.SeriesDir)}
}

// storageOffline reports why the media disk looks unmounted: "" while every
// storage folder is there (library.Scanner.StorageOffline, the disk
// monitor; another build runs the same check). The
// message is a catalog key.
func storageOffline(cfg config.Config) func() string {
	dirs := storageDirs(cfg)
	return func() string {
		for _, d := range dirs {
			if st, err := os.Stat(d); err != nil || !st.IsDir() {
				return fmt.Sprintf("Медиадиск недоступен: нет папки %s", d)
			}
		}
		return ""
	}
}

// dataDiskMinFree: background work writing into DataDir (cleaned
// voice-overs) waits below this much free space, or at 95 % full (the
// downloads' «full» level).
const dataDiskMinFree = 20 << 30

// spaceFor reports whether dir's filesystem has room for background
// writes; a filesystem it cannot read counts as full.
func spaceFor(dir string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return false
	}
	return roomFor(int64(st.Blocks)*int64(st.Bsize), int64(st.Bavail)*int64(st.Bsize))
}

// roomFor: a filesystem of total bytes with avail free has room.
func roomFor(total, avail int64) bool {
	if total <= 0 {
		return false
	}
	return avail >= dataDiskMinFree && float64(total-avail)*100/float64(total) < 95
}
