package runkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// Source is one service a run takes over from. The Service registers
// Jellyfin, Kavita and Audiobookshelf itself; the sources build registers
// its own (Register).
type Source interface {
	ID() string   // "jellyfin": Line.Source, RunRequest.Sources, UserMap keys "<id>:<user>"
	Name() string // "Jellyfin"
	Order() int   // run order: Jellyfin 40, Kavita 50, Audiobookshelf 60 (a build's own sources take other numbers)
	Path() string // where its data is mounted (read only)
	IsDir() bool  // Path is a folder, not a file
	Detail(ctx context.Context, tmp string) (string, error)
	Problem(ctx context.Context) string // an error to show once found ("" = none)
	Import(ctx context.Context, r Run) error
}

// Run is what a source gets to import: the report to add lines to, the
// run's private folder for database snapshots, and the request's choices.
type Run struct {
	Report     *Report
	Tmp        string
	DryRun     bool
	UserMap    map[string]int64
	AdminID    int64
	Checkpoint func() // saves the report (a long import calls it between steps)
}

// UserError is an error of a source whose text is shown to the admin as
// it is (already in the request's language), instead of the general
// «Не удалось прочитать базу».
type UserError struct {
	Msg string
	Err error
}

func (e *UserError) Error() string { return e.Msg + ": " + e.Err.Error() }
func (e *UserError) Unwrap() error { return e.Err }

// Errors of the service; the API maps them with errors.Is.
var (
	ErrNeedsDryRun    = errors.New("migrate: no recent dry run of the same plan")
	ErrNotReady       = errors.New("migrate: the library is not scanned yet")
	ErrBusy           = errors.New("migrate: a migration is running")
	ErrUnknownSource  = errors.New("migrate: unknown source")
	ErrNoSources      = errors.New("migrate: no source chosen")
	ErrUnknownProfile = errors.New("migrate: unknown Vault profile")
	ErrNotFound       = errors.New("migrate: report not found")
)

// SourceStatus is one old service as the «Переезд» screen shows it.
type SourceStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Found  bool   `json:"found"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
	Error  string `json:"error"`
}

// RunRequest starts a run. UserMap: "<source>:<sourceUserID>" → Vault
// profile (missing = «Не переносить»). AdminID comes from the session.
type RunRequest struct {
	Sources []string         `json:"sources"`
	DryRun  bool             `json:"dryRun"`
	PlanID  string           `json:"planId"`
	UserMap map[string]int64 `json:"userMap"`
	AdminID int64            `json:"-"`
}

// Progress is the data of the migrate.progress event, sent after each
// source (Finished on the last one).
type Progress struct {
	ReportID string         `json:"reportId"`
	Source   string         `json:"source"`
	Done     int            `json:"done"`
	Total    int            `json:"total"`
	Counts   map[string]int `json:"counts"`
	Finished bool           `json:"finished"`
}

// PlanHash identifies what a run does: the sources (in any order) and the
// profile mapping. A real run needs a dry run with the same hash.
func PlanHash(sources []string, userMap map[string]int64) string {
	src := slices.Clone(sources)
	sort.Strings(src)
	if userMap == nil {
		userMap = map[string]int64{}
	}
	um, _ := json.Marshal(userMap) // map keys are sorted: canonical
	h := sha256.Sum256([]byte(strings.Join(src, ",") + "\n" + string(um)))
	return hex.EncodeToString(h[:])
}

// PathRule rewrites the prefix From (a path of another service) to To (the
// same folder as Vault sees it).
type PathRule struct{ From, To string }

// PathMap is a set of prefix rewrites; the longest matching From wins.
type PathMap []PathRule

// ParsePathMap parses "from=to,from=to" (VAULT_IMPORT_PATHS). "" is an
// empty map.
func ParsePathMap(s string) (PathMap, error) {
	var m PathMap
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, ok := strings.Cut(part, "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("сопоставление путей %q: нужен вид «откуда=куда»", part)
		}
		m = append(m, PathRule{From: path.Clean(from), To: path.Clean(to)})
	}
	return m, nil
}

// Map rewrites p by the rule with the longest From that is p itself or a
// parent folder of it (whole path segments only); p is returned as is when
// no rule matches.
func (m PathMap) Map(p string) string {
	best := -1
	var rest string
	for i, r := range m {
		var tail string
		switch {
		case p == r.From:
		case r.From == "/" && strings.HasPrefix(p, "/"):
			tail = p[1:]
		case strings.HasPrefix(p, r.From+"/"):
			tail = p[len(r.From)+1:]
		default:
			continue
		}
		if best < 0 || len(r.From) > len(m[best].From) {
			best, rest = i, tail
		}
	}
	if best < 0 {
		return p
	}
	return path.Join(m[best].To, rest)
}
