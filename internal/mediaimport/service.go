package mediaimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
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

// planTTL: how long a dry run allows the real run.
const planTTL = time.Hour

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

// Service runs «Переезд»: what the old services have, a dry run and the
// run itself (in the background). Create it with NewService.
type Service struct {
	Profiles *auth.Store // Vault profiles (proposals, user map check)
	Library  *library.Repo
	Progress *progress.Store

	// Jellyfin, Kavita and ABS are the databases of the media servers.
	Jellyfin, Kavita, ABS string
	ImportPaths           PathMap // Kavita and Audiobookshelf paths → Vault's

	// ReportDir holds the reports (DataDir/migrate); TmpDir the run
	// folders with database snapshots (DataDir/tmp/migrate), removed after
	// each run and on start.
	ReportDir, TmpDir string
	// BaseCtx is the server's: a run survives the browser leaving and ends
	// with the server.
	BaseCtx context.Context
	Now     func() time.Time
	Bus     *events.Bus
	// LibraryReady: false (with the reason) until the library was scanned
	// once — matching by path needs the library; nil = ready.
	LibraryReady func() (bool, string)
	Log          *slog.Logger

	// ExtraLeftovers: what the sources build's old services have that does not
	// move; running are the sources of the run (nil on the screen). nil:
	// none. LeftoverFiles are the files it reads: the screen's cache is
	// dropped when one changes.
	ExtraLeftovers func(ctx context.Context, tmp string, running []string) []Line
	LeftoverFiles  []string

	mu        sync.Mutex
	running   string // report id of the run in progress
	switching bool
	runs      sync.WaitGroup

	srcMu   sync.RWMutex
	sources []Source // by Order

	scans atomic.Int64 // reads of the old databases for the screen
	cache screenCache
}

// NewService removes the run folders an earlier process left (database
// snapshots hold tracker passwords and cookies), registers Jellyfin,
// Kavita and Audiobookshelf and returns s.
func NewService(s *Service) *Service {
	if s.TmpDir != "" {
		old, _ := filepath.Glob(filepath.Join(s.TmpDir, "run-*"))
		for _, p := range old {
			if err := os.RemoveAll(p); err != nil {
				s.log().Warn("remove old run folder", "dir", p, "err", err)
			}
		}
	}
	s.Register(&dbSource{s: s, id: SourceJellyfin, name: "Jellyfin", order: 40, path: s.Jellyfin, imp: ImportJellyfin})
	s.Register(&dbSource{s: s, id: SourceKavita, name: "Kavita", order: 50, path: s.Kavita, imp: ImportKavita})
	s.Register(&dbSource{s: s, id: SourceABS, name: "Audiobookshelf", order: 60, path: s.ABS, imp: ImportABS})
	return s
}

// Register adds a source (at start-up, before the service serves); a
// source with the same ID replaces the earlier one.
func (s *Service) Register(src Source) {
	s.srcMu.Lock()
	defer s.srcMu.Unlock()
	s.sources = slices.DeleteFunc(s.sources, func(o Source) bool { return o.ID() == src.ID() })
	s.sources = append(s.sources, src)
	sort.SliceStable(s.sources, func(i, j int) bool { return s.sources[i].Order() < s.sources[j].Order() })
}

func (s *Service) list() []Source {
	s.srcMu.RLock()
	defer s.srcMu.RUnlock()
	return slices.Clone(s.sources)
}

func (s *Service) source(id string) Source {
	for _, src := range s.list() {
		if src.ID() == id {
			return src
		}
	}
	return nil
}

// Scans counts how many times the screen's status (Discover, Leftovers,
// Users) read the old services.
func (s *Service) Scans() int64 { return s.scans.Load() }

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Clock is the service's time (UTC).
func (s *Service) Clock() time.Time { return s.now() }

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return impLog()
}

// Logger is the service's logger.
func (s *Service) Logger() *slog.Logger { return s.log() }

