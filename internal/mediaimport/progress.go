package mediaimport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
	"github.com/antresol-app/antresol/internal/progress"
)

// Line.Source of the progress importers.
const (
	SourceJellyfin = "jellyfin"
	SourceKavita   = "kavita"
	SourceABS      = "audiobookshelf"
)

// ProgressOptions configure ImportJellyfin, ImportKavita, ImportABS and
// ProposeUsers.
type ProgressOptions struct {
	JellyfinDB, KavitaDB, ABSDB string
	// Tmp is the run's private folder for database snapshots ("" = a
	// temporary folder of its own).
	Tmp   string
	Paths PathMap
	// UserMap picks the Vault profile of a user of an old service, keyed
	// "<source>:<sourceUserID>"; a user without a key is not carried over
	// («Не переносить»).
	UserMap map[string]int64
	DryRun  bool
	// Remotes are the connections kept on the page, by source id: a source
	// with one is read over its API, its database file is not. HTTP is the
	// client for them (nil: the default, see remoteHTTP).
	Remotes map[string]Remote
	HTTP    *http.Client
}

// LibraryLookup is the part of *library.Repo the progress importers use.
type LibraryLookup interface {
	ItemByPath(ctx context.Context, path string) (library.Item, library.File, error)
	ItemByTMDB(ctx context.Context, kind string, tmdbID int64) (library.Item, error)
	Children(ctx context.Context, id int64) ([]library.Item, error)
	FilesUnder(ctx context.Context, dir string) ([]library.File, error)
	FilesOf(ctx context.Context, itemID int64) ([]library.File, error)
	Item(ctx context.Context, id int64) (library.Item, error)
}

// ProgressSink is the part of *progress.Store the importers write to. It
// has no MarkWatched on purpose: that writes "now" without the stale-write
// rule, so an import could overwrite fresher progress.
type ProgressSink interface {
	SaveAt(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time) error
	SaveListenAt(ctx context.Context, userID, itemID, posMs, durMs int64, at time.Time) error
	SaveLocator(ctx context.Context, userID, itemID int64, locator string, fraction float64, at time.Time) error
	Get(ctx context.Context, userID, itemID int64) (progress.Entry, error)
	AddToWatchlist(ctx context.Context, userID, itemID int64) error
	Watchlist(ctx context.Context, userID int64) ([]int64, error)
}

const (
	reasonNoProfile   = "Профиль не выбран"
	reasonVaultNewer  = "В Antresol позиция новее"
	reasonInVault     = "Уже в Antresol"
	reasonFutureTime  = "Время в источнике в будущем — перенесено без даты"
	reasonInWatchlist = "Уже в «Хочу посмотреть»"
	reasonNoData      = "Нет данных для переноса"
	reasonToWatchlist = "В «Хочу посмотреть»"
	reasonByPercent   = "Позиция внутри книги — по проценту"
	reasonNoPosition  = "Нет позиции для переноса"
)

func notFoundInVault(name, path string) string {
	return fmt.Sprintf("Не найдено в Antresol: %s (%s)", name, path)
}

func itemRef(id int64) string { return "item:" + strconv.FormatInt(id, 10) }

// srcDB, errNoSourceDB and snapshotDB are runkit's (one copy for the
// importers here and the counts there).
type srcDB = runkit.SrcDB

var (
	errNoSourceDB = runkit.ErrNoSourceDB
	snapshotDB    = runkit.SnapshotDB
)

// loadFailed adds the line of a source an importer could not read: a
// missing database is skipped; an API problem fails with its own text; an
// unreadable database (a snapshot that failed, tables not as expected)
// fails. It returns ctx's error, else nil.
func loadFailed(ctx context.Context, source, app string, rep *Report, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var re *RemoteError
	switch {
	case errors.Is(err, errNoSourceDB):
		rep.Add(Line{Source: source, Kind: "progress", Name: app, Status: StatusSkipped, Reason: fmt.Sprintf("%s не найден", app)})
	case errors.As(err, &re):
		impLog().Warn("api", "app", app, "err", err)
		rep.Add(Line{Source: source, Kind: "progress", Name: app, Status: StatusFailed, Reason: re.Msg})
	default:
		impLog().Warn("read", "app", app, "err", err)
		rep.Add(Line{Source: source, Kind: "progress", Name: app, Status: StatusFailed, Reason: fmt.Sprintf("Не удалось прочитать базу %s", app)})
	}
	return nil
}

// record is one position of a source user to carry over.
type record struct {
	source, userKey string
	name            string // the line's name: "<user>: <title>"
	itemID          int64
	at              time.Time
	reason          string // of the planned/imported line
	write           func(ctx context.Context, uid int64, at time.Time) error
}

// put writes r for its Vault profile unless Vault holds a position as new
// as r or newer (re-running the import changes nothing). The caller has
// checked that the user is mapped.
func put(ctx context.Context, o ProgressOptions, ps ProgressSink, rep *Report, r record) error {
	uid := o.UserMap[r.userKey]
	at := r.at
	l := Line{Source: r.source, Kind: "progress", Name: r.name, Ref: itemRef(r.itemID), Reason: r.reason}
	switch {
	case at.Before(neverTime): // year 1 is time.Time's zero, which the store reads as "now"
		at = neverTime
	case at.After(time.Now()):
		// The store would clamp it to "now" at every run, and a re-run
		// would then overwrite what was watched in Vault since: an
		// untrustworthy time counts as no time.
		at, l.Reason = neverTime, reasonFutureTime
	}
	e, err := ps.Get(ctx, uid, r.itemID)
	switch {
	case err == nil && e.UpdatedAt.Equal(at):
		l.Status, l.Reason = StatusSkipped, reasonInVault
		rep.Add(l)
		return nil
	case err == nil && e.UpdatedAt.After(at):
		l.Status, l.Reason = StatusSkipped, reasonVaultNewer
		rep.Add(l)
		return nil
	case err != nil && !errors.Is(err, progress.ErrNotFound):
		return err
	}
	if o.DryRun {
		l.Status = StatusPlanned
		rep.Add(l)
		return nil
	}
	if err := r.write(ctx, uid, at); err != nil {
		return err
	}
	l.Status = StatusImported
	rep.Add(l)
	return nil
}

// mapped reports whether the source user has a Vault profile; when not,
// it adds the skipped line.
func mapped(o ProgressOptions, rep *Report, source, userKey, name string) bool {
	if _, ok := o.UserMap[userKey]; ok {
		return true
	}
	rep.Add(Line{Source: source, Kind: "progress", Name: name, Status: StatusSkipped, Reason: reasonNoProfile})
	return false
}

// itemUnder finds the Vault item of a source path: the file itself, else
// the first file below it (a folder book). ok is false when there is none.
func itemUnder(ctx context.Context, lib LibraryLookup, p string) (library.Item, library.File, bool, error) {
	it, f, err := lib.ItemByPath(ctx, p)
	if err == nil {
		return it, f, true, nil
	}
	if !errors.Is(err, library.ErrNotFound) {
		return it, f, false, err
	}
	files, err := lib.FilesUnder(ctx, p)
	if err != nil || len(files) == 0 {
		return library.Item{}, library.File{}, false, err
	}
	it, err = lib.Item(ctx, files[0].ItemID)
	if errors.Is(err, library.ErrNotFound) {
		return it, library.File{}, false, nil
	}
	return it, library.File{}, err == nil, err
}

func ctxOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
