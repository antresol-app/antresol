package jellyfin

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
)

// partRE is the web player's PART_RE (web/src/player/timeline.ts): «cd2»,
// «Part 1», «часть 3»… in a file name.
var partRE = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])(?:cd|dvd|disc|disk|part|pt|часть|диск)[\s._-]*(\d{1,2})(?:$|[\s._\-\])])`)

// partNumber is the part number of a file name (0: none), as partNumber of
// the web player.
func partNumber(name string) int {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	m := partRE.FindStringSubmatch(stem)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// playbackParts are the files of an item played one after another, as the
// web player and the app do (playbackParts): when every file is a distinct
// numbered part, all of them in order; otherwise the files are alternative
// versions and nil is returned.
func playbackParts(fs []library.File) []library.File {
	if len(fs) < 2 {
		return nil
	}
	seen := map[int]bool{}
	for _, f := range fs {
		n := partNumber(filepath.Base(f.Path))
		if n == 0 || seen[n] {
			return nil
		}
		seen[n] = true
	}
	out := append([]library.File(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool {
		return partNumber(filepath.Base(out[i].Path)) < partNumber(filepath.Base(out[j].Path))
	})
	return out
}

// fileDurationMs is a file's duration: its cached probe's, else the scan's.
func fileDurationMs(f library.File) int64 {
	if p, ok := cachedProbe(f); ok && p.DurationMs > 0 {
		return p.DurationMs
	}
	return f.DurationMs
}

// timeline maps a position in file fileID (0: the first) to the item's
// timeline, the one the web and the app store progress on: a multi-part
// movie is one timeline of its parts (the part's start plus the position;
// the duration is the whole), an item with alternative versions is the
// played file's (its own position and duration).
func timeline(fs []library.File, fileID, posMs int64) (absMs, durMs int64) {
	if parts := playbackParts(fs); parts != nil {
		var start, total int64
		found := false
		for _, f := range parts {
			if !found && (f.ID == fileID || fileID == 0) {
				start, found = total, true
			}
			total += fileDurationMs(f)
		}
		return start + posMs, total
	}
	for _, f := range fs {
		if f.ID == fileID {
			return posMs, fileDurationMs(f)
		}
	}
	if len(fs) > 0 {
		return posMs, fileDurationMs(fs[0])
	}
	return posMs, 0
}