// DefaultPaths lays the media servers' databases out under importDir
// (/import in the container, every folder mounted read-only).
func DefaultPaths(importDir string) (jellyfin, kavita, abs string) {
	at := func(p ...string) string { return filepath.Join(append([]string{importDir}, p...)...) }
	return at("jellyfin", "jellyfin.db"), at("kavita", "kavita.db"), at("audiobookshelf", "absdatabase.sqlite")
}

func impLog() *slog.Logger { return slog.Default().With("component", "migrate") }

func (s *Service) baseCtx() context.Context {
	if s.BaseCtx != nil {
		return s.BaseCtx
	}
	return context.Background()
}

// Ready says whether a run may start (the library was scanned).
func (s *Service) Ready() (bool, string) {
	if s.LibraryReady == nil {
		return true, ""
	}
	return s.LibraryReady()
}

// Wait returns once no run is left (BaseCtx ended: they stop at the next
// step, their report saved).
func (s *Service) Wait() { s.runs.Wait() }

// Running is the id of the report being written, "" when idle.
func (s *Service) Running() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Begin takes the service for something that is not a run of Start (a
// rollback, id = its report; «Перейти на Vault», id ""): ErrBusy while a
// run, rollback or switch holds it. Call end when done.
func (s *Service) Begin(id string) (end func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != "" || s.switching {
		return nil, ErrBusy
	}
	if id == "" {
		s.switching = true
		return func() {
			s.mu.Lock()
			s.switching = false
			s.mu.Unlock()
		}, nil
	}
	s.running = id
	return func() { s.idle(id) }, nil
}

// Go runs f in the background on BaseCtx; Wait waits for it.
func (s *Service) Go(f func(ctx context.Context)) {
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		f(s.baseCtx())
	}()
}

func (s *Service) idle(id string) {
	s.mu.Lock()
	if s.running == id {
		s.running = ""
	}
	s.mu.Unlock()
}

// Save writes the report; a failure is logged.
func (s *Service) Save(rep *Report) {
	if err := SaveReport(s.ReportDir, rep); err != nil {
		s.log().Error("save report", "id", rep.ID, "err", err)
	}
}

// Publish sends the migrate.progress event.
func (s *Service) Publish(rep *Report, source string, done, total int) {
	if s.Bus == nil {
		return
	}
	s.Bus.Publish(events.Event{Type: events.MigrateProgress, Data: Progress{ReportID: rep.ID, Source: source, Done: done, Total: total,
		Counts: rep.Tally(), Finished: !rep.FinishedAt.IsZero()}})
}

// RunTmp makes a private folder for snapshots.
func (s *Service) RunTmp() (string, error) {
	base := s.TmpDir
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "run-*")
}

// --- what is there ---

// Discover looks at each old service: found or not, and what it has.
// While none of their files changed, the last look is reused.
func (s *Service) Discover(ctx context.Context) []SourceStatus {
	srcs := s.list()
	paths := make([]string, 0, len(srcs))
	for _, src := range srcs {
		paths = append(paths, src.Path())
	}
	key := fingerprint(paths...)
	for _, src := range srcs {
		key += src.ID() + "\n"
	}
	key += i18n.FromContext(ctx)
	if v, ok := s.cache.get("discover", key); ok {
		return slices.Clone(v.([]SourceStatus))
	}
	s.scans.Add(1)
	out := s.discover(ctx, srcs)
	if ctx.Err() == nil {
		s.cache.put("discover", key, slices.Clone(out))
	}
	return out
}

