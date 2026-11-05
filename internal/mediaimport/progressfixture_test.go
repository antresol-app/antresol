package mediaimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// Minimal schemas of Jellyfin 12.3 (jellyfin.db), Kavita (kavita.db) and
// Audiobookshelf 2.37 (absdatabase.sqlite): the columns the importers read
// (map-context.md §9–11) plus some neighbours, as the real tables have.

const (
	jfMovie   = "MediaBrowser.Controller.Entities.Movies.Movie"
	jfEpisode = "MediaBrowser.Controller.Entities.TV.Episode"
	jfSeries  = "MediaBrowser.Controller.Entities.TV.Series"
	jfSeason  = "MediaBrowser.Controller.Entities.TV.Season"
	jfChannel = "MediaBrowser.Controller.LiveTv.LiveTvChannel"
	jfPerson  = "MediaBrowser.Controller.Entities.Person"
)

type jfItem struct {
	ID, Type, Name, Path string
	RunTimeTicks         int64
	Index, ParentIndex   int // 0 = NULL
	SeriesID             string
	Tmdb                 string // BaseItemProviders 'Tmdb'
}

type jfUserData struct {
	ItemID, UserID, Key string
	Played, Favorite    bool
	PosTicks            int64
	LastPlayed          string // "" = NULL
	Retention           string // "" = NULL
}

type jfUser struct{ ID, Name string }

func jellyfinDB(t *testing.T, users []jfUser, items []jfItem, data []jfUserData) string {
	t.Helper()
	return fixtureDB(t, "jellyfin.db", []string{
		`CREATE TABLE "BaseItems" ("Id" TEXT NOT NULL PRIMARY KEY, "Type" TEXT NOT NULL, "Name" TEXT, "Path" TEXT,
			"RunTimeTicks" INTEGER, "IndexNumber" INTEGER, "ParentIndexNumber" INTEGER, "SeriesId" TEXT, "SeasonId" TEXT,
			"ParentId" TEXT, "SeriesName" TEXT, "ProductionYear" INTEGER, "IsFolder" INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE "BaseItemProviders" ("ItemId" TEXT NOT NULL, "ProviderId" TEXT NOT NULL, "ProviderValue" TEXT NOT NULL,
			PRIMARY KEY ("ItemId", "ProviderId"))`,
		`CREATE TABLE "Users" ("Id" TEXT NOT NULL PRIMARY KEY, "Username" TEXT NOT NULL, "Password" TEXT,
			"MustUpdatePassword" INTEGER NOT NULL DEFAULT 0, "InternalId" INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE "UserData" ("ItemId" TEXT NOT NULL, "UserId" TEXT NOT NULL, "CustomDataKey" TEXT NOT NULL,
			"AudioStreamIndex" INTEGER, "IsFavorite" INTEGER NOT NULL, "LastPlayedDate" TEXT, "Likes" INTEGER,
			"PlayCount" INTEGER NOT NULL DEFAULT 0, "PlaybackPositionTicks" INTEGER NOT NULL, "Played" INTEGER NOT NULL,
			"Rating" REAL, "SubtitleStreamIndex" INTEGER, "RetentionDate" TEXT,
			PRIMARY KEY ("ItemId", "UserId", "CustomDataKey"))`,
	}, func(d *sql.DB) {
		for _, u := range users {
			mustExec(t, d, `INSERT INTO Users (Id, Username) VALUES (?, ?)`, u.ID, u.Name)
		}
		for _, it := range items {
			mustExec(t, d, `INSERT INTO BaseItems (Id, Type, Name, Path, RunTimeTicks, IndexNumber, ParentIndexNumber, SeriesId)
				VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, ''))`,
				it.ID, it.Type, it.Name, it.Path, it.RunTimeTicks, it.Index, it.ParentIndex, it.SeriesID)
			if it.Tmdb != "" {
				mustExec(t, d, `INSERT INTO BaseItemProviders VALUES (?, 'Tmdb', ?)`, it.ID, it.Tmdb)
				mustExec(t, d, `INSERT INTO BaseItemProviders VALUES (?, 'Imdb', 'tt0000001')`, it.ID)
			}
		}
		for _, u := range data {
			key := u.Key
			if key == "" {
				key = u.ItemID
			}
			mustExec(t, d, `INSERT INTO UserData (ItemId, UserId, CustomDataKey, IsFavorite, LastPlayedDate, PlaybackPositionTicks, Played, RetentionDate)
				VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, NULLIF(?, ''))`,
				u.ItemID, u.UserID, key, u.Favorite, u.LastPlayed, u.PosTicks, u.Played, u.Retention)
		}
	})
}

