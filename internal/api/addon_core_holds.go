package api

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/go-chi/chi/v5"
)

// The core API's file holds (files.holds): an addon gives the whole set of
// files it still needs; the file browser then asks it before moving,
// renaming or trashing them (addons.FileHooks).

const (
	msgAddonHoldsOff     = "Файлы недоступны"
	msgAddonOutsideFiles = "Путь вне файлов сервера: %s"
	msgAddonBadHolds     = "Неверный список файлов расширения: %s"
	msgAddonHoldHooks    = "Расширение, которое держит файлы, должно отвечать на хуки files.plan и files.pause"

	addonHoldsBody = 32 << 20 // 20 000 holds, 200 000 files
)

func mountAddonCoreHolds(r chi.Router, a *addonsAPI) {
	r.Put("/files/holds", a.coreHolds)
	r.Get("/files/holds", a.coreHoldsGet)
}

// coreHoldsGet: GET → {holds: [Hold]}, the addon's set as the server keeps
// it (saved across restarts; paths in the file browser's form); [] when it
// has none.
func (a *addonsAPI) coreHoldsGet(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "files.holds")
	if !ok {
		return
	}
	if a.holds == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonHoldsOff)
		return
	}
	holds := a.holds.Sets()[in.ID]
	if holds == nil {
		holds = []addonkit.Hold{}
	}
	writeJSON(w, http.StatusOK, map[string][]addonkit.Hold{"holds": holds})
}

// coreHolds: PUT {holds: [Hold]} → 204, the addon's set replaced whole.
// Every root and file is absolute, clean and, its symlinks resolved, at or
// inside a root of the file browser, a listed file strictly below one (a
// path not on disk yet is fine). A hold whose root is at or inside a root
// not on disk (a mount not up yet) is accepted and checked as written:
// clean, absolute, its files inside its root and none the missing root
// itself. On any refusal (400) the previous set stays.
func (a *addonsAPI) coreHolds(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "files.holds")
	if !ok {
		return
	}
	if a.holds == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonHoldsOff)
		return
	}
	var body struct {
		Holds *[]addonkit.Hold `json:"holds"` // required: [] clears the set
	}
	if !decodeMax(w, r, &body, addonHoldsBody) {
		return
	}
	if body.Holds == nil {
		badRequest(w, r)
		return
	}
	holds := *body.Holds
	if len(holds) > 0 && (!in.Manifest.Answers("files.plan") || !in.Manifest.Answers("files.pause")) {
		// its data could never be moved again
		writeErr(w, r, http.StatusBadRequest, msgAddonHoldHooks)
		return
	}
	if len(holds) > addons.MaxHolds {
		writeErr(w, r, http.StatusBadRequest, fmt.Sprintf(msgAddonBadHolds, fmt.Sprintf("more than %d holds", addons.MaxHolds)))
		return
	}
	g := newPathGuard(a.holds.Roots)
	missing := missingDirs(a.holds.Roots)
	for _, x := range holds {
		if under, ok := underMissing(missing, x); under {
			// its root is not on disk (a mount not up yet): nothing there
			// to resolve; checked as written (Holds.Put validates it too)
			if !ok {
				writeErr(w, r, http.StatusBadRequest, fmt.Sprintf(msgAddonOutsideFiles, x.Root))
				return
			}
			continue
		}
		for i, p := range holdPaths(x) {
			// resolved, at or inside a root (Holds.Put stores it in the
			// root's spelling); a listed path is below a root: a listed
			// folder covers all below it
			if !g.checkResolved(p, i > 0) {
				writeErr(w, r, http.StatusBadRequest, fmt.Sprintf(msgAddonOutsideFiles, p))
				return
			}
		}
	}
	if err := a.holds.Put(r.Context(), in.ID, holds); err != nil {
		if errors.Is(err, addons.ErrBadHold) {
			detail := strings.TrimPrefix(err.Error(), addons.ErrBadHold.Error()+": ")
			writeErr(w, r, http.StatusBadRequest, fmt.Sprintf(msgAddonBadHolds, detail))
			return
		}
		libFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// missingDirs are the roots (clean, absolute) not on disk: their symlinks
// do not resolve.
func missingDirs(roots []string) []string {
	var out []string
	for _, r := range roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		if _, err := filepath.EvalSymlinks(r); err != nil {
			out = append(out, filepath.Clean(r))
		}
	}
	return out
}

// underMissing: x's root is at or inside a root not on disk (under); ok
// when, as written, every path of it is clean and absolute, inside its
// root, and no listed path is that missing root itself.
func underMissing(missing []string, x addonkit.Hold) (under, ok bool) {
	var root string
	for _, m := range missing {
		if filepath.IsAbs(x.Root) && filepath.Clean(x.Root) == x.Root && within(m, x.Root) {
			root, under = m, true
			break
		}
	}
	if !under {
		return false, false
	}
	for _, f := range x.Files {
		if !filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path || !within(x.Root, f.Path) || f.Path == root {
			return true, false
		}
	}
	return true, true
}

// holdPaths: a hold's root and the paths it lists.
func holdPaths(x addonkit.Hold) []string {
	out := make([]string, 0, len(x.Files)+1)
	out = append(out, x.Root)
	for _, f := range x.Files {
		out = append(out, f.Path)
	}
	return out
}

// newPathGuard is a libGuard over other roots (the file browser's).
func newPathGuard(roots []string) *libGuard {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		if r != "" && filepath.IsAbs(r) {
			clean = append(clean, filepath.Clean(r))
		}
	}
	return newGuardOver(clean)
}
