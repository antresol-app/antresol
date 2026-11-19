package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/go-chi/chi/v5"
)

// «Франшизы» (admin): the franchises the franchise channels play, their
// order (an admin's survives recomputes) and a forced recompute.

// FranchiseTMDB is the franchise pass's TMDB: the collections of the
// «Похожее» block's cached fetch (tmdb_related), in the metadata language.
func FranchiseTMDB(r *tmdb.Related) channels.FranchiseSource { return franchiseTMDB{r} }

type franchiseTMDB struct{ r *tmdb.Related }

func (t franchiseTMDB) Language() string { return t.r.Language() }

func (t franchiseTMDB) MovieCollection(ctx context.Context, id int) (int, string, bool) {
	rel, ok := t.r.Titles(ctx, "movie", id, t.r.Language())
	if !ok {
		return 0, "", false
	}
	if rel.Collection == nil {
		return 0, "", true
	}
	return rel.Collection.ID, rel.Collection.Name, true
}

func (t franchiseTMDB) CollectionParts(ctx context.Context, id int) (string, []int, bool) {
	c, ok := t.r.Collection(ctx, id, t.r.Language())
	if !ok {
		return "", nil, false
	}
	parts := make([]int, 0, len(c.Parts))
	for _, p := range c.Parts {
		parts = append(parts, p.ID)
	}
	return c.Name, parts, true
}

func mountFranchises(r chi.Router, a *channelsAPI) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/channels/franchises", a.franchises)
		r.Post("/api/channels/franchises/refresh", a.franchisesRefresh)
		r.Put("/api/channels/franchises/{key}/order", a.franchiseOrder)
		r.Delete("/api/channels/franchises/{key}/order", a.franchiseOrderReset)
	})
}

type franchiseItemJSON struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

type franchiseJSON struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Error  string `json:"error,omitempty"`
	// Manual: an admin's order is set.
	Manual bool `json:"manual"`
	// Items: the effective order, then the hidden titles.
	Items []franchiseItemJSON `json:"items"`
	// ChannelID: its channel (0: none, under the minimum).
	ChannelID int64 `json:"channelId,omitempty"`
	// ChannelRemoved: its channel was deleted from «Управление каналами»
	// (the generator leaves it until it is given back there).
	ChannelRemoved bool `json:"channelRemoved,omitempty"`
}

// franchiseChans is where the franchises' channels are: the enabled one of a
// franchise by its generator key, and the deleted ones' keys.
type franchiseChans struct {
	ids     map[string]int64
	removed map[string]bool
}

func (a *channelsAPI) franchiseOut(ctx context.Context, f channels.Franchise, chans franchiseChans) franchiseJSON {
	out := franchiseJSON{Key: f.Key, Name: f.Name, Source: f.Source, Error: f.Error, Manual: f.Manual != nil,
		Items: []franchiseItemJSON{}, ChannelID: chans.ids["franchise:"+f.Key], ChannelRemoved: chans.removed["franchise:"+f.Key]}
	ids := f.Effective()
	var hidden []int64
	if f.Manual != nil {
		hidden = f.Manual.Hidden
	}
	add := func(id int64, h bool) {
		it, err := a.lib.Item(ctx, id)
		if err != nil {
			return // gone from the library since the pass
		}
		out.Items = append(out.Items, franchiseItemJSON{ID: id, Kind: it.Kind, Title: it.Title, Year: it.Year, Hidden: h})
	}
	for _, id := range ids {
		add(id, false)
	}
	for _, id := range hidden {
		if slices.Contains(f.Items, id) { // still the franchise's
			add(id, true)
		}
	}
	return out
}

func (a *channelsAPI) franchiseChannels(ctx context.Context) franchiseChans {
	out := franchiseChans{ids: map[string]int64{}}
	all, _ := a.svc.Store.All(ctx)
	for _, c := range all {
		if strings.HasPrefix(c.AutoKey, "franchise:") && c.Enabled {
			out.ids[c.AutoKey] = c.ID
		}
	}
	out.removed, _ = a.svc.Store.RemovedKeys(ctx)
	return out
}

// GET /api/channels/franchises: {status, franchises}.
func (a *channelsAPI) franchises(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fs, err := a.svc.Store.Franchises(ctx)
	if err != nil {
		internalErr(w, r, "franchises", err)
		return
	}
	chans := a.franchiseChannels(ctx)
	list := make([]franchiseJSON, 0, len(fs))
	for _, f := range fs {
		if len(f.Items) == 0 { // an order kept for titles that may come back
			continue
		}
		list = append(list, a.franchiseOut(ctx, f, chans))
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": a.svc.Franchises.Status(ctx), "available": a.svc.Franchises.TMDB != nil, "franchises": list})
}

// POST /api/channels/franchises/refresh: a pass now, whatever changed (202).
func (a *channelsAPI) franchisesRefresh(w http.ResponseWriter, r *http.Request) {
	if a.svc.Franchises.TMDB == nil {
		writeErr(w, r, http.StatusConflict, "Франшизы недоступны без ключа TMDB")
		return
	}
	a.svc.Franchises.Kick(true)
	w.WriteHeader(http.StatusAccepted)
}

// PUT /api/channels/franchises/{key}/order {order, hidden}: the admin's
// order, of the franchise's own titles only.
func (a *channelsAPI) franchiseOrder(w http.ResponseWriter, r *http.Request) {
	var body channels.FranchiseManual
	if !decode(w, r, &body) {
		return
	}
	a.setFranchiseOrder(w, r, &body)
}

// DELETE /api/channels/franchises/{key}/order: the computed order again.
func (a *channelsAPI) franchiseOrderReset(w http.ResponseWriter, r *http.Request) {
	a.setFranchiseOrder(w, r, nil)
}

func (a *channelsAPI) setFranchiseOrder(w http.ResponseWriter, r *http.Request, m *channels.FranchiseManual) {
	ctx := r.Context()
	f, err := a.svc.Store.SetFranchiseOrder(ctx, chi.URLParam(r, "key"), m)
	switch {
	case errors.Is(err, channels.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Франшиза не найдена")
		return
	case errors.Is(err, channels.ErrFranchiseOrder):
		writeErr(w, r, http.StatusBadRequest, "В порядке есть фильм не из этой франшизы")
		return
	case err != nil:
		internalErr(w, r, "franchise order", err)
		return
	}
	// the channel follows (Generator.Sync in the refresh)
	if _, err := a.svc.Generator.Sync(ctx); err != nil {
		internalErr(w, r, "franchise order", err)
		return
	}
	a.svc.Kick()
	writeJSON(w, http.StatusOK, a.franchiseOut(ctx, f, a.franchiseChannels(ctx)))
}
