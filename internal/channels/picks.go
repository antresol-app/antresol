package channels

import (
	"context"
	"errors"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/release"
)

// Kinds of a pick (Rules.Picks).
const (
	// PickTitle: a movie, or a series with all its episodes in order
	// (season 0 left out).
	PickTitle = "title"
	// PickItem: one movie or one episode.
	PickItem = "item"
	// PickFolder: every playable video file under a library folder, in
	// natural path order.
	PickFolder = "folder"
)

// MaxPicks bounds a channel's picks.
const MaxPicks = 1000

// Pick is one entry of a channel's hand-made pool: a title or an item by
// id, or a library folder by its path relative to the library roots
// ("series/Друзья/Season 01", as Browse returns it).
type Pick struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

// liftTitles turns round 1's Rules.Titles into picks of kind PickTitle
// (after the picks already there, without repeats).
func liftTitles(r *Rules) {
	for _, id := range r.Titles {
		if p := (Pick{Kind: PickTitle, ID: id}); id > 0 && !contains(r.Picks, p) {
			r.Picks = append(r.Picks, p)
		}
	}
	r.Titles = nil
}

// normalizePicks checks and cleans r's picks (titles become picks).
func normalizePicks(r *Rules) error {
	liftTitles(r)
	var out []Pick
	for _, p := range r.Picks {
		switch p.Kind {
		case PickTitle, PickItem:
			if p.ID <= 0 {
				return &Invalid{"Неизвестный фильм или серия в составе канала"}
			}
			p.Path = ""
		case PickFolder:
			clean, ok := CleanPickPath(p.Path)
			if !ok || clean == "" {
				return &Invalid{"Недопустимая папка в составе канала"}
			}
			p.ID, p.Path = 0, clean
		default:
			return &Invalid{"Неизвестный вид элемента в составе канала"}
		}
		if !contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) > MaxPicks {
		return &Invalid{"Слишком много элементов в составе канала"}
	}
	r.Picks = out
	return nil
}

// CleanPickPath cleans a folder path relative to the library roots: "/"
// separated, no empty, "." or ".." segment, no leading or trailing "/".
// ok is false for a path that would leave its root.
func CleanPickPath(p string) (string, bool) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", true
	}
	if strings.ContainsRune(p, '\\') || strings.ContainsRune(p, 0) || !utf8.ValidString(p) {
		return "", false
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return "", false
		}
	}
	return strings.Join(segs, "/"), true
}

// Root is a library folder picks and Browse see, by Key (the first segment
// of a pick's path).
type Root struct {
	Key string
	Dir string
}

// LibraryRoots names the video library folders by their base names
// ("movies", "series"; a repeated name gets "-2").
func LibraryRoots(dirs ...string) []Root {
	var out []Root
	used := map[string]bool{}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		key := filepath.Base(d)
		for n := 2; used[key]; n++ {
			key = filepath.Base(d) + "-" + strconv.Itoa(n)
		}
		used[key] = true
		out = append(out, Root{Key: key, Dir: d})
	}
	return out
}

// ErrBadPath: a folder path that leaves the library roots.
var ErrBadPath = errors.New("channels: bad folder path")

// folderDir is the absolute folder of a pick path ("" with ok: the path
// names a root itself, dir is then the root).
func (c Catalog) folderDir(path string) (dir string, ok bool) {
	key, rest, _ := strings.Cut(path, "/")
	for _, r := range c.Roots {
		if r.Key == key {
			if rest == "" {
				return r.Dir, true
			}
			return filepath.Join(r.Dir, filepath.FromSlash(rest)), true
		}
	}
	return "", false
}

// videoFile is a library video file with the title it belongs to.
type videoFile struct {
	FileID, DurationMs int64
	Path               string
	ItemID             int64
	Kind               string // movie | episode
	Season, Episode    int
	TitleID            int64 // the movie, or the episode's series
	TitleKind          string
	TitleRating        string
}

