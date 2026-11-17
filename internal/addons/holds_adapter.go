package addons

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// FileHooks puts the addons' holds behind the file browser: it wraps the
// build's own file hooks (Inner*, nil in the core build) and routes by
// key. A key "<addon>:<key>" is an addon's hold, answered from the hold
// index and the addon's files.* hooks; any other key, and every path no
// addon holds, goes to the wrapped hooks as before. Plans, listings and
// linked data merge both.
//
// Lookups (HeldInfo, Owner, HoldOf, the warnings) never call an addon:
// they read the index. Hooks are called only when an operation touches
// held data, once per hook per addon, and a down addon fails closed: its
// keys are stopped, holding them fails, and nothing moves.
type FileHooks struct {
	H           *Holds
	Inner       files.HeldInfo
	InnerT      files.Holders
	InnerLinked func(ctx context.Context, set library.DeleteSet) []files.Linked
	Lib         *library.Repo

	innerLinkFiles func(ctx context.Context, key string) ([]library.File, error)

	// plans and failed plans belong to one file operation (files.OpID):
	// another operation's never touches them
	mu     sync.Mutex
	plans  map[string]followPlan // op\x00from\x00to → the plan not yet relocated
	failed map[string]planFail   // op\x00namespaced key → its addon's failed plan
	names  map[string]addonNames // language → display names
}

type followPlan struct {
	at     time.Time
	addons map[string][]string // addon id → its keys that follow
	keys   []string            // every addon key of the plan, namespaced
	inner  bool                // the wrapped hooks follow too
}

// planFail: the addon's files.plan for a key failed, timed out or was
// refused; holding the key is refused without asking the addon.
type planFail struct {
	at  time.Time
	id  string
	err error
}

// planTTL: how long a plan or a failed plan is kept (two operations'
// deadlines).
const planTTL = 3 * time.Minute

type addonNames struct {
	at    time.Time
	names map[string]string
}

// namesTTL: how long the addons' display names are kept.
const namesTTL = 5 * time.Second

// Wrap replaces the file browser's hooks in fc (Held, Holders, Linked,
// Texts, LinkFiles) with a *FileHooks over the ones it had.
func (h *Holds) Wrap(fc *files.ServiceConfig) {
	f := &FileHooks{H: h, Inner: fc.Held, InnerT: fc.Holders, InnerLinked: fc.Linked, Lib: fc.Library, innerLinkFiles: fc.LinkFiles}
	fc.Held, fc.Holders, fc.Linked, fc.Texts, fc.LinkFiles = f, f, f.Linked, f, f.LinkFiles
}

// HoldError is a file operation an addon did not let happen (it is down,
// failed or refused). Message is what the user is shown.
type HoldError struct {
	Addon, Message string
	Err            error
}

func (e *HoldError) Error() string { return e.Message }
func (e *HoldError) Unwrap() error { return e.Err }

// holdKey is the namespaced key of an addon's hold.
func holdKey(id, key string) string { return id + ":" + key }

// splitHoldKey: k is "<addon>:<key>" of an addon; else the wrapped hooks'.
func splitHoldKey(k string) (id, key string, ok bool) {
	id, key, ok = strings.Cut(k, ":")
	if !ok || !idRe.MatchString(id) || key == "" {
		return "", "", false
	}
	return id, key, true
}

// groupKeys sorts keys into the addons' (by id, raw keys, each once) and
// the wrapped hooks'.
func groupKeys(keys []string) (byAddon map[string][]string, inner []string) {
	byAddon = map[string][]string{}
	for _, k := range keys {
		if id, key, ok := splitHoldKey(k); ok {
			if !slices.Contains(byAddon[id], key) {
				byAddon[id] = append(byAddon[id], key)
			}
			continue
		}
		inner = append(inner, k)
	}
	return byAddon, inner
}

func sortedIDs[V any](m map[string]V) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func namespaced(id string, keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = holdKey(id, k)
	}
	return out
}

// text is t in the language of ctx (the other one when it is empty),
// cleaned as a refusal text is (no control characters, at most 500).
func text(ctx context.Context, t addonkit.Text) string {
	if i18n.FromContext(ctx) == i18n.EN && t.EN != "" || t.RU == "" {
		return refusalText(t.EN)
	}
	return refusalText(t.RU)
}