type kvProgress struct {
	UserID, ChapterID, PagesRead int
	Modified                     string
}

type kvChapter struct {
	ID, Pages int
	Path      string
}

type kvUser struct {
	ID   int
	Name string
}

func kavitaDB(t *testing.T, users []kvUser, chapters []kvChapter, prog []kvProgress) string {
	t.Helper()
	return fixtureDB(t, "kavita.db", []string{
		`CREATE TABLE "AspNetUsers" ("Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "UserName" TEXT, "NormalizedUserName" TEXT,
			"Email" TEXT, "PasswordHash" TEXT, "Created" TEXT NOT NULL DEFAULT '', "LastActive" TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE "Chapter" ("Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "Range" TEXT, "Number" TEXT, "Title" TEXT,
			"Pages" INTEGER NOT NULL, "VolumeId" INTEGER NOT NULL DEFAULT 1, "IsSpecial" INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE "MangaFile" ("Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "FilePath" TEXT, "Pages" INTEGER NOT NULL,
			"Format" INTEGER NOT NULL DEFAULT 0, "ChapterId" INTEGER NOT NULL, "Bytes" INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE "AppUserProgresses" ("Id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, "PagesRead" INTEGER NOT NULL,
			"VolumeId" INTEGER NOT NULL DEFAULT 1, "SeriesId" INTEGER NOT NULL DEFAULT 1, "ChapterId" INTEGER NOT NULL,
			"AppUserId" INTEGER NOT NULL, "Created" TEXT NOT NULL DEFAULT '', "LastModified" TEXT NOT NULL DEFAULT '',
			"BookScrollId" TEXT, "CreatedUtc" TEXT NOT NULL DEFAULT '', "LastModifiedUtc" TEXT NOT NULL DEFAULT '',
			"LibraryId" INTEGER NOT NULL DEFAULT 1, "TotalReads" INTEGER NOT NULL DEFAULT 0)`,
	}, func(d *sql.DB) {
		for _, u := range users {
			mustExec(t, d, `INSERT INTO AspNetUsers (Id, UserName) VALUES (?, ?)`, u.ID, u.Name)
		}
		for _, c := range chapters {
			mustExec(t, d, `INSERT INTO Chapter (Id, Pages, Title) VALUES (?, ?, '')`, c.ID, c.Pages)
			mustExec(t, d, `INSERT INTO MangaFile (FilePath, Pages, ChapterId) VALUES (?, ?, ?)`, c.Path, c.Pages, c.ID)
		}
		for _, p := range prog {
			mustExec(t, d, `INSERT INTO AppUserProgresses (PagesRead, ChapterId, AppUserId, LastModified, LastModifiedUtc)
				VALUES (?, ?, ?, ?, ?)`, p.PagesRead, p.ChapterID, p.UserID, p.Modified, p.Modified)
		}
	})
}

type absProgress struct {
	UserID, MediaID   string
	Duration, Current float64
	Finished          bool
	EbookLoc          string
	EbookProgress     float64
	Updated           string
	ItemType          string // "" = book
}

type absItem struct{ MediaID, Path, Title string }

type absUser struct{ ID, Name string }

