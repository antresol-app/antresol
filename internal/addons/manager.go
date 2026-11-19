package addons

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Manager installs, updates, enables and removes addons: it ties the
// releases (Resolver), the database (Store) and the processes
// (Supervisor) together. Set Sup.Mint = m.Mint.
type Manager struct {
	Store    *Store
	Sup      *Supervisor
	Resolver *Resolver
	// Refs (optional): the addons' file references and holds; Remove
	// drops the addon's holds, and its refs with its data.
	Refs *RefStore
	// Holds (optional): the hold index; Remove drops the addon's holds
	// through it (saved and indexed), else through Refs.
	Holds *Holds
	// Dev is a directory of local addons (VAULT_ADDONS_DEV): source
	// "local:<name>" installs the LocalRelease in Dev/<name>. "": none.
	Dev string
	// OnRemove (optional) runs after an addon's row is deleted, for what
	// the core keeps of it elsewhere (its notification topics and letters).
	OnRemove func(ctx context.Context, id string)

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per addon id: install, update, enable, remove
	// checkLog: the last update check failure logged per id (logCheck)
	checkLog map[string]string
	newMu    sync.Mutex // the checks across ids (env prefix, scan kinds) and the row they guard

	pinMu     sync.Mutex
	pins      map[string]string // VAULT_ADDONS_PIN: id → version (SetPins)
	pinFailed map[string]bool   // pins EnsurePinned could not apply
	pinDone   map[string]bool   // pins EnsurePinned applied in this process
}

// LocalPrefix starts the source of a local (development) addon.
const LocalPrefix = "local:"

var (
	errNoDev        = userError("Локальные расширения на сервере не включены")
	errBadLocal     = userError("Локальное расширение не найдено")
	errIDMismatch   = userError("id в манифесте не совпадает с именем каталога")
	errIDChanged    = userError("Новый релиз расширения сменил id")
	errOtherSource  = userError("Расширение с таким id уже установлено из другого источника")
	errNotNewer     = userError("Эта или более новая версия расширения уже установлена")
	errNoPlatform   = userError("Расширение не поддерживает платформу сервера")
	errNoBinary     = userError("В архиве расширения нет bin/addon")
	errInstall      = userError("Не удалось установить расширение")
	errBadEvent     = userError("Неверное имя события")
	errNotPreviewed = userError("Релиз расширения изменился после просмотра, проверьте его ещё раз")
	// ErrNoLibrary: the server has no library for the core API's library
	// calls (they answer 503).
	ErrNoLibrary = userError("Медиатека недоступна")
	// ErrPrefixOverlap: the addon's EnvPrefix is a prefix of an installed
	// addon's, or the other way round, so one server variable would reach
	// both. The error Install returns carries a user message naming them.
	ErrPrefixOverlap = errors.New("addon env prefix overlaps another addon")
	// ErrScanKindTaken: a tracking.scan:<kind> the addon declares is the
	// core's (library) or another installed addon's (E2). The error
	// Install returns carries a user message naming the kind.
	ErrScanKindTaken = errors.New("addon scan kind taken")
)

// scanKindTaken is ErrScanKindTaken with its user message.
type scanKindTaken struct{ msg userError }

func (e scanKindTaken) Error() string        { return string(e.msg) }
func (e scanKindTaken) Unwrap() error        { return e.msg }
func (e scanKindTaken) Is(target error) bool { return target == ErrScanKindTaken }

// checkScanKinds refuses a manifest declaring a scan kind of the core or
// of another installed addon (on or off: a disabled addon's rows stay
// listed under its kind).
func (m *Manager) checkScanKinds(ctx context.Context, man Manifest) error {
	mine := ScanKindsOf(man)
	if len(mine) == 0 {
		return nil
	}
	taken := func(k string) error {
		return scanKindTaken{userError(fmt.Sprintf("Вид источника «%s» уже занят", k))}
	}
	for _, k := range mine {
		if coreKinds[k] {
			return taken(k)
		}
	}
	list, err := m.Store.List(ctx)
	if err != nil {
		return err
	}
	for _, in := range list {
		if in.ID == man.ID {
			continue
		}
		for _, k := range ScanKindsOf(in.Manifest) {
			if slices.Contains(mine, k) {
				return taken(k)
			}
		}
	}
	return nil
}

// prefixOverlap is ErrPrefixOverlap with its user message.
type prefixOverlap struct{ msg userError }

