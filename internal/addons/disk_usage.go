package addons

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/files"
)

// maxDiskSegments bounds the segments one addon adds to the disk meter.
const maxDiskSegments = 8

// maxDiskLabel bounds a segment's name, in characters.
const maxDiskLabel = 60

var diskSegmentID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// DiskUsage asks every addon declaring disk.usage (Each: in parallel, the
// hook's 2 s, down ones left out) for its disk segments, as the file
// browser's usage report takes them: id addon:<addonId>:<id>. A segment
// is left out when its id is not ^[a-z0-9][a-z0-9-]{0,31}$ or repeats, or
// it has no name in either language; past maxDiskSegments the rest of an
// addon's are. Names are cut to maxDiskLabel characters. The folder is
// checked by the report (files.Usage).
func (h *Hooks) DiskUsage(ctx context.Context) []files.ExtraSegment {
	var out []files.ExtraSegment
	h.Each(ctx, "disk.usage", nil, nil, func(id string, raw json.RawMessage) {
		var u addonkit.DiskUsage
		if err := json.Unmarshal(raw, &u); err != nil {
			h.log().Warn("addon hook: left out", "addon", id, "hook", "disk.usage", "err", err)
			return
		}
		seen, n := map[string]bool{}, 0
		for _, s := range u.Segments {
			ru, en := diskLabel(s.Label.RU), diskLabel(s.Label.EN)
			if !diskSegmentID.MatchString(s.ID) || seen[s.ID] || ru == "" && en == "" {
				continue
			}
			if n++; n > maxDiskSegments {
				break
			}
			seen[s.ID] = true
			label := map[string]string{}
			if ru != "" {
				label["ru"] = ru
			}
			if en != "" {
				label["en"] = en
			}
			out = append(out, files.ExtraSegment{ID: "addon:" + id + ":" + s.ID, Label: label, Dir: s.Dir, Bytes: max(s.Bytes, 0)})
		}
	})
	return out
}

// diskLabel is a segment name without control characters, trimmed and cut
// to maxDiskLabel characters.
func diskLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > maxDiskLabel {
		s = strings.TrimSpace(string(r[:maxDiskLabel]))
	}
	return s
}
