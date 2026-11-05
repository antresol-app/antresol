package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/extaudio"
	"github.com/antresol-app/antresol/internal/library"
)

var (
	// ErrMediaOffline: the media disk (or a library folder) is not there;
	// nothing is deleted.
	ErrMediaOffline = errors.New("files: the media disk is not available")
	// ErrNothingOnDisk: none of the item's files is on disk; nothing is
	// deleted (an unmounted disk can look like that).
	ErrNothingOnDisk = errors.New("files: none of the item's files is on disk")
	// ErrDeleting: the item is being deleted by another request.
	ErrDeleting = errors.New("files: the item is being deleted already")
	// ErrOtherDelete: another delete runs and did not finish in time.
	ErrOtherDelete = errors.New("files: another delete is running")
	// ErrPlanChanged: files kept appearing in what would go; nothing moved.
	ErrPlanChanged = errors.New("files: the files changed while the delete was planned")
	// ErrImporting: data the item's files come from is still being put
	// into the library; nothing moved.
	ErrImporting = errors.New("files: the download is being imported")
)

// waitLock is how long Delete waits for another delete.
var waitLock = 5 * time.Second

// Linked is data elsewhere that library files were made from (the full
// build: a download's data in «Загрузки»).
type Linked struct {
	Path string
	// Key: the library files made from it carry it (files.torrent_infohash);
	// "" when only ItemID links it.
	Key    string
	ItemID int64 // the item it was added for, 0 = none
	// Files: its media and other files (another build: its own
	// selection), compared by size when no library file carries Key.
	Files []LinkedFile
	// Busy: it is being put into the library right now.
	Busy bool
}

// LinkedFile is one file of linked data.
type LinkedFile struct {
	Path string // inside it
	Size int64
}

// ItemDelete deletes library items: their files go to the trash («Корзина»
// in «Файлы», where they can be restored) and the library forgets the
// items at once, as a scan does when files vanish (missing_since), so a
// restore and the next scan bring them back with their progress.
//
// What goes: each file of the item, or the highest folder above it that
// holds nothing else: the item's own folder (the one holding all its files)
// and the folders inside it may hold its pictures, subtitles and texts but
// no file of another item and no media the library has not indexed; a
// folder above the own one goes only when it holds nothing but the item's
// own. A file left on its own takes its sidecars («Film.ru.srt», «Film.jpg»)
// along. Nothing outside Roots is touched; a file another item uses stays.
//
// Deletes run one at a time, holding Hold (the scans) from the plan to the
// end, and every path is checked before anything moves.
type ItemDelete struct {
	Ops     *Ops
	Library *library.Repo
	// Roots are the library folders (the scanner's).
	Roots []string
	// Offline reports why the media disk is unavailable ("" = available).
	Offline func() string
	// Linked (the sources build) names data elsewhere that the item's files
	// were made from (separate links or copies of it). When everything the
	// library made from one goes, it goes to the trash first (whatever holds
	// it open is stopped by the trash); otherwise it stays as it is.
	Linked func(ctx context.Context, set library.DeleteSet) []Linked
	// Hold (optional) keeps the scans off until release; it gives up when
	// a scan does not let go soon (library.ErrScanBusy) or ctx ends.
	Hold func(ctx context.Context) (release func(), err error)

	mu       sync.Mutex // one delete at a time
	inflight sync.Map   // item id → deleting
}

// ItemDeleteResult is what a delete did.
type ItemDeleteResult struct {
	Items []int64 `json:"items"` // the items the library forgot
	// Trashed: everything moved, the linked data included.
	Trashed []TrashItem `json:"trashed"`
	Size    int64       `json:"size"` // of the library's files moved (freed when the trash is emptied)
	// Late: files that appeared in a trashed folder after the last check;
	// they are in the trash with it (restored together).
	Late int `json:"late"`
}

// Plan returns what deleting the item would take away (see library.DeleteSet).
func (d *ItemDelete) Plan(ctx context.Context, id int64) (library.DeleteSet, error) {
	return d.Library.DeleteSet(ctx, id)
}

