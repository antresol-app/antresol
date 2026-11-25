package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/mediacheck"
	"github.com/go-chi/chi/v5"
)

// foldersKey stores the library folders an administrator picked (the
// first-run wizard, «Настройки сервера → Медиатека») as config.Folders; the
// server reads them at its start, over VAULT_*_DIR.
const foldersKey = "folders"

// wizardKey is «pending» from the creation of the first administrator until
// the first-run wizard is finished or skipped; without a row (every server
// set up before the wizard, and every one that finished it) the wizard is
// not reachable.
const wizardKey = "setup_wizard"

// forgetKey lists the folders (absolute) taken out of the library by the
// last save: the next start forgets their files once more before its first
// scan, in case a scan of the old folders ran between the save and the
// restart.
const forgetKey = "folders_forget"

// Messages of refusals (catalog keys).
const (
	msgInside  = "Папка должна быть внутри медиа-папки"
	msgPlace   = "Папка должна быть в медиа-папке или в другой папке, подключённой к серверу"
	msgNotRoot = "Выберите папку внутри медиа-папки, а не её саму"
	msgTwin    = "Две библиотеки не могут лежать в одной папке"
	msgNested  = "Папка одной библиотеки не может лежать внутри папки другой"
	msgOffline = "Диск не подключён. Пока он не вернётся, папки не создаются"
	msgRemove  = "Из медиатеки уйдут файлы убранных папок. Подтвердите, что их нужно убрать"
	msgBusy    = "Медиатека сканируется. Попробуйте через минуту"
)

// LoadFolderSettings are the folders an administrator picked, and whether
// any were (config.Config.WithFolders applies them at start). A row from
// before several folders per kind (one path each) reads as one folder each.
func LoadFolderSettings(ctx context.Context, conn *sql.DB) (f config.Folders, custom bool, err error) {
	if found, err := db.GetJSON(ctx, conn, foldersKey, &f); err != nil || !found {
		return config.Folders{}, false, err
	}
	return f, true, nil
}

