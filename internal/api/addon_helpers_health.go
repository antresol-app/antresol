package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
)

// helperHealth is one row of GET /api/addons/helpers: a helper service an
// enabled addon declares (its manifest's helpers), asked where the addon
// itself goes: the admin's own address when one is set
// (ANTRESOL_<ADDON>_<HELPER>_URL, as the addons read it), else its
// compose address.
type helperHealth struct {
	Addon   string `json:"addon"`
	Name    string `json:"name"`
	Address string `json:"address"`
	// Custom: the admin's own address was asked; it is never shown (it may
	// carry a secret), so Address is then "".
	Custom bool `json:"custom,omitempty"`
	OK     bool `json:"ok"`
	// Problem: why it does not answer, for the page's own words: "dns" (no
	// such name on the network), "refused", "timeout", "status", "other".
	Problem string `json:"problem,omitempty"`
	Error   string `json:"error,omitempty"`
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
	var dns *net.DNSError
	var st statusErr
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		if dns.IsTimeout {
			return "timeout"
		}
		return "dns"
	case errors.As(err, &st):
		return "status"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case strings.Contains(err.Error(), "connection refused"):
		return "refused"
	}
	return "other"
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
	out := []helperHealth{}
	var hs []addons.Helper
	var bases []string
	for _, in := range list {
		if !in.Enabled {
			continue
		}
		for _, h := range in.Manifest.Helpers {
			hh := helperHealth{Addon: in.ID, Name: h.Name, Address: h.Address()}
			base := h.Address()
			if o := helperOverride(in.ID, h.Name); o != "" {
				hh.Address, hh.Custom, base = "", true, o
			}
			out = append(out, hh)
			hs = append(hs, h)
			bases = append(bases, base)
		}
	}
	var wg sync.WaitGroup
	for i, h := range hs {
		wg.Go(func() {
			err := probeHelper(r.Context(), bases[i], h)
			// the admin's own address may put the service under a path of
			// its own, where the manifest's health path is not: any HTTP
			// answer there means it is up
			var st statusErr
			if out[i].Custom && errors.As(err, &st) {
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
