package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/go-chi/chi/v5"
)

// The core API's library writes (library.write): import, references,
// owners; and the image cache (images). These routes only scan and
// record: they never move, overwrite or delete a file on disk.

const (
	msgAddonOutsideLibraryPath = "Путь вне медиатеки: %s"
	msgAddonTooManyPaths       = "Не больше 10 000 путей за раз"
	msgAddonTooManyPairs       = "Не больше 500 000 пар за раз"
	msgAddonNoProfile          = "Профиль не найден"
	msgAddonImagesOff          = "Картинки недоступны"
	msgAddonImageHTTPS         = "Нужен адрес https"
	msgAddonImageFailed        = "Не удалось загрузить картинку"

	addonMaxPaths     = 10_000
	addonMaxRefPairs  = 500_000
	addonPathsBody    = 8 << 20  // 10 000 paths of up to ~800 bytes
	addonRefPairsBody = 32 << 20 // POST /library/refs
	addonMaxImage     = 20 << 20
)

func mountAddonCoreWrite(r chi.Router, a *addonsAPI) {
	r.Post("/library/import", a.coreImport)
	r.Put("/library/refs/{ref}", a.coreRefReplace)
	r.Delete("/library/refs/{ref}", a.coreRefDelete)
	r.Post("/library/refs", a.coreRefsBatch)
	r.Post("/library/owners", a.coreOwners)
	r.Post("/images", a.coreImage)
}

// writeLibrary: the calling addon has library.write and the server a
// library and its references; else the answer is written, ok is false.
func (a *addonsAPI) writeLibrary(w http.ResponseWriter, r *http.Request) (addons.Installed, bool) {
	in, ok := a.callingAddon(w, r, "library.write")
	if !ok {
		return in, false
	}
	if a.lib == nil || a.refs == nil || a.jobs == nil || a.jobs.Scanner == nil {
		writeErr(w, r, http.StatusServiceUnavailable, addons.ErrNoLibrary.Error())
		return in, false
	}
	return in, true
}

// decodeMax is decode with a body limit of max bytes.
func decodeMax(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		badRequest(w, r)
		return false
	}
	return true
}

// writePaths checks every path an addon writes for (libGuard.checkWrite),
// before anything is done: the first one outside the library, or a root
// itself, is 400 with its name. ok is false then; missing are those not
// on disk, each once.
func (a *addonsAPI) writePaths(w http.ResponseWriter, r *http.Request, paths []string) (missing []string, ok bool) {
	g := a.newLibGuard()
	missing = []string{}
	seen := map[string]bool{}
	for _, p := range paths {
		exists, ok := g.checkWrite(p)
		if !ok {
			writeErr(w, r, http.StatusBadRequest, fmt.Sprintf(msgAddonOutsideLibraryPath, p))
			return nil, false
		}
		if !exists && !seen[p] {
			seen[p] = true
			missing = append(missing, p)
		}
	}
	return missing, true
}

// knownUser: id is a profile (a.users unset: the claim itself skips
// unknown ids). Else 400 is written.
func (a *addonsAPI) knownUser(w http.ResponseWriter, r *http.Request, id int64) bool {
	if id <= 0 {
		writeErr(w, r, http.StatusBadRequest, msgAddonNoProfile)
		return false
	}
	if a.users == nil {
		return true
	}
	_, err := a.users.Get(r.Context(), id)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeErr(w, r, http.StatusBadRequest, msgAddonNoProfile)
		return false
	case err != nil:
		libFail(w, r, err)
		return false
	}
	return true
}