// TakeForgottenFolders are the folders the last save took out of the
// library (forgetKey), and drops the row: the start forgets them before its
// first scan.
func TakeForgottenFolders(ctx context.Context, db *sql.DB) ([]string, error) {
	var raw string
	err := db.QueryRowContext(ctx, `DELETE FROM settings WHERE key=? RETURNING value`, forgetKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

// FolderFlags are the marks of folders f (MediaRoot-relative paths made
// absolute with abs), as the scanner takes them.
func FolderFlags(f config.Folders, abs func(string) string) library.FolderFlags {
	var out library.FolderFlags
	for _, k := range config.FolderKinds {
		for _, d := range f.Of(k) {
			if d.Adult {
				out.Adult = append(out.Adult, abs(d.Path))
			}
			if d.Unlisted {
				out.Unlisted = append(out.Unlisted, abs(d.Path))
			}
		}
	}
	return out
}

// StorageInfo is how the server's folders are set up: the first-run
// wizard's «Папки» and «Права» steps and the same checks in «Настройки
// сервера». The media folder is Deps.MediaRoot.
type StorageInfo struct {
	// Env are the library folders VAULT_*_DIR give, what «Как в настройках
	// запуска» goes back to.
	Env config.Folders
	// TranscodeDir holds the playback segments; ConfigDir the server's own
	// settings and database.
	TranscodeDir, ConfigDir string
	// Private are folders the folder browser never shows, nor anything
	// that holds them (the settings and database, the import folder).
	Private []string
	// Mounts are the container's mount points the browser may show beside
	// the media folder (nil: mediacheck.SelfMounts).
	Mounts func() []string
	// Service is the compose service the server runs as, for the fix
	// commands ("" is "antresol").
	Service string
	// Offline is the scanner's own test of an unmounted media disk (a
	// folder it watches is missing: "" while the disk is there); nil: only
	// an empty media folder says so.
	Offline func() string
	// Restart restarts the server so that other folders take effect (nil:
	// the administrator restarts it).
	Restart func()
	// SetFlags hands the scanner the folders' marks when only they changed:
	// they apply without a restart (nil: from the next start).
	SetFlags func(library.FolderFlags)
}

// bootID tells this run of the server from the next: a restart is over when
// /api/health says another one.
var bootID = newBootID()

func newBootID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// restartDelay lets the answer that asked for the restart reach the browser.
var restartDelay = 300 * time.Millisecond

// dockerEnv says whether the server runs in a container (tests replace it).
var dockerEnv = func() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

type storageAPI struct {
	db   *sql.DB
	root string
	info StorageInfo
	deps Deps
}

// mountStorage: the folder checks of «Настройки сервера → Медиатека» and the
// first-run wizard (admin).
//
//   - GET /api/settings/storage: storageView of the saved folders;
//   - POST /api/settings/storage/check {folders}: the same for folders not
//     saved yet (a missing kind keeps its saved one);
//   - PUT /api/settings/storage {folders, confirmRemove} | {reset}: saves
//     them; the server restarts to use other folders (restarting in the
//     answer), marks alone apply at once; taking out a folder the library
//     has titles from is 409 {removing} until confirmRemove;
//   - POST /api/settings/storage/create {path}: makes the folder under the
//     media folder; answers its mediacheck.Dir;
//   - GET /api/settings/storage/browse?path=: the folder browser (browseView),
//     confined to the media folder and the server's other mounts;
//   - GET /api/setup/wizard: whether the first-run wizard is pending;
//   - POST /api/setup/wizard/finish: ends it and starts the library scan.
func mountStorage(r chi.Router, deps Deps) {
	if deps.Storage == nil || deps.DB == nil || deps.MediaRoot == "" {
		return
	}
	s := &storageAPI{db: deps.DB, root: filepath.Clean(deps.MediaRoot), info: *deps.Storage, deps: deps}
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/settings/storage", s.get)
		r.Post("/api/settings/storage/check", s.check)
		r.Put("/api/settings/storage", s.put)
		r.Post("/api/settings/storage/create", s.create)
		r.Get("/api/settings/storage/browse", s.browse)
		r.Get("/api/setup/wizard", s.wizard)
		r.Post("/api/setup/wizard/finish", s.finish)
	})
}

// folderRow is one library folder of storageView.
type folderRow struct {
	Kind string `json:"kind"`
	// Abs is the path inside the container; Default the kind's VAULT_*_DIR
	// one; Primary: the kind's first folder, where its downloads and uploads
	// go.
	Abs     string `json:"abs"`
	Default string `json:"default"`
	Primary bool   `json:"primary"`
	// Adult and Unlisted are the folder's marks (config.Folder).
	Adult    bool `json:"adult"`
	Unlisted bool `json:"unlisted"`
	// Indexed is how many titles the library has from the folder: what
	// taking it out removes from the library.
	Indexed int `json:"indexed"`
	// Suggest is a folder under the media folder that looks like this kind
	// and has files in it, offered while the kind's first folder is missing
	// or empty (the server makes empty default folders itself).
	Suggest string `json:"suggest,omitempty"`
	mediacheck.Dir
}

// storageView is what the server sees: the media folder with the folders
// under it, each library folder and the transcode folder with their checks,
// and, when the server may not write somewhere, how to fix it.
type storageView struct {
	Root      mediacheck.Dir      `json:"root"`
	Entries   []mediacheck.Entry  `json:"entries"`
	Folders   []folderRow         `json:"folders"`
	Transcode mediacheck.Dir      `json:"transcode"`
	Custom    bool                `json:"custom"`
	Server    mediacheck.Identity `json:"server"`
	Fix       *mediacheck.Fix     `json:"fix,omitempty"`
	// Places are where the folder browser starts: the media folder, then
	// the other folders mounted into the server.
	Places []string `json:"places"`
	// Offline: the library has files but the media disk looks unmounted;
	// CanCreate: «Создать» may make a folder (the media folder is there and
	// the disk is not offline).
	Offline   bool `json:"offline"`
	CanCreate bool `json:"canCreate"`
	// Restarts: saving other folders restarts the server by itself.
	Restarts bool `json:"restarts"`
}