// Notes are the warnings of deleting set (another build adds its
// own).
func (d *ItemDelete) Notes(ctx context.Context, set library.DeleteSet) []string {
	out := []string{}
	if d.Linked == nil {
		return out
	}
	for _, l := range d.links(ctx, set) {
		w := keptWarningText(ctx, filepath.Base(l.Path))
		if d.coversLinked(ctx, set, l) {
			w = seedWarningText(ctx, filepath.Base(l.Path))
		}
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

func (d *ItemDelete) links(ctx context.Context, set library.DeleteSet) []Linked {
	if d.Linked == nil {
		return nil
	}
	return d.Linked(ctx, set)
}

// coversLinked reports whether everything the library made from l is in
// set: the files that carry its Key; with none, its media files matched
// by size; and its item, if it names one, goes as well (or keeps nothing
// outside set: the last season of a series pack).
func (d *ItemDelete) coversLinked(ctx context.Context, set library.DeleteSet, l Linked) bool {
	if l.ItemID != 0 && !d.goesWith(ctx, set, l.ItemID) {
		return false
	}
	mine := map[int64]bool{}
	for _, f := range set.Files {
		mine[f.ID] = true
	}
	var made []library.File
	if l.Key != "" {
		var err error
		if made, err = d.Library.FilesOfLink(ctx, l.Key); err != nil {
			return false
		}
	}
	if len(made) > 0 {
		for _, f := range made {
			if !mine[f.ID] {
				return false
			}
		}
		return true
	}
	// nothing in the library carries it (an older import): its media
	// files must all be the set's, by size
	if l.ItemID == 0 {
		return false
	}
	sizes := map[int64]int{}
	for _, f := range set.Files {
		sizes[f.Size]++
	}
	media := 0
	for _, f := range l.Files {
		if !foreignMedia[KindOf(f.Path, false)] {
			continue
		}
		media++
		if sizes[f.Size] == 0 {
			return false
		}
		sizes[f.Size]--
	}
	return media > 0
}

// goesWith reports whether item id is in set or is left with nothing
// outside it.
func (d *ItemDelete) goesWith(ctx context.Context, set library.DeleteSet, id int64) bool {
	for _, x := range set.Items {
		if x == id {
			return true
		}
	}
	other, err := d.Library.DeleteSet(ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		return true // gone already
	}
	if err != nil {
		return false
	}
	mine := map[int64]bool{}
	for _, f := range set.Files {
		mine[f.ID] = true
	}
	for _, f := range other.Files {
		if !mine[f.ID] {
			return false
		}
	}
	return true
}

// libRoot is the most specific library folder holding p ("" = none).
func (d *ItemDelete) libRoot(p string) string {
	best := ""
	for _, r := range d.Roots {
		r = filepath.Clean(r)
		if r != "" && r != "." && within(r, p) && p != r && len(r) > len(best) {
			best = r
		}
	}
	return best
}

func (d *ItemDelete) isRoot(dir string) bool {
	for _, r := range d.Roots {
		if filepath.Clean(r) == dir {
			return true
		}
	}
	for _, r := range d.Ops.Sandbox.Roots() {
		if r.Dir == dir {
			return true
		}
	}
	return dir == d.Ops.Sandbox.MediaRoot()
}

// sandboxPath maps abs to the most specific root of the file browser that
// may receive it in the trash (never the stream cache).
func (d *ItemDelete) sandboxPath(abs string) (root, rel string, err error) {
	var best Root
	for _, r := range d.Ops.Sandbox.Roots() {
		if !r.NoDrop && within(r.Dir, abs) && abs != r.Dir && len(r.Dir) > len(best.Dir) {
			best = r
		}
	}
	if best.ID == "" {
		return "", "", ErrOutside
	}
	rel, ok := d.Ops.Sandbox.Rel(best.ID, abs)
	if !ok || rel == "" {
		return "", "", ErrOutside
	}
	// symlinks that lead out of the root, the trash itself
	if _, err := d.Ops.Sandbox.Resolve(best.ID, rel); err != nil {
		return "", "", err
	}
	return best.ID, rel, nil
}

// foreignMedia: kinds of files that are someone's media when the library
// has not indexed them; they keep their folder.
var foreignMedia = map[string]bool{"video": true, "audio": true, "book": true, "comic": true, "archive": true}

// planner works out the paths to trash for one set.
type planner struct {
	d     *ItemDelete
	ctx   context.Context
	mine  map[string]bool // paths of the set's files
	own   string          // the item's own folder (holds all its files)
	cache map[string]bool // folder → may go whole
}

// whole reports whether dir may go to the trash whole: at or below the
// item's own folder it holds no file of another item and no media the
// library has not indexed; above it, nothing but the set's files and the
// own folder.
func (p *planner) whole(dir string) bool {
	if v, ok := p.cache[dir]; ok {
		return v
	}
	ok := true
	others, err := p.d.Library.FilesUnder(p.ctx, dir)
	if err != nil {
		ok = false
	}
	for _, f := range others {
		if !p.mine[f.Path] {
			ok = false
			break
		}
	}
	above := !within(p.own, dir)
	ok = ok && !p.hasOther(dir, above)
	p.cache[dir] = ok
	return ok
}

// hasOther reports whether dir holds a file that is not the set's nor
// beside it (or cannot be read through); strict: any file that is not the
// set's and not in the own folder (hidden files aside).
func (p *planner) hasOther(dir string, strict bool) bool {
	found := false
	filepath.WalkDir(dir, func(q string, e fs.DirEntry, err error) error {
		if err != nil {
			found = true
			return fs.SkipAll
		}
		if e.IsDir() {
			if strict && q == p.own {
				return fs.SkipDir
			}
			return nil
		}
		if p.mine[q] || hidden(e.Name()) {
			return nil
		}
		if strict || !beside(e.Name()) && !p.track(q) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// besideExts are the files that may go with an item's folder besides
// pictures and subtitles: descriptions, CUE sheets, rip logs, checksums.
var besideExts = map[string]bool{".nfo": true, ".cue": true, ".log": true, ".m3u": true, ".m3u8": true,
	".sfv": true, ".md5": true, ".accurip": true, ".ffp": true}

// beside reports whether a file that is not the item's may still go with
// its own folder: a picture, a subtitle or one of besideExts.
func beside(name string) bool {
	switch KindOf(name, false) {
	case "image", "subtitle":
		return true
	}
	return besideExts[strings.ToLower(filepath.Ext(name))]
}

// sidecarTags may stand between a video's name and the extension of its
// subtitles: «Film.ru.forced.srt».
var sidecarTags = map[string]bool{"forced": true, "sdh": true, "default": true, "full": true, "cc": true, "hi": true}

// isSidecarName reports whether name belongs to the file with stem: the
// stem, a dot, then the extension alone or language codes and tags before
// it — «Alien.jpg», «Alien.ru.srt», but not «Alien.Resurrection.ru.srt» or
// «Alien.2.jpg».
func isSidecarName(name, stem string) bool {
	rest, ok := strings.CutPrefix(name, stem+".")
	if !ok || rest == "" {
		return false
	}
	parts := strings.Split(rest, ".")
	for _, s := range parts[:len(parts)-1] {
		lower := strings.ToLower(s)
		if sidecarTags[lower] {
			continue
		}
		if l := len(s); l < 2 || l > 3 || strings.IndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
			return false
		}
	}
	return true
}

// track reports whether q is an audio track of one of the set's videos:
// an audio file beside it named after it («Film.ru.LostFilm.ac3» beside
// «Film.mkv», as the organizer places a release's sound tracks) that the
// library does not index.
func (p *planner) track(q string) bool {
	if !extaudio.IsAudio(q) {
		return false
	}
	dir, n := filepath.Dir(q), filepath.Base(q)
	for f := range p.mine {
		// a video's tracks only: audio beside a book or an audiobook is media
		if filepath.Dir(f) != dir || KindOf(f, false) != "video" {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		if !strings.HasPrefix(n, stem+".") {
			continue
		}
		// «Брат.2.ru.ac3» is «Брат.2.mkv»'s, not «Брат.mkv»'s
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			o := e.Name()
			other := strings.TrimSuffix(o, filepath.Ext(o))
			if KindOf(o, e.IsDir()) == "video" && len(other) > len(stem) && strings.HasPrefix(n, other+".") && !p.mine[filepath.Join(dir, o)] {
				return false
			}
		}
		_, err := p.d.Library.FileByPath(p.ctx, q)
		return errors.Is(err, library.ErrNotFound)
	}
	return false
}

// sidecars are the files beside f named after it that are not media.
func (p *planner) sidecars(f string) []string {
	dir, base := filepath.Dir(f), filepath.Base(f)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		n := e.Name()
		q := filepath.Join(dir, n)
		if e.IsDir() || q == f || p.mine[q] {
			continue
		}
		if strings.HasPrefix(n, stem+".") && p.track(q) {
			out = append(out, q)
			continue
		}
		if !isSidecarName(n, stem) {
			continue
		}
		switch KindOf(n, false) {
		case "subtitle", "text", "image":
			if _, err := p.d.Library.FileByPath(p.ctx, q); errors.Is(err, library.ErrNotFound) {
				out = append(out, q)
			}
		}
	}
	return out
}

// commonDir is the deepest folder holding every one of paths.
func commonDir(paths []string) string {
	dir := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		for !within(dir, p) && filepath.Dir(dir) != dir {
			dir = filepath.Dir(dir)
		}
	}
	return dir
}

// plan returns the paths to trash (none inside another), what each folder
// among them held (see restoreNew), and changes nothing.
func (d *ItemDelete) plan(ctx context.Context, set library.DeleteSet) (paths []string, held map[string]bool, err error) {
	if d.Offline != nil {
		if why := d.Offline(); why != "" {
			return nil, nil, fmt.Errorf("%w: %s", ErrMediaOffline, why)
		}
	}
	p := &planner{d: d, ctx: ctx, mine: map[string]bool{}, cache: map[string]bool{}}
	for _, f := range set.Files {
		p.mine[filepath.Clean(f.Path)] = true
	}
	var present []string
	roots := map[string]string{}
	for _, f := range set.Files {
		fp := filepath.Clean(f.Path)
		root := d.libRoot(fp)
		if root == "" {
			return nil, nil, ErrOutside
		}
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			return nil, nil, fmt.Errorf("%w: missing folder %s", ErrMediaOffline, root)
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, nil, err
		}
		if _, err := os.Lstat(fp); errors.Is(err, os.ErrNotExist) {
			continue // gone already: only forgotten
		} else if err != nil {
			return nil, nil, err
		}
		realDir, err := filepath.EvalSymlinks(filepath.Dir(fp))
		if err != nil {
			return nil, nil, err
		}
		if !within(realRoot, realDir) {
			return nil, nil, ErrOutside
		}
		present = append(present, fp)
		roots[fp] = root
	}
	if len(present) == 0 {
		if len(set.Files) > 0 {
			return nil, nil, ErrNothingOnDisk
		}
		return nil, map[string]bool{}, nil
	}
	p.own = commonDir(present)
	var cands []string
	for _, fp := range present {
		root := roots[fp]
		cand := fp
		for dir := filepath.Dir(fp); within(root, dir) && !d.isRoot(dir) && p.whole(dir); dir = filepath.Dir(dir) {
			cand = dir
		}
		cands = append(cands, cand)
		if cand == fp {
			cands = append(cands, p.sidecars(fp)...)
		}
	}
	paths = topmost(cands)
	held = map[string]bool{}
	for _, t := range paths {
		filepath.WalkDir(t, func(q string, _ fs.DirEntry, err error) error {
			if err == nil {
				held[q] = true
			}
			return nil
		})
	}
	return paths, held, nil
}

// topmost drops duplicates and paths inside another one of paths.
func topmost(paths []string) []string {
	sort.Slice(paths, func(i, j int) bool {
		return len(paths[i]) < len(paths[j]) || len(paths[i]) == len(paths[j]) && paths[i] < paths[j]
	})
	var out []string
	for _, p := range paths {
		covered := false
		for _, q := range out {
			if within(q, p) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// linked returns the data of d.Linked to trash before paths: only what
// the set covers whole (coversLinked), lies in the file browser's roots,
// holds no library file outside set, and is not inside one of paths
// already. Paths inside one of them are dropped from paths (it takes them
// along).
func (d *ItemDelete) linked(ctx context.Context, set library.DeleteSet, links []Linked, paths []string) (srcs, rest []string) {
	if len(links) == 0 {
		return nil, paths
	}
	mine := map[string]bool{}
	for _, f := range set.Files {
		mine[filepath.Clean(f.Path)] = true
	}
	var cands []string
	for _, l := range links {
		if d.coversLinked(ctx, set, l) {
			cands = append(cands, filepath.Clean(l.Path))
		}
	}
	for _, s := range topmost(cands) {
		if _, err := os.Lstat(s); err != nil {
			continue
		}
		inside := false
		for _, p := range paths {
			if within(p, s) {
				inside = true
			}
		}
		if inside || d.isRoot(s) {
			continue
		}
		if _, _, err := d.sandboxPath(s); err != nil {
			continue
		}
		under, err := d.Library.FilesUnder(ctx, s)
		if err != nil {
			continue
		}
		if f, err := d.Library.FileByPath(ctx, s); err == nil {
			under = append(under, f)
		}
		foreign := false
		for _, f := range under {
			if !mine[filepath.Clean(f.Path)] {
				foreign = true
			}
		}
		if !foreign {
			srcs = append(srcs, s)
		}
	}
	for _, p := range paths {
		inside := false
		for _, s := range srcs {
			if within(s, p) {
				inside = true
			}
		}
		if !inside {
			rest = append(rest, p)
		}
	}
	return srcs, rest
}

// devOf is the device of the file system p is on (a variable for tests).
var devOf = func(p string) (uint64, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return 0, err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), nil
	}
	return 0, nil
}

// movable checks, before anything moves, that each of paths can be
// renamed into its trash (Sandbox.TrashFor): inside a root of the file
// browser, on the same file system and mount as that trash, the trash (or
// where it will be made), its folder and a folder itself writable.
func (d *ItemDelete) movable(paths []string) error {
	sb := d.Ops.Sandbox
	for _, p := range paths {
		if _, _, err := d.sandboxPath(p); err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return osErr(err)
		}
		// its own trash (that of its mount), or where it will be made
		base := sb.TrashFor(p)
		if _, err := os.Stat(base); err != nil {
			base = filepath.Dir(base)
		}
		trashDev, err := devOf(base)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrMediaOffline, err)
		}
		dev, err := devOf(filepath.Dir(p))
		if err != nil {
			return osErr(err)
		}
		if dev != trashDev {
			return ErrCrossDevice
		}
		// st_dev is the same for two bind mounts of one disk; the mount is not
		if m, ok := sb.mountOf(filepath.Dir(p)); ok {
			if tm, ok := sb.mountOf(base); ok && tm != m {
				return ErrCrossDevice
			}
		}
		if syscall.Access(base, 2 /* W_OK */) != nil {
			return os.ErrPermission
		}
		if syscall.Access(filepath.Dir(p), 2) != nil {
			return os.ErrPermission
		}
		if fi.IsDir() && syscall.Access(p, 2) != nil {
			return os.ErrPermission // its ".." changes
		}
	}
	return nil
}

