package addons

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// Pin is one pinned release (VAULT_ADDONS_PIN): the server installs
// exactly this version of the addon from its local releases (Dev) at
// boot, and the admin cannot install, update or remove it.
type Pin struct{ ID, Version string }

func (p Pin) String() string { return p.ID + "@" + p.Version }

// ErrPinned: the addon is pinned by the deployment (VAULT_ADDONS_PIN);
// install, update, remove and a change of its source are refused.
var ErrPinned = userError("Расширение закреплено при развёртывании")

// ErrPinNotApplied: a pinned addon is switched on only once its pin has
// been applied in this process (not yet at boot, or it failed).
var ErrPinNotApplied = userError("Закреплённая версия расширения не установлена")

var errPinVersion = errors.New("the local release has another version")

// ParsePins reads VAULT_ADDONS_PIN: "id@version[,id@version…]". Blank
// means no pins. An empty part, a part without exactly one "@", an id or
// a version a manifest could not carry, or an id named twice is an error.
func ParsePins(s string) ([]Pin, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []Pin
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("pin %q: an empty part", s)
		}
		id, v, ok := strings.Cut(part, "@")
		if !ok || strings.Contains(v, "@") {
			return nil, fmt.Errorf("pin %q: want id@version", part)
		}
		if !idRe.MatchString(id) || reservedIDs[id] {
			return nil, fmt.Errorf("pin %q: bad addon id", part)
		}
		if !semverRe.MatchString(v) {
			return nil, fmt.Errorf("pin %q: bad version", part)
		}
		if seen[id] {
			return nil, fmt.Errorf("pin %q: %s pinned twice", part, id)
		}
		seen[id] = true
		out = append(out, Pin{ID: id, Version: v})
	}
	return out, nil
}

// SetPins adds pins to the pinned ids: from now on Install, Update and
// Remove refuse them (ErrPinned). The server calls it before it serves;
// EnsurePinned calls it too.
func (m *Manager) SetPins(pins []Pin) {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	if m.pins == nil {
		m.pins = map[string]string{}
	}
	for _, p := range pins {
		m.pins[p.ID] = p.Version
	}
}

// Pinned reports whether id is pinned (VAULT_ADDONS_PIN), whether or not
// its pin could be applied.
func (m *Manager) Pinned(id string) bool {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	_, ok := m.pins[id]
	return ok
}

// pinFailedID reports whether id's pin failed at boot: StartAll does not
// start it (a stale or other version never runs in its pin's place).
func (m *Manager) pinFailedID(id string) bool {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	return m.pinFailed[id]
}

// PinFailed reports whether id is pinned and EnsurePinned could not apply
// its pin: the addon stays stopped and cannot be switched on.
func (m *Manager) PinFailed(id string) bool { return m.pinFailedID(id) }