// coreImport: {paths, ref?, ownerUserId?} → ImportResult. Everything is
// checked first; then, one import at a time and to the end even if the
// addon goes away: the library is scanned, the files found at the paths
// (a folder: every file below it) get ref and the owner, the enrichers
// are woken and library.updated published.
func (a *addonsAPI) coreImport(w http.ResponseWriter, r *http.Request) {
	in, ok := a.writeLibrary(w, r)
	if !ok {
		return
	}
	var body struct {
		Paths       []string `json:"paths"`
		Ref         string   `json:"ref"`
		OwnerUserID int64    `json:"ownerUserId"`
	}
	if !decodeMax(w, r, &body, addonPathsBody) {
		return
	}
	if len(body.Paths) == 0 || (body.Ref != "" && !addons.ValidRef(body.Ref)) {
		badRequest(w, r)
		return
	}
	if len(body.Paths) > addonMaxPaths {
		writeErr(w, r, http.StatusBadRequest, msgAddonTooManyPaths)
		return
	}
	if body.OwnerUserID != 0 && !a.knownUser(w, r, body.OwnerUserID) {
		return
	}
	missing, ok := a.writePaths(w, r, body.Paths)
	if !ok {
		return
	}
	res, err := a.runImport(context.WithoutCancel(r.Context()), in.ID, body.Paths, missing, body.Ref, body.OwnerUserID)
	if err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *addonsAPI) runImport(ctx context.Context, addonID string, paths, missing []string, ref string, owner int64) (addonkit.ImportResult, error) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	res := addonkit.ImportResult{Files: []addonkit.ImportedFile{}, Missing: missing, Taken: []string{}}
	if _, err := a.jobs.Scanner.ScanAll(ctx); err != nil {
		// one failing root does not stop the others: import what was found
		log.Printf("api: addon %s import: scan: %v", addonID, err)
	}
	gone := map[string]bool{} // already in res.Missing
	for _, p := range missing {
		gone[p] = true
	}
	miss := func(p string) {
		if !gone[p] {
			gone[p] = true
			res.Missing = append(res.Missing, p)
		}
	}
	seen := map[int64]bool{}
	add := func(f library.File) {
		if !seen[f.ID] {
			seen[f.ID] = true
			res.Files = append(res.Files, addonkit.ImportedFile{Path: f.Path, FileID: f.ID, ItemID: f.ItemID})
		}
	}
	for _, p := range paths {
		if gone[p] {
			continue
		}
		f, err := a.lib.FileByPath(ctx, p)
		switch {
		case err == nil:
			add(f)
			continue
		case !errors.Is(err, library.ErrNotFound):
			return res, err
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			under, err := a.lib.FilesUnderNoProbe(ctx, p)
			if err != nil {
				return res, err
			}
			if len(under) == 0 {
				miss(p) // a folder with no library file below it
			}
			for _, f := range under {
				add(f)
			}
			continue
		}
		miss(p) // on disk, but no library file
	}
	taken := map[string]bool{}
	if ref != "" && len(res.Files) > 0 {
		filePaths := make([]string, len(res.Files))
		for i, f := range res.Files {
			filePaths[i] = f.Path
		}
		b, err := a.refs.Bind(ctx, addonID, ref, filePaths)
		if err != nil {
			return res, err
		}
		res.Taken = b.Taken
		for _, p := range b.Taken {
			taken[p] = true
		}
	} else if len(res.Files) > 0 {
		// no binding: the files another addon references are still its own
		ids := make([]int64, len(res.Files))
		for i, f := range res.Files {
			ids[i] = f.FileID
		}
		refs, err := a.refs.RefsOf(ctx, ids)
		if err != nil {
			return res, err
		}
		for _, f := range res.Files {
			if r, ok := refs[f.FileID]; ok && r.Addon != addonID {
				taken[f.Path] = true
				res.Taken = append(res.Taken, f.Path)
			}
		}
	}
	if owner != 0 {
		var ids []int64
		for _, f := range res.Files {
			if !taken[f.Path] { // another addon's file is not this import's
				ids = append(ids, f.FileID)
			}
		}
		if err := a.lib.ClaimFiles(ctx, ids, owner); err != nil {
			return res, err
		}
	}
	a.jobs.Trigger()
	if a.bus != nil {
		items := make([]int64, 0, len(res.Files))
		for _, f := range res.Files {
			if !slices.Contains(items, f.ItemID) {
				items = append(items, f.ItemID)
			}
		}
		a.bus.Publish(events.Event{Type: events.LibraryUpdated, Data: map[string]any{"addon": addonID, "itemIds": items}})
	}
	return res, nil
}