func absDB(t *testing.T, users []absUser, items []absItem, prog []absProgress) string {
	t.Helper()
	return fixtureDB(t, "absdatabase.sqlite", []string{
		`CREATE TABLE "users" ("id" UUID PRIMARY KEY, "username" VARCHAR(255), "email" VARCHAR(255), "pash" VARCHAR(255),
			"type" VARCHAR(255), "isActive" TINYINT(1) DEFAULT 0, "createdAt" DATETIME NOT NULL, "updatedAt" DATETIME NOT NULL)`,
		`CREATE TABLE "libraryItems" ("id" UUID PRIMARY KEY, "ino" VARCHAR(255), "path" VARCHAR(255), "relPath" VARCHAR(255),
			"mediaId" UUID, "mediaType" VARCHAR(255), "isFile" TINYINT(1) DEFAULT 0, "isMissing" TINYINT(1) DEFAULT 0,
			"title" VARCHAR(255), "createdAt" DATETIME NOT NULL, "updatedAt" DATETIME NOT NULL)`,
		`CREATE TABLE "mediaProgresses" ("id" UUID PRIMARY KEY, "mediaItemId" UUID, "mediaItemType" VARCHAR(255),
			"duration" FLOAT, "currentTime" FLOAT, "isFinished" TINYINT(1) DEFAULT 0, "hideFromContinueListening" TINYINT(1) DEFAULT 0,
			"ebookLocation" VARCHAR(255), "ebookProgress" FLOAT, "finishedAt" DATETIME, "extraData" JSON, "podcastId" UUID,
			"createdAt" DATETIME NOT NULL, "updatedAt" DATETIME NOT NULL, "userId" UUID)`,
	}, func(d *sql.DB) {
		const ts = "2026-01-01 00:00:00.000 +00:00"
		for _, u := range users {
			mustExec(t, d, `INSERT INTO users (id, username, type, createdAt, updatedAt) VALUES (?, ?, 'user', ?, ?)`, u.ID, u.Name, ts, ts)
		}
		for _, it := range items {
			mustExec(t, d, `INSERT INTO libraryItems (id, path, mediaId, mediaType, title, createdAt, updatedAt) VALUES (?, ?, ?, 'book', ?, ?, ?)`,
				"li-"+it.MediaID, it.Path, it.MediaID, it.Title, ts, ts)
		}
		for i, p := range prog {
			typ := p.ItemType
			if typ == "" {
				typ = "book"
			}
			mustExec(t, d, `INSERT INTO mediaProgresses (id, mediaItemId, mediaItemType, duration, currentTime, isFinished, ebookLocation, ebookProgress, createdAt, updatedAt, userId) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				"mp-"+string(rune('a'+i)), p.MediaID, typ, p.Duration, p.Current, p.Finished, p.EbookLoc, p.EbookProgress, ts, p.Updated, p.UserID)
		}
	})
}

// progEnv is a Vault database with a library, progress and profiles.
type progEnv struct {
	t     *testing.T
	db    *sql.DB
	lib   *library.Repo
	ps    *progress.Store
	users *auth.Store
	root  string
	uid   int64 // profile «alex»
}

func newProgEnv(t *testing.T) *progEnv {
	t.Helper()
	root := t.TempDir()
	d, err := db.Open(filepath.Join(root, "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &progEnv{t: t, db: d, lib: library.NewRepo(d), ps: progress.New(d), users: auth.NewStore(d), root: root}
	u, err := e.users.CreateUser(context.Background(), auth.NewUser{Name: "alex"})
	if err != nil {
		t.Fatal(err)
	}
	e.uid = u.ID
	return e
}

// file creates an empty media file under the env root and returns its path.
func (e *progEnv) file(rel string) string {
	e.t.Helper()
	p := filepath.Join(e.root, "media", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *progEnv) movie(title string, tmdb int64, path string, durMs int64) int64 {
	e.t.Helper()
	id, err := e.lib.UpsertMovie(context.Background(), library.MovieInput{Title: title, Year: 2014, TmdbID: tmdb, File: library.FileInput{Path: path, Size: 1, Mtime: 1}})
	if err != nil {
		e.t.Fatal(err)
	}
	if durMs > 0 {
		mustExec(e.t, e.db, `UPDATE files SET duration_ms=? WHERE path=?`, durMs, path)
	}
	return id
}

func (e *progEnv) episode(series string, tmdb int64, season, ep int, path string) int64 {
	e.t.Helper()
	id, err := e.lib.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: series, SeriesYear: 2008, SeriesTmdbID: tmdb,
		Season: season, Episode: ep, File: library.FileInput{Path: path, Size: 1, Mtime: 1}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

type progRow struct {
	Pos, Dur  int64
	Finished  bool
	UpdatedAt string
	Locator   string
}

// row returns the progress row of the profile for item (ok false: none).
func (e *progEnv) row(item int64) (progRow, bool) {
	e.t.Helper()
	var r progRow
	err := e.db.QueryRow(`SELECT position_ms, duration_ms, finished, updated_at, locator FROM progress WHERE user_id=? AND item_id=?`, e.uid, item).
		Scan(&r.Pos, &r.Dur, &r.Finished, &r.UpdatedAt, &r.Locator)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return r, true
}

func (e *progEnv) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *progEnv) watchlist() []int64 {
	e.t.Helper()
	ids, err := e.ps.Watchlist(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

func (e *progEnv) opts() ProgressOptions {
	return ProgressOptions{Tmp: filepath.Join(e.root, "tmp")}
}

type importFunc func(context.Context, ProgressOptions, LibraryLookup, ProgressSink, *Report) error

func (e *progEnv) run(f importFunc, o ProgressOptions) *Report {
	e.t.Helper()
	rep := &Report{ID: NewReportID(time.Now()), DryRun: o.DryRun, StartedAt: time.Now()}
	if err := f(context.Background(), o, e.lib, e.ps, rep); err != nil {
		e.t.Fatal(err)
	}
	return rep
}

// tsOf is t in the layout of progress.updated_at.
func tsOf(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func linesWith(rep *Report, status string) []Line {
	var out []Line
	for _, l := range rep.Lines {
		if l.Status == status {
			out = append(out, l)
		}
	}
	return out
}
