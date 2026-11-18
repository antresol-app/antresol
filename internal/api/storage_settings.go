package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/config"
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

// Messages of refusals (catalog keys).
const (
	msgInside  = "Папка должна быть внутри медиа-папки"
	msgNotRoot = "Выберите папку внутри медиа-папки, а не её саму"
	msgTwin    = "Две библиотеки не могут лежать в одной папке"
	msgNested  = "Папка одной библиотеки не может лежать внутри папки другой"
	msgOffline = "Диск не подключён. Пока он не вернётся, папки не создаются"
)

// LoadFolderSettings are the folders an administrator picked, and whether
// any were (config.Config.WithFolders applies them at start).
func LoadFolderSettings(ctx context.Context, db *sql.DB) (f config.Folders, custom bool, err error) {
	var raw string
	err = db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, foldersKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return config.Folders{}, false, err
	}
	return f, true, nil
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
//   - PUT /api/settings/storage {folders} | {reset}: saves them; the server
//     restarts to use them (restarting in the answer);
//   - POST /api/settings/storage/create {path}: makes the folder under the
//     media folder; answers its mediacheck.Dir;
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
		r.Get("/api/setup/wizard", s.wizard)
		r.Post("/api/setup/wizard/finish", s.finish)
	})
}

// folderRow is one library folder of storageView.
type folderRow struct {
	Kind string `json:"kind"`
	// Abs is the path inside the container; Default the VAULT_*_DIR one.
	Abs     string `json:"abs"`
	Default string `json:"default"`
	// Suggest is a folder under the media folder that looks like this kind
	// and has files in it, offered while this one is missing or empty (the
	// server makes empty default folders itself).
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
	// Offline: the library has files but the media disk looks unmounted;
	// CanCreate: «Создать» may make a folder (the media folder is there and
	// the disk is not offline).
	Offline   bool `json:"offline"`
	CanCreate bool `json:"canCreate"`
	// Restarts: saving other folders restarts the server by itself.
	Restarts bool `json:"restarts"`
}

// get reads one kind's folder.
func get(f config.Folders, k mediacheck.Kind) string {
	switch k {
	case mediacheck.Movies:
		return f.Movies
	case mediacheck.Series:
		return f.Series
	case mediacheck.Music:
		return f.Music
	case mediacheck.Ebooks:
		return f.Ebooks
	case mediacheck.Audiobooks:
		return f.Audiobooks
	}
	return f.Comics
}

// setKind stores a folder into f.
func setKind(f *config.Folders, k mediacheck.Kind, v string) {
	switch k {
	case mediacheck.Movies:
		f.Movies = v
	case mediacheck.Series:
		f.Series = v
	case mediacheck.Music:
		f.Music = v
	case mediacheck.Ebooks:
		f.Ebooks = v
	case mediacheck.Audiobooks:
		f.Audiobooks = v
	default:
		f.Comics = v
	}
}

