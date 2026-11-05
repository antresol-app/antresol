// Package mediaimport moves what other media servers know (Jellyfin, Kavita,
// Audiobookshelf: who watched what, where they stopped, favourites) into
// Vault. Importers only read the old services; every outcome is a line of a
// Report, saved after each step. The sources build's migration adds its own
// sources to the same Service.
package mediaimport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
)

// Line statuses.
const (
	StatusPlanned  = "planned"
	StatusImported = "imported"
	StatusSkipped  = "skipped"
	StatusFailed   = "failed"
	StatusWarning  = "warning"
)

// Line is one outcome of a migration: a download, source, follow, progress
// record, user or leftover. Reason is Russian. Ref identifies what was
// touched: an infohash, "movie:<tmdb>"/"series:<tmdb>" or a Vault item id.
type Line struct {
	Source string `json:"source"`
	Kind   string `json:"kind"` // torrent|source|follow|progress|user|leftover
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	Ref    string `json:"ref"`
	Size   int64  `json:"size"`
	// Prev is the item's state in its source before the move
	// (running|stopped); a rollback restarts only "running" ones.
	Prev string `json:"prev,omitempty"`
}

// Report is one run (dry or real). Use it by pointer only: Add may be
// called from the importer while a handler saves it.
type Report struct {
	mu       sync.Mutex `json:"-"`
	ID       string     `json:"id"`
	Sources  []string   `json:"sources"`
	DryRun   bool       `json:"dryRun"`
	PlanID   string     `json:"planId"`
	PlanHash string     `json:"planHash"`
	// RollbackOf: this report gives back what that report moved.
	RollbackOf string         `json:"rollbackOf,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt time.Time      `json:"finishedAt"`
	Lines      []Line         `json:"lines"`
	Counts     map[string]int `json:"counts"` // lines per status, filled by SaveReport
}

// Add appends a line.
func (r *Report) Add(l Line) {
	r.mu.Lock()
	r.Lines = append(r.Lines, l)
	r.mu.Unlock()
}

// LinesWhere copies the lines for which keep is true.
func (r *Report) LinesWhere(keep func(Line) bool) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Line
	for _, l := range r.Lines {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

// Tally counts the lines per status. (The method cannot be called Counts:
// that is the saved field.)
func (r *Report) Tally() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return tally(r.Lines)
}

func tally(lines []Line) map[string]int {
	c := map[string]int{}
	for _, l := range lines {
		c[l.Status]++
	}
	return c
}

// Localize turns the saved Russian texts of r into the language of ctx,
// in place (call it on a report just loaded, before serving it): every
// line's Reason, and its Name when that is a message rather than a title.
func (r *Report) Localize(ctx context.Context) {
	lang := i18n.FromContext(ctx)
	if lang == i18n.RU {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Lines {
		r.Lines[i] = localizeLine(lang, r.Lines[i])
	}
}

// LocalizeLines is Report.Localize for lines served on their own.
func LocalizeLines(ctx context.Context, lines []Line) []Line {
	lang := i18n.FromContext(ctx)
	if lang == i18n.RU {
		return lines
	}
	out := make([]Line, len(lines))
	for i, l := range lines {
		out[i] = localizeLine(lang, l)
	}
	return out
}

func localizeLine(lang string, l Line) Line {
	l.Reason = localizeReason(lang, l.Reason)
	// names are titles and download names; only a whole message is one
	// («ТВ-каналы», «Правила RSS»…)
	if v, ok := i18n.Translation(lang, l.Name); ok && l.Name != "" {
		l.Name = v
	}
	return l
}

// othersRe is the rendering of othersWarning: its list of kinds is joined
// text that i18n.Localize cannot take apart.
var othersRe = regexp.MustCompile(`^(.+?): не переносятся (\d+ \S+) \((.+)\)$`)

func localizeReason(lang, s string) string {
	if m := othersRe.FindStringSubmatch(s); m != nil {
		kinds := strings.Split(m[3], ", ")
		for i, k := range kinds {
			kinds[i] = i18n.LocalizeL(lang, k)
		}
		return i18n.TL(lang, othersKey, m[1], i18n.LocalizeL(lang, m[2]), strings.Join(kinds, ", "))
	}
	return i18n.LocalizeL(lang, s)
}

// NewReportID returns an id like 20261025T193000Z-1a2b3c (UTC time first,
// so ids sort by time).
func NewReportID(now time.Time) string {
	var b [3]byte
	rand.Read(b[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

var reportIDRe = regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{6}$`)

var errBadReportID = errors.New("migrate: bad report id")

// SaveReport writes dir/<id>.json atomically (temporary file + rename),
// filling Counts from Lines.
func SaveReport(dir string, r *Report) error {
	if !reportIDRe.MatchString(r.ID) {
		return errBadReportID
	}
	r.mu.Lock()
	r.Counts = tally(r.Lines)
	b, err := json.MarshalIndent(r, "", "  ")
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, r.ID+".json")); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// LoadReport reads dir/<id>.json.
func LoadReport(dir, id string) (*Report, error) {
	if !reportIDRe.MatchString(id) {
		return nil, errBadReportID
	}
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, err
	}
	r := &Report{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	if r.Counts == nil {
		r.Counts = tally(r.Lines)
	}
	return r, nil
}

// ListReports returns every saved report, newest first, without its lines
// (Counts are filled). A missing dir is an empty list.
func ListReports(dir string) ([]*Report, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Report
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !reportIDRe.MatchString(id) {
			continue
		}
		r, err := LoadReport(dir, id)
		if err != nil {
			continue // a broken file must not hide the others
		}
		r.Lines = nil
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}
