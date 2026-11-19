package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
)

// helperHealth is one row of GET /api/addons/helpers: a helper service an
// enabled addon declares (its manifest's helpers). An addon answering
// helpers.status tells how it is as the addon itself uses it (the address
// of its settings page, the admin's variable or the compose name, and the
// outcome of its own last request); for any other the core asks where the
// addon would go: the admin's own address when one is set
// (ANTRESOL_<ADDON>_<HELPER>_URL, as the addons read it), else its compose
// address.
type helperHealth struct {
	Addon   string `json:"addon"`
	Name    string `json:"name"`
	Address string `json:"address"`
	// Custom: an address of the admin's is in use; it is never shown (it
	// may carry a secret), so Address is then "".
	Custom bool `json:"custom,omitempty"`
	// Source: where the address comes from, when the addon told
	// (addonkit.HelperOverride, HelperSettings, HelperCompose, HelperNone);
	// "" when the core asked by itself.
	Source string `json:"source,omitempty"`
	OK     bool   `json:"ok"`
	// Problem: why it does not answer, for the page's own words: "dns" (no
	// such name on the network), "refused", "timeout", "status", "auth"
	// (the credentials are refused), "unset" (no address), "other".
	Problem string `json:"problem,omitempty"`
	Error   string `json:"error,omitempty"`
	// CheckedAt: when the addon learnt it (zero: just now, by the core).
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	// Live: the addon's own last real request, not a probe.
	Live bool `json:"live,omitempty"`
}

// helperOverride is the address the admin set for an addon's helper, the
// way the addons read it (ANTRESOL_<ADDON>_<HELPER>_URL, the addon's id
// and the helper's name upper-cased); "" when none.
func helperOverride(addonID, name string) string {
	up := func(v string) string { return strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(v)) }
	return strings.TrimSpace(os.Getenv("ANTRESOL_" + up(addonID) + "_" + up(name) + "_URL"))
}

// probeHelper asks h's health path on base (its compose address, or the
// admin's own); nil when it answers below 400 (tests replace it).
var probeHelper = func(ctx context.Context, base string, h addons.Helper) error {
	if err := h.Valid(); err != nil {
		return err
	}
	c := http.Client{Timeout: addonkit.HelperProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+h.Health, nil)
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
		return statusErr(resp.StatusCode)
	}
	return nil
}

type statusErr int

func (s statusErr) Error() string { return fmt.Sprintf("status %d", int(s)) }

// helperProblem sorts a probe's error for the page.
func helperProblem(err error) string {
	var st statusErr
	if errors.As(err, &st) {
		return helperProblemOf(int(st))
	}
	return addonkit.HelperProblem(err)
}

func helperProblemOf(code int) string {
	return addonkit.HelperProblem(&addonkit.HelperStatusError{Code: code})
}

// helperStates asks the addons answering helpers.status (tests replace it).
var helperStates = func(ctx context.Context, h *addons.Hooks, declared func(id, name string) (addons.Helper, bool)) map[string][]addonkit.HelperState {
	if h == nil {
		return nil
	}
	return h.HelperStates(ctx, declared)
}

// helpers is GET /api/addons/helpers (admin, «Состояние»): every enabled
// addon's helper services, all asked at once.
func (a *addonsAPI) helpers(w http.ResponseWriter, r *http.Request) {
	list, err := a.rt.List(r.Context())
	if err != nil {
		log.Printf("api: addon helpers: %v", err)
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	declared := map[string]addons.Helper{} // "<addon> <helper>"
	for _, in := range list {
		if in.Enabled {
			for _, h := range in.Manifest.Helpers {
				declared[in.ID+" "+h.Name] = h
			}
		}
	}
	told := helperStates(r.Context(), a.hooks, func(id, name string) (addons.Helper, bool) {
		h, ok := declared[id+" "+name]
		return h, ok
	})
	out := []helperHealth{}
	var hs []addons.Helper
	var bases []string
	var asks []int // rows the core probes by itself
	for _, in := range list {
		if !in.Enabled {
			continue
		}
		for _, h := range in.Manifest.Helpers {
			if st, ok := toldState(told[in.ID], h.Name); ok {
				out = append(out, helperHealth{Addon: in.ID, Name: h.Name, Address: st.Address, Source: st.Source,
					Custom: st.Source == addonkit.HelperOverride || st.Source == addonkit.HelperSettings,
					OK:     st.OK, Problem: st.Problem, CheckedAt: st.CheckedAt, Live: st.Live})
				continue
			}
			hh := helperHealth{Addon: in.ID, Name: h.Name, Address: h.Address()}
			base := h.Address()
			if o := helperOverride(in.ID, h.Name); o != "" {
				hh.Address, hh.Custom, base = "", true, o
			}
			asks = append(asks, len(out))
			out = append(out, hh)
			hs = append(hs, h)
			bases = append(bases, base)
		}
	}
	var wg sync.WaitGroup
	for j, h := range hs {
		i := asks[j]
		wg.Go(func() {
			err := probeHelper(r.Context(), bases[j], h)
			// the admin's own address may put the service under a path of
			// its own, where the manifest's health path is not: any HTTP
			// answer there but 401/403 means it is up
			var st statusErr
			if out[i].Custom && errors.As(err, &st) && helperProblemOf(int(st)) != addonkit.HelperProblemAuth {
				err = nil
			}
			if err != nil {
				out[i].Problem = helperProblem(err)
				out[i].Error = err.Error()
				if out[i].Custom {
					// the error names the address
					out[i].Error = ""
				}
				return
			}
			out[i].OK = true
		})
	}
	wg.Wait()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// toldState is the state an addon told of helper name (the first one).
func toldState(states []addonkit.HelperState, name string) (addonkit.HelperState, bool) {
	for _, st := range states {
		if st.Name == name {
			return st, true
		}
	}
	return addonkit.HelperState{}, false
}