func (s *Service) discover(ctx context.Context, srcs []Source) []SourceStatus {
	tmp, err := s.RunTmp()
	if err != nil {
		s.log().Warn("discover: temporary folder", "err", err)
	} else {
		defer os.RemoveAll(tmp)
	}
	out := make([]SourceStatus, 0, len(srcs))
	for _, src := range srcs {
		st := SourceStatus{ID: src.ID(), Name: src.Name(), Path: src.Path()}
		if !present(st.Path, src.IsDir()) {
			st.Detail = i18n.T(ctx, "Не найден — смонтируйте %s", st.Path)
			out = append(out, st)
			continue
		}
		st.Found = true
		detail, err := src.Detail(ctx, tmp)
		if err != nil {
			if ctx.Err() != nil {
				return out
			}
			s.log().Warn("discover", "source", src.ID(), "err", err)
			var ue *UserError
			if errors.As(err, &ue) {
				st.Error = ue.Msg
			} else {
				st.Error = i18n.T(ctx, "Не удалось прочитать базу %s", st.Name)
			}
		}
		st.Detail = detail
		if st.Error == "" {
			st.Error = src.Problem(ctx)
		}
		out = append(out, st)
	}
	return out
}

func present(p string, dir bool) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir() == dir
}

// dbSource is Jellyfin, Kavita or Audiobookshelf: a database of progress.
type dbSource struct {
	s     *Service
	id    string
	name  string
	order int
	path  string
	imp   func(context.Context, ProgressOptions, LibraryLookup, ProgressSink, *Report) error
}

func (d *dbSource) ID() string                     { return d.id }
func (d *dbSource) Name() string                   { return d.name }
func (d *dbSource) Order() int                     { return d.order }
func (d *dbSource) Path() string                   { return d.path }
func (d *dbSource) IsDir() bool                    { return false }
func (d *dbSource) Problem(context.Context) string { return "" }
func (d *dbSource) Detail(ctx context.Context, tmp string) (string, error) {
	users, rows, epubs, err := progressUsers(ctx, tmp, d.id, d.path)
	if err != nil {
		return "", err
	}
	if d.id != SourceJellyfin && rows == 0 {
		return i18n.T(ctx, reasonNoData), nil
	}
	det := i18n.Plural(ctx, users, "пользователь", "пользователя", "пользователей") + ", " + i18n.Plural(ctx, rows, "отметка", "отметки", "отметок")
	if epubs > 0 {
		det = i18n.T(ctx, "%s · место внутри EPUB не переносится (только процент)", det)
	}
	return det, nil
}

func (d *dbSource) Import(ctx context.Context, r Run) error {
	return d.imp(ctx, d.s.progressOptions(r.Tmp, r.UserMap, r.DryRun), d.s.Library, d.s.Progress, r.Report)
}