// name is the addon's display name in the language of ctx (its id when
// unknown), from a cache refreshed at most every namesTTL.
func (f *FileHooks) name(ctx context.Context, id string) string {
	lang := i18n.FromContext(ctx)
	f.mu.Lock()
	c, ok := f.names[lang]
	f.mu.Unlock()
	if !ok || time.Since(c.at) > namesTTL {
		c = addonNames{at: time.Now()}
		if f.H.Installed != nil {
			if names, err := f.H.Installed(ctx); err == nil {
				c.names = names
			} else {
				f.H.log().Warn("addon holds: names", "err", err)
			}
		}
		f.mu.Lock()
		if f.names == nil {
			f.names = map[string]addonNames{}
		}
		f.names[lang] = c
		f.mu.Unlock()
	}
	if n := c.names[id]; n != "" {
		return n
	}
	return id
}

// HeldInfo is the wrapped hooks' answer: an addon's hold shows as
// Entry.Hold (HoldOf), not as the build's own data.
func (f *FileHooks) HeldInfo(path string) (string, bool, bool) {
	if f.Inner == nil {
		return "", false, false
	}
	return f.Inner.HeldInfo(path)
}

// Owner reports data an addon holds (selected or not) or the wrapped hooks
// own: file operations ask before touching it.
func (f *FileHooks) Owner(path string) (string, bool) {
	if id, x, _, ok := f.H.Lookup(path); ok {
		return holdKey(id, x.Key), true
	}
	if ow, ok := f.Inner.(files.HoldOwner); ok {
		return ow.Owner(path)
	}
	if f.Inner != nil {
		k, _, ok := f.Inner.HeldInfo(path)
		return k, ok
	}
	return "", false
}

// HoldOf names the addon holding path, from the index.
func (f *FileHooks) HoldOf(ctx context.Context, path string) (files.HoldRef, bool) {
	id, x, selected, ok := f.H.Lookup(path)
	if !ok {
		return files.HoldRef{}, false
	}
	return files.HoldRef{Addon: f.name(ctx, id), Label: text(ctx, x.Label), Active: x.Active && selected}, true
}

// MoveWarning is the addon's own warning of moving path ({name} replaced),
// the neutral one when it gives none; "" when no addon holds path or
// anything below it.
func (f *FileHooks) MoveWarning(ctx context.Context, path, name string) string {
	id, x, _, ok := f.H.Lookup(path)
	if !ok {
		// a folder holding held data
		t := f.H.Touching(path)
		if len(t) == 0 {
			return ""
		}
		id = sortedIDs(t)[0]
		if x, ok = f.H.Get(id, t[id][0]); !ok {
			return ""
		}
	}
	if w := text(ctx, x.MoveWarning); w != "" {
		return strings.ReplaceAll(w, "{name}", name)
	}
	return i18n.T(ctx, "Эти данные использует расширение «%s»", f.name(ctx, id))
}

// KeptWarning is the addon's own note of a delete leaving the data of key.
func (f *FileHooks) KeptWarning(ctx context.Context, key, name string) string {
	id, k, ok := splitHoldKey(key)
	if !ok {
		return ""
	}
	x, ok := f.H.Get(id, k)
	if !ok {
		return ""
	}
	return strings.ReplaceAll(text(ctx, x.KeptWarning), "{name}", name)
}

// innerPlan is the wrapped hooks' plan (none in the core build).
func (f *FileHooks) innerPlan(ctx context.Context, from, to string) (follow, stop []string) {
	if f.InnerT == nil {
		return nil, nil
	}
	if tc, ok := f.InnerT.(files.HoldersCtx); ok {
		return tc.PlanRelocateCtx(ctx, from, to)
	}
	return f.InnerT.PlanRelocate(from, to)
}

// planMove is one move of a files.plan body; To "" is away (trash).
type planMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// planAddon calls files.plan on addon id for moves touching its keys and
// returns the keys that follow: those the answer names in follow and not
// in stop. A move away has none, nor has an addon that is down, fails or
// refuses: all its keys stop, and holding them is refused (failPlan).
func (f *FileHooks) planAddon(ctx context.Context, id string, moves []planMove, keys []string) []string {
	var out struct {
		Follow []string `json:"follow"`
		Stop   []string `json:"stop"`
	}
	in := struct {
		Moves []planMove `json:"moves"`
		Keys  []string   `json:"keys"`
	}{moves, keys}
	if err := f.callOut(ctx, id, "files.plan", in, &out); err != nil {
		f.H.log().Warn("addon holds: plan failed, its keys are refused", "addon", id, "err", err)
		f.failPlan(ctx, id, keys, err)
		return nil
	}
	f.planOK(ctx, id, keys)
	if len(moves) != 1 || moves[0].To == "" {
		return nil
	}
	var follow []string
	for _, k := range keys {
		if slices.Contains(out.Follow, k) && !slices.Contains(out.Stop, k) {
			follow = append(follow, k)
		}
	}
	return follow
}