// trashAll trashes paths, grouped by root. It stops at the first error;
// the result lists what was moved until then.
func (d *ItemDelete) trashAll(ctx context.Context, paths []string, userID int64) ([]TrashItem, error) {
	type group struct {
		root string
		rels []string
	}
	var groups []*group
	byRoot := map[string]*group{}
	for _, p := range paths {
		root, rel, err := d.sandboxPath(p)
		if err != nil {
			return nil, err
		}
		g := byRoot[root]
		if g == nil {
			g = &group{root: root}
			byRoot[root] = g
			groups = append(groups, g)
		}
		g.rels = append(g.rels, rel)
	}
	var out []TrashItem
	for _, g := range groups {
		res, err := d.Ops.Trash(ctx, g.root, g.rels, true, userID)
		out = append(out, res.Trashed...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// beforeCheck runs before each look at the plan again, beforeMove after
// the last one, before the first move (tests).
var beforeCheck, beforeMove func()

// deletePlan is what one look found: the linked data to trash first, the
// library's paths, and everything inside those paths.
type deletePlan struct {
	srcs, paths []string
	held        map[string]bool
}

func (a deletePlan) same(b deletePlan) bool {
	if len(a.srcs) != len(b.srcs) || len(a.paths) != len(b.paths) || len(a.held) != len(b.held) {
		return false
	}
	for i := range a.srcs {
		if a.srcs[i] != b.srcs[i] {
			return false
		}
	}
	for i := range a.paths {
		if a.paths[i] != b.paths[i] {
			return false
		}
	}
	for k := range a.held {
		if !b.held[k] {
			return false
		}
	}
	return true
}

// look plans the delete of set and checks every path can move.
func (d *ItemDelete) look(ctx context.Context, set library.DeleteSet, links []Linked) (deletePlan, error) {
	paths, held, err := d.plan(ctx, set)
	if err != nil {
		return deletePlan{}, err
	}
	srcs, paths := d.linked(ctx, set, links, paths)
	if err := d.movable(append(append([]string{}, srcs...), paths...)); err != nil {
		return deletePlan{}, err
	}
	return deletePlan{srcs: srcs, paths: paths, held: held}, nil
}

// lockWithin takes mu, waiting at most waitLock (or until ctx ends).
func lockWithin(ctx context.Context, mu *sync.Mutex) error {
	deadline := time.NewTimer(waitLock)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for !mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrOtherDelete
		case <-tick.C:
		}
	}
	return nil
}

// Delete deletes the item (with everything under it): its files go to the
// trash, the library forgets it. Before anything moves every path is
// checked: one outside the library folders (ErrOutside), a missing
// library folder or an unavailable disk (ErrMediaOffline), no file on
// disk at all (ErrNothingOnDisk), a path that cannot be renamed into the
// trash (ErrCrossDevice, a permission error), linked data still being
// imported (ErrImporting) refuse the whole delete. The plan is looked at
// again under the lock; when files appeared it is made once more, and
// when they appear again the delete is refused (ErrPlanChanged). A second
// request for an item being deleted gets ErrDeleting; a scan or another
// delete that does not end within a few seconds, library.ErrScanBusy or
// ErrOtherDelete.
func (d *ItemDelete) Delete(ctx context.Context, id, userID int64) (ItemDeleteResult, error) {
	res := ItemDeleteResult{Items: []int64{}, Trashed: []TrashItem{}}
	if _, busy := d.inflight.LoadOrStore(id, true); busy {
		return res, ErrDeleting
	}
	defer d.inflight.Delete(id)
	if err := lockWithin(ctx, &d.mu); err != nil {
		return res, err
	}
	defer d.mu.Unlock()
	if d.Hold != nil {
		release, err := d.Hold(ctx)
		if err != nil {
			return res, err
		}
		defer release()
	}

	set, err := d.Library.DeleteSet(ctx, id)
	if err != nil {
		return res, err
	}
	links := d.links(ctx, set)
	for _, l := range links {
		if l.Busy {
			return res, ErrImporting
		}
	}
	pl, err := d.look(ctx, set, links)
	if err != nil {
		return res, err
	}
	for again := 0; ; again++ {
		if beforeCheck != nil {
			beforeCheck()
		}
		next, err := d.look(ctx, set, links)
		if err != nil {
			return res, err
		}
		if next.same(pl) {
			break
		}
		if again == 1 {
			return res, ErrPlanChanged
		}
		pl = next
	}
	if beforeMove != nil {
		beforeMove()
	}
	if len(pl.srcs) > 0 {
		// the data the files came from first: what holds it stops
		moved, err := d.trashAll(ctx, pl.srcs, userID)
		res.Trashed = append(res.Trashed, moved...)
		if err != nil {
			return res, err
		}
	}
	moved, err := d.trashAll(ctx, pl.paths, userID)
	res.Trashed = append(res.Trashed, moved...)
	for _, t := range moved {
		res.Size += t.Size
	}
	res.Late = d.lateFiles(moved, pl.held)
	if err != nil {
		// forget what has gone, so nothing broken stays on show
		if len(moved) > 0 {
			gone := d.goneFiles(set, moved)
			if ferr := d.Library.Forget(context.WithoutCancel(ctx), nil, gone); ferr != nil {
				log.Printf("files: forget after a partial delete of item %d: %v", id, ferr)
			}
		}
		return res, err
	}
	ids := make([]int64, 0, len(set.Files))
	for _, f := range set.Files {
		ids = append(ids, f.ID)
	}
	if err := d.Library.Forget(context.WithoutCancel(ctx), set.Items, ids); err != nil {
		return res, err
	}
	res.Items = set.Items
	return res, nil
}

// lateFiles counts (and logs) the files that reached a trashed folder
// after the last check: they stay in it, so restoring the folder brings
// them back where they were.
func (d *ItemDelete) lateFiles(moved []TrashItem, held map[string]bool) int {
	n := 0
	for _, t := range moved {
		if !t.IsDir || t.at == "" {
			continue
		}
		r, err := d.Ops.Sandbox.Root(t.Root)
		if err != nil {
			continue
		}
		orig := filepath.Join(r.Dir, filepath.FromSlash(t.Path))
		filepath.WalkDir(t.at, func(q string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(t.at, q)
			if back := filepath.Join(orig, rel); !held[back] {
				n++
				log.Printf("files: %s reached the folder during a delete: it is in the trash with it", back)
			}
			return nil
		})
	}
	return n
}

// goneFiles are the ids of set's files at or under the moved items.
func (d *ItemDelete) goneFiles(set library.DeleteSet, moved []TrashItem) []int64 {
	var out []int64
	for _, f := range set.Files {
		for _, t := range moved {
			r, err := d.Ops.Sandbox.Root(t.Root)
			if err == nil && within(filepath.Join(r.Dir, filepath.FromSlash(t.Path)), filepath.Clean(f.Path)) {
				out = append(out, f.ID)
				break
			}
		}
	}
	return out
}