// titleOf is the SQL of a schedule or file row's title (item i, its
// parent se and grand-parent sr): a movie itself, an episode's series.
const titleOf = `CASE WHEN i.kind='episode' THEN COALESCE(sr.id, se.id) ELSE i.id END`

const videoFileSQL = `SELECT f.id, COALESCE(f.duration_ms,0), f.path, i.id, i.kind, COALESCE(i.season_no, se.season_no, 0), COALESCE(i.episode_no,0),
	  ` + titleOf + `, CASE WHEN i.kind='episode' THEN 'series' ELSE i.kind END,
	  CASE WHEN i.kind='episode' THEN COALESCE(sr.rating, se.rating, '') ELSE i.rating END
	FROM files f JOIN items i ON i.id=f.item_id
	LEFT JOIN items se ON se.id=i.parent_id AND i.kind='episode'
	LEFT JOIN items sr ON sr.id=se.parent_id AND se.kind='season'
	WHERE i.kind IN ('movie','episode') AND i.missing_since IS NULL AND f.missing_since IS NULL AND `

func (c Catalog) videoFiles(ctx context.Context, cond string, args ...any) ([]videoFile, error) {
	rows, err := c.DB.QueryContext(ctx, videoFileSQL+cond, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []videoFile
	for rows.Next() {
		var v videoFile
		if err := rows.Scan(&v.FileID, &v.DurationMs, &v.Path, &v.ItemID, &v.Kind, &v.Season, &v.Episode, &v.TitleID, &v.TitleKind, &v.TitleRating); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// filesUnder are the library video files under dir (any depth), by
// natural path order.
func (c Catalog) filesUnder(ctx context.Context, dir string) ([]videoFile, error) {
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	fs, err := c.videoFiles(ctx, `substr(f.path, 1, ?) = ?`, utf8.RuneCountInString(prefix), prefix)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(fs, func(i, j int) bool { return pathLess(fs[i].Path, fs[j].Path) })
	return fs, nil
}

func allowed(maxRating string, v videoFile) bool {
	return library.RatingAllowed(maxRating, library.Item{Kind: v.TitleKind, Rating: v.TitleRating})
}

// progsOf makes programmes of files in their order: the consecutive files
// of one item are its parts; files without a duration are left out.
func progsOf(unit int64, fs []videoFile) []Prog {
	var out []Prog
	for _, f := range fs {
		if f.DurationMs <= 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].ItemID == f.ItemID {
			out[n-1].Parts = append(out[n-1].Parts, Part{FileID: f.FileID, DurationMs: f.DurationMs})
			continue
		}
		out = append(out, Prog{ItemID: f.ItemID, UnitID: unit, TitleID: f.TitleID, Parts: []Part{{FileID: f.FileID, DurationMs: f.DurationMs}}})
	}
	return out
}

// folderUnit is the unit id of a folder pick: above every item id (and
// positive, as a library unit), the same for the same path.
func folderUnit(path string) int64 {
	h := fnv.New64a()
	h.Write([]byte(path))
	return 1<<52 + int64(h.Sum64()&(1<<50-1))
}

// picksPool is the pool of r.Picks, in the picks' order: each pick is a
// unit (a title: its id; an item: its id; a folder: folderUnit) with its
// programmes in their natural order. A pick with nothing playable within
// r.MaxRating (and the library owner's titles, when set) is left out, as
// is a pick that repeats a unit already in the pool.
func (c Catalog) picksPool(ctx context.Context, units []Unit, r Rules) (Pool, error) {
	var owned map[int64]bool
	if r.LibraryUser != 0 {
		var err error
		if owned, err = c.Owned(ctx, r.LibraryUser); err != nil {
			return Pool{}, err
		}
	}
	ok := func(titleID int64, kind, rating string) bool {
		return (owned == nil || owned[titleID]) && library.RatingAllowed(r.MaxRating, library.Item{Kind: kind, Rating: rating})
	}
	byID := map[int64]Unit{}
	for _, u := range units {
		byID[u.ID] = u
	}
	// the titles' programmes in one read
	var titles []Unit
	for _, p := range r.Picks {
		if u, has := byID[p.ID]; has && (p.Kind == PickTitle || p.Kind == PickItem) && ok(u.ID, u.Kind, u.Rating) {
			titles = append(titles, u)
		}
	}
	tprogs, err := c.Progs(ctx, titles)
	if err != nil {
		return Pool{}, err
	}
	pool := Pool{Progs: map[int64][]Prog{}}
	add := func(u Unit, progs []Prog) {
		if len(progs) == 0 || pool.Progs[u.ID] != nil {
			return
		}
		pool.Units = append(pool.Units, u)
		pool.Progs[u.ID] = progs
	}
	for _, p := range r.Picks {
		switch {
		case p.Kind == PickFolder:
			dir, has := c.folderDir(p.Path)
			if !has {
				continue
			}
			fs, err := c.filesUnder(ctx, dir)
			if err != nil {
				return Pool{}, err
			}
			kept := fs[:0]
			for _, f := range fs {
				if ok(f.TitleID, f.TitleKind, f.TitleRating) {
					kept = append(kept, f)
				}
			}
			u := Unit{ID: folderUnit(p.Path), Kind: PickFolder, Title: p.Path, Sort: strings.ToLower(p.Path)}
			add(u, progsOf(u.ID, kept))
		case byID[p.ID].ID != 0: // a movie or a series
			add(byID[p.ID], tprogs[p.ID])
		case p.Kind == PickItem: // an episode
			fs, err := c.videoFiles(ctx, `i.id=? AND i.kind='episode' ORDER BY f.path`, p.ID)
			if err != nil {
				return Pool{}, err
			}
			if len(fs) == 0 || !ok(fs[0].TitleID, fs[0].TitleKind, fs[0].TitleRating) {
				continue
			}
			add(Unit{ID: p.ID, Kind: "episode", Rating: fs[0].TitleRating}, progsOf(p.ID, fs))
		}
	}
	return pool, nil
}

// pathLess orders paths naturally, folder by folder ("Серия 2" before
// "Серия 10").
func pathLess(a, b string) bool {
	as, bs := strings.Split(filepath.ToSlash(a), "/"), strings.Split(filepath.ToSlash(b), "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := naturalCompare(as[i], bs[i]); c != 0 {
			return c < 0
		}
	}
	return len(as) < len(bs)
}

// naturalCompare compares case-insensitively with digit runs as numbers.
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if isDigit(ra) && isDigit(rb) {
			da, db := digitRun(a), digitRun(b)
			ta, tb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(ta) != len(tb) {
				return cmpInt(len(ta), len(tb))
			}
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if la, lb := unicode.ToLower(ra), unicode.ToLower(rb); la != lb {
			return cmpInt(int(la), int(lb))
		}
		a, b = a[na:], b[nb:]
	}
	return cmpInt(len(a), len(b))
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func digitRun(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ---- the editor's options ----

// SearchHit is a movie, series or episode the editor may pick. Pick is
// what to add to Rules.Picks for it.
type SearchHit struct {
	Kind     string `json:"kind"` // movie | series | episode
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Year     int    `json:"year,omitempty"`
	Pick     Pick   `json:"pick"`
}

// SearchLimit bounds each part (titles, episodes) of a search.
const SearchLimit = 40

// Search finds playable movies and series (by any of their titles) and
// episodes (by their own title, or their series') within maxRating.
func (c Catalog) Search(ctx context.Context, q, maxRating string) ([]SearchHit, error) {
	norm := release.Normalize(q)
	out := []SearchHit{}
	if norm == "" {
		return out, nil
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT i.id, MAX(t.norm=?) + MAX(t.norm LIKE ?) FROM items i JOIN item_titles t ON t.item_id=i.id
	  WHERE i.kind IN ('movie','series') AND i.missing_since IS NULL AND t.norm LIKE ? GROUP BY i.id`, norm, norm+"%", "%"+norm+"%")
	if err != nil {
		return nil, err
	}
	score := map[int64]int{}
	for rows.Next() {
		var id int64
		var s int
		if err := rows.Scan(&id, &s); err != nil {
			rows.Close()
			return nil, err
		}
		score[id] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	units, err := c.Units(ctx)
	if err != nil {
		return nil, err
	}
	var found []Unit
	series := map[int64]bool{}
	for _, u := range units {
		if _, hit := score[u.ID]; hit && library.RatingAllowed(maxRating, library.Item{Kind: u.Kind, Rating: u.Rating}) {
			found = append(found, u)
		}
	}
	progs, err := c.Progs(ctx, found)
	if err != nil {
		return nil, err
	}
	var titles []Unit
	for _, u := range found {
		if len(progs[u.ID]) > 0 {
			titles = append(titles, u)
			if u.Kind == library.KindSeries {
				series[u.ID] = true
			}
		}
	}
	sort.SliceStable(titles, func(i, j int) bool {
		if score[titles[i].ID] != score[titles[j].ID] {
			return score[titles[i].ID] > score[titles[j].ID]
		}
		return naturalCompare(titles[i].Sort, titles[j].Sort) < 0
	})
	for _, u := range titles[:min(len(titles), SearchLimit)] {
		out = append(out, SearchHit{Kind: u.Kind, ID: u.ID, Title: u.Title, Year: u.Year, Pick: Pick{Kind: PickTitle, ID: u.ID}})
	}
	// episodes: their own title, or their series' title
	erows, err := c.DB.QueryContext(ctx, `SELECT e.id, e.title, COALESCE(e.season_no, se.season_no, 0), COALESCE(e.episode_no,0), e.year,
	    s.id, s.title, s.sort_title, s.rating, s.year
	  FROM items e JOIN items se ON se.id=e.parent_id AND se.kind='season' JOIN items s ON s.id=se.parent_id AND s.kind='series'
	  WHERE e.kind='episode' AND e.missing_since IS NULL AND s.missing_since IS NULL
	    AND EXISTS (SELECT 1 FROM files f WHERE f.item_id=e.id AND f.missing_since IS NULL AND f.duration_ms > 0)`)
	if err != nil {
		return nil, err
	}
	defer erows.Close()
	type ep struct {
		hit        SearchHit
		sort       string
		season, no int
	}
	var eps []ep
	for erows.Next() {
		var x ep
		var title, sTitle, sSort, sRating string
		var year, sYear int
		var sID int64
		if err := erows.Scan(&x.hit.ID, &title, &x.season, &x.no, &year, &sID, &sTitle, &sSort, &sRating, &sYear); err != nil {
			return nil, err
		}
		if !library.RatingAllowed(maxRating, library.Item{Kind: library.KindSeries, Rating: sRating}) {
			continue
		}
		if !series[sID] && !strings.Contains(release.Normalize(title), norm) {
			continue
		}
		if year == 0 {
			year = sYear
		}
		label := EpisodeLabel(Info{Season: x.season, Number: x.no})
		sub := sTitle
		if label != "" {
			sub += " · " + label
		}
		x.hit = SearchHit{Kind: "episode", ID: x.hit.ID, Title: title, Subtitle: sub, Year: year, Pick: Pick{Kind: PickItem, ID: x.hit.ID}}
		x.sort = sSort
		eps = append(eps, x)
	}
	if err := erows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(eps, func(i, j int) bool {
		a, b := eps[i], eps[j]
		if a.sort != b.sort {
			return naturalCompare(a.sort, b.sort) < 0
		}
		if a.season != b.season {
			return a.season < b.season
		}
		if a.no != b.no {
			return a.no < b.no
		}
		return a.hit.ID < b.hit.ID
	})
	for _, e := range eps[:min(len(eps), SearchLimit)] {
		out = append(out, e.hit)
	}
	return out, nil
}

// FolderEntry is a sub-folder Browse lists: Path is what a PickFolder
// takes.
type FolderEntry struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// FileEntry is a video file Browse lists, by the item it plays (a movie or
// an episode: what a PickItem takes).
type FileEntry struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // movie | episode
}

// Folder is what Browse sees in a library folder.
type Folder struct {
	Path    string        `json:"path"`
	Folders []FolderEntry `json:"folders"`
	Files   []FileEntry   `json:"files"`
}

// Browse lists a library folder by its path relative to the library roots
// ("": the roots): the sub-folders that hold library video files within
// maxRating, and the files right in it, in natural order. Only the
// library's files are seen (the database, not the disk). ErrBadPath: a
// path leaving the roots; ErrNotFound: no such folder in the library.
func (c Catalog) Browse(ctx context.Context, path, maxRating string) (Folder, error) {
	clean, ok := CleanPickPath(path)
	if !ok {
		return Folder{}, ErrBadPath
	}
	out := Folder{Path: clean, Folders: []FolderEntry{}, Files: []FileEntry{}}
	if clean == "" {
		for _, r := range c.Roots {
			out.Folders = append(out.Folders, FolderEntry{Path: r.Key, Name: r.Key})
		}
		return out, nil
	}
	dir, ok := c.folderDir(clean)
	if !ok {
		return out, ErrNotFound
	}
	fs, err := c.filesUnder(ctx, dir)
	if err != nil {
		return out, err
	}
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	seen := map[string]bool{}
	seen1 := false
	for _, f := range fs {
		seen1 = true
		if !allowed(maxRating, f) {
			continue
		}
		rel := filepath.ToSlash(strings.TrimPrefix(f.Path, prefix))
		if seg, _, sub := strings.Cut(rel, "/"); sub {
			if !seen[seg] {
				seen[seg] = true
				out.Folders = append(out.Folders, FolderEntry{Path: clean + "/" + seg, Name: seg})
			}
			continue
		}
		out.Files = append(out.Files, FileEntry{ID: f.ItemID, Name: rel, Kind: f.Kind})
	}
	if !seen1 && strings.Contains(clean, "/") {
		return out, ErrNotFound
	}
	return out, nil
}

// PickInfo describes a pick for the editor's list: Missing when the
// library has nothing of it to play now.
type PickInfo struct {
	Pick
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Year     int    `json:"year,omitempty"`
	Missing  bool   `json:"missing,omitempty"`
}

// DescribePicks describes r's picks in order (the pool of r tells which
// ones play).
func (c Catalog) DescribePicks(ctx context.Context, r Rules) ([]PickInfo, error) {
	if len(r.Picks) == 0 {
		return nil, nil
	}
	pool, err := c.Pool(ctx, Rules{Picks: r.Picks, MaxRating: r.MaxRating, LibraryUser: r.LibraryUser})
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, p := range r.Picks {
		if p.ID > 0 {
			ids = append(ids, p.ID)
		}
	}
	info := map[int64]Info{}
	if len(ids) > 0 {
		if info, err = (&Service{Store: &Store{DB: c.DB}}).Describe(ctx, ids); err != nil {
			return nil, err
		}
	}
	out := make([]PickInfo, 0, len(r.Picks))
	for _, p := range r.Picks {
		pi := PickInfo{Pick: p}
		switch p.Kind {
		case PickFolder:
			pi.Title, pi.Subtitle = p.Path[strings.LastIndex(p.Path, "/")+1:], p.Path
			pi.Missing = pool.Progs[folderUnit(p.Path)] == nil
		default:
			x, has := info[p.ID]
			pi.Title, pi.Year = x.Title, x.Year
			if x.Kind == "episode" {
				pi.Title, pi.Subtitle = x.Episode, x.Title
				if l := EpisodeLabel(Info{Season: x.Season, Number: x.Number}); l != "" {
					pi.Subtitle += " · " + l
				}
			}
			pi.Missing = !has || pool.Progs[p.ID] == nil
		}
		out = append(out, pi)
	}
	return out, nil
}
