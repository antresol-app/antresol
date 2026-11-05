package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrIntoSelf: a folder cannot be moved into itself.
	ErrIntoSelf = errors.New("files: cannot move a folder into itself")
	// ErrNeedsConfirm: the change would stop sharing; see ConfirmError.
	ErrNeedsConfirm = errors.New("files: confirmation required")
)

// ConfirmError is returned by Rename and Move without confirm when the data
// belongs to a managed download: moving it away stops it like trashing does.
type ConfirmError struct{ Warnings []string }

func (e *ConfirmError) Error() string {
	return "files: confirmation required: " + strings.Join(e.Warnings, "; ")
}
func (e *ConfirmError) Is(target error) bool { return target == ErrNeedsConfirm }

// seedWarning is the warning shown when p (a file or folder) holds managed download data.
func (o *Ops) seedWarning(ctx context.Context, p string, fi os.FileInfo) []string {
	if o.Seeds == nil || !o.inTorrent(ctx, p, fi.IsDir()) {
		return nil
	}
	return []string{seedWarningText(ctx, fi.Name())}
}

// needsConfirm returns a ConfirmError when p holds managed download data and the
// caller has not confirmed.
func (o *Ops) needsConfirm(ctx context.Context, p string, fi os.FileInfo, confirm bool) error {
	if confirm {
		return nil
	}
	if w := o.seedWarning(ctx, p, fi); len(w) > 0 {
		return &ConfirmError{Warnings: w}
	}
	return nil
}

// Torrents keeps managed downloads in step with file operations on their data:
// without it a download whose files are moved away fetches them again.
// *torrent.Engine provides PlanRelocate and Relocate; Stop removes a download
// from the client and keeps its data (acquire.Service.Remove).
type Torrents interface {
	PlanRelocate(from, to string) (follow, stop []string)
	Relocate(ctx context.Context, from, to string, rename func() error) error
	Stop(ctx context.Context, infohash string) error
	// Pause and Resume hold a download while its files move: it is stopped
	// for good only once the rename has worked.
	Pause(ctx context.Context, infohash string) error
	Resume(ctx context.Context, infohash string) error
}

// ErrTorrent: a download using the data could not be stopped or re-pointed,
// so nothing was moved. errors.As finds a *TorrentError with the cause.
var ErrTorrent = errors.New("files: torrent could not be stopped")

type TorrentError struct{ Err error }

func (e *TorrentError) Error() string        { return "files: torrent: " + e.Err.Error() }
func (e *TorrentError) Is(target error) bool { return target == ErrTorrent }
func (e *TorrentError) Unwrap() error        { return e.Err }

// Ops performs admin file operations. Seeds, Torrents and Changed are optional.
type Ops struct {
	Sandbox  *Sandbox
	DB       *sql.DB
	Seeds    SeedInfo
	Torrents Torrents
	// Changed is called with the affected root ids after every change
	// (the API triggers a library rescan).
	Changed func(roots ...string)
}

type TrashItem struct {
	ID        int64     `json:"id"`
	Root      string    `json:"root"`
	Path      string    `json:"path"` // original path, relative to Root
	Name      string    `json:"name"`
	IsDir     bool      `json:"isDir"`
	Size      int64     `json:"size"`
	DeletedAt time.Time `json:"deletedAt"`
	at        string    // where it is in the trash (set by Trash only)
}

type TrashResult struct {
	Trashed  []TrashItem `json:"trashed"`
	Warnings []string    `json:"warnings"`
}

func (o *Ops) changed(roots ...string) {
	if o.Changed != nil {
		o.Changed(roots...)
	}
}