func (e prefixOverlap) Error() string        { return string(e.msg) }
func (e prefixOverlap) Unwrap() error        { return e.msg }
func (e prefixOverlap) Is(target error) bool { return target == ErrPrefixOverlap }

// checkOverlap refuses a new id whose env prefix overlaps an installed
// addon's (fake and fake-x: ANTRESOL_FAKE_ and ANTRESOL_FAKE_X_).
func (m *Manager) checkOverlap(ctx context.Context, id string) error {
	list, err := m.Store.List(ctx)
	if err != nil {
		return err
	}
	p := EnvPrefix(id)
	for _, in := range list {
		q := EnvPrefix(in.ID)
		if in.ID != id && (strings.HasPrefix(p, q) || strings.HasPrefix(q, p)) {
			return prefixOverlap{userError(fmt.Sprintf("Расширение «%s» мешает «%s»: их переменные окружения пересекаются", id, in.ID))}
		}
	}
	return nil
}

func (m *Manager) lock(id string) func() {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	l := m.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Mint makes a fresh token for id and stores its hash: the Supervisor's
// Mint, called on every process start. The previous token stops working.
func (m *Manager) Mint(id string) (string, error) {
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if err := m.Store.SetToken(context.Background(), id, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// resolve reads the release a source names: "local:<name>" (a directory
// of Dev) or a GitHub repository URL. src is the canonical source.
func (m *Manager) resolve(ctx context.Context, source string) (rel Release, src string, err error) {
	if name, ok := strings.CutPrefix(strings.TrimSpace(source), LocalPrefix); ok {
		if m.Dev == "" {
			return Release{}, "", errNoDev
		}
		if !idRe.MatchString(name) {
			return Release{}, "", errBadLocal
		}
		dev, err := filepath.EvalSymlinks(m.Dev)
		if err != nil {
			return Release{}, "", errBadLocal
		}
		dir, err := filepath.EvalSymlinks(filepath.Join(dev, name))
		if err != nil {
			return Release{}, "", errBadLocal
		}
		if r, err := filepath.Rel(dev, dir); err != nil || r != name {
			return Release{}, "", errBadLocal // a link out of Dev
		}
		if rel, err = LocalRelease(dir); err != nil {
			return Release{}, "", err
		}
		if rel.Manifest.ID != name {
			return Release{}, "", errIDMismatch
		}
		return rel, LocalPrefix + name, nil
	}
	repo, err := ParseRepoURL(source)
	if err != nil {
		return Release{}, "", err
	}
	if rel, err = m.Resolver.Latest(ctx, repo); err != nil {
		return Release{}, "", err
	}
	return rel, "github.com/" + repo.Owner + "/" + repo.Name, nil
}

// Preview reads the manifest of the latest release of url without
// installing anything.
func (m *Manager) Preview(ctx context.Context, url string) (Manifest, error) {
	rel, _, err := m.resolve(ctx, url)
	return rel.Manifest, err
}

// Install installs the latest release of url, or updates the addon when
// its id is installed already (from the same source, to a newer version).
// A non-empty version pins the release the admin previewed: a latest
// release of another version is refused.
// An update keeps the previous version running until the new one is
// healthy, then deletes it; if the new one fails, it is deleted and the
// previous one stays.
func (m *Manager) Install(ctx context.Context, url, version string) (Installed, error) {
	rel, src, err := m.resolve(ctx, url)
	if err != nil {
		return Installed{}, err
	}
	if m.Pinned(rel.Manifest.ID) {
		return Installed{}, ErrPinned
	}
	if version != "" && rel.Manifest.Version != version {
		return Installed{}, errNotPreviewed
	}
	return m.install(ctx, rel, src)
}

// Update installs the latest release of an installed addon's source;
// version pins it as in Install.
func (m *Manager) Update(ctx context.Context, id, version string) (Installed, error) {
	if m.Pinned(id) {
		return Installed{}, ErrPinned
	}
	in, err := m.Store.Get(ctx, id)
	if err != nil {
		return Installed{}, err
	}
	rel, src, err := m.resolve(ctx, in.Source)
	if err != nil {
		return Installed{}, err
	}
	if rel.Manifest.ID != id {
		return Installed{}, errIDChanged
	}
	if version != "" && rel.Manifest.Version != version {
		return Installed{}, errNotPreviewed
	}
	return m.install(ctx, rel, src)
}

func (m *Manager) install(ctx context.Context, rel Release, src string) (Installed, error) {
	// an admin closing the page must not leave a half-done install
	ctx = context.WithoutCancel(ctx)
	defer m.lock(rel.Manifest.ID)()
	if m.Pinned(rel.Manifest.ID) { // pinned meanwhile
		return Installed{}, ErrPinned
	}
	return m.installLocked(ctx, rel, src, installOpts{})
}

// installOpts: how EnsurePinned installs. pin installs its version whether
// it is newer or older than the installed one; noStart records it (on) but
// leaves the start to the caller.
type installOpts struct{ pin, noStart bool }

// installLocked is install under m.lock(id).
func (m *Manager) installLocked(ctx context.Context, rel Release, src string, o installOpts) (Installed, error) {
	lg := m.Sup.logger()
	man := rel.Manifest
	id := man.ID

	prev, err := m.Store.Get(ctx, id)
	existing := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		lg.Printf("addon %s: install: %v", id, err)
		return Installed{}, errInstall
	}
	if existing {
		if prev.Source != src {
			return Installed{}, errOtherSource
		}
		if o.pin && prev.Version == man.Version {
			return Installed{}, errNotNewer // EnsurePinned never asks for it
		}
		if !o.pin && !semverLess(prev.Version, man.Version) {
			return Installed{}, errNotNewer
		}
	} else if err := m.checkOverlap(ctx, id); err != nil {
		if !errors.Is(err, ErrPrefixOverlap) {
			lg.Printf("addon %s: install: %v", id, err)
			return Installed{}, errInstall
		}
		return Installed{}, err
	}
	// before unpacking: a taken scan kind is refused at once (checked again
	// with the row below)
	if err := m.checkScanKinds(ctx, man); err != nil {
		if !errors.Is(err, ErrScanKindTaken) {
			lg.Printf("addon %s: install: %v", id, err)
			return Installed{}, errInstall
		}
		return Installed{}, err
	}
	_, statErr := os.Stat(m.Sup.DataDir(id))
	hadData := statErr == nil

	vdir := filepath.Join(m.Sup.Dir, id, man.Version)
	// a leftover of an interrupted install (never the recorded version:
	// that one is older)
	if err := os.RemoveAll(vdir); err != nil {
		lg.Printf("addon %s: install: %v", id, err)
		return Installed{}, errInstall
	}
	if err := m.unpack(ctx, rel, vdir); err != nil {
		os.RemoveAll(vdir)
		return Installed{}, err
	}

	now := time.Now().UTC()
	in := Installed{ID: id, Source: src, Version: man.Version, Manifest: man, Enabled: true, InstalledAt: now, UpdatedAt: now}
	if existing {
		in.PrevVersion, in.Enabled, in.InstalledAt = prev.Version, prev.Enabled, prev.InstalledAt
	}
	prevRunning := m.Sup.Socket(id) != ""
	// again, with the row, under newMu: an install or update of another id
	// may have run meanwhile (the per-id locks do not order them)
	m.newMu.Lock()
	if err = m.checkScanKinds(ctx, man); err == nil {
		if existing {
			// the running version keeps its token until the new one's Mint
			err = m.Store.Save(ctx, in)
		} else if err = m.checkOverlap(ctx, id); err == nil {
			var tok string
			if tok, err = newToken(); err == nil {
				err = m.Store.Put(ctx, in, tok) // replaced by Mint on start
			}
		}
	}
	m.newMu.Unlock()
	if errors.Is(err, ErrPrefixOverlap) || errors.Is(err, ErrScanKindTaken) {
		os.RemoveAll(vdir)
		if !existing && !hadData {
			os.RemoveAll(filepath.Dir(vdir))
		}
		return Installed{}, err
	}
	if err != nil {
		lg.Printf("addon %s: install: %v", id, err)
		os.RemoveAll(vdir)
		return Installed{}, errInstall
	}

	if in.Enabled && !o.noStart {
		if err := m.Sup.Start(ctx, in); err != nil {
			m.rollback(ctx, id, prev, existing, prevRunning, vdir, hadData)
			return Installed{}, err
		}
	}
	if existing {
		if err := os.RemoveAll(filepath.Join(m.Sup.Dir, id, prev.Version)); err != nil {
			lg.Printf("addon %s: remove %s: %v", id, prev.Version, err)
		}
		if err := m.Store.ClearPrevVersion(ctx, id); err != nil {
			lg.Printf("addon %s: clear previous version: %v", id, err)
		}
		in.PrevVersion = ""
	}
	return in, nil
}

// rollback undoes a version that did not start: its files go; a first
// install leaves no row (and no data dir it made); an update restores the
// previous record and, if it was running, restarts it so that its process
// holds a valid token again: the failed start's Mint replaced the token
// (one hash per addon).
func (m *Manager) rollback(ctx context.Context, id string, prev Installed, existing, prevRunning bool, vdir string, hadData bool) {
	lg := m.Sup.logger()
	os.RemoveAll(vdir)
	if !existing {
		m.Sup.Stop(id)
		if err := m.Store.Delete(ctx, id); err != nil {
			lg.Printf("addon %s: rollback: %v", id, err)
		}
		if !hadData {
			os.RemoveAll(filepath.Dir(vdir))
		}
		return
	}
	if err := m.Store.Save(ctx, prev); err != nil {
		lg.Printf("addon %s: rollback: %v", id, err)
		return
	}
	if prevRunning {
		if err := m.Sup.Start(ctx, prev); err != nil {
			lg.Printf("addon %s: restart %s after a failed update: %v", id, prev.Version, err)
		}
	}
}

// unpack puts the release's platform asset (which must hold bin/addon)
// into vdir and its web bundle, if any, into vdir/web.
func (m *Manager) unpack(ctx context.Context, rel Release, vdir string) error {
	man := rel.Manifest
	p, ok := man.PlatformFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return errNoPlatform
	}
	if err := m.Resolver.Fetch(ctx, rel, p, vdir); err != nil {
		return err
	}
	if fi, err := os.Lstat(m.Sup.Binary(man.ID, man.Version)); err != nil || !fi.Mode().IsRegular() {
		return errNoBinary
	}
	if man.Web != nil {
		if err := m.Resolver.Fetch(ctx, rel, *man.Web, m.Sup.WebDir(man.ID, man.Version)); err != nil {
			return err
		}
	}
	return nil
}

// SetEnabled switches an addon on (and starts it: an error is a start
// that failed, the addon stays enabled) or off (and stops it). A pinned
// addon is switched on only once EnsurePinned applied its pin in this
// process (ErrPinNotApplied): a stale build never runs in its place.
func (m *Manager) SetEnabled(ctx context.Context, id string, on bool) error {
	ctx = context.WithoutCancel(ctx)
	defer m.lock(id)()
	if on && m.Pinned(id) && !m.pinApplied(id) {
		return ErrPinNotApplied
	}
	return m.setEnabledLocked(ctx, id, on)
}

func (m *Manager) setEnabledLocked(ctx context.Context, id string, on bool) error {
	in, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := m.Store.SetEnabled(ctx, id, on); err != nil {
		return err
	}
	if !on {
		return m.Sup.Stop(id)
	}
	in.Enabled = true
	return m.Sup.Start(ctx, in)
}

// Remove stops and uninstalls an addon; its data directory is kept
// unless deleteData.
// A pinned addon is not removed (ErrPinned).
func (m *Manager) Remove(ctx context.Context, id string, deleteData bool) error {
	ctx = context.WithoutCancel(ctx)
	defer m.lock(id)()
	if m.Pinned(id) {
		return ErrPinned
	}
	return m.removeLocked(ctx, id, deleteData)
}

func (m *Manager) removeLocked(ctx context.Context, id string, deleteData bool) error {
	if _, err := m.Store.Get(ctx, id); err != nil {
		return err
	}
	m.Sup.Stop(id)
	if err := m.Store.Delete(ctx, id); err != nil {
		return err
	}
	if err := m.dropState(ctx, id, deleteData); err != nil {
		return err
	}
	return m.removeFiles(id, deleteData)
}

// dropState forgets what the core keeps of a removed addon: its
// notifications (OnRemove), its holds and, with its data, its refs.
func (m *Manager) dropState(ctx context.Context, id string, deleteData bool) error {
	if m.OnRemove != nil {
		m.OnRemove(ctx, id)
	}
	if m.Holds != nil {
		if err := m.Holds.Drop(ctx, id); err != nil {
			return err
		}
	}
	if m.Refs != nil {
		if m.Holds == nil {
			if err := m.Refs.DropHolds(ctx, id); err != nil {
				return err
			}
		}
		if deleteData {
			if err := m.Refs.DropRefs(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeFiles deletes the addon's versions, and with deleteData its data.
func (m *Manager) removeFiles(id string, deleteData bool) error {
	dir := filepath.Join(m.Sup.Dir, id)
	if deleteData {
		return os.RemoveAll(dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range ents {
		if e.Name() != "data" {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// StartAll starts the enabled addons in the background and returns at
// once: the server never waits for an addon. It leaves the pinned ones
// alone: EnsurePinned, run before it, started those whose pin it applied,
// and one whose pin failed is never started.
func (m *Manager) StartAll(ctx context.Context) {
	list, err := m.Store.List(ctx)
	if err != nil {
		m.Sup.logger().Printf("addons: list: %v", err)
		return
	}
	for _, in := range list {
		if in.PrevVersion != "" {
			in = m.reconcile(ctx, in)
		}
		if !in.Enabled || m.Pinned(in.ID) || m.pinFailedID(in.ID) {
			continue
		}
		go func() {
			if err := m.Sup.Start(ctx, in); err != nil {
				m.Sup.logger().Printf("addon %s: start: %v", in.ID, err)
			}
		}()
	}
}

// reconcile finishes an update the server died in the middle of: the
// version recorded as current stays, the previous one's files go and the
// record forgets it.
func (m *Manager) reconcile(ctx context.Context, in Installed) Installed {
	defer m.lock(in.ID)()
	lg := m.Sup.logger()
	if in.PrevVersion != in.Version {
		if err := os.RemoveAll(filepath.Join(m.Sup.Dir, in.ID, in.PrevVersion)); err != nil {
			lg.Printf("addon %s: remove %s: %v", in.ID, in.PrevVersion, err)
			return in
		}
	}
	if err := m.Store.ClearPrevVersion(ctx, in.ID); err != nil {
		lg.Printf("addon %s: clear previous version: %v", in.ID, err)
		return in
	}
	in.PrevVersion = ""
	return in
}

// UpdateCheck is what an update check of an installed addon found.
type UpdateCheck struct {
	// Version of the newer release that can be installed; "" none.
	Version string
	// Pending: the check is still running in the background (GitHub is
	// slow); asking again later finds its result.
	Pending bool
	// NeedsCore: a newer release this server cannot run; Err says which.
	NeedsCore bool
	// Err: the check failed, or (NeedsCore) what the newer release needs;
	// a user message.
	Err error
}

// UpdateAvailable returns the version of the source's latest release when
// it is newer than the installed one.
func (m *Manager) UpdateAvailable(ctx context.Context, id string) (string, bool) {
	c := m.CheckUpdate(ctx, id, false)
	return c.Version, c.Version != ""
}

// CheckUpdate checks the source of an installed addon for a newer release.
// It reuses a recent check unless fresh, and never fails silently: a check
// that could not finish says why (Err), one still running says so
// (Pending) and goes on in the background.
// A pinned addon has none: only a new pin changes its version.
func (m *Manager) CheckUpdate(ctx context.Context, id string, fresh bool) UpdateCheck {
	if m.Pinned(id) {
		return UpdateCheck{}
	}
	in, err := m.Store.Get(ctx, id)
	if err != nil {
		m.logCheck(id, fmt.Errorf("read the addon: %w", err))
		return UpdateCheck{Err: errRelease}
	}
	var rel Release
	if strings.HasPrefix(strings.TrimSpace(in.Source), LocalPrefix) {
		rel, _, err = m.resolve(ctx, in.Source)
	} else {
		var repo Repo
		if repo, err = ParseRepoURL(in.Source); err == nil {
			rel, err = m.Resolver.Check(ctx, repo, fresh)
		}
	}
	var nc NeedsCoreError
	switch {
	case errors.Is(err, ErrPending):
		return UpdateCheck{Pending: true}
	case errors.As(err, &nc):
		if !semverRe.MatchString(in.Version) || !semverLess(in.Version, nc.Version) {
			return UpdateCheck{}
		}
		m.logCheck(id, err)
		return UpdateCheck{NeedsCore: true, Err: err}
	case err != nil:
		m.logCheck(id, fmt.Errorf("source %q: %w", in.Source, err))
		return UpdateCheck{Err: err}
	case rel.Manifest.ID != id:
		m.logCheck(id, fmt.Errorf("the latest release of %s is addon %q", in.Source, rel.Manifest.ID))
		return UpdateCheck{Err: errIDChanged}
	case !semverLess(in.Version, rel.Manifest.Version):
		m.logCheck(id, nil)
		return UpdateCheck{}
	}
	if _, ok := rel.Manifest.PlatformFor(runtime.GOOS, runtime.GOARCH); !ok {
		err := userError(fmt.Sprintf("Версия %s не поддерживает платформу сервера", rel.Manifest.Version))
		m.logCheck(id, err)
		return UpdateCheck{Err: err}
	}
	m.logCheck(id, nil)
	return UpdateCheck{Version: rel.Manifest.Version}
}

// logCheck logs a failed update check once: the pages poll the list, so
// the same failure comes back every few seconds.
func (m *Manager) logCheck(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if m.checkLog[id] == msg {
		return
	}
	if m.checkLog == nil {
		m.checkLog = map[string]string{}
	}
	m.checkLog[id] = msg
	if err != nil {
		m.Sup.logger().Printf("addons: update check of %s: %v", id, err)
	}
}

// IDForToken returns the addon owning token; the token of an addon that is
// disabled or not running is refused (ErrNotFound).
func (m *Manager) IDForToken(ctx context.Context, token string) (string, error) {
	id, err := m.Store.IDForToken(ctx, token)
	if err != nil {
		return "", err
	}
	in, err := m.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if st := m.Sup.Status(id); !in.Enabled || st == StatusStopped || st == StatusIncompatible {
		return "", ErrNotFound
	}
	return id, nil
}

// List returns the installed addons.
func (m *Manager) List(ctx context.Context) ([]Installed, error) { return m.Store.List(ctx) }

// Get returns one installed addon, or ErrNotFound.
func (m *Manager) Get(ctx context.Context, id string) (Installed, error) { return m.Store.Get(ctx, id) }

// Status of the addon's process.
func (m *Manager) Status(id string) Status { return m.Sup.Status(id) }

// Serving: the addon's status and, while it is running, its socket.
func (m *Manager) Serving(id string) (Status, string) { return m.Sup.Serving(id) }

// OnStatus adds f to hear every status change of every addon (see
// Supervisor.OnStatus: f runs under its lock and must return quickly).
func (m *Manager) OnStatus(f func(id string, st Status)) { m.Sup.OnStatus(f) }

// WebDir is the web bundle of an installed version.
func (m *Manager) WebDir(id, version string) string { return m.Sup.WebDir(id, version) }

// LogTail returns the newest n output lines of the addon.
func (m *Manager) LogTail(id string, n int) []string { return m.Sup.LogTail(id, n) }

var eventNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,39}$`)

// EventType is the bus type of an addon's event: addon.<id>.<name>. A
// name starting with "admin." reaches admins only.
func EventType(id, name string) (string, error) {
	if !eventNameRe.MatchString(name) {
		return "", errBadEvent
	}
	return "addon." + id + "." + name, nil
}

// semverLess reports a < b by semver precedence (build metadata ignored).
func semverLess(a, b string) bool { return semverCmp(a, b) < 0 }

func semverCmp(a, b string) int {
	a, _, _ = strings.Cut(a, "+")
	b, _, _ = strings.Cut(b, "+")
	ac, ap, aPre := strings.Cut(a, "-")
	bc, bp, bPre := strings.Cut(b, "-")
	as, bs := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < 3; i++ {
		if c := numCmp(part(as, i), part(bs, i)); c != 0 {
			return c
		}
	}
	switch {
	case !aPre && !bPre:
		return 0
	case !aPre:
		return 1
	case !bPre:
		return -1
	}
	ai, bi := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		x, y := ai[i], bi[i]
		_, xe := strconv.ParseUint(x, 10, 64)
		_, ye := strconv.ParseUint(y, 10, 64)
		var c int
		switch {
		case xe == nil && ye == nil:
			c = numCmp(x, y)
		case xe == nil:
			c = -1
		case ye == nil:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return len(ai) - len(bi)
}

func part(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "0"
}

// numCmp compares decimal strings without leading zeros of any length.
func numCmp(x, y string) int {
	if len(x) != len(y) {
		if len(x) < len(y) {
			return -1
		}
		return 1
	}
	return strings.Compare(x, y)
}
