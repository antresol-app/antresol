package api

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/disk"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/go-chi/chi/v5"
)

// stats serves «Статистика» and the storage panel of «Файлы» (admin).
type stats struct {
	db    *sql.DB
	files *files.Service // optional: disk categories and delete targets
	// diskUsage (optional) gives the disk figures without the file browser.
	diskUsage func() (disk.Usage, error)
	now       func() time.Time
	// statsFull: the sources build's sources (stats_full.go)
	statsFull
}

func newStats(deps Deps, now func() time.Time) *stats {
	s := &stats{db: deps.DB, files: deps.Files, diskUsage: deps.DiskUsage, now: now}
	s.statsFull = newStatsFull(deps)
	return s
}

func mountStats(r chi.Router, s *stats) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/stats", s.overview)
		r.Get("/api/stats/storage", s.storage)
		mountStatsDisk(r, s)
	})
}

type statsDisk struct {
	Total      int64            `json:"total"`
	Used       int64            `json:"used"`
	Free       int64            `json:"free"`
	Categories []files.Category `json:"categories"`
}

type statsCounts struct {
	Movies     int `json:"movies"`
	Series     int `json:"series"`
	Episodes   int `json:"episodes"`
	Albums     int `json:"albums"`
	Tracks     int `json:"tracks"`
	Books      int `json:"books"`
	Audiobooks int `json:"audiobooks"`
	Comics     int `json:"comics"` // series (kind comic), not volumes
}

type userHours struct {
	User     string  `json:"user"`
	Avatar   string  `json:"avatar"`
	Hours30d float64 `json:"hours30d"`
}

type topItem struct {
	ID      int64   `json:"id"`
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Year    int     `json:"year,omitempty"`
	Poster  string  `json:"poster"`
	Viewers int     `json:"viewers"`
	Hours   float64 `json:"hours"`
}

type statsOverview struct {
	Disk          statsDisk   `json:"disk"`
	Counts        statsCounts `json:"counts"`
	WatchedByUser []userHours `json:"watchedByUser"`
	TopItems30d   []topItem   `json:"topItems30d"`
	// statsOverviewFull: the sources build's own blocks (stats_full.go)
	statsOverviewFull
}

// topOf maps an item to its movie or series (episode → season → series).
const topOf = `COALESCE(g.id, par.id, i.id)`

func (s *stats) disk(ctx context.Context) statsDisk {
	d := statsDisk{Categories: []files.Category{}}
	if s.files != nil {
		u, err := s.files.Usage.Report(ctx)
		if err == nil {
			return statsDisk{Total: u.Total, Used: u.Used, Free: u.Free, Categories: u.Categories}
		}
		if ctx.Err() == nil {
			log.Printf("api: stats disk usage: %v", err)
		}
	}
	s.fallbackDisk(&d)
	return d
}

// fallbackDisk: the disk figures without the file browser.
func (s *stats) fallbackDisk(d *statsDisk) {
	if s.diskUsage == nil {
		return
	}
	u, err := s.diskUsage()
	if err == nil {
		d.Total, d.Used, d.Free = u.Total, u.Used, max(u.Total-u.Used, 0)
	} else {
		log.Printf("api: stats disk: %v", err)
	}
}

