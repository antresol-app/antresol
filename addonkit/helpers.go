package addonkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Helper is a helper service an addon declares in its manifest
// ("helpers"): a container the admin runs beside the server in the same
// compose project, with no published port. The addon finds it by its
// compose service name (Name, the DNS name on the project's network) and
// internal Port, and asks Health to know it is up. An address the admin
// gives the addon (its own setting or variable) always wins over it.
type Helper struct {
	// Name is the compose service name and so its DNS name:
	// ^[a-z][a-z0-9-]{0,62}$, not ending in "-".
	Name string `json:"name"`
	// Image is the container image, best with a pinned tag.
	Image string `json:"image"`
	// Port is the port the service listens on inside the network.
	Port int `json:"port"`
	// Health is the path asked to tell the service is up ("/" is fine):
	// any answer below 400 counts, a redirect is not followed.
	Health string `json:"health"`
}

var (
	helperNameRe   = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
	helperImageRe  = regexp.MustCompile(`^[a-z0-9][A-Za-z0-9._/:@+-]{0,254}$`)
	helperHealthRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,199}$`)
)

// MaxHelpers is the most helpers one manifest may declare.
const MaxHelpers = 4

// Valid reports whether h may be declared (the server refuses a manifest
// with an invalid one; ProbeHelper never asks one).
func (h Helper) Valid() error {
	switch {
	case !helperNameRe.MatchString(h.Name):
		return errors.New("helper: bad name")
	case !helperImageRe.MatchString(h.Image):
		return errors.New("helper: bad image")
	case h.Port < 1 || h.Port > 65535:
		return errors.New("helper: bad port")
	case !helperHealthRe.MatchString(h.Health) || strings.HasPrefix(h.Health, "//") || strings.Contains(h.Health, "/../") ||
		strings.HasSuffix(h.Health, "/..") || strings.Contains(h.Health, "/./"):
		return errors.New("helper: bad health path")
	}
	return nil
}

// ValidHelpers checks a manifest's whole list: each one valid, names
// unique, at most MaxHelpers.
func ValidHelpers(hs []Helper) error {
	if len(hs) > MaxHelpers {
		return errors.New("helper: too many")
	}
	seen := make(map[string]bool, len(hs))
	for _, h := range hs {
		if err := h.Valid(); err != nil {
			return err
		}
		if seen[h.Name] {
			return errors.New("helper: repeated name")
		}
		seen[h.Name] = true
	}
	return nil
}

// Address is where the helper answers on the compose network:
// http://<name>:<port>.
func (h Helper) Address() string { return "http://" + h.Name + ":" + strconv.Itoa(h.Port) }

// Compose is the block an admin adds under services: in compose.yml: no
// ports (the service stays on the project's internal network).
func (h Helper) Compose() string {
	return "  " + h.Name + ":\n" +
		"    image: " + h.Image + "\n" +
		"    restart: unless-stopped\n"
}

// HelpersFromManifest reads the "helpers" of a manifest (an addon embeds
// its own antresol-addon.json); the other fields are not looked at. None
// declared: nil. An invalid list is an error, as the server refuses it.
func HelpersFromManifest(data []byte) ([]Helper, error) {
	var m struct {
		Helpers json.RawMessage `json:"helpers"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if len(m.Helpers) == 0 || string(m.Helpers) == "null" {
		return nil, nil
	}
	var hs []Helper
	dec := json.NewDecoder(bytes.NewReader(m.Helpers))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hs); err != nil {
		return nil, fmt.Errorf("manifest: helpers: %w", err)
	}
	if err := ValidHelpers(hs); err != nil {
		return nil, err
	}
	return hs, nil
}

// HelperProbeTimeout bounds one ProbeHelper: as long as HelperCheckTimeout
// (a helper that checks credentials or warms up answers slowly).
const HelperProbeTimeout = HelperCheckTimeout

// ProbeHelper asks h's health path on its compose address (client nil:
// a plain client); nil when it answers below 400. Redirects are not
// followed, and an invalid h is never asked.
func ProbeHelper(ctx context.Context, client *http.Client, h Helper) error {
	if err := h.Valid(); err != nil {
		return err
	}
	c := http.Client{Timeout: HelperProbeTimeout}
	if client != nil {
		c = *client
		if c.Timeout == 0 {
			c.Timeout = HelperProbeTimeout
		}
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Address()+h.Health, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("helper %s: %w", h.Name, &HelperStatusError{Code: resp.StatusCode})
	}
	return nil
}

// Where an addon's helper service comes from (HelperStatus.Source).
const (
	HelperOverride = "override" // the admin's own address
	HelperCompose  = "compose"  // found by its compose service name
	HelperNone     = "none"     // neither: the settings page says what to add
)

// HelperStatus is what an addon's settings page shows of a helper.
type HelperStatus struct {
	Name   string `json:"name"`
	Source string `json:"source"` // HelperOverride | HelperCompose | HelperNone
	// Address is the compose address (asked, or found); never the
	// admin's own, which may carry a secret.
	Address string `json:"address,omitempty"`
	// Compose is the block to add to compose.yml, while it is not found.
	Compose string `json:"compose,omitempty"`
}

// HelperWatch keeps asking a helper whether it is up, so that one started
// after the addon is found without a restart, and one that went away is
// noticed.
type HelperWatch struct {
	Helper Helper
	// Probe asks the helper (nil: ProbeHelper with Client).
	Probe  func(ctx context.Context) error
	Client *http.Client
	// Every is the time between probes (0: 30 s).
	Every time.Duration
	// OnChange is called after a probe that found or lost it.
	OnChange func(found bool)
	// Health (optional) keeps every probe's outcome (helpers.status).
	Health *HelperHealth

	mu    sync.Mutex
	found bool
}

func (w *HelperWatch) probe(ctx context.Context) error {
	if w.Probe != nil {
		return w.Probe(ctx)
	}
	return ProbeHelper(ctx, w.Client, w.Helper)
}

// Check probes once and reports whether the helper is up (OnChange when
// that changed).
func (w *HelperWatch) Check(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, HelperProbeTimeout)
	err := w.probe(pctx)
	cancel()
	found := err == nil
	w.Health.RecordProbe(err)
	w.mu.Lock()
	changed := found != w.found
	w.found = found
	w.mu.Unlock()
	if changed && w.OnChange != nil {
		w.OnChange(found)
	}
	return found
}

// Run checks every Every until ctx ends.
func (w *HelperWatch) Run(ctx context.Context) {
	every := w.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Check(ctx)
		}
	}
}

// Found reports whether the last probe found the helper (nil: false).
func (w *HelperWatch) Found() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.found
}

// Address is the helper's compose address while it is found, else "".
func (w *HelperWatch) Address() string {
	if !w.Found() {
		return ""
	}
	return w.Helper.Address()
}

// Status is the helper's line for the settings page: overridden says the
// admin gave an address of their own.
func (w *HelperWatch) Status(overridden bool) HelperStatus {
	if w == nil {
		return HelperStatus{Source: HelperNone}
	}
	st := HelperStatus{Name: w.Helper.Name}
	switch {
	case overridden:
		st.Source = HelperOverride
	case w.Found():
		st.Source, st.Address = HelperCompose, w.Helper.Address()
	default:
		st.Source, st.Address, st.Compose = HelperNone, w.Helper.Address(), w.Helper.Compose()
	}
	return st
}