// coreRefReplace: PUT {paths} → Bound: the addon's ref holds exactly the
// files at paths afterwards (RefStore.Replace).
func (a *addonsAPI) coreRefReplace(w http.ResponseWriter, r *http.Request) {
	in, ok := a.writeLibrary(w, r)
	if !ok {
		return
	}
	ref, ok := refParam(r)
	if !ok {
		badRequest(w, r)
		return
	}
	var body struct {
		Paths *[]string `json:"paths"` // required: [] clears the ref
	}
	if !decodeMax(w, r, &body, addonPathsBody) {
		return
	}
	if body.Paths == nil {
		badRequest(w, r)
		return
	}
	paths := *body.Paths
	if len(paths) > addonMaxPaths {
		writeErr(w, r, http.StatusBadRequest, msgAddonTooManyPaths)
		return
	}
	if _, ok := a.writePaths(w, r, paths); !ok {
		return
	}
	b, err := a.refs.Replace(r.Context(), in.ID, ref, paths)
	if err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// coreRefDelete: the addon's ref leaves its files (the files stay).
func (a *addonsAPI) coreRefDelete(w http.ResponseWriter, r *http.Request) {
	in, ok := a.writeLibrary(w, r)
	if !ok {
		return
	}
	ref, ok := refParam(r)
	if !ok {
		badRequest(w, r)
		return
	}
	if err := a.refs.Unbind(r.Context(), in.ID, ref); err != nil {
		libFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coreRefsBatch: {refs: [RefPath]} → Bound, each path bound to its own
// ref in one transaction (RefStore.BindMany).
func (a *addonsAPI) coreRefsBatch(w http.ResponseWriter, r *http.Request) {
	in, ok := a.writeLibrary(w, r)
	if !ok {
		return
	}
	var body struct {
		Refs []addonkit.RefPath `json:"refs"`
	}
	if !decodeMax(w, r, &body, addonRefPairsBody) {
		return
	}
	if len(body.Refs) > addonMaxRefPairs {
		writeErr(w, r, http.StatusBadRequest, msgAddonTooManyPairs)
		return
	}
	paths := make([]string, len(body.Refs))
	for i, p := range body.Refs {
		if !addons.ValidRef(p.Ref) {
			badRequest(w, r)
			return
		}
		paths[i] = p.Path
	}
	if _, ok := a.writePaths(w, r, paths); !ok {
		return
	}
	b, err := a.refs.BindMany(r.Context(), in.ID, body.Refs)
	if err != nil {
		libFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// coreOwners: {ref | paths, userId} → 204: the profile becomes an owner
// of the titles of the addon's files with ref, or of the library files at
// paths (those it has; the others are skipped).
func (a *addonsAPI) coreOwners(w http.ResponseWriter, r *http.Request) {
	in, ok := a.writeLibrary(w, r)
	if !ok {
		return
	}
	var body struct {
		Ref    string   `json:"ref"`
		Paths  []string `json:"paths"`
		UserID int64    `json:"userId"`
	}
	if !decodeMax(w, r, &body, addonPathsBody) {
		return
	}
	if (body.Ref != "") == (len(body.Paths) > 0) || (body.Ref != "" && !addons.ValidRef(body.Ref)) {
		badRequest(w, r)
		return
	}
	if len(body.Paths) > addonMaxPaths {
		writeErr(w, r, http.StatusBadRequest, msgAddonTooManyPaths)
		return
	}
	if !a.knownUser(w, r, body.UserID) {
		return
	}
	var err error
	if body.Ref != "" {
		err = a.lib.ClaimRef(r.Context(), in.ID, body.Ref, body.UserID)
	} else {
		if _, ok := a.writePaths(w, r, body.Paths); !ok {
			return
		}
		ids := make([]int64, 0, len(body.Paths))
		for _, p := range body.Paths {
			f, ferr := a.lib.FileByPath(r.Context(), p)
			if errors.Is(ferr, library.ErrNotFound) {
				continue
			}
			if ferr != nil {
				err = ferr
				break
			}
			ids = append(ids, f.ID)
		}
		if err == nil {
			err = a.lib.ClaimFiles(r.Context(), ids, body.UserID)
		}
	}
	if err != nil {
		libFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// coreImage: {url} (https) → {name}: the image in the server's cache,
// served at /api/images/{name}.
func (a *addonsAPI) coreImage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.callingAddon(w, r, "images"); !ok {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		writeErr(w, r, http.StatusBadRequest, msgAddonImageHTTPS)
		return
	}
	if a.images == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonImagesOff)
		return
	}
	im := *a.images // the shared cache, with a client that stays on https
	c := http.Client{Timeout: 30 * time.Second}
	if im.HTTP != nil {
		c = *im.HTTP
	}
	c.CheckRedirect = httpsRedirects
	im.HTTP = &c
	name, err := im.FetchMax(r.Context(), body.URL, addonMaxImage)
	if err != nil {
		var local *metadata.LocalError
		if errors.As(err, &local) {
			log.Printf("api: addon image: %v", err)
		}
		writeErr(w, r, http.StatusBadGateway, msgAddonImageFailed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// httpsRedirects follows at most 10 redirects, each to https.
func httpsRedirects(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return errors.New("api: an addon image redirects off https")
	}
	if len(via) >= 10 {
		return errors.New("api: an addon image redirects too often")
	}
	return nil
}

// libGuard checks many paths against the library roots, resolving each
// parent folder and root once.
type libGuard struct {
	roots []string
	real  map[string]string // root → resolved ("" when it cannot be)
	dirs  map[string]dirReal
}

type dirReal struct {
	real string
	ok   bool
}

func (a *addonsAPI) newLibGuard() *libGuard { return newGuardOver(a.libraryRoots()) }

// newGuardOver is a guard of the paths at or inside roots (clean,
// absolute).
func newGuardOver(roots []string) *libGuard {
	g := &libGuard{roots: roots, real: map[string]string{}, dirs: map[string]dirReal{}}
	for _, root := range g.roots {
		rr, err := filepath.EvalSymlinks(root)
		if err != nil {
			rr = ""
		}
		g.real[root] = rr
	}
	return g
}

// check is inLibrary: p absolute, clean, inside a library root as
// written and, every symlink resolved, inside that root resolved; one not
// on disk through its deepest existing parent (exists false). ok is false
// otherwise, also for an entry that exists but cannot be resolved (a
// dangling symlink).
func (g *libGuard) check(p string) (exists, ok bool) {
	_, exists, ok = g.resolve(p)
	return exists, ok
}

// checkWrite is check refusing a library root itself, as written or
// resolved: the writing calls take files and folders below a root.
func (g *libGuard) checkWrite(p string) (exists, ok bool) {
	real, exists, ok := g.resolve(p)
	if !ok {
		return false, false
	}
	for _, root := range g.roots {
		if p == root || real == g.real[root] {
			return false, false
		}
	}
	return exists, true
}

// resolve is check with p's resolved path.
func (g *libGuard) resolve(p string) (real string, exists, ok bool) {
	real, exists, ok = g.realPath(p)
	if !ok {
		return "", false, false
	}
	for _, root := range g.roots {
		if rr := g.real[root]; rr != "" && within(root, p) && within(rr, real) {
			return real, exists, true
		}
	}
	return "", false, false
}

// checkResolved is the file holds' check (PUT /files/holds, as the
// contract and /info.fileRoots give it): p absolute and clean, and,
// resolved, at or inside a root resolved, however p is spelled (a hold
// under the real path of a symlinked root, or through an alias outside
// every root, is in). below also refuses a root itself, resolved.
func (g *libGuard) checkResolved(p string, below bool) bool {
	real, _, ok := g.realPath(p)
	if !ok {
		return false
	}
	in := false
	for _, root := range g.roots {
		rr := g.real[root]
		if rr == "" {
			continue
		}
		if below && real == rr {
			return false
		}
		in = in || within(rr, real)
	}
	return in
}

// realPath is p resolved: its parent folder through its deepest existing
// part, p itself followed when it is a link; ok false when p is not
// absolute and clean or cannot be resolved (a dangling link).
func (g *libGuard) realPath(p string) (real string, exists, ok bool) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", false, false
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", false, false // the filesystem root is in no library root
	}
	d, hit := g.dirs[parent]
	if !hit {
		r, _, ok := resolveDeepest(parent)
		d = dirReal{r, ok}
		g.dirs[parent] = d
	}
	if !d.ok {
		return "", false, false
	}
	st, err := os.Lstat(p)
	switch {
	case err == nil && st.Mode()&fs.ModeSymlink != 0:
		if real, err = filepath.EvalSymlinks(p); err != nil {
			return "", false, false
		}
		exists = true
	case err == nil:
		real, exists = filepath.Join(d.real, filepath.Base(p)), true
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		real = filepath.Join(d.real, filepath.Base(p))
	default:
		return "", false, false
	}
	return real, exists, true
}