// merged puts over's kinds over base's (a kind over has no folder for
// keeps base's).
func merged(base, over config.Folders) config.Folders {
	for _, k := range config.FolderKinds {
		if l := over.Of(k); len(l) > 0 {
			base.Set(k, l)
		}
	}
	return base
}

// rel is p as the settings keep it: relative to the media folder when it is
// inside, as given otherwise.
func (s *storageAPI) rel(p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(filepath.Clean(p))
	}
	if r, err := filepath.Rel(s.root, p); err == nil && r != "." && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.Clean(p)
}

func (s *storageAPI) abs(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(s.root, filepath.FromSlash(p))
}

// normalized is f with every path as the settings keep it.
func (s *storageAPI) normalized(f config.Folders) config.Folders {
	var out config.Folders
	for _, k := range config.FolderKinds {
		var l config.FolderList
		for _, d := range f.Of(k) {
			d.Path = s.rel(d.Path)
			l = append(l, d)
		}
		out.Set(k, l)
	}
	return out
}

// effective are the folders the server uses from its next start: the
// environment's, the administrator's over them, and over those a candidate.
func (s *storageAPI) effective(ctx context.Context, cand config.Folders) (f config.Folders, custom bool, err error) {
	saved, custom, err := LoadFolderSettings(ctx, s.db)
	if err != nil {
		return f, false, err
	}
	return s.normalized(merged(merged(s.info.Env, saved), cand)), custom, nil
}

// places are where the browser may go and the library folders may be: the
// media folder and the server's other mounts, never its own settings.
func (s *storageAPI) places() []string {
	mounts := s.info.Mounts
	if mounts == nil {
		mounts = mediacheck.SelfMounts
	}
	private := append([]string{s.info.ConfigDir}, s.info.Private...)
	return mediacheck.Places(s.root, mounts(), private)
}

func (s *storageAPI) view(ctx context.Context, cand config.Folders) (storageView, error) {
	f, custom, err := s.effective(ctx, cand)
	if err != nil {
		return storageView{}, err
	}
	v := storageView{Custom: custom, Server: mediacheck.Self(), Restarts: s.info.Restart != nil,
		Entries: []mediacheck.Entry{}, Folders: []folderRow{}, Places: s.places()}
	v.Root = mediacheck.Check(s.root)
	v.Root.Path = s.root
	rootThere := v.Root.Status != mediacheck.StatusMissing && v.Root.Status != mediacheck.StatusNotDir && v.Root.Status != mediacheck.StatusUnreadable
	if rootThere {
		if ents, err := mediacheck.Subfolders(s.root, nestedTops(f, s.info.Env)); err == nil {
			v.Entries = ents
		}
	}
	v.Offline = s.offline(ctx)
	v.CanCreate = rootThere && !v.Offline
	suggest := mediacheck.Suggest(v.Entries)
	items := map[string]int{}
	for _, e := range v.Entries {
		items[e.Name] = e.Items
	}
	var bad []mediacheck.Bad
	var mount []string
	// addBad: a folder that failed. What to hand over is the nearest folder
	// that exists, but never one above the folder the server was told to use
	// (the media folder, or the folder itself when it is outside): a place
	// that is not there is a mount to fix, not an owner to change.
	addBad := func(path string, d mediacheck.Dir) {
		if d.Status != mediacheck.StatusReadOnly && d.Status != mediacheck.StatusUnreadable {
			return
		}
		limit := path
		if mediacheck.Inside(s.root, path) {
			limit = s.root
		}
		if target, ok := mediacheck.NearestWithin(path, limit); ok {
			bad = append(bad, mediacheck.Bad{Path: target, Why: d.Why})
		} else {
			mount = append(mount, filepath.Clean(path))
		}
	}
	addBad(s.root, v.Root)
	taken := map[string]bool{}
	for _, k := range config.FolderKinds {
		for _, d := range f.Of(k) {
			taken[d.Path] = true
		}
	}
	repo := library.NewRepo(s.db)
	for _, k := range mediacheck.Kinds {
		for i, d := range f.Of(string(k)) {
			abs := s.abs(d.Path)
			row := folderRow{Kind: string(k), Abs: abs, Default: s.rel(s.info.Env.Of(string(k)).First()), Primary: i == 0,
				Adult: d.Adult, Unlisted: d.Unlisted, Dir: mediacheck.Check(abs)}
			row.Dir.Path = d.Path
			if n, err := repo.FolderCount(ctx, abs); err == nil {
				row.Indexed = n
			}
			if sg := suggest[k]; i == 0 && sg != "" && !taken[sg] {
				// a missing folder, or an empty one (the server makes the
				// default ones empty on a new install): offer one with files
				empty := row.Status == mediacheck.StatusOK && row.Items == 0 && !row.More
				if row.Status == mediacheck.StatusMissing || (empty && items[sg] > 0) {
					row.Suggest = sg
				}
			}
			// a folder under an unwritable media folder is covered by its fix
			if v.Root.Status != mediacheck.StatusReadOnly || !mediacheck.Inside(s.root, abs) {
				addBad(row.Abs, row.Dir)
			}
			v.Folders = append(v.Folders, row)
		}
	}
	if s.info.TranscodeDir != "" {
		within := ""
		if mediacheck.Inside(s.root, s.info.TranscodeDir) {
			within = s.root // not made on the disk under an unmounted one
		}
		if v.Offline {
			// looking must not put a folder on a disk that is not there: the
			// mount point would stop looking empty
			v.Transcode = mediacheck.Check(s.info.TranscodeDir)
		} else {
			v.Transcode = mediacheck.CheckTranscode(s.info.TranscodeDir, within)
		}
		v.Transcode.Path = s.info.TranscodeDir
		addBad(s.info.TranscodeDir, v.Transcode)
	}
	if len(bad) > 0 || len(mount) > 0 {
		fix := mediacheck.FixFor(mediacheck.FixInput{Bad: bad, Mount: mount, Service: s.info.Service, ConfigDir: s.info.ConfigDir,
			Docker: dockerEnv(), Self: v.Server})
		v.Fix = &fix
	}
	return v, nil
}