// merged puts over's folders over base's (an empty one leaves the base's).
func merged(base, over config.Folders) config.Folders {
	for _, k := range mediacheck.Kinds {
		if v := get(over, k); v != "" {
			setKind(&base, k, v)
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
	return p
}

func (s *storageAPI) abs(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(s.root, filepath.FromSlash(p))
}

// effective are the folders the server uses from its next start: the
// environment's, the administrator's over them, and over those a candidate.
func (s *storageAPI) effective(ctx context.Context, cand config.Folders) (f config.Folders, custom bool, err error) {
	saved, custom, err := LoadFolderSettings(ctx, s.db)
	if err != nil {
		return f, false, err
	}
	f = merged(merged(s.info.Env, saved), cand)
	for _, k := range mediacheck.Kinds {
		setKind(&f, k, s.rel(get(f, k)))
	}
	return f, custom, nil
}

func (s *storageAPI) view(ctx context.Context, cand config.Folders) (storageView, error) {
	f, custom, err := s.effective(ctx, cand)
	if err != nil {
		return storageView{}, err
	}
	v := storageView{Custom: custom, Server: mediacheck.Self(), Restarts: s.info.Restart != nil,
		Entries: []mediacheck.Entry{}, Folders: []folderRow{}}
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
	for _, k := range mediacheck.Kinds {
		taken[get(f, k)] = true
	}
	for _, k := range mediacheck.Kinds {
		p := get(f, k)
		row := folderRow{Kind: string(k), Abs: s.abs(p), Default: s.rel(get(s.info.Env, k)), Dir: mediacheck.Check(s.abs(p))}
		row.Dir.Path = p
		if sg := suggest[k]; sg != "" && !taken[sg] {
			// a missing folder, or an empty one (the server makes the
			// default ones empty on a new install): offer one with files
			empty := row.Status == mediacheck.StatusOK && row.Items == 0 && !row.More
			if row.Status == mediacheck.StatusMissing || (empty && items[sg] > 0) {
				row.Suggest = sg
			}
		}
		// a folder under an unwritable media folder is covered by its fix
		if v.Root.Status != mediacheck.StatusReadOnly {
			addBad(row.Abs, row.Dir)
		}
		v.Folders = append(v.Folders, row)
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
		for _, p := range []string{f.Ebooks, f.Audiobooks, f.Comics} {
			p = filepath.ToSlash(filepath.Clean(p))
			if top, _, ok := strings.Cut(p, "/"); ok && !filepath.IsAbs(p) && !seen[top] {
				seen[top] = true
				out = append(out, top)
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

// storageIn is the body of check and put.
type storageIn struct {
	Folders config.Folders `json:"folders"`
	Reset   bool           `json:"reset"`
}

// valid cleans the folders a request names: each inside the media folder (a
// path under it is kept relative, and no link leads out of it), none the
// media folder itself. The message is Russian (a catalog key).
func (s *storageAPI) valid(cand config.Folders) (config.Folders, string) {
	var out config.Folders
	for _, k := range mediacheck.Kinds {
		p := strings.TrimSpace(get(cand, k))
		if p == "" {
			continue
		}
		if strings.ContainsRune(p, 0) {
			return out, msgInside
		}
		p = filepath.Clean(filepath.FromSlash(p))
		abs := s.abs(p)
		r, err := filepath.Rel(s.root, abs)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return out, msgInside
		}
		if r == "." {
			return out, msgNotRoot
		}
		if msg := s.where(abs); msg != "" {
			return out, msg
		}
		setKind(&out, k, filepath.ToSlash(r))
	}
	return out, ""
}

// where checks abs the way the file system will read it: every link
// followed, it must lie inside the media folder (itself followed) and not be
// that folder. A path with a link in it that leads out is how a folder made
// or picked "inside" the media folder lands elsewhere. "" when it is fine.
func (s *storageAPI) where(abs string) string {
	realRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return "" // no media folder: nothing there to lead out of
	}
	real, err := mediacheck.Resolve(abs)
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "" // cannot be told; the server cannot make anything there either
	case err != nil || !mediacheck.Inside(realRoot, real):
		return msgInside
	case real == realRoot:
		return msgNotRoot
	}
	return ""
}

// clash is why the folders cannot be saved: two kinds in one folder, or one
// kind's folder inside another's (each scan would walk the other's files).
// Only folders this request changed are judged: a layout that was already
// like that in the environment is left to its owner.
func (s *storageAPI) clash(before, next config.Folders) string {
	for i, a := range mediacheck.Kinds {
		for _, b := range mediacheck.Kinds[i+1:] {
			if get(next, a) == get(next, b) {
				return msgTwin
			}
			if get(next, a) == get(before, a) && get(next, b) == get(before, b) {
				continue
			}
			pa, pb := s.abs(get(next, a)), s.abs(get(next, b))
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
	var next config.Folders
	if in.Reset {
		next = s.normalized(s.info.Env)
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
	if next == s.normalized(s.info.Env) {
		// the same as the server's own: no row, so a later change of
		// VAULT_*_DIR is still heard
		_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, foldersKey)
	} else {
		raw, _ := json.Marshal(next)
		_, err = s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, foldersKey, string(raw))
	}
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	v, err := s.view(ctx, config.Folders{})
	if err != nil {
		internalErr(w, r, "storage settings", err)
		return
	}
	out := storagePut{storageView: v, BootID: bootID}
	if next != before && s.info.Restart != nil {
		out.Restarting = true
		restart := s.info.Restart
		time.AfterFunc(restartDelay, restart)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *storageAPI) normalized(f config.Folders) config.Folders {
	for _, k := range mediacheck.Kinds {
		setKind(&f, k, s.rel(get(f, k)))
	}
	return f
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
	cand, msg := s.valid(config.Folders{Movies: in.Path})
	if msg != "" || cand.Movies == "" {
		if msg == "" {
			msg = msgInside
		}
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	if !s.canCreate(r.Context()) {
		writeErr(w, r, http.StatusConflict, msgOffline)
		return
	}
	abs := s.abs(cand.Movies)
	var d mediacheck.Dir
	if err := os.MkdirAll(abs, 0o755); err != nil {
		log.Printf("api: create folder %s: %v", abs, err)
		d = mediacheck.Dir{Status: mediacheck.StatusReadOnly, Error: err.Error(), Why: mediacheck.Classify(err)}
	} else {
		d = mediacheck.Check(abs)
	}
	d.Path = cand.Movies
	writeJSON(w, http.StatusOK, d)
}

// markWizardPending is called when the first administrator is created.
func markWizardPending(ctx context.Context, db *sql.DB) {
	if db == nil {
		return
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, 'pending')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, wizardKey); err != nil {
		log.Printf("api: %s: %v", wizardKey, err)
	}
}

func wizardPending(ctx context.Context, db *sql.DB) bool {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, wizardKey).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
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
