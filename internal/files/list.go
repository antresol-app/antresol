package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/library"
)

// Library is the part of *library.Repo the browser uses to link files to items.
type Library interface {
	FilesUnder(ctx context.Context, dir string) ([]library.File, error)
	Item(ctx context.Context, id int64) (library.Item, error)
}

// SeedInfo reports whether path (a file, or a directory containing files)
// belongs to a managed download and whether it is shared. *torrent.Engine
// is adapted by EngineSeeds.
type SeedInfo interface {
	SeedInfo(path string) (infohash string, seeding bool, ok bool)
}

type ItemRef struct {
	ID    int64  `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

type TorrentRef struct {
	Infohash string `json:"infohash"`
	Seeding  bool   `json:"seeding"`
}

// Entry is one file or folder. Path is relative to the listing's root.
type Entry struct {
	Name       string      `json:"name"`
	Path       string      `json:"path"`
	IsDir      bool        `json:"isDir"`
	Size       int64       `json:"size"`
	Mtime      time.Time   `json:"mtime"`
	Kind       string      `json:"kind"`
	ChildCount *int        `json:"childCount,omitempty"`
	Thumb      string      `json:"thumb,omitempty"`
	Item       *ItemRef    `json:"item,omitempty"`
	Torrent    *TorrentRef `json:"torrent,omitempty"`

	abs string
}

type Crumb struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Listing struct {
	Root        string  `json:"root"`
	Path        string  `json:"path"`
	Breadcrumbs []Crumb `json:"breadcrumbs"`
	Entries     []Entry `json:"entries"`
	Truncated   bool    `json:"truncated,omitempty"` // search hit the limit or the timeout
}

// ListOptions: Sort name|size|mtime|kind (default name), Order asc|desc
// (default asc), Q searches names in the whole subtree.
type ListOptions struct {
	Sort, Order, Q string
}

const (
	searchLimit   = 500
	searchTimeout = 5 * time.Second
)

// listLimit caps a folder listing (and the child count read per subfolder):
// a download can unpack hundreds of thousands of files into one folder.
var listLimit = 5000

// Browser lists folders. Library, Seeds and ThumbURL are optional.
type Browser struct {
	Sandbox *Sandbox
	Library Library
	Seeds   SeedInfo
	// ThumbURL builds the thumbnail URL of an entry (v changes when the file does).
	ThumbURL func(root, rel string, v int64) string
}

var kinds = map[string]string{}

func init() {
	for k, exts := range map[string]string{
		"video":    "mkv mp4 m4v avi mov ts m2ts mts webm wmv mpg mpeg flv vob 3gp ogv",
		"audio":    "mp3 flac m4a aac ogg oga opus wav alac ape wma m4b aiff aif dsf wv mka",
		"image":    "jpg jpeg png gif webp bmp heic heif tif tiff avif",
		"subtitle": "srt ass ssa vtt sub idx sup",
		"text":     "txt nfo md log cue json xml yml yaml ini csv",
		"book":     "epub fb2 pdf mobi azw azw3 djvu",
		"comic":    "cbz cbr cb7 cbt",
		"archive":  "zip rar 7z tar gz tgz bz2 xz zst iso",
	} {
		for _, e := range strings.Fields(exts) {
			kinds["."+e] = k
		}
	}
}

// KindOf classifies a name by extension.
func KindOf(name string, isDir bool) string {
	if isDir {
		return "folder"
	}
	if k, ok := kinds[strings.ToLower(filepath.Ext(name))]; ok {
		return k
	}
	return "other"
}

// hidden entries (dotfiles, including the trash) are not listed.
func hidden(name string) bool { return strings.HasPrefix(name, ".") }

func (b *Browser) entry(abs, rel string, fi fs.FileInfo) Entry {
	e := Entry{Name: fi.Name(), Path: rel, IsDir: fi.IsDir(), Mtime: fi.ModTime().UTC().Truncate(time.Second),
		Kind: KindOf(fi.Name(), fi.IsDir()), abs: abs}
	if e.IsDir {
		n := 0
		if f, err := os.Open(abs); err == nil {
			names, _ := f.Readdirnames(listLimit)
			f.Close()
			for _, name := range names {
				if !hidden(name) {
					n++
				}
			}
		}
		e.ChildCount = &n
	} else {
		e.Size = fi.Size()
	}
	return e
}

// statFollow stats abs following a symlink (listing shows the target's type).
func statFollow(abs string, d fs.DirEntry) (fs.FileInfo, error) {
	if d != nil && d.Type()&fs.ModeSymlink == 0 {
		return d.Info()
	}
	return os.Stat(abs)
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func breadcrumbs(rootTitle, rel string) []Crumb {
	out := []Crumb{{Name: rootTitle, Path: ""}}
	if rel == "" {
		return out
	}
	acc := ""
	for _, p := range strings.Split(rel, "/") {
		acc = joinRel(acc, p)
		out = append(out, Crumb{Name: p, Path: acc})
	}
	return out
}

func osErr(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// List returns the contents of a folder (or search results below it).
func (b *Browser) List(ctx context.Context, root, rel string, o ListOptions) (Listing, error) {
	r, err := b.Sandbox.Root(root)
	if err != nil {
		return Listing{}, err
	}
	abs, err := b.Sandbox.Resolve(root, rel)
	if err != nil {
		return Listing{}, err
	}
	rel, _ = cleanRel(rel)
	st, err := os.Stat(abs)
	if err != nil {
		return Listing{}, osErr(err)
	}
	if !st.IsDir() {
		return Listing{}, ErrNotDir
	}
	l := Listing{Root: root, Path: rel, Breadcrumbs: breadcrumbs(r.Title, rel), Entries: []Entry{}}
	if q := strings.TrimSpace(o.Q); q != "" {
		l.Entries, l.Truncated = b.search(ctx, root, abs, rel, q)
	} else {
		des, truncated, err := readDirLimited(ctx, abs)
		if err != nil {
			return Listing{}, err
		}
		l.Truncated = truncated
		for _, d := range des {
			if err := ctx.Err(); err != nil {
				return Listing{}, err
			}
			p, sub := filepath.Join(abs, d.Name()), joinRel(rel, d.Name())
			if !b.linkInside(root, sub, d) {
				continue
			}
			fi, err := statFollow(p, d)
			if err != nil {
				continue // vanished or a dangling link
			}
			l.Entries = append(l.Entries, b.entry(p, sub, fi))
		}
	}
	sortEntries(l.Entries, o.Sort, o.Order == "desc")
	b.decorate(ctx, root, abs, l.Entries)
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	return l, nil
}

// readDirLimited reads up to listLimit visible entries of dir in batches
// (a huge folder is never read into memory whole), checking ctx between
// batches. truncated reports that more entries exist.
func readDirLimited(ctx context.Context, dir string) (des []fs.DirEntry, truncated bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, osErr(err)
	}
	defer f.Close()
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		batch, err := f.ReadDir(256)
		for _, d := range batch {
			if hidden(d.Name()) {
				continue
			}
			if len(des) >= listLimit {
				return des, true, nil
			}
			des = append(des, d)
		}
		if errors.Is(err, io.EOF) || err == nil && len(batch) == 0 {
			return des, false, nil
		}
		if err != nil {
			return nil, false, osErr(err)
		}
	}
}

func (b *Browser) search(ctx context.Context, root, abs, rel, q string) ([]Entry, bool) {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	needle := strings.ToLower(q)
	out := []Entry{}
	truncated := false
	errStop := errors.New("stop")
	filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == abs {
			return nil
		}
		if ctx.Err() != nil || len(out) >= searchLimit {
			truncated = true
			return errStop
		}
		if hidden(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(strings.ToLower(d.Name()), needle) {
			sub, _ := filepath.Rel(abs, p)
			sub = joinRel(rel, filepath.ToSlash(sub))
			if !b.linkInside(root, sub, d) {
				return nil
			}
			if fi, err := statFollow(p, d); err == nil {
				out = append(out, b.entry(p, sub, fi))
			}
		}
		return nil
	})
	return out, truncated
}

// linkInside: symlinks are listed only when they resolve inside the root
// (an outside target's size and dates are not shown either).
func (b *Browser) linkInside(root, rel string, d fs.DirEntry) bool {
	if d.Type()&fs.ModeSymlink == 0 {
		return true
	}
	_, err := b.Sandbox.Resolve(root, rel)
	return err == nil
}

// Stat returns one entry (decorated like a listing entry).
func (b *Browser) Stat(ctx context.Context, root, rel string) (Entry, error) {
	abs, err := b.Sandbox.Resolve(root, rel)
	if err != nil {
		return Entry{}, err
	}
	rel, _ = cleanRel(rel)
	fi, err := os.Stat(abs)
	if err != nil {
		return Entry{}, osErr(err)
	}
	e := b.entry(abs, rel, fi)
	if rel == "" {
		r, _ := b.Sandbox.Root(root)
		e.Name = r.Title
	}
	es := []Entry{e}
	base := abs
	if !fi.IsDir() {
		base = filepath.Dir(abs)
	}
	b.decorate(ctx, root, base, es)
	return es[0], nil
}

// Abs returns the absolute path of an entry produced by this package.
func (e Entry) Abs() string { return e.abs }

// decorate fills item, download and thumbnail references. base is a folder
// containing every entry (used for one library query).
func (b *Browser) decorate(ctx context.Context, root, base string, es []Entry) {
	if b.Library != nil && len(es) > 0 {
		b.linkItems(ctx, base, es)
	}
	for i := range es {
		if ctx.Err() != nil {
			return
		}
		e := &es[i]
		if b.Seeds != nil {
			if ih, seeding, ok := b.Seeds.SeedInfo(e.abs); ok {
				e.Torrent = &TorrentRef{Infohash: ih, Seeding: seeding}
			}
		}
		if b.ThumbURL != nil && hasThumb(e.abs, e.Kind) {
			e.Thumb = b.ThumbURL(root, e.Path, e.Mtime.Unix())
		}
	}
}

func (b *Browser) linkItems(ctx context.Context, base string, es []Entry) {
	fs, err := b.Library.FilesUnder(ctx, base)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("files: library files under %s: %v", base, err)
		}
		return
	}
	if len(fs) == 0 {
		return
	}
	logItemErr := func(id int64, err error) {
		if !errors.Is(err, library.ErrNotFound) && ctx.Err() == nil {
			log.Printf("files: library item %d: %v", id, err)
		}
	}
	byPath := make(map[string]library.File, len(fs))
	for _, f := range fs {
		byPath[f.Path] = f
	}
	own := map[int64]*ItemRef{} // item id -> the item itself
	ownRef := func(id int64) *ItemRef {
		if r, ok := own[id]; ok {
			return r
		}
		var ref *ItemRef
		it, err := b.Library.Item(ctx, id)
		if err == nil && it.Kind == library.KindTrack && it.ParentID != 0 {
			// a song has no page of its own: its album does
			if al, aerr := b.Library.Item(ctx, it.ParentID); aerr == nil {
				it = al
			}
		}
		if err == nil {
			ref = &ItemRef{ID: it.ID, Kind: it.Kind, Title: it.Title}
		} else {
			logItemErr(id, err)
		}
		own[id] = ref
		return ref
	}
	items := map[int64]*ItemRef{} // file item id -> top-level item (movie / series)
	top := func(id int64) *ItemRef {
		if r, ok := items[id]; ok {
			return r
		}
		var ref *ItemRef
		it, err := b.Library.Item(ctx, id)
		for i := 0; err == nil && i < 3 && it.ParentID != 0 && (it.Kind == library.KindEpisode || it.Kind == library.KindSeason); i++ {
			it, err = b.Library.Item(ctx, it.ParentID)
		}
		if err == nil {
			ref = &ItemRef{ID: it.ID, Kind: it.Kind, Title: it.Title}
		} else {
			logItemErr(id, err)
		}
		items[id] = ref
		return ref
	}
	for i := range es {
		e := &es[i]
		if !e.IsDir {
			if f, ok := byPath[e.abs]; ok {
				e.Item = ownRef(f.ItemID)
			}
			continue
		}
		// A folder is linked when all library files inside belong to one movie or series.
		var ref *ItemRef
		single := true
		prefix := e.abs + string(filepath.Separator)
		for _, f := range fs {
			if !strings.HasPrefix(f.Path, prefix) {
				continue
			}
			r := top(f.ItemID)
			if r == nil || ref != nil && r.ID != ref.ID {
				single = false
				break
			}
			ref = r
		}
		if single && ref != nil {
			e.Item = ref
		}
	}
}

// sortEntries keeps folders first and sorts each group by key.
func sortEntries(es []Entry, key string, desc bool) {
	less := func(a, b *Entry) int {
		switch key {
		case "size":
			if a.Size != b.Size {
				return cmp64(a.Size, b.Size)
			}
		case "mtime":
			if !a.Mtime.Equal(b.Mtime) {
				return a.Mtime.Compare(b.Mtime)
			}
		case "kind":
			if a.Kind != b.Kind {
				return strings.Compare(a.Kind, b.Kind)
			}
		}
		return naturalCompare(a.Name, b.Name)
	}
	sort.SliceStable(es, func(i, j int) bool {
		a, b := &es[i], &es[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		c := less(a, b)
		if desc {
			return c > 0
		}
		return c < 0
	})
}

func cmp64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// naturalCompare compares case-insensitively with digit runs as numbers
// ("Серия 2" < "Серия 10").
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if isDigit(ra) && isDigit(rb) {
			da, db := digitRun(a), digitRun(b)
			ta, tb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(ta) != len(tb) {
				return cmp64(int64(len(ta)), int64(len(tb)))
			}
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		la, lb := unicode.ToLower(ra), unicode.ToLower(rb)
		if la != lb {
			return cmp64(int64(la), int64(lb))
		}
		a, b = a[na:], b[nb:]
	}
	return cmp64(int64(len(a)), int64(len(b)))
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func digitRun(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}