// FailedPins are the pins EnsurePinned could not apply, in id order (the
// admin's list shows those not installed at all too).
func (m *Manager) FailedPins() []Pin {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	var out []Pin
	for id, failed := range m.pinFailed {
		if failed {
			out = append(out, Pin{ID: id, Version: m.pins[id]})
		}
	}
	slices.SortFunc(out, func(a, b Pin) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// pinApplied reports whether EnsurePinned applied id's pin in this
// process: only then may the admin switch a pinned addon on.
func (m *Manager) pinApplied(id string) bool {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	return m.pinDone[id]
}

func (m *Manager) setPinResult(id string, applied bool) {
	m.pinMu.Lock()
	defer m.pinMu.Unlock()
	if m.pinFailed == nil {
		m.pinFailed, m.pinDone = map[string]bool{}, map[string]bool{}
	}
	m.pinFailed[id], m.pinDone[id] = !applied, applied
}

// Boot is the server's start of the addons: the pins first (EnsurePinned),
// then the others (StartAll). It returns the pins' errors.
func (m *Manager) Boot(ctx context.Context, pins []Pin) []error {
	errs := m.EnsurePinned(ctx, pins)
	m.StartAll(ctx)
	return errs
}

// EnsurePinned brings each pinned addon to its pin, from the local release
// "local:<id>" in Dev, and runs it:
//   - not installed: installed;
//   - another version (older or newer): updated to it;
//   - the same version with other release files (their sha256s): installed
//     again from the release;
//   - installed from another source: installed from the release with its
//     data kept (its notifications and holds are dropped once the new one
//     is in place);
//   - then enabled and started.
//
// Run it before StartAll, which leaves the pinned ids alone. A pin that
// failed (one error each, the others still applied) leaves its addon
// stopped, whatever version is installed, and SetEnabled refuses to
// switch it on (ErrPinNotApplied).
func (m *Manager) EnsurePinned(ctx context.Context, pins []Pin) []error {
	m.SetPins(pins)
	var errs []error
	for _, p := range pins {
		if err := m.ensurePin(ctx, p); err != nil {
			m.setPinResult(p.ID, false)
			m.Sup.Stop(p.ID) // a failed update's rollback never runs the old one either
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		m.setPinResult(p.ID, true)
	}
	return errs
}

func (m *Manager) ensurePin(ctx context.Context, p Pin) error {
	ctx = context.WithoutCancel(ctx)
	src := LocalPrefix + p.ID
	rel, _, err := m.resolve(ctx, src)
	if err != nil {
		return err
	}
	if rel.Manifest.Version != p.Version {
		return fmt.Errorf("%w: %s", errPinVersion, rel.Manifest.Version)
	}
	// one lock for the whole pin: the admin's switch waits for it
	defer m.lock(p.ID)()
	in, err := m.Store.Get(ctx, p.ID)
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return err
	case in.Source != src:
		// another addon under this id: replaced by the release; what the
		// core keeps of the old one goes only once the new one is in place
		if err := m.forgetLocked(ctx, p.ID); err != nil {
			return err
		}
		got, err := m.installLocked(ctx, rel, src, installOpts{pin: true, noStart: true})
		if err != nil {
			return err
		}
		if err := m.dropState(ctx, p.ID, false); err != nil {
			return err
		}
		return m.Sup.Start(ctx, got)
	case in.Version == p.Version && !sameRelease(in.Manifest, rel.Manifest):
		// the same version from other files: installed again (data kept)
		if err := m.forgetLocked(ctx, p.ID); err != nil {
			return err
		}
	case in.Version == p.Version:
		if !in.Enabled {
			return m.setEnabledLocked(ctx, p.ID, true) // starts it
		}
		return m.Sup.Start(ctx, in)
	}
	got, err := m.installLocked(ctx, rel, src, installOpts{pin: true})
	if err != nil {
		return err
	}
	if !got.Enabled { // an update keeps the switch: a pin turns it on (and starts it)
		return m.setEnabledLocked(ctx, p.ID, true)
	}
	return nil
}

// forgetLocked stops the addon and deletes its row and versions, keeping
// its data and everything the core keeps of it (refs, holds,
// notifications): an install of the same id follows.
func (m *Manager) forgetLocked(ctx context.Context, id string) error {
	m.Sup.Stop(id)
	if err := m.Store.Delete(ctx, id); err != nil {
		return err
	}
	return m.removeFiles(id, false)
}

// sameRelease reports whether an installed manifest came from the same
// release files as rel: the server platform's archive and the web bundle
// have the same sha256.
func sameRelease(installed, rel Manifest) bool {
	a, okA := installed.PlatformFor(runtime.GOOS, runtime.GOARCH)
	b, okB := rel.PlatformFor(runtime.GOOS, runtime.GOARCH)
	if !okA || !okB || !strings.EqualFold(a.SHA256, b.SHA256) {
		return false
	}
	if (installed.Web == nil) != (rel.Web == nil) {
		return false
	}
	return installed.Web == nil || strings.EqualFold(installed.Web.SHA256, rel.Web.SHA256)
}
