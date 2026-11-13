package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PathHints explains configured paths whose top-level folder does not exist
// at all (VAULT_MOVIES_DIR under /tank/media while only /media is there):
// almost always a host path put into a container's settings. One line per
// missing top-level folder, naming the variables that point under it; nil
// when every root exists. exists nil checks the file system.
func (c Config) PathHints(exists func(path string) bool) []string {
	if exists == nil {
		exists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	paths := []struct{ name, path string }{
		{"VAULT_MEDIA_ROOT", c.MediaRoot},
		{"VAULT_DATA_DIR", c.DataDir},
		{"VAULT_MOVIES_DIR", c.Abs(c.MoviesDir)},
		{"VAULT_SERIES_DIR", c.Abs(c.SeriesDir)},
		{"VAULT_MUSIC_DIR", c.Abs(c.MusicDir)},
		{"VAULT_EBOOKS_DIR", c.Abs(c.EbooksDir)},
		{"VAULT_AUDIOBOOKS_DIR", c.Abs(c.AudiobooksDir)},
		{"VAULT_COMICS_DIR", c.Abs(c.ComicsDir)},
		{"VAULT_TRANSCODE_DIR", c.TranscodeDir},
	}
	byRoot := map[string][]string{}
	for _, p := range paths {
		if p.path == "" || !filepath.IsAbs(p.path) || exists(p.path) {
			continue
		}
		root := topLevel(p.path)
		if root == "" || exists(root) {
			continue
		}
		byRoot[root] = append(byRoot[root], p.name)
	}
	roots := make([]string, 0, len(byRoot))
	for r := range byRoot {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	var out []string
	for _, r := range roots {
		out = append(out, fmt.Sprintf("config: %s point under %s, which does not exist here. "+
			"In Docker these are paths inside the container, not on the host: mount the host folder "+
			"(MEDIA_DIR in compose.yml mounts it at /media) and use container paths or the defaults "+
			"(README, \"Install with Docker\")", strings.Join(byRoot[r], ", "), r))
	}
	return out
}

// topLevel returns /a for /a/b/c, "" for /.
func topLevel(p string) string {
	p = filepath.Clean(p)
	rest := strings.TrimPrefix(p, string(filepath.Separator))
	if rest == "" || rest == p {
		return ""
	}
	first, _, _ := strings.Cut(rest, string(filepath.Separator))
	return string(filepath.Separator) + first
}