// offline: the media disk looks unmounted. The scanner's rule tells an
// unmounted disk from an empty one by the folders it watches, and only
// while the library has no file may a missing one be made; so a library
// with files and a media folder that is empty, or a watched folder that is
// gone, is a disk that is not there.
func (s *storageAPI) offline(ctx context.Context) bool {
	if library.LibraryEmpty(ctx, s.db) {
		return false
	}
	if s.info.Offline != nil && s.info.Offline() != "" {
		return true
	}
	return mediacheck.Unmounted(s.root)
}

// canCreate: «Создать» may make a folder. Never when the media folder is
// not there (it is never made, as the scanner never makes it) or the disk
// looks unmounted (a folder made on the disk under it would make an empty
// mount point look like the real one, and the next scan would hide the
// library).
func (s *storageAPI) canCreate(ctx context.Context) bool {
	st, err := os.Stat(s.root)
	return err == nil && st.IsDir() && !s.offline(ctx)
}

// nestedTops are the top-level folders the book folders live in ("books"):
// their subfolders are offered too.
func nestedTops(folders ...config.Folders) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range folders {
		for _, l := range []config.FolderList{f.Ebooks, f.Audiobooks, f.Comics} {
			for _, p := range l.Paths() {
				p = filepath.ToSlash(filepath.Clean(p))
				if top, _, ok := strings.Cut(p, "/"); ok && !filepath.IsAbs(p) && !seen[top] {
					seen[top] = true
					out = append(out, top)
				}
			}
		}
	}
	return out
}