// PlanRelocate is PlanRelocateCtx without a deadline of its own.
func (f *FileHooks) PlanRelocate(from, to string) (follow, stop []string) {
	return f.PlanRelocateCtx(context.Background(), from, to)
}

// PlanRelocateCtx merges the wrapped hooks' plan with the addons': one
// files.plan per addon holding data at, below or above from (none when no
// addon does). An addon's key that does not follow is stopped.
func (f *FileHooks) PlanRelocateCtx(ctx context.Context, from, to string) (follow, stop []string) {
	f.recall(ctx, from, to) // a plan this operation made before is replaced
	follow, stop = f.innerPlan(ctx, from, to)
	touch := f.H.Touching(from)
	if len(touch) == 0 {
		return follow, stop
	}
	p := followPlan{at: time.Now(), addons: map[string][]string{}, inner: len(follow) > 0}
	for _, id := range sortedIDs(touch) {
		keys := touch[id]
		fol := f.planAddon(ctx, id, []planMove{{From: from, To: to}}, keys)
		for _, k := range keys {
			if slices.Contains(fol, k) {
				follow = append(follow, holdKey(id, k))
			} else {
				stop = append(stop, holdKey(id, k))
			}
			p.keys = append(p.keys, holdKey(id, k))
		}
		if len(fol) > 0 {
			p.addons[id] = fol
		}
	}
	f.remember(ctx, from, to, p) // also with no addon following: Relocate checks
	return follow, stop
}

// PlanTrashCtx is the plan of trashing every one of froms: the wrapped
// hooks' stop per path and every addon key each path touches, with one
// files.plan per addon for all its paths.
func (f *FileHooks) PlanTrashCtx(ctx context.Context, froms []string) [][]string {
	out := make([][]string, len(froms))
	moves := map[string][]planMove{}
	keys := map[string][]string{}
	for i, from := range froms {
		_, out[i] = f.innerPlan(ctx, from, "")
		t := f.H.Touching(from)
		for _, id := range sortedIDs(t) {
			out[i] = append(out[i], namespaced(id, t[id])...)
			moves[id] = append(moves[id], planMove{From: from})
			for _, k := range t[id] {
				if !slices.Contains(keys[id], k) {
					keys[id] = append(keys[id], k)
				}
			}
		}
	}
	for _, id := range sortedIDs(moves) {
		sort.Strings(keys[id])
		f.planAddon(ctx, id, moves[id], keys[id]) // all stop: told, not asked
	}
	return out
}

func planID(ctx context.Context, from, to string) string {
	return strconv.FormatUint(files.OpID(ctx), 10) + "\x00" + from + "\x00" + to
}

func markID(ctx context.Context, key string) string {
	return strconv.FormatUint(files.OpID(ctx), 10) + "\x00" + key
}

// remember keeps the operation's plan of from → to touching addon holds
// until its Relocate (or its keys are released), replacing an earlier one
// and dropping plans older than planTTL (their Relocate never came).
func (f *FileHooks) remember(ctx context.Context, from, to string, p followPlan) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.plans == nil {
		f.plans = map[string]followPlan{}
	}
	for id, q := range f.plans {
		if time.Since(q.at) > planTTL {
			delete(f.plans, id)
		}
	}
	f.plans[planID(ctx, from, to)] = p
}

// recall takes the operation's plan of from → to; ok is false when there
// is none.
func (f *FileHooks) recall(ctx context.Context, from, to string) (followPlan, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.plans[planID(ctx, from, to)]
	delete(f.plans, planID(ctx, from, to))
	if !ok || time.Since(p.at) > planTTL {
		return followPlan{}, false
	}
	return p, true
}

// failPlan marks the addon's keys whose plan in this operation failed.
func (f *FileHooks) failPlan(ctx context.Context, id string, keys []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed == nil {
		f.failed = map[string]planFail{}
	}
	for k, x := range f.failed {
		if time.Since(x.at) > planTTL {
			delete(f.failed, k)
		}
	}
	for _, k := range keys {
		f.failed[markID(ctx, holdKey(id, k))] = planFail{at: time.Now(), id: id, err: err}
	}
}

// planOK: a later plan of this operation for the keys worked.
func (f *FileHooks) planOK(ctx context.Context, id string, keys []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.failed, markID(ctx, holdKey(id, k)))
	}
}

// planFailed is the failed plan, in this operation, of one of the addon's
// keys, if any.
func (f *FileHooks) planFailed(ctx context.Context, id string, keys []string) (error, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		if x, ok := f.failed[markID(ctx, holdKey(id, k))]; ok && time.Since(x.at) <= planTTL {
			return x.err, true
		}
	}
	return nil, false
}