// stopTorrents holds the downloads (so they touch nothing while the files
// move), runs move, then stops them (data kept). If a download cannot be held,
// or move fails, the held ones are released: a failed move loses nothing.
func (o *Ops) stopTorrents(ctx context.Context, ihs []string, move func() error) error {
	var held []string
	release := func() {
		for _, ih := range held {
			if err := o.Torrents.Resume(context.WithoutCancel(ctx), ih); err != nil {
				log.Printf("files: resume torrent %s: %v", ih, err)
			}
		}
	}
	for _, ih := range ihs {
		if err := o.Torrents.Pause(ctx, ih); err != nil {
			release()
			return &TorrentError{Err: err}
		}
		held = append(held, ih)
	}
	if err := move(); err != nil {
		release()
		return err
	}
	for _, ih := range held {
		if err := o.Torrents.Stop(context.WithoutCancel(ctx), ih); err != nil {
			log.Printf("files: stop torrent %s after the move: %v", ih, err) // it stays paused, nothing is downloaded
		}
	}
	return nil
}

// moveData renames src to dst. Downloads whose data moves along are
// re-pointed (they keep serving from dst); those that cannot follow
// (a file taken out of a download, its folder renamed, stream cache
// entries) are stopped first. A failed rename comes back as is.
func (o *Ops) moveData(ctx context.Context, src, dst string) error {
	if o.Torrents == nil {
		return os.Rename(src, dst)
	}
	follow, stop := o.Torrents.PlanRelocate(src, dst)
	return o.stopTorrents(ctx, stop, func() error {
		if len(follow) == 0 {
			return os.Rename(src, dst)
		}
		var rerr error
		err := o.Torrents.Relocate(ctx, src, dst, func() error { rerr = os.Rename(src, dst); return rerr })
		if rerr != nil {
			return rerr
		}
		if err != nil {
			return &TorrentError{Err: err}
		}
		return nil
	})
}

func renameErr(err error) error {
	if errors.Is(err, ErrTorrent) {
		return err
	}
	var le *os.LinkError
	if errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV) {
		return ErrCrossDevice
	}
	if errors.Is(err, os.ErrExist) {
		return ErrExists
	}
	return osErr(err)
}

// existing resolves a non-root path that must exist.
func (o *Ops) existing(root, rel string) (abs, clean string, fi os.FileInfo, err error) {
	if abs, err = o.Sandbox.Resolve(root, rel); err != nil {
		return
	}
	if clean, _ = cleanRel(rel); clean == "" {
		return "", "", nil, ErrIsRoot
	}
	fi, err = os.Lstat(abs)
	return abs, clean, fi, osErr(err)
}

// free fails with ErrExists when p exists (unless it is the same file as
// self: a case-only rename on a case-insensitive disk).
func free(p, self string) error {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if self != "" {
		if si, err := os.Lstat(self); err == nil && os.SameFile(fi, si) && p != self {
			return nil
		}
	}
	return ErrExists
}

// Mkdir creates folder name inside rel.
func (o *Ops) Mkdir(ctx context.Context, root, rel, name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	parent, err := o.Sandbox.Resolve(root, rel)
	if err != nil {
		return err
	}
	if st, err := os.Stat(parent); err != nil {
		return osErr(err)
	} else if !st.IsDir() {
		return ErrNotDir
	}
	if r, err := o.Sandbox.Root(root); err != nil || r.NoDrop || o.Sandbox.NoDropAt(parent) {
		return ErrReadOnly // the stream cache belongs to the app
	}
	clean, _ := cleanRel(rel)
	p, err := o.Sandbox.Resolve(root, joinRel(clean, name))
	if err != nil {
		return err
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		return renameErr(err)
	}
	o.changed(root)
	return nil
}