func (s *storageAPI) get(w http.ResponseWriter, r *http.Request) {
	v, err := s.view(r.Context(), config.Folders{})
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// storageIn is the body of check and put: each kind named gets exactly the
// folders listed (a kind not named keeps its own). ConfirmRemove: the
// administrator agreed that the folders this takes out leave the library.
type storageIn struct {
	Folders       config.Folders `json:"folders"`
	Reset         bool           `json:"reset"`
	ConfirmRemove bool           `json:"confirmRemove"`
}

// valid cleans the folders a request names: each in a place (the media
// folder or another folder mounted into the server: a path under the media
// folder is kept relative, and no link leads out of the places), none the
// media folder itself, no folder twice. The message is Russian (a catalog
// key).
func (s *storageAPI) valid(cand config.Folders) (config.Folders, string) {
	var out config.Folders
	places := s.places()
	seen := map[string]bool{}
	for _, k := range config.FolderKinds {
		var l config.FolderList
		for _, d := range cand.Of(k) {
			p := strings.TrimSpace(d.Path)
			if p == "" {
				continue
			}
			if strings.ContainsRune(p, 0) {
				return out, msgPlace
			}
			abs := s.abs(filepath.FromSlash(p))
			if !filepath.IsAbs(p) {
				// relative is to the media folder, and stays in it
				if r, err := filepath.Rel(s.root, abs); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
					return out, msgInside
				}
			}
			if abs == s.root {
				return out, msgNotRoot
			}
			if msg := s.where(abs, places); msg != "" {
				return out, msg
			}
			d.Path = s.rel(abs)
			if seen[d.Path] {
				return out, msgTwin
			}
			seen[d.Path] = true
			l = append(l, d)
		}
		out.Set(k, l)
	}
	return out, ""
}

// where checks abs the way the file system will read it: every link
// followed, it must lie in a place (mediacheck.Confine) and not be the
// media folder. A path with a link in it that leads out is how a folder
// made or picked "inside" the media folder lands elsewhere. "" when it is
// fine.
func (s *storageAPI) where(abs string, places []string) string {
	if _, err := mediacheck.Confine(abs, places); err != nil {
		if mediacheck.Inside(s.root, abs) {
			return msgInside
		}
		return msgPlace
	}
	if realRoot, err := filepath.EvalSymlinks(s.root); err == nil {
		if real, err := mediacheck.Resolve(abs); err == nil && real == realRoot {
			return msgNotRoot
		}
	}
	return ""
}

// clash is why the folders cannot be saved: one folder twice, or one
// folder inside another (each scan would walk the other's files). Only
// pairs with a folder this request added are judged: a layout that was
// already like that in the environment is left to its owner.
func (s *storageAPI) clash(before, next config.Folders) string {
	type at struct{ kind, path string }
	had := map[at]bool{}
	for _, k := range config.FolderKinds {
		for _, p := range before.Of(k).Paths() {
			had[at{k, p}] = true
		}
	}
	var all []at
	for _, k := range config.FolderKinds {
		for _, p := range next.Of(k).Paths() {
			all = append(all, at{k, p})
		}
	}
	for i, a := range all {
		for _, b := range all[i+1:] {
			if a.path == b.path {
				return msgTwin
			}
			if had[a] && had[b] {
				continue
			}
			pa, pb := s.abs(a.path), s.abs(b.path)
			if mediacheck.Inside(pa, pb) || mediacheck.Inside(pb, pa) {
				return msgNested
			}
		}
	}
	return ""
}

func (s *storageAPI) check(w http.ResponseWriter, r *http.Request) {
	var in storageIn
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	cand, msg := s.valid(in.Folders)
	if msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	v, err := s.view(r.Context(), cand)
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// storagePut is the answer of PUT /api/settings/storage: the view, and
// whether the server is restarting to use the new folders.
type storagePut struct {
	storageView
	Restarting bool `json:"restarting"`
	// BootID is this run's (the one about to stop): the restart is over
	// when /api/health answers with another.
	BootID string `json:"bootId"`
	// Removed is how many titles left the library with the folders taken out.
	Removed int `json:"removed"`
}

// removal is a folder a save takes out, with what the library has from it.
type removal struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Items int    `json:"items"`
}

// removed are the folders of before that next has under no kind (a folder
// moved from one kind to another stays: its files are scanned again).
func (s *storageAPI) removed(ctx context.Context, before, next config.Folders) []removal {
	keep := map[string]bool{}
	for _, k := range config.FolderKinds {
		for _, p := range next.Of(k).Paths() {
			keep[p] = true
		}
	}
	repo := library.NewRepo(s.db)
	var out []removal
	for _, k := range config.FolderKinds {
		for _, p := range before.Of(k).Paths() {
			if keep[p] {
				continue
			}
			n, _ := repo.FolderCount(ctx, s.abs(p))
			out = append(out, removal{Kind: k, Path: p, Items: n})
		}
	}
	return out
}