// progressUsers counts the users with marks to carry over and the marks
// (the rows the importer reads); epubs: Kavita marks in EPUB books, whose
// place inside the book does not move.
func progressUsers(ctx context.Context, tmp, id, path string) (users, rows, epubs int, err error) {
	d, err := snapshotDB(tmp, path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer d.close()
	seen := map[string]bool{}
	switch id {
	case SourceJellyfin:
		data, err := readJellyfin(ctx, d.DB)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, r := range append(data.progress, data.favorites...) {
			seen[r.userID] = true
			rows++
		}
	case SourceKavita:
		list, err := readKavita(ctx, d.DB)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, r := range list {
			seen[strconv.FormatInt(r.userID, 10)] = true
			rows++
			if strings.HasSuffix(strings.ToLower(r.filePath), ".epub") {
				epubs++
			}
		}
	case SourceABS:
		list, err := readABS(ctx, d.DB)
		if err != nil {
			return 0, 0, 0, err
		}
		for _, r := range list {
			seen[r.userID] = true
			rows++
		}
	}
	return len(seen), rows, epubs, nil
}

// Leftovers is what the old services have that does not move (the last
// look while their files are unchanged).
func (s *Service) Leftovers(ctx context.Context) []Line {
	key := fingerprint(append([]string{s.Jellyfin}, s.LeftoverFiles...)...) + "\n" + i18n.FromContext(ctx)
	if v, ok := s.cache.get("leftovers", key); ok {
		return slices.Clone(v.([]Line))
	}
	s.scans.Add(1)
	tmp, err := s.RunTmp()
	if err != nil {
		s.log().Warn("leftovers: temporary folder", "err", err)
		return nil
	}
	defer os.RemoveAll(tmp)
	out := s.leftovers(ctx, tmp, nil)
	if ctx.Err() == nil {
		s.cache.put("leftovers", key, slices.Clone(out))
	}
	return out
}

// leftovers lists what stays behind: the sources build's (ExtraLeftovers), then
// Jellyfin's TV channels — unless Jellyfin is among running, whose importer
// writes that line itself.
func (s *Service) leftovers(ctx context.Context, tmp string, running []string) []Line {
	var out []Line
	if s.ExtraLeftovers != nil {
		out = s.ExtraLeftovers(ctx, tmp, running)
	}
	if slices.Contains(running, SourceJellyfin) {
		return out
	}
	lang := i18n.FromContext(ctx)
	if n, err := CountIn(ctx, tmp, s.Jellyfin, TableCount{"BaseItems", "Type LIKE '%LiveTvChannel'"}); err != nil {
		impLog().Warn("leftovers", "source", SourceJellyfin, "err", err)
		out = append(out, Line{Source: SourceJellyfin, Kind: "leftover", Name: "Jellyfin", Status: StatusWarning,
			Reason: i18n.TL(lang, "Не удалось проверить %s — посмотрите там вручную, что не переносится", "Jellyfin")})
	} else if n > 0 {
		out = append(out, JellyfinChannelsLine(lang, n))
	}
	return out
}

// Users proposes the Vault profile for each user of Jellyfin, Kavita and
// Audiobookshelf (the last look while their databases and the Vault
// profiles are unchanged).
func (s *Service) Users(ctx context.Context) ([]UserProposal, error) {
	key := fingerprint(s.Jellyfin, s.Kavita, s.ABS) + "\n" + i18n.FromContext(ctx) + "\n"
	if s.Profiles != nil {
		profiles, err := s.Profiles.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, u := range profiles {
			key += strconv.FormatInt(u.ID, 10) + "=" + u.Name + "\n"
		}
	}
	if v, ok := s.cache.get("users", key); ok {
		return slices.Clone(v.([]UserProposal)), nil
	}
	s.scans.Add(1)
	tmp, err := s.RunTmp()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	list, err := ProposeUsers(ctx, s.progressOptions(tmp, nil, true), s.Profiles)
	if list == nil {
		list = []UserProposal{}
	}
	if err == nil {
		s.cache.put("users", key, slices.Clone(list))
	}
	return list, err
}

func (s *Service) progressOptions(tmp string, um map[string]int64, dry bool) ProgressOptions {
	return ProgressOptions{JellyfinDB: s.Jellyfin, KavitaDB: s.Kavita, ABSDB: s.ABS,
		Tmp: tmp, Paths: s.ImportPaths, UserMap: um, DryRun: dry}
}

// --- runs ---

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

// checkSources checks the chosen sources and puts them in run order.
func (s *Service) checkSources(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, ErrNoSources
	}
	want := map[string]bool{}
	for _, id := range in {
		if s.source(id) == nil {
			return nil, ErrUnknownSource
		}
		want[id] = true
	}
	var out []string
	for _, src := range s.list() {
		if want[src.ID()] {
			out = append(out, src.ID())
		}
	}
	return out, nil
}

// Start checks req and runs it in the background (the server's context: a
// browser that leaves does not stop it); the report id comes back at once.
// A real run needs a finished dry run of the same plan from the last hour;
// no run starts before the library was scanned once.
func (s *Service) Start(ctx context.Context, req RunRequest) (string, error) {
	sources, err := s.checkSources(req.Sources)
	if err != nil {
		return "", err
	}
	um := map[string]int64{}
	for k, v := range req.UserMap {
		if v <= 0 {
			continue // «Не переносить»
		}
		if s.Profiles != nil {
			if _, err := s.Profiles.Get(ctx, v); errors.Is(err, auth.ErrNotFound) {
				return "", ErrUnknownProfile
			} else if err != nil {
				return "", err
			}
		}
		um[k] = v
	}
	if ok, _ := s.Ready(); !ok {
		return "", ErrNotReady
	}
	hash := PlanHash(sources, um)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != "" || s.switching {
		return "", ErrBusy
	}
	if !req.DryRun {
		if err := s.checkPlan(req.PlanID, hash); err != nil {
			return "", err
		}
	}
	now := s.now()
	rep := &Report{ID: NewReportID(now), Sources: sources, DryRun: req.DryRun, PlanHash: hash, StartedAt: now, Lines: []Line{}}
	if !req.DryRun {
		rep.PlanID = req.PlanID
	}
	if err := SaveReport(s.ReportDir, rep); err != nil {
		return "", err
	}
	s.running = rep.ID
	s.cache.forget() // the screen looks afresh after a run
	s.runs.Add(1)
	go s.run(rep, um, req.AdminID)
	return rep.ID, nil
}