// release forgets this operation's failed plans and remembered plans of
// keys it gave up (ResumeAll); another operation's stay.
func (f *FileHooks) release(ctx context.Context, keys []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.failed, markID(ctx, k))
	}
	op := strconv.FormatUint(files.OpID(ctx), 10) + "\x00"
	for id, p := range f.plans {
		if !strings.HasPrefix(id, op) {
			continue
		}
		for _, k := range p.keys {
			if slices.Contains(keys, k) {
				delete(f.plans, id)
				break
			}
		}
	}
}

// Relocate moves held data that follows: files.detach on each addon whose
// keys follow, the rename (through the wrapped hooks when they follow
// too), then files.attach{renamed}. A failed detach renames nothing, and
// so does a relocation of held data this operation has no plan for.
func (f *FileHooks) Relocate(ctx context.Context, from, to string, rename func() error) error {
	p, ok := f.recall(ctx, from, to)
	if !ok {
		if t := f.H.Touching(from); len(t) > 0 {
			id := sortedIDs(t)[0]
			return &files.HeldError{Err: f.holdErr(ctx, id, errors.New("relocation without a plan"))}
		}
		p.inner = true // Relocate is asked only when something follows
	}
	type moveBody struct {
		From    string   `json:"from"`
		To      string   `json:"to"`
		Keys    []string `json:"keys"`
		Renamed *bool    `json:"renamed,omitempty"`
	}
	after := context.WithoutCancel(ctx) // attach runs even if the request ends
	var detached []string
	attach := func(renamed bool) error {
		var errs []error
		for _, id := range detached {
			err := f.call(after, id, "files.attach", moveBody{From: from, To: to, Keys: p.addons[id], Renamed: &renamed})
			if err != nil {
				f.H.log().Warn("addon holds: attach", "addon", id, "renamed", renamed, "err", err)
				if renamed {
					errs = append(errs, &HoldError{Addon: id, Err: err,
						Message: i18n.T(ctx, "Расширение «%s» не ответило после перемещения файлов", f.name(ctx, id))})
				}
			}
		}
		return errors.Join(errs...)
	}
	for _, id := range sortedIDs(p.addons) {
		if err := f.call(ctx, id, "files.detach", moveBody{From: from, To: to, Keys: p.addons[id]}); err != nil {
			attach(false)
			return &files.HeldError{Err: f.holdErr(ctx, id, err)}
		}
		detached = append(detached, id)
	}
	renamed := false
	run := func() error {
		err := rename()
		renamed = err == nil
		return err
	}
	var err error
	if p.inner && f.InnerT != nil {
		err = f.InnerT.Relocate(ctx, from, to, run)
	} else {
		err = run()
	}
	if aerr := attach(renamed); err == nil && aerr != nil {
		return &files.HeldError{Err: aerr}
	}
	return err
}

// call runs a files.* hook with no answer expected.
func (f *FileHooks) call(ctx context.Context, id, hook string, in any) error {
	return f.callOut(ctx, id, hook, in, nil)
}

func (f *FileHooks) callOut(ctx context.Context, id, hook string, in, out any) error {
	if f.H.Hooks == nil {
		return fmt.Errorf("addon %s hook %s: %w: no hooks", id, hook, ErrHookDown)
	}
	return f.H.Hooks.Call(ctx, id, hook, nil, in, out)
}

// holdErr is the user's view of a hook that failed: the addon's refusal,
// or that it did not answer and the files stayed where they were.
func (f *FileHooks) holdErr(ctx context.Context, id string, err error) *HoldError {
	var r *HookRefusal
	if errors.As(err, &r) {
		return &HoldError{Addon: id, Message: r.Message, Err: err}
	}
	return &HoldError{Addon: id, Err: err,
		Message: i18n.T(ctx, "Расширение «%s» не ответило: файлы остались на месте", f.name(ctx, id))}
}