func (s *storageAPI) put(w http.ResponseWriter, r *http.Request) {
	var in storageIn
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	cand, msg := s.valid(in.Folders)
	if msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	before, _, err := s.effective(ctx, config.Folders{})
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	env := s.normalized(s.info.Env)
	var next config.Folders
	if in.Reset {
		next = env
	} else {
		next, _, err = s.effective(ctx, cand)
		if err != nil {
			internalErr(w, r, "storage settings", err)
			return
		}
	}
	ref := before
	if in.Reset {
		ref = next // going back to the environment's folders: its layout is its owner's
	}
	if msg := s.clash(ref, next); msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	// taking a folder out takes its titles out of the library (never its
	// files): only once the administrator said so
	gone := s.removed(ctx, before, next)
	if !in.ConfirmRemove {
		for _, g := range gone {
			if g.Items > 0 {
				writeJSON(w, http.StatusConflict, map[string]any{"error": i18n.Localize(ctx, msgRemove), "removing": gone})
				return
			}
		}
	}
	var release func()
	if len(gone) > 0 && s.deps.Jobs != nil && s.deps.Jobs.Scanner != nil {
		// no scan of the old folders runs while their files leave
		if release, err = s.deps.Jobs.Scanner.Hold(ctx); err != nil {
			writeErr(w, r, http.StatusConflict, msgBusy)
			return
		}
		defer release()
	}
	if next.Equal(env) {
		// the same as the server's own: no row, so a later change of
		// VAULT_*_DIR is still heard
		err = db.DeleteSetting(ctx, s.db, foldersKey)
	} else {
		err = db.PutJSON(ctx, s.db, foldersKey, next)
	}
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	removedItems, err := s.forget(ctx, gone)
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	flags := FolderFlags(next, s.abs)
	moved := !next.SamePaths(before)
	if !moved && !next.Equal(before) {
		// only marks changed: they apply now, to what is there and to the
		// next scans, without a restart
		if s.info.SetFlags != nil {
			s.info.SetFlags(flags)
		}
		repo := library.NewRepo(s.db)
		repo.SetFolderFlags(flags)
		if err := repo.SyncFolderFlags(ctx); err != nil {
			internalErr(w, r, "storage settings", err)
			return
		}
	}
	v, err := s.view(ctx, config.Folders{})
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	out := storagePut{storageView: v, BootID: bootID, Removed: removedItems}
	if moved && s.info.Restart != nil {
		out.Restarting = true
		restart := s.info.Restart
		time.AfterFunc(restartDelay, restart)
	}
	writeJSON(w, http.StatusOK, out)
}

// forget takes the files of the folders gone out of the library now, and
// keeps the folders for the next start to do it again before its first
// scan (TakeForgottenFolders).
func (s *storageAPI) forget(ctx context.Context, gone []removal) (int, error) {
	if len(gone) == 0 {
		return 0, nil
	}
	prev, _ := TakeForgottenFolders(ctx, s.db)
	dirs := append([]string(nil), prev...)
	n := 0
	repo := library.NewRepo(s.db)
	for _, g := range gone {
		abs := s.abs(g.Path)
		dirs = append(dirs, abs)
		if _, err := repo.ForgetFolder(ctx, abs); err != nil {
			return n, err
		}
		n += g.Items
	}
	return n, db.PutJSON(ctx, s.db, forgetKey, dirs)
}

// browseView is GET /api/settings/storage/browse: one folder the browser
// shows (Dir: its check, the write test included), the folders in it, and
// the places it may go.
type browseView struct {
	Places []string `json:"places"`
	// Path is the folder shown ("" with no place to show); Parent the one
	// above it, "" at a place.
	Path   string           `json:"path"`
	Parent string           `json:"parent"`
	Dir    mediacheck.Dir   `json:"dir"`
	Dirs   []mediacheck.Sub `json:"dirs"`
	// Truncated: the folder has more folders than the list holds.
	Truncated bool `json:"truncated,omitempty"`
	// Library: the kind whose folder this is already ("" none).
	Library string `json:"library,omitempty"`
}