// checkPlan: id is a finished dry run of the same plan, at most an hour old.
func (s *Service) checkPlan(id, hash string) error {
	if id == "" {
		return ErrNeedsDryRun
	}
	plan, err := LoadReport(s.ReportDir, id)
	if err != nil || !plan.DryRun || plan.RollbackOf != "" || plan.FinishedAt.IsZero() || plan.PlanHash != hash {
		return ErrNeedsDryRun
	}
	if age := s.now().Sub(plan.FinishedAt); age > planTTL || age < -time.Minute {
		return ErrNeedsDryRun
	}
	return nil
}

func (s *Service) run(rep *Report, um map[string]int64, adminID int64) {
	defer s.runs.Done()
	defer s.idle(rep.ID)
	ctx := s.baseCtx()
	log := s.log().With("report", rep.ID)
	tmp, err := s.RunTmp()
	if err != nil {
		log.Error("run folder", "err", err)
		rep.Add(Line{Kind: "leftover", Name: "Vault", Status: StatusFailed, Reason: "Не удалось создать временную папку переноса — ничего не перенесено"})
		rep.FinishedAt = s.now()
		s.Save(rep)
		s.Publish(rep, "", 0, len(rep.Sources))
		return
	}
	defer os.RemoveAll(tmp)

	// what does not move comes first; the TV channels line is the Jellyfin
	// importer's when it runs
	for _, l := range s.leftovers(ctx, tmp, rep.Sources) {
		rep.Add(l)
	}
	s.Save(rep)

	for i, id := range rep.Sources {
		if ctx.Err() != nil {
			break
		}
		if err := s.importOne(ctx, id, rep, tmp, um, adminID); err != nil && ctx.Err() == nil {
			name := id
			if src := s.source(id); src != nil {
				name = src.Name()
			}
			log.Error("import", "source", id, "err", err)
			rep.Add(Line{Source: id, Name: name, Status: StatusFailed,
				Reason: fmt.Sprintf("Перенос из %s прервался: что-то пошло не так (подробности в журнале Antresol)", name)})
		}
		if i == len(rep.Sources)-1 && ctx.Err() == nil {
			rep.FinishedAt = s.now()
		}
		s.Save(rep)
		s.Publish(rep, id, i+1, len(rep.Sources))
	}
	if ctx.Err() != nil {
		log.Warn("run interrupted", "err", ctx.Err()) // FinishedAt stays empty: «Прерван»
	}
}

func (s *Service) importOne(ctx context.Context, id string, rep *Report, tmp string, um map[string]int64, adminID int64) error {
	src := s.source(id)
	if src == nil {
		return ErrUnknownSource
	}
	return src.Import(ctx, Run{Report: rep, Tmp: tmp, DryRun: rep.DryRun, UserMap: um, AdminID: adminID,
		Checkpoint: func() { s.Save(rep) }})
}

// --- reports ---

// Report loads a saved report.
func (s *Service) Report(id string) (*Report, error) {
	r, err := LoadReport(s.ReportDir, id)
	if errors.Is(err, errBadReportID) || errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return r, err
}

// Reports is the history, newest first, without the lines.
func (s *Service) Reports() ([]*Report, error) { return ListReports(s.ReportDir) }