// batch runs hook once per addon with all its keys, and inner for each of
// the wrapped hooks' keys. With firstErr the first failure ends it (a
// hold: the caller releases what was held); else every call is made.
func (f *FileHooks) batch(ctx context.Context, hook string, keys []string, firstErr bool, inner func(context.Context, string) error) error {
	byAddon, own := groupKeys(keys)
	var errs []error
	for _, id := range sortedIDs(byAddon) {
		if hook == "files.pause" {
			// its plan failed: refused without asking it again
			if perr, failed := f.planFailed(ctx, id, byAddon[id]); failed {
				return &files.HeldError{Err: f.holdErr(ctx, id, perr)}
			}
		}
		if err := f.call(ctx, id, hook, struct {
			Keys []string `json:"keys"`
		}{byAddon[id]}); err != nil {
			herr := &files.HeldError{Err: f.holdErr(ctx, id, err)}
			if firstErr {
				return herr
			}
			errs = append(errs, herr)
		}
	}
	for _, k := range own {
		var err error
		if f.InnerT == nil {
			err = fmt.Errorf("no hooks for key %s", k)
		} else {
			err = inner(ctx, k)
		}
		if err != nil {
			if firstErr {
				return err
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PauseAll holds keys: one files.pause per addon; the first failure ends it.
func (f *FileHooks) PauseAll(ctx context.Context, keys []string) error {
	return f.batch(ctx, "files.pause", keys, true, func(ctx context.Context, k string) error { return f.InnerT.Pause(ctx, k) })
}

// ResumeAll releases keys: one files.resume per addon. The operation is
// over for them: their plans (failed or not) are forgotten.
func (f *FileHooks) ResumeAll(ctx context.Context, keys []string) error {
	f.release(ctx, keys)
	return f.batch(ctx, "files.resume", keys, false, func(ctx context.Context, k string) error { return f.InnerT.Resume(ctx, k) })
}

// StopAll stops keys for good, their data kept: one files.stop per addon.
func (f *FileHooks) StopAll(ctx context.Context, keys []string) error {
	return f.batch(ctx, "files.stop", keys, false, func(ctx context.Context, k string) error { return f.InnerT.Stop(ctx, k) })
}

func (f *FileHooks) Pause(ctx context.Context, key string) error {
	return f.PauseAll(ctx, []string{key})
}
func (f *FileHooks) Resume(ctx context.Context, key string) error {
	return f.ResumeAll(ctx, []string{key})
}
func (f *FileHooks) Stop(ctx context.Context, key string) error { return f.StopAll(ctx, []string{key}) }

// Linked is the wrapped hooks' linked data and the addons' holds a delete
// of set concerns: those whose item is in set and those whose key a file
// of set carries as its ref. Each is listed (files.Linked.Listed): only
// its selected regular files may go, never its root.
func (f *FileHooks) Linked(ctx context.Context, set library.DeleteSet) []files.Linked {
	var out []files.Linked
	if f.InnerLinked != nil {
		out = f.InnerLinked(ctx, set)
	}
	sets := f.H.Sets()
	if len(sets) == 0 {
		return out
	}
	items := map[int64]bool{}
	for _, id := range set.Items {
		items[id] = true
	}
	refs := map[string]bool{}
	if f.H.Refs != nil && len(set.Files) > 0 {
		ids := make([]int64, len(set.Files))
		for i, x := range set.Files {
			ids[i] = x.ID
		}
		rs, err := f.H.Refs.RefsOf(ctx, ids)
		if err != nil {
			f.H.log().Warn("addon holds: refs of a delete", "err", err)
		}
		for _, r := range rs {
			refs[r.Addon+"\x00"+r.Ref] = true
		}
	}
	for _, id := range sortedIDs(sets) {
		for i := range sets[id] {
			x := &sets[id][i]
			if x.ItemID != 0 && items[x.ItemID] || refs[id+"\x00"+x.Key] {
				out = append(out, linkedOf(id, x))
			}
		}
	}
	return out
}

// linkedOf is a hold as linked data: Paths its selected regular files.
func linkedOf(id string, x *addonkit.Hold) files.Linked {
	l := files.Linked{Path: x.Root, Key: holdKey(id, x.Key), ItemID: x.ItemID, Busy: x.Busy, Listed: true}
	for _, hf := range x.Files {
		l.Files = append(l.Files, files.LinkedFile{Path: hf.Path, Size: hf.Size})
		if !hf.Selected || slices.Contains(l.Paths, hf.Path) {
			continue
		}
		if fi, err := os.Lstat(hf.Path); err == nil && fi.Mode().IsRegular() {
			l.Paths = append(l.Paths, hf.Path)
		}
	}
	return l
}

// LinkFiles lists the library files made from the data of key: an
// addon's key through its ref, others as the wrapped config did.
func (f *FileHooks) LinkFiles(ctx context.Context, key string) ([]library.File, error) {
	if id, k, ok := splitHoldKey(key); ok {
		if f.Lib == nil {
			return nil, library.ErrNotFound
		}
		return f.Lib.FilesOfRef(ctx, id, k)
	}
	if f.innerLinkFiles != nil {
		return f.innerLinkFiles(ctx, key)
	}
	return nil, nil
}