func (s *stats) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := statsOverview{Disk: s.disk(ctx), WatchedByUser: []userHours{}, TopItems30d: []topItem{}}
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM items WHERE kind='movie' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='series' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='episode' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='album' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='track' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='book' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='audiobook' AND missing_since IS NULL),
		(SELECT COUNT(*) FROM items WHERE kind='comic' AND missing_since IS NULL)`).Scan(&out.Counts.Movies, &out.Counts.Series, &out.Counts.Episodes,
		&out.Counts.Albums, &out.Counts.Tracks, &out.Counts.Books, &out.Counts.Audiobooks, &out.Counts.Comics); err != nil {
		internalErr(w, r, "stats counts", err)
		return
	}
	// the time played (watch_time, per UTC day), not the positions: a seek
	// to the end of a film is not three hours watched
	month := s.now().Add(-30 * 24 * time.Hour).UTC().Format(time.DateOnly)
	rows, err := s.db.QueryContext(ctx, `SELECT u.name, u.avatar, COALESCE(SUM(w.ms), 0)
		FROM watch_time w JOIN users u ON u.id = w.user_id
		WHERE w.day >= ? GROUP BY u.id ORDER BY 3 DESC`, month)
	if err != nil {
		internalErr(w, r, "stats users", err)
		return
	}
	for rows.Next() {
		var h userHours
		var ms int64
		if err := rows.Scan(&h.User, &h.Avatar, &ms); err != nil {
			rows.Close()
			internalErr(w, r, "stats users", err)
			return
		}
		h.Hours30d = float64(ms) / float64(time.Hour/time.Millisecond)
		out.WatchedByUser = append(out.WatchedByUser, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalErr(w, r, "stats users", err)
		return
	}
	rows, err = s.db.QueryContext(ctx, `SELECT t.id, t.kind, t.title, t.year, COALESCE(t.poster, ''), x.viewers, x.ms FROM (
			SELECT `+topOf+` AS top, COUNT(DISTINCT w.user_id) AS viewers, SUM(w.ms) AS ms
			FROM watch_time w JOIN items i ON i.id = w.item_id
			LEFT JOIN items par ON par.id = i.parent_id LEFT JOIN items g ON g.id = par.parent_id
			WHERE w.day >= ? GROUP BY top) x
		JOIN items t ON t.id = x.top WHERE t.missing_since IS NULL ORDER BY x.viewers DESC, x.ms DESC LIMIT 10`, month)
	if err != nil {
		internalErr(w, r, "stats top", err)
		return
	}
	for rows.Next() {
		var it topItem
		var poster string
		var ms int64
		if err := rows.Scan(&it.ID, &it.Kind, &it.Title, &it.Year, &poster, &it.Viewers, &ms); err != nil {
			rows.Close()
			internalErr(w, r, "stats top", err)
			return
		}
		it.Poster = imageURL(it.ID, "poster", poster)
		it.Hours = float64(ms) / float64(time.Hour/time.Millisecond)
		out.TopItems30d = append(out.TopItems30d, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalErr(w, r, "stats top", err)
		return
	}
	s.fillOverview(&out.statsOverviewFull)
	writeJSON(w, http.StatusOK, out)
}

// trashTarget is what POST /api/files/trash takes to delete an item.
type trashTarget struct {
	Root  string   `json:"root"`
	Paths []string `json:"paths"`
}

type storageItem struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// the album's artist
	Artist      string        `json:"artist,omitempty"`
	Year        int           `json:"year,omitempty"`
	Poster      string        `json:"poster"`
	Size        int64         `json:"size"`
	Files       int           `json:"files"`
	AddedAt     string        `json:"addedAt"`
	LastWatched *string       `json:"lastWatched"`
	Trash       []trashTarget `json:"trash"`
}

// storageUnit maps an item to what «Файлы» deletes as one: a movie, a series
// (episode → season → series), an album (track → album, not the artist: an
// album is what one listens to and deletes), a book, an audiobook, a comic
// (volume → comic).
const storageUnit = `CASE WHEN i.kind='album' THEN i.id WHEN i.kind='track' THEN COALESCE(par.id, i.id) ELSE ` + topOf + ` END`

// storageRoots are the roots a unit's files are trashed from.
var storageRoots = []string{"movies", "series", "music", "books", "downloads"}

// storage lists every title of the library that has files on the disk —
// movies, series, albums, books, audiobooks, comics — with its size, the last
// time anyone played it, and what to move to the Корзина to delete it. All of
// them, unsorted by meaning: the page sorts and filters (a home library is
// thousands of titles at most, and «not watched for a year» or «music only»
// must be right over all of them, not over a top N).
func (s *stats) storage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.db.QueryContext(ctx, `SELECT `+storageUnit+`, f.path, f.size
		FROM files f JOIN items i ON i.id = f.item_id
		LEFT JOIN items par ON par.id = i.parent_id LEFT JOIN items g ON g.id = par.parent_id
		WHERE f.missing_since IS NULL`)
	if err != nil {
		internalErr(w, r, "storage files", err)
		return
	}
	type fileRow struct {
		top  int64
		path string
	}
	var all []fileRow
	byItem := map[int64]*storageItem{}
	for rows.Next() {
		var f fileRow
		var size int64
		if err := rows.Scan(&f.top, &f.path, &size); err != nil {
			rows.Close()
			internalErr(w, r, "storage files", err)
			return
		}
		it := byItem[f.top]
		if it == nil {
			it = &storageItem{ID: f.top, Trash: []trashTarget{}}
			byItem[f.top] = it
		}
		it.Size += size
		it.Files++
		all = append(all, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalErr(w, r, "storage files", err)
		return
	}
	if err := s.fillItems(ctx, byItem); err != nil {
		internalErr(w, r, "storage items", err)
		return
	}

	// Delete targets: the unit's own folder, the deepest one that holds
	// all its files in a root, when no other unit has files in it (a
	// movie's folder, an album's folder inside an artist folder), else the
	// files themselves. From there it goes up only through parents that
	// hold nothing else on the disk (a series' folder above its only
	// season folder): never into a folder with anything the index may not
	// know of (an unscanned album, a file just imported).
	type place struct{ root, rel string }
	locate := func(p string) (place, bool) {
		if s.files == nil {
			return place{}, false
		}
		for _, id := range storageRoots {
			if rel, ok := s.files.Sandbox.Rel(id, p); ok && rel != "" {
				return place{id, rel}, true
			}
		}
		return place{}, false
	}
	folderOwners := map[place]map[int64]bool{}
	type unitRoot struct {
		top  int64
		root string
	}
	rels := map[unitRoot][]string{}
	var order []unitRoot
	for _, f := range all {
		pl, ok := locate(f.path)
		if !ok {
			continue
		}
		for d := path.Dir(pl.rel); d != "."; d = path.Dir(d) {
			k := place{pl.root, d}
			if folderOwners[k] == nil {
				folderOwners[k] = map[int64]bool{}
			}
			folderOwners[k][f.top] = true
		}
		k := unitRoot{f.top, pl.root}
		if rels[k] == nil {
			order = append(order, k)
		}
		rels[k] = append(rels[k], pl.rel)
	}
	for _, k := range order {
		targets := rels[k]
		if base := commonDir(targets); base != "" && !kindFolder(k.root, base) && len(folderOwners[place{k.root, base}]) == 1 {
			targets = []string{s.climbEmpty(k.root, base)}
		}
		it := byItem[k.top]
		it.Trash = append(it.Trash, trashTarget{Root: k.root, Paths: targets})
	}
	for _, it := range byItem {
		for j := range it.Trash {
			sort.Strings(it.Trash[j].Paths)
			it.Trash[j].Paths = dropNested(it.Trash[j].Paths)
		}
	}

	list := make([]storageItem, 0, len(byItem))
	for _, it := range byItem {
		if it.Kind == "" {
			continue // a file of a vanished item, or of a kind not listed
		}
		list = append(list, *it)
	}
	// largest first: the page's default order, and a stable one
	sort.Slice(list, func(i, j int) bool {
		if list[i].Size != list[j].Size {
			return list[i].Size > list[j].Size
		}
		return list[i].ID < list[j].ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// fillItems loads title, poster and the last time anyone watched (listened
// to, read) each unit or any of its parts: for freeing space what counts is
// whether anyone in the household still plays it.
func (s *stats) fillItems(ctx context.Context, byItem map[int64]*storageItem) error {
	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.kind, i.title, i.year, COALESCE(i.poster, ''), i.added_at, COALESCE(a.title, '')
		FROM items i LEFT JOIN items a ON a.id = i.parent_id AND a.kind = 'artist'
		WHERE i.kind IN ('movie', 'series', 'album', 'book', 'audiobook', 'comic')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var kind, title, poster, added, artist string
		var year int
		if err := rows.Scan(&id, &kind, &title, &year, &poster, &added, &artist); err != nil {
			rows.Close()
			return err
		}
		if it := byItem[id]; it != nil {
			it.Kind, it.Title, it.Year, it.AddedAt, it.Artist = kind, title, year, added, artist
			it.Poster = imageURL(id, "poster", poster)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT `+storageUnit+`, MAX(p.updated_at)
		FROM progress p JOIN items i ON i.id = p.item_id
		LEFT JOIN items par ON par.id = i.parent_id LEFT JOIN items g ON g.id = par.parent_id
		GROUP BY 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return err
		}
		if it := byItem[id]; it != nil {
			it.LastWatched = &at
		}
	}
	return rows.Err()
}

// dropNested removes duplicates and paths inside another listed path
// (sorted input: a parent sorts before its children).
// commonDir is the deepest folder holding every one of the paths ("" when
// it is the root itself).
func commonDir(rels []string) string {
	dir := path.Dir(rels[0])
	for _, r := range rels[1:] {
		for dir != "." && !strings.HasPrefix(r, dir+"/") {
			dir = path.Dir(dir)
		}
	}
	if dir == "." {
		return ""
	}
	return dir
}

// kindFolder: a top folder of the books root — «ebooks», «audiobooks»,
// «manga» (the configured folders of each kind live there by default) —
// is never a title's folder to delete, even when it holds one title only.
func kindFolder(root, rel string) bool {
	return root == "books" && !strings.Contains(rel, "/")
}

// climbEmpty goes up from the folder rel of a root while its parent holds
// nothing else on the disk (a Finder .DS_Store aside).
func (s *stats) climbEmpty(root, rel string) string {
	r, err := s.files.Sandbox.Root(root)
	if err != nil {
		return rel
	}
	for parent := path.Dir(rel); parent != "." && !kindFolder(root, parent); parent = path.Dir(rel) {
		entries, err := os.ReadDir(filepath.Join(r.Dir, filepath.FromSlash(parent)))
		if err != nil {
			return rel
		}
		for _, e := range entries {
			if e.Name() != path.Base(rel) && e.Name() != ".DS_Store" {
				return rel
			}
		}
		rel = parent
	}
	return rel
}

func dropNested(ps []string) []string {
	var out []string
next:
	for _, p := range ps {
		for _, o := range out {
			if p == o || strings.HasPrefix(p, o+"/") {
				continue next
			}
		}
		out = append(out, p)
	}
	return out
}