// Rename gives rel a new name in the same folder. Data of a managed download needs
// confirm (otherwise a *ConfirmError with the warnings).
func (o *Ops) Rename(ctx context.Context, root, rel, newName string, confirm bool) error {
	if err := ValidName(newName); err != nil {
		return err
	}
	src, clean, fi, err := o.existing(root, rel)
	if err != nil {
		return err
	}
	dst, err := o.Sandbox.Resolve(root, joinRel(parentRel(clean), newName))
	if err != nil {
		return err
	}
	if dst == src {
		return nil
	}
	if r, err := o.Sandbox.Root(root); err != nil || r.NoDrop || o.Sandbox.NoDropAt(filepath.Dir(src)) {
		// renaming a stream's cached files would orphan them: the cache
		// prunes by download, not by file
		return ErrReadOnly
	}
	if err := free(dst, src); err != nil {
		return err
	}
	if err := o.needsConfirm(ctx, src, fi, confirm); err != nil {
		return err
	}
	if err := o.moveData(ctx, src, dst); err != nil {
		return renameErr(err)
	}
	o.changed(root)
	return nil
}

func parentRel(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// Move moves rel into the folder toPath of toRoot, keeping its name. The
// stream cache is never a destination (ErrReadOnly). Data of a managed download
// needs confirm (otherwise a *ConfirmError with the warnings).
func (o *Ops) Move(ctx context.Context, root, rel, toRoot, toPath string, confirm bool) error {
	src, clean, fi, err := o.existing(root, rel)
	if err != nil {
		return err
	}
	dir, err := o.Sandbox.Resolve(toRoot, toPath)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil {
		return osErr(err)
	} else if !st.IsDir() {
		return ErrNotDir
	}
	if r, err := o.Sandbox.Root(toRoot); err != nil || r.NoDrop || o.Sandbox.NoDropAt(dir) {
		return ErrReadOnly
	}
	toClean, _ := cleanRel(toPath)
	name := filepath.Base(clean)
	dst, err := o.Sandbox.Resolve(toRoot, joinRel(toClean, name))
	if err != nil {
		return err
	}
	if fi.IsDir() {
		rs, err1 := filepath.EvalSymlinks(src)
		rd, err2 := filepath.EvalSymlinks(dir)
		if err1 != nil || err2 != nil {
			return errors.Join(err1, err2)
		}
		if within(rs, rd) {
			return ErrIntoSelf
		}
	}
	if err := free(dst, ""); err != nil {
		return err
	}
	if err := o.needsConfirm(ctx, src, fi, confirm); err != nil {
		return err
	}
	if err := o.moveData(ctx, src, dst); err != nil {
		return renameErr(err)
	}
	o.changed(root, toRoot)
	return nil
}

// Trash moves paths to MediaRoot/.vault-trash/<ts>/<n>/<name>. When some of
// them belong to a managed download and confirm is false, nothing is moved and the
// result carries the warnings. On an error after some items were moved,
// the result still lists them.
func (o *Ops) Trash(ctx context.Context, root string, rels []string, confirm bool, userID int64) (TrashResult, error) {
	res := TrashResult{Trashed: []TrashItem{}, Warnings: []string{}}
	type target struct {
		abs, rel string
		fi       os.FileInfo
	}
	var ts []target
	for _, rel := range rels {
		abs, clean, fi, err := o.existing(root, rel)
		if err != nil {
			return res, err
		}
		ts = append(ts, target{abs, clean, fi})
	}
	// Drop duplicates and paths inside another selected folder: shallow
	// paths first, then each path is checked against every kept ancestor
	// (a plain string sort puts "X 2" between "X" and "X/a").
	sort.SliceStable(ts, func(i, j int) bool {
		di, dj := strings.Count(ts[i].rel, "/"), strings.Count(ts[j].rel, "/")
		if di != dj {
			return di < dj
		}
		return ts[i].rel < ts[j].rel
	})
	var sel []target
	kept := map[string]bool{}
	for _, t := range ts {
		if covered(kept, t.rel) {
			continue
		}
		kept[t.rel] = true
		sel = append(sel, t)
	}
	for _, t := range sel {
		res.Warnings = append(res.Warnings, o.seedWarning(ctx, t.abs, t.fi)...)
	}
	if len(res.Warnings) > 0 && !confirm {
		return res, nil
	}
	if len(sel) == 0 {
		return res, nil
	}
	// one batch folder in each trash used (a path goes to the trash of its
	// own mount: see Sandbox.TrashFor)
	stamp := time.Now().UTC().Format("20060102-150405") + "-"
	batches := map[string]string{}
	defer func() {
		for trash, batch := range batches {
			removeEmpty(batch, trash) // nothing moved: leave no empty batch folder
		}
	}()
	for i, t := range sel {
		trash := o.Sandbox.TrashFor(t.abs)
		batch, ok := batches[trash]
		if !ok {
			if err := ensureTrash(trash); err != nil {
				return res, err
			}
			b, err := os.MkdirTemp(trash, stamp)
			if err != nil {
				return res, err
			}
			batch, batches[trash] = b, b
		}
		sub := filepath.Join(batch, strconv.Itoa(i))
		if err := os.Mkdir(sub, 0o755); err != nil {
			return res, err
		}
		size := t.fi.Size()
		if t.fi.IsDir() {
			size = treeSize(ctx, t.abs, map[inode]bool{})
		}
		dst := filepath.Join(sub, t.fi.Name())
		var rerr error
		if o.Torrents != nil {
			// «Раздача … прекратится»: a download left running would
			// download the trashed files again.
			_, stop := o.Torrents.PlanRelocate(t.abs, "")
			rerr = o.stopTorrents(ctx, stop, func() error { return os.Rename(t.abs, dst) })
		} else {
			rerr = os.Rename(t.abs, dst)
		}
		if err := rerr; err != nil {
			if errors.Is(err, ErrTorrent) {
				os.Remove(sub)
				if len(res.Trashed) > 0 {
					o.changed(root)
				}
				return res, err
			}
			os.Remove(sub)
			if len(res.Trashed) > 0 {
				o.changed(root)
			}
			return res, renameErr(err)
		}
		trashRel, _ := filepath.Rel(trash, dst)
		it := TrashItem{Root: root, Path: t.rel, Name: t.fi.Name(), IsDir: t.fi.IsDir(), Size: size, DeletedAt: time.Now().UTC().Truncate(time.Second), at: dst}
		trashDir := "" // MediaRoot's
		if trash != o.Sandbox.TrashDir() {
			trashDir = trash
		}
		r, err := o.DB.ExecContext(ctx, `INSERT INTO trash(root, orig_path, trash_path, trash_dir, name, is_dir, size, deleted_by, deleted_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			it.Root, it.Path, filepath.ToSlash(trashRel), trashDir, it.Name, it.IsDir, it.Size, nullID(userID), it.DeletedAt.Format(time.RFC3339))
		if err != nil {
			// Keep the data reachable: put it back.
			if rerr := os.Rename(dst, t.abs); rerr != nil {
				err = errors.Join(err, rerr)
			}
			os.Remove(sub)
			if len(res.Trashed) > 0 {
				o.changed(root)
			}
			return res, err
		}
		if it.ID, err = r.LastInsertId(); err != nil {
			res.Trashed = append(res.Trashed, it) // moved and recorded, only the id is unknown
			o.changed(root)
			return res, fmt.Errorf("trash id: %w", err)
		}
		res.Trashed = append(res.Trashed, it)
	}
	o.changed(root)
	return res, nil
}

// covered reports whether rel or one of its parent folders is in kept.
func covered(kept map[string]bool, rel string) bool {
	for p := rel; p != ""; p = parentRel(p) {
		if kept[p] {
			return true
		}
	}
	return false
}

// TorrentOwner is optionally implemented by a SeedInfo that also knows
// download files it does not report as shared (unselected files of a partial
// download, which hold boundary pieces): file operations protect them too.
type TorrentOwner interface {
	Owner(path string) (infohash string, ok bool)
}

// owned reports whether p is (or contains) data of a live download.
func (o *Ops) owned(p string) bool {
	if ow, ok := o.Seeds.(TorrentOwner); ok {
		_, ok := ow.Owner(p)
		return ok
	}
	_, _, ok := o.Seeds.SeedInfo(p)
	return ok
}

// inTorrent reports whether p, or for a folder anything inside it, belongs to a managed download.
func (o *Ops) inTorrent(ctx context.Context, p string, dir bool) bool {
	if ok := o.owned(p); ok || !dir {
		return ok
	}
	found := false
	filepath.WalkDir(p, func(q string, _ fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if o.owned(q) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func nullID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

// removeEmpty removes dir and its empty parents up to (not including) stop.
func removeEmpty(dir, stop string) {
	for within(stop, dir) && dir != stop {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ListTrash returns trash items, newest first.
func (o *Ops) ListTrash(ctx context.Context) ([]TrashItem, error) {
	rows, err := o.DB.QueryContext(ctx, `SELECT id, root, orig_path, name, is_dir, size, deleted_at FROM trash ORDER BY deleted_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashItem{}
	for rows.Next() {
		var it TrashItem
		var at string
		if err := rows.Scan(&it.ID, &it.Root, &it.Path, &it.Name, &it.IsDir, &it.Size, &at); err != nil {
			return nil, err
		}
		if it.DeletedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("trash %d deleted_at: %w", it.ID, err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

type trashRow struct {
	root, orig, abs string
	trash           string // the trash it is in
}

func (o *Ops) trashRow(ctx context.Context, id int64) (trashRow, error) {
	var r trashRow
	var rel, dir string
	err := o.DB.QueryRowContext(ctx, `SELECT root, orig_path, trash_path, trash_dir FROM trash WHERE id=?`, id).Scan(&r.root, &r.orig, &rel, &dir)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	clean, err := cleanRel(rel)
	if err != nil || clean == "" {
		return r, ErrOutside
	}
	r.trash = o.Sandbox.TrashDir()
	if dir != "" {
		// another mount's trash: only one the sandbox knows
		if !o.Sandbox.knownTrash(dir) {
			return r, ErrOutside
		}
		r.trash = filepath.Clean(dir)
	}
	r.abs = filepath.Join(r.trash, filepath.FromSlash(clean))
	return r, nil
}

// Restore moves a trash item back to where it was.
func (o *Ops) Restore(ctx context.Context, id int64) error {
	r, err := o.trashRow(ctx, id)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(r.abs); err != nil {
		return osErr(err)
	}
	dst, err := o.Sandbox.Resolve(r.root, r.orig)
	if err != nil {
		return err
	}
	if clean, _ := cleanRel(r.orig); clean == "" {
		return ErrIsRoot
	}
	if err := free(dst, ""); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(r.abs, dst); err != nil {
		return renameErr(err)
	}
	if _, err := o.DB.ExecContext(ctx, `DELETE FROM trash WHERE id=?`, id); err != nil {
		return err
	}
	removeEmpty(filepath.Dir(r.abs), r.trash)
	o.changed(r.root)
	return nil
}

// Delete removes a trash item from disk for good.
func (o *Ops) Delete(ctx context.Context, id int64) error {
	r, err := o.trashRow(ctx, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(r.abs); err != nil {
		return err
	}
	if _, err := o.DB.ExecContext(ctx, `DELETE FROM trash WHERE id=?`, id); err != nil {
		return err
	}
	removeEmpty(filepath.Dir(r.abs), r.trash)
	return nil
}

// ensureTrash makes the trash folder trash when it is missing. A server
// running as root gives it the owner of the folder it is made in (the
// mount point), so the media's owner can manage it too.
func ensureTrash(trash string) error {
	if _, err := os.Stat(trash); err == nil {
		return nil
	}
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if fi, err := os.Stat(filepath.Dir(trash)); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				if err := os.Lchown(trash, int(st.Uid), int(st.Gid)); err != nil {
					log.Printf("files: owner of %s: %v", trash, err)
				}
			}
		}
	}
	return nil
}