// browse shows ?path= (absolute; "" the first place). Only folders in the
// places: anything else, a ".." or a link out of them, is 400.
func (s *storageAPI) browse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	places := s.places()
	v := browseView{Places: places, Dirs: []mediacheck.Sub{}}
	path := r.URL.Query().Get("path")
	if path == "" {
		if len(places) == 0 {
			writeJSON(w, http.StatusOK, v)
			return
		}
		path = places[0]
	}
	place, err := mediacheck.Confine(path, places)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgPlace)
		return
	}
	path = filepath.Clean(path)
	v.Path, v.Dir = path, mediacheck.Check(path)
	v.Dir.Path = path
	if path != place {
		v.Parent = filepath.Dir(path)
	}
	if v.Dir.Status == mediacheck.StatusOK || v.Dir.Status == mediacheck.StatusReadOnly {
		subs, truncated, err := mediacheck.Browse(path, places)
		if err == nil {
			v.Dirs, v.Truncated = subs, truncated
		}
	}
	if f, _, err := s.effective(ctx, config.Folders{}); err == nil {
		for _, k := range config.FolderKinds {
			for _, p := range f.Of(k).Paths() {
				if s.abs(p) == path {
					v.Library = k
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// create makes a folder under the media folder, as «Создать» asks. It does
// not while the disk looks unmounted: the folder would be made on the disk
// under the mount point, and the next scan would take the empty mount point
// for the real disk.
func (s *storageAPI) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	cand, msg := s.valid(config.Folders{Movies: config.Dirs(in.Path)})
	if msg != "" || len(cand.Movies) == 0 || filepath.IsAbs(cand.Movies.First()) {
		if msg == "" || msg == msgPlace {
			msg = msgInside
		}
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	if !s.canCreate(r.Context()) {
		writeErr(w, r, http.StatusConflict, msgOffline)
		return
	}
	abs := s.abs(cand.Movies.First())
	var d mediacheck.Dir
	if err := os.MkdirAll(abs, 0o755); err != nil {
		log.Printf("api: create folder %s: %v", abs, err)
		d = mediacheck.Dir{Status: mediacheck.StatusReadOnly, Error: err.Error(), Why: mediacheck.Classify(err)}
	} else {
		d = mediacheck.Check(abs)
	}
	d.Path = cand.Movies.First()
	writeJSON(w, http.StatusOK, d)
}

// markWizardPending is called when the first administrator is created.
func markWizardPending(ctx context.Context, conn *sql.DB) {
	if conn == nil {
		return
	}
	if err := db.PutSetting(ctx, conn, wizardKey, "pending"); err != nil {
		log.Printf("api: %s: %v", wizardKey, err)
	}
}

func wizardPending(ctx context.Context, conn *sql.DB) bool {
	v, _, err := db.GetSetting(ctx, conn, wizardKey)
	if err != nil && ctx.Err() == nil {
		log.Printf("api: %s: %v", wizardKey, err)
	}
	return v == "pending"
}

func (s *storageAPI) wizard(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"pending": wizardPending(r.Context(), s.db)})
}

// finish ends the wizard (done, or every step skipped) and starts a library
// scan. Once it is over it answers 403: the steps are then only in the
// settings.
func (s *storageAPI) finish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !wizardPending(ctx, s.db) {
		writeErr(w, r, http.StatusForbidden, "Первая настройка уже завершена")
		return
	}
	// only the request that finds it pending ends it (two tabs: one scan)
	res, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=? AND value='pending'`, wizardKey)
	if err != nil {
		internalErr(w, r, "setup wizard", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, r, http.StatusForbidden, "Первая настройка уже завершена")
		return
	}
	if s.deps.Jobs != nil {
		s.deps.Jobs.Trigger()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"pending": false})
}
