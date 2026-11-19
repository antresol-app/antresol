package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
)

// helperHealth is one row of GET /api/addons/helpers: a helper service an
// enabled addon declares (its manifest's helpers), asked on its compose
// address. An address the admin gave the addon itself is not known here,
// so a helper not found at the compose address says so, not that the
// addon is broken.
type helperHealth struct {
	Addon   string `json:"addon"`
	Name    string `json:"name"`
	Address string `json:"address"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// probeHelper asks a helper's health path (tests replace it).
var probeHelper = func(ctx context.Context, h addons.Helper) error { return addonkit.ProbeHelper(ctx, nil, h) }

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
	for _, in := range list {
		if !in.Enabled {
			continue
		}
		for _, h := range in.Manifest.Helpers {
			out = append(out, helperHealth{Addon: in.ID, Name: h.Name, Address: h.Address()})
			hs = append(hs, h)
		}
	}
	var wg sync.WaitGroup
	for i, h := range hs {
		wg.Go(func() {
			if err := probeHelper(r.Context(), h); err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].OK = true
		})
	}
	wg.Wait()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
