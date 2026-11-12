package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/go-chi/chi/v5"
)

// «ТВ»: external IPTV sources, their editor, sets and devices (admin,
// /api/tv/…). Secrets (a source's URL, an Xtream password) are written
// here and never read back: answers carry iptv.Mask of them only.

type iptvAdmin struct {
	ca  *channelsAPI
	svc *iptv.Service
	// links: IPTV links may be made (Deps.IPTV)
	links bool
}

func mountIPTVAdmin(r chi.Router, a *iptvAdmin) {
	r.Post("/api/channels/opened", a.opened) // any profile: «ТВ» opened
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/tv/sources", a.sources)
		r.Post("/api/tv/sources", a.createSource)
		r.Put("/api/tv/sources/{id}", a.updateSource)
		r.Delete("/api/tv/sources/{id}", a.deleteSource)
		r.Post("/api/tv/sources/{id}/refresh", a.refreshSource(false))
		r.Post("/api/tv/sources/{id}/confirm", a.refreshSource(true))
		r.Post("/api/tv/sources/{id}/reject", a.rejectDrop)
		r.Get("/api/tv/sources/{id}/channels", a.sourceChannels)
		r.Post("/api/tv/epg/{id}/refresh", a.refreshEPG)
		r.Get("/api/tv/epg/search", a.epgSearch)
		r.Patch("/api/tv/channels", a.patchChannels)
		r.Patch("/api/tv/groups", a.patchGroup)
		r.Put("/api/tv/channels/{id}/epg", a.bindEPG)
		r.Delete("/api/tv/channels/{id}/epg", a.unbindEPG)
		r.Get("/api/tv/categories", a.categories)
		r.Get("/api/tv/sets", a.sets)
		r.Post("/api/tv/sets", a.saveSet)
		r.Put("/api/tv/sets/{id}", a.saveSet)
		r.Delete("/api/tv/sets/{id}", a.deleteSet)
		r.Get("/api/tv/devices", a.devices)
		r.Put("/api/tv/devices/assign", a.assign)
		r.Post("/api/tv/links", a.createLink)
		r.Delete("/api/tv/links/{linkId}", a.deleteLink)
		r.Get("/api/tv/settings", a.settings)
		r.Put("/api/tv/settings", a.putSettings)
	})
}

// Messages of the admin API.
const (
	msgSourceNotFound = "Источник каналов не найден"
	msgBadSourceURL   = "Укажите адрес плейлиста: http:// или https://"
	msgBadEPGURL      = "Укажите адрес телепрограммы: http:// или https://"
	msgXtreamOff      = "Xtream выключен в настройках ТВ"
	msgXtreamFields   = "Укажите сервер, логин и пароль Xtream"
	msgBadInterval    = "Интервал обновления: выключено, 6, 12, 24 часа или 3 дня"
	msgSourceName     = "Укажите название источника"
	msgNoChannels     = "Выберите каналы"
	msgOneNumber      = "Номер задаётся одному каналу"
	msgSetNotFound    = "Набор не найден"
	msgLinkNotFound   = "Ссылка не найдена"
	msgSetName        = "Укажите название набора"
	msgUnknownProfile = "Профиль не найден"
	msgUnknownDevice  = "Устройство не найдено"
	msgGuideNotFound  = "Телепрограмма не найдена"
	msgLinksOff       = "IPTV-ссылки выключены на этом сервере"
	msgExternalEdit   = "Внешний канал меняется в редакторе источников"
)

// Why codes: what the web localizes (whyText carries the text).
var whyCodes = map[string]string{
	iptv.WhyAdult:         "preset-adult",
	iptv.WhyTimeshift:     "preset-timeshift",
	iptv.WhySourceDefault: "source-default",
	iptv.WhyManual:        "manual",
}

func whyCode(w string) string {
	if c, ok := whyCodes[w]; ok {
		return c
	}
	return ""
}

var refreshIntervals = []int{0, 6, 12, 24, 72}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func tvID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

// ---- sources ----

type presetsJSON struct {
	Timeshift bool `json:"timeshift"`
	Adult     bool `json:"adult"`
}

type xtreamJSON struct {
	Server string `json:"server"`
	User   string `json:"user"`
}

type countsJSON struct {
	Total  int `json:"total"`
	Shown  int `json:"shown"`
	Hidden int `json:"hidden"`
	New    int `json:"new"`
}

type epgURLJSON struct {
	ID            int64      `json:"id"`
	URL           string     `json:"url"` // masked
	Auto          bool       `json:"auto"`
	Position      int        `json:"position"`
	RefreshHours  int        `json:"refreshHours"`
	RefreshOnOpen bool       `json:"refreshOnOpen"`
	LastOK        *time.Time `json:"lastOk"`
	LastTry       *time.Time `json:"lastTry"`
	LastError     string     `json:"lastError"`
	Matched       int        `json:"matched"`
}

type sourceJSON struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Kind          string       `json:"kind"`
	URL           string       `json:"url"` // masked, "" for xtream
	Xtream        *xtreamJSON  `json:"xtream"`
	DefaultShown  *bool        `json:"defaultShown"`
	HideURLs      bool         `json:"hideUrls"`
	Presets       presetsJSON  `json:"presets"`
	RefreshHours  int          `json:"refreshHours"`
	RefreshOnOpen bool         `json:"refreshOnOpen"`
	Position      int          `json:"position"`
	Profiles      []int64      `json:"profiles"`
	LastOK        *time.Time   `json:"lastOk"`
	LastTry       *time.Time   `json:"lastTry"`
	LastError     string       `json:"lastError"`
	PendingDrop   int          `json:"pendingDrop"`
	Counts        countsJSON   `json:"counts"`
	EPG           []epgURLJSON `json:"epg"`
}

// newWindow: an entry first seen within it, after the source's first
// import, is «new».
const newWindow = 7 * 24 * time.Hour

func isNew(m iptv.Member, firstImport, now time.Time) bool {
	return m.FirstSeen.After(firstImport) && now.Sub(m.FirstSeen) < newWindow
}

func firstImport(ms []iptv.Member) time.Time {
	var t time.Time
	for _, m := range ms {
		if !m.FirstSeen.IsZero() && (t.IsZero() || m.FirstSeen.Before(t)) {
			t = m.FirstSeen
		}
	}
	return t
}

func (a *iptvAdmin) sourceJSON(ctx context.Context, src iptv.Source) (sourceJSON, error) {
	j := sourceJSON{ID: src.ID, Name: src.Name, Kind: src.Kind, DefaultShown: src.DefaultShown, HideURLs: src.HideURLs,
		Presets: presetsJSON{src.PresetTimeshift, src.PresetAdult}, RefreshHours: src.RefreshHours, RefreshOnOpen: src.RefreshOnOpen,
		Position: src.Position, Profiles: src.Profiles, LastOK: optTime(src.LastOK), LastTry: optTime(src.LastTry),
		LastError: i18n.Localize(ctx, src.LastErr), PendingDrop: src.PendingDrop, EPG: []epgURLJSON{}}
	if j.Profiles == nil {
		j.Profiles = []int64{}
	}
	if src.Kind == iptv.KindXtream {
		j.Xtream = &xtreamJSON{Server: iptv.StripCreds(src.XtreamServer), User: src.XtreamUser}
		if j.Xtream.Server == "<invalid url>" {
			j.Xtream.Server = iptv.Mask(src.XtreamServer)
		}
	} else if raw, err := a.svc.Seal.Open(src.URLEnc); err == nil {
		j.URL = iptv.Mask(raw)
	} else {
		j.URL = "***"
	}
	ms, err := a.svc.Store.Members(ctx, src.ID)
	if err != nil {
		return j, err
	}
	first, now := firstImport(ms), time.Now()
	matched := map[int64]int{}
	for _, m := range ms {
		if !m.Present {
			continue
		}
		j.Counts.Total++
		if m.Shown {
			j.Counts.Shown++
		} else {
			j.Counts.Hidden++
		}
		if isNew(m, first, now) {
			j.Counts.New++
		}
		if m.EPGID != "" {
			matched[m.EPGURLID]++
		}
	}
	es, err := a.svc.Store.EPGURLs(ctx, src.ID)
	if err != nil {
		return j, err
	}
	for _, e := range es {
		raw, err := a.svc.Seal.Open(e.URLEnc)
		masked := "***"
		if err == nil {
			masked = iptv.Mask(raw)
		}
		n := matched[e.ID]
		if len(es) == 1 {
			n += matched[0]
		}
		j.EPG = append(j.EPG, epgURLJSON{ID: e.ID, URL: masked, Auto: e.Auto, Position: e.Position, RefreshHours: e.RefreshHours,
			RefreshOnOpen: e.RefreshOnOpen, LastOK: optTime(e.LastOK), LastTry: optTime(e.LastTry), LastError: i18n.Localize(ctx, e.LastErr), Matched: n})
	}
	return j, nil
}

// GET /api/tv/sources
func (a *iptvAdmin) sources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srcs, err := a.svc.Store.Sources(ctx)
	if err != nil {
		internalErr(w, r, "iptv sources", err)
		return
	}
	out := []sourceJSON{}
	for _, src := range srcs {
		j, err := a.sourceJSON(ctx, src)
		if err != nil {
			internalErr(w, r, "iptv sources", err)
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": out, "xtreamEnabled": a.svc.Store.XtreamEnabled(ctx)})
}

type epgBody struct {
	ID            int64  `json:"id"`
	URL           string `json:"url"` // "" keeps the stored one
	RefreshHours  *int   `json:"refreshHours"`
	RefreshOnOpen bool   `json:"refreshOnOpen"`
}

type sourceBody struct {
	Name   *string `json:"name"`
	Kind   string  `json:"kind"`
	URL    string  `json:"url"` // "" keeps the stored one
	Xtream *struct {
		Server   string `json:"server"`
		User     string `json:"user"`
		Password string `json:"password"` // "" keeps the stored one
	} `json:"xtream"`
	// DefaultShown: absent keeps; null: by the playlist's size.
	DefaultShown  optBool      `json:"defaultShown"`
	HideURLs      *bool        `json:"hideUrls"`
	Presets       *presetsJSON `json:"presets"`
	RefreshHours  *int         `json:"refreshHours"`
	RefreshOnOpen *bool        `json:"refreshOnOpen"`
	Position      *int         `json:"position"`
	Profiles      *[]int64     `json:"profiles"`
	EPG           *[]epgBody   `json:"epg"`
}

// optBool is a JSON field that may be absent (Set false), null or a bool.
type optBool struct {
	Set   bool
	Value *bool
}

func (o *optBool) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v bool
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

func httpURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// apply writes the body into src; it answers a refusal's message ("" ok).
func (a *iptvAdmin) apply(ctx context.Context, b sourceBody, src *iptv.Source, create bool) (string, error) {
	if b.Name != nil {
		src.Name = strings.TrimSpace(*b.Name)
	}
	if src.Name == "" {
		return msgSourceName, nil
	}
	if create {
		src.Kind = b.Kind
		if src.Kind == "" {
			src.Kind = iptv.KindM3U
		}
		src.PresetAdult, src.PresetTimeshift, src.RefreshHours = true, true, 24
	}
	switch src.Kind {
	case iptv.KindM3U:
		if b.URL != "" || create {
			if !httpURL(b.URL) {
				return msgBadSourceURL, nil
			}
			enc, err := a.svc.Seal.Seal(strings.TrimSpace(b.URL))
			if err != nil {
				return "", err
			}
			src.URLEnc = enc
		}
	case iptv.KindXtream:
		if !a.svc.Store.XtreamEnabled(ctx) {
			return msgXtreamOff, nil
		}
		if b.Xtream != nil {
			src.XtreamServer, src.XtreamUser = strings.TrimSpace(b.Xtream.Server), strings.TrimSpace(b.Xtream.User)
			if b.Xtream.Password != "" {
				enc, err := a.svc.Seal.Seal(b.Xtream.Password)
				if err != nil {
					return "", err
				}
				src.XtreamPassEnc = enc
			}
		}
		if src.XtreamServer == "" || src.XtreamUser == "" || len(src.XtreamPassEnc) == 0 {
			return msgXtreamFields, nil
		}
	default:
		return msgBadSourceURL, nil
	}
	if b.DefaultShown.Set {
		src.DefaultShown = b.DefaultShown.Value
	}
	if b.HideURLs != nil {
		src.HideURLs = *b.HideURLs
	}
	if b.Presets != nil {
		src.PresetTimeshift, src.PresetAdult = b.Presets.Timeshift, b.Presets.Adult
	}
	if b.RefreshHours != nil {
		if !slices.Contains(refreshIntervals, *b.RefreshHours) {
			return msgBadInterval, nil
		}
		src.RefreshHours = *b.RefreshHours
	}
	if b.RefreshOnOpen != nil {
		src.RefreshOnOpen = *b.RefreshOnOpen
	}
	if b.Position != nil {
		src.Position = *b.Position
	}
	if b.Profiles != nil {
		src.Profiles = *b.Profiles
	}
	if b.EPG != nil {
		for _, e := range *b.EPG {
			if e.URL != "" && !httpURL(e.URL) || e.ID == 0 && e.URL == "" {
				return msgBadEPGURL, nil
			}
			if e.RefreshHours != nil && !slices.Contains(refreshIntervals, *e.RefreshHours) {
				return msgBadInterval, nil
			}
		}
	}
	return "", nil
}

// saveEPG writes the body's guide URLs of a source: listed ones in their
// order (new ones added), an admin's own not listed removed; the
// playlist's own (auto) stay unless listed.
func (a *iptvAdmin) saveEPG(ctx context.Context, srcID int64, list []epgBody) error {
	have, err := a.svc.Store.EPGURLs(ctx, srcID)
	if err != nil {
		return err
	}
	byID := map[int64]iptv.EPGURL{}
	for _, e := range have {
		byID[e.ID] = e
	}
	listed := map[int64]bool{}
	for i, b := range list {
		e, ok := byID[b.ID]
		if b.ID != 0 && !ok {
			continue // another source's, or gone
		}
		if !ok {
			e = iptv.EPGURL{SourceID: srcID, RefreshHours: 24}
		}
		if b.URL != "" {
			enc, err := a.svc.Seal.Seal(strings.TrimSpace(b.URL))
			if err != nil {
				return err
			}
			e.URLEnc, e.Auto, e.LastOK, e.LastErr = enc, false, time.Time{}, ""
		}
		if b.RefreshHours != nil {
			e.RefreshHours = *b.RefreshHours
		}
		e.RefreshOnOpen, e.Position = b.RefreshOnOpen, i
		e, err = a.svc.Store.SaveEPGURL(ctx, e)
		if err != nil {
			return err
		}
		listed[e.ID] = true
	}
	for _, e := range have {
		if !listed[e.ID] && !e.Auto {
			if err := a.svc.Store.DeleteEPGURL(ctx, e.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *iptvAdmin) writeSource(w http.ResponseWriter, r *http.Request, code int, src iptv.Source, extra map[string]any) {
	j, err := a.sourceJSON(r.Context(), src)
	if err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	if extra == nil {
		writeJSON(w, code, j)
		return
	}
	extra["source"] = j
	writeJSON(w, code, extra)
}

// POST /api/tv/sources
func (a *iptvAdmin) createSource(w http.ResponseWriter, r *http.Request) {
	var b sourceBody
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	var src iptv.Source
	msg, err := a.apply(ctx, b, &src, true)
	if err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	if msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	if b.Position == nil {
		all, _ := a.svc.Store.Sources(ctx)
		for _, s := range all {
			src.Position = max(src.Position, s.Position+1)
		}
	}
	if src, err = a.svc.Store.SaveSource(ctx, src); err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	if b.EPG != nil {
		if err := a.saveEPG(ctx, src.ID, *b.EPG); err != nil {
			internalErr(w, r, "iptv source", err)
			return
		}
	}
	a.writeSource(w, r, http.StatusCreated, src, nil)
}

func (a *iptvAdmin) sourceParam(w http.ResponseWriter, r *http.Request) (iptv.Source, bool) {
	id, ok := tvID(r)
	if !ok {
		writeErr(w, r, http.StatusNotFound, msgSourceNotFound)
		return iptv.Source{}, false
	}
	src, err := a.svc.Store.Source(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, r, http.StatusNotFound, msgSourceNotFound)
		return src, false
	}
	if err != nil {
		internalErr(w, r, "iptv source", err)
		return src, false
	}
	return src, true
}

// PUT /api/tv/sources/{id}
func (a *iptvAdmin) updateSource(w http.ResponseWriter, r *http.Request) {
	src, ok := a.sourceParam(w, r)
	if !ok {
		return
	}
	var b sourceBody
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	msg, err := a.apply(ctx, b, &src, false)
	if err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	if msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	if src, err = a.svc.Store.SaveSource(ctx, src); err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	if b.EPG != nil {
		if err := a.saveEPG(ctx, src.ID, *b.EPG); err != nil {
			internalErr(w, r, "iptv source", err)
			return
		}
	}
	// the rules (presets, default, order) apply at once
	if err := a.svc.Sync(ctx); err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	a.writeSource(w, r, http.StatusOK, src, nil)
}

// DELETE /api/tv/sources/{id}
func (a *iptvAdmin) deleteSource(w http.ResponseWriter, r *http.Request) {
	src, ok := a.sourceParam(w, r)
	if !ok {
		return
	}
	if err := a.svc.DeleteSource(r.Context(), src.ID); err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type resultJSON struct {
	Total       int `json:"total"`
	New         int `json:"new"`
	Lost        int `json:"lost"`
	Shown       int `json:"shown"`
	Hidden      int `json:"hidden"`
	PendingDrop int `json:"pendingDrop"`
}

// POST /api/tv/sources/{id}/refresh (confirm: …/confirm, accepting the
// pending drop): {ok, error?, result, source}; the source's guides are
// refreshed after it in the background.
func (a *iptvAdmin) refreshSource(confirm bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src, ok := a.sourceParam(w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		res, err := a.svc.RefreshSource(ctx, src.ID, confirm)
		out := map[string]any{"ok": err == nil, "result": resultJSON(res)}
		if err != nil {
			out["error"] = i18n.Localize(ctx, iptv.ErrText(err))
		} else if res.PendingDrop == 0 {
			go a.svc.RefreshSourceEPGs(context.WithoutCancel(ctx), src.ID)
		}
		src, gerr := a.svc.Store.Source(ctx, src.ID)
		if gerr != nil {
			internalErr(w, r, "iptv source", gerr)
			return
		}
		a.writeSource(w, r, http.StatusOK, src, out)
	}
}

// POST /api/tv/sources/{id}/reject: the pending drop is refused, the list
// stays as it was.
func (a *iptvAdmin) rejectDrop(w http.ResponseWriter, r *http.Request) {
	src, ok := a.sourceParam(w, r)
	if !ok {
		return
	}
	src, err := a.svc.RejectDrop(r.Context(), src.ID)
	if err != nil {
		internalErr(w, r, "iptv source", err)
		return
	}
	a.writeSource(w, r, http.StatusOK, src, nil)
}

// POST /api/tv/epg/{id}/refresh: started in the background (202); the
// result is the guide's lastOk/lastError in GET /api/tv/sources.
func (a *iptvAdmin) refreshEPG(w http.ResponseWriter, r *http.Request) {
	id, ok := tvID(r)
	if ok {
		_, err := a.svc.Store.EPGURL(r.Context(), id)
		ok = err == nil
	}
	if !ok {
		writeErr(w, r, http.StatusNotFound, msgGuideNotFound)
		return
	}
	go func() {
		if err := a.svc.RefreshEPG(context.WithoutCancel(r.Context()), id); err != nil {
			log.Printf("iptv: guide %d: %s", id, iptv.ErrText(err))
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

// ---- the editor ----

type memberEPGJSON struct {
	XMLID    string `json:"xmlId"`
	EPGURLID int64  `json:"epgUrlId"`
	How      string `json:"how"` // tvg-id|name|norm-name|manual|""
	Manual   bool   `json:"manual"`
}

type memberJSON struct {
	MemberID     int64         `json:"memberId"`
	ChannelID    int64         `json:"channelId"`
	Name         string        `json:"name"`         // the channel's (the editor's)
	ProviderName string        `json:"providerName"` // the playlist's
	TvgID        string        `json:"tvgId"`
	Group        string        `json:"group"` // the playlist's group-title
	Logo         string        `json:"logo"`
	Shown        bool          `json:"shown"`
	Number       int           `json:"number"`
	NumberManual bool          `json:"numberManual"`
	Category     string        `json:"category"`
	EPG          memberEPGJSON `json:"epg"`
	Why          string        `json:"why"`
	WhyText      string        `json:"whyText"`
	Present      bool          `json:"present"`
	New          bool          `json:"new"`
}

type groupJSON struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
	Shown int    `json:"shown"`
}

// GET /api/tv/sources/{id}/channels?q=&group=&state=all|shown|hidden|new|gone&offset=&limit=
// (no limit: every row): the source's entries, groups counted over the
// present ones.
func (a *iptvAdmin) sourceChannels(w http.ResponseWriter, r *http.Request) {
	src, ok := a.sourceParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ms, err := a.svc.Store.Members(ctx, src.ID)
	if err != nil {
		internalErr(w, r, "iptv channels", err)
		return
	}
	rows, err := a.externalRows(ctx)
	if err != nil {
		internalErr(w, r, "iptv channels", err)
		return
	}
	qv := r.URL.Query()
	q := strings.ToLower(strings.TrimSpace(qv.Get("q")))
	group, state := qv.Get("group"), qv.Get("state")
	offset, _ := strconv.Atoi(qv.Get("offset"))
	limit, _ := strconv.Atoi(qv.Get("limit"))
	if limit <= 0 || limit > iptv.MaxEntries {
		limit = iptv.MaxEntries
	}
	first, now := firstImport(ms), time.Now()
	groups := []groupJSON{}
	gi := map[string]int{}
	out := []memberJSON{}
	total := 0
	for _, m := range ms {
		nw := m.Present && isNew(m, first, now)
		if m.Present {
			i, ok := gi[m.Group]
			if !ok {
				i = len(groups)
				gi[m.Group] = i
				groups = append(groups, groupJSON{Name: m.Group})
			}
			groups[i].Total++
			if m.Shown {
				groups[i].Shown++
			}
		}
		switch state {
		case "shown":
			ok = m.Present && m.Shown
		case "hidden":
			ok = m.Present && !m.Shown
		case "new":
			ok = nw
		case "gone":
			ok = !m.Present
		default:
			ok = m.Present
		}
		c := rows[m.ChannelID]
		if !ok || (group != "" && m.Group != group) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.Name+"\n"+c.Name+"\n"+m.TvgID), q) {
			continue
		}
		total++
		if total <= offset || len(out) >= limit {
			continue
		}
		out = append(out, memberJSON{MemberID: m.ID, ChannelID: m.ChannelID, Name: c.Name, ProviderName: m.Name, TvgID: m.TvgID, Group: m.Group,
			Logo: m.Logo, Shown: m.Shown, Number: c.Number, NumberManual: c.NumberManual, Category: c.Category,
			EPG: memberEPGJSON{XMLID: m.EPGID, EPGURLID: m.EPGURLID, How: m.EPGHow, Manual: m.EPGManual},
			Why: whyCode(m.Why), WhyText: i18n.Localize(ctx, m.Why), Present: m.Present, New: nw})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "channels": out, "total": total})
}

func (a *iptvAdmin) externalRows(ctx context.Context) (map[int64]channels.Channel, error) {
	all, err := a.ca.svc.Store.All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]channels.Channel{}
	for _, c := range all {
		if c.External() {
			out[c.ID] = c
		}
	}
	return out, nil
}

// PATCH /api/tv/channels {channelIds, shown?, category?, name?, number?, reset?}:
// the admin's decision for every entry of those channels (category, name:
// "" back to the playlist's; number: one channel only, 0 back to automatic;
// reset: every manual decision dropped). Answers {updated}.
func (a *iptvAdmin) patchChannels(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ChannelIDs []int64 `json:"channelIds"`
		Shown      *bool   `json:"shown"`
		Category   *string `json:"category"`
		Name       *string `json:"name"`
		Number     *int    `json:"number"`
		Reset      bool    `json:"reset"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.ChannelIDs) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgNoChannels)
		return
	}
	ctx := r.Context()
	if b.Number != nil {
		if len(b.ChannelIDs) != 1 {
			writeErr(w, r, http.StatusBadRequest, msgOneNumber)
			return
		}
		if *b.Number < 0 || *b.Number > 99999 {
			writeErr(w, r, http.StatusBadRequest, "Номер канала — от 1 до 99999")
			return
		}
		if *b.Number > 0 {
			var other int64
			err := a.svc.Store.DB.QueryRowContext(ctx, `SELECT id FROM channels WHERE number=? AND id<>?`, *b.Number, b.ChannelIDs[0]).Scan(&other)
			if err == nil {
				writeErr(w, r, http.StatusConflict, "Этот номер уже занят другим каналом")
				return
			}
		}
	}
	n := 0
	for _, id := range b.ChannelIDs {
		ms, err := a.svc.Store.ChannelMembers(ctx, id)
		if err != nil {
			internalErr(w, r, "iptv channels", err)
			return
		}
		for _, m := range ms {
			d := iptv.Decision{}
			if m.Manual != nil && !b.Reset {
				d = *m.Manual
			}
			if b.Shown != nil {
				v := *b.Shown
				d.Shown = &v
			}
			if b.Category != nil {
				d.Group = strings.TrimSpace(*b.Category)
			}
			if b.Name != nil {
				d.Name = strings.TrimSpace(*b.Name)
			}
			if b.Number != nil {
				d.Number = *b.Number
			}
			var dp *iptv.Decision
			if d != (iptv.Decision{}) {
				dp = &d
			}
			if err := a.svc.Store.SetManual(ctx, m.ID, dp); err != nil {
				internalErr(w, r, "iptv channels", err)
				return
			}
			n++
		}
	}
	if err := a.svc.Sync(ctx); err != nil {
		internalErr(w, r, "iptv channels", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

// PATCH /api/tv/groups {sourceId, group, shown}: show or hide a whole
// group of a source's playlist (a manual decision per entry).
func (a *iptvAdmin) patchGroup(w http.ResponseWriter, r *http.Request) {
	var b struct {
		SourceID int64  `json:"sourceId"`
		Group    string `json:"group"`
		Shown    bool   `json:"shown"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	ms, err := a.svc.Store.Members(ctx, b.SourceID)
	if err != nil {
		internalErr(w, r, "iptv groups", err)
		return
	}
	n := 0
	for _, m := range ms {
		if m.Group != b.Group {
			continue
		}
		d := iptv.Decision{}
		if m.Manual != nil {
			d = *m.Manual
		}
		v := b.Shown
		d.Shown = &v
		if err := a.svc.Store.SetManual(ctx, m.ID, &d); err != nil {
			internalErr(w, r, "iptv groups", err)
			return
		}
		n++
	}
	if err := a.svc.Sync(ctx); err != nil {
		internalErr(w, r, "iptv groups", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

type xmlChannelJSON struct {
	EPGURLID int64    `json:"epgUrlId"`
	SourceID int64    `json:"sourceId"`
	XMLID    string   `json:"xmlId"`
	Names    []string `json:"names"`
	Icon     string   `json:"icon"`
}

// GET /api/tv/epg/search?q=: guide channels whose id or a display-name
// contains q (at most 50).
func (a *iptvAdmin) epgSearch(w http.ResponseWriter, r *http.Request) {
	res, err := a.svc.SearchGuide(r.Context(), r.URL.Query().Get("q"), 50)
	if err != nil {
		internalErr(w, r, "iptv epg search", err)
		return
	}
	out := []xmlChannelJSON{}
	for _, c := range res {
		out = append(out, xmlChannelJSON{EPGURLID: c.EPGURLID, SourceID: c.SourceID, XMLID: c.XMLID, Names: c.Names, Icon: c.Icon})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// PUT /api/tv/channels/{id}/epg {epgUrlId, xmlId}: the channel's guide by
// hand (wins the automatic binding, survives renames); its guide is
// refreshed in the background for the programmes.
func (a *iptvAdmin) bindEPG(w http.ResponseWriter, r *http.Request) {
	id, _ := tvID(r)
	var b struct {
		EPGURLID int64  `json:"epgUrlId"`
		XMLID    string `json:"xmlId"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	if _, err := a.svc.Store.EPGURL(ctx, b.EPGURLID); err != nil || strings.TrimSpace(b.XMLID) == "" {
		writeErr(w, r, http.StatusBadRequest, msgGuideNotFound)
		return
	}
	if !a.setEPG(w, r, id, b.EPGURLID, strings.TrimSpace(b.XMLID)) {
		return
	}
	go func() {
		if err := a.svc.RefreshEPG(context.WithoutCancel(ctx), b.EPGURLID); err != nil {
			log.Printf("iptv: guide %d: %s", b.EPGURLID, iptv.ErrText(err))
		}
	}()
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/tv/channels/{id}/epg: back to the automatic binding (made
// again on the guide's next refresh).
func (a *iptvAdmin) unbindEPG(w http.ResponseWriter, r *http.Request) {
	id, _ := tvID(r)
	if a.setEPG(w, r, id, 0, "") {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *iptvAdmin) setEPG(w http.ResponseWriter, r *http.Request, channelID, epgURLID int64, xmlID string) bool {
	ctx := r.Context()
	ms, err := a.svc.Store.ChannelMembers(ctx, channelID)
	if err != nil {
		internalErr(w, r, "iptv epg", err)
		return false
	}
	if len(ms) == 0 {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return false
	}
	for _, m := range ms {
		var err error
		if xmlID == "" {
			// manual off: an automatic call may bind it again
			_, err = a.svc.Store.DB.ExecContext(ctx, `UPDATE iptv_members SET epg_id='', epg_url_id=0, epg_how='', epg_manual=0 WHERE id=?`, m.ID)
		} else {
			err = a.svc.Store.SetMemberEPG(ctx, m.ID, epgURLID, xmlID, "manual", true)
		}
		if err != nil {
			internalErr(w, r, "iptv epg", err)
			return false
		}
	}
	return true
}

// GET /api/tv/categories: every category in use (own and external
// channels; equal names are one), for «Перенести в категорию».
func (a *iptvAdmin) categories(w http.ResponseWriter, r *http.Request) {
	all, err := a.ca.svc.Store.All(r.Context())
	if err != nil {
		internalErr(w, r, "iptv categories", err)
		return
	}
	seen := map[string]bool{}
	out := []string{}
	lang := i18n.FromContext(r.Context())
	for _, c := range all {
		cat := c.Category
		if !c.External() {
			_, cat = channels.Display(lang, c)
		}
		cat = strings.TrimSpace(cat)
		if k := strings.ToLower(cat); cat != "" && !seen[k] {
			seen[k] = true
			out = append(out, cat)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

// ---- sets ----

// GET /api/tv/sets
func (a *iptvAdmin) sets(w http.ResponseWriter, r *http.Request) {
	sets, err := a.svc.Store.Sets(r.Context())
	if err != nil {
		internalErr(w, r, "iptv sets", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sets": sets})
}

// POST /api/tv/sets, PUT /api/tv/sets/{id}: {name, sources, hiddenGroups, hiddenChannels}
func (a *iptvAdmin) saveSet(w http.ResponseWriter, r *http.Request) {
	var b iptv.Set
	if !decode(w, r, &b) {
		return
	}
	b.ID = 0
	code := http.StatusCreated
	if chi.URLParam(r, "id") != "" {
		id, ok := tvID(r)
		if !ok {
			writeErr(w, r, http.StatusNotFound, msgSetNotFound)
			return
		}
		b.ID, code = id, http.StatusOK
	}
	if strings.TrimSpace(b.Name) == "" {
		writeErr(w, r, http.StatusBadRequest, msgSetName)
		return
	}
	set, err := a.svc.Store.SaveSet(r.Context(), b)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, r, http.StatusNotFound, msgSetNotFound)
		return
	}
	if err != nil {
		internalErr(w, r, "iptv set", err)
		return
	}
	writeJSON(w, code, set)
}

// DELETE /api/tv/sets/{id}
func (a *iptvAdmin) deleteSet(w http.ResponseWriter, r *http.Request) {
	id, _ := tvID(r)
	if err := a.svc.Store.DeleteSet(r.Context(), id); err != nil {
		internalErr(w, r, "iptv set", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- devices and links ----

type deviceJSON struct {
	UserID     int64      `json:"userId"`
	Profile    string     `json:"profile"`
	Kind       string     `json:"kind"`     // app | iptv
	DeviceID   string     `json:"deviceId"` // app: its device id; iptv: ""
	LinkID     string     `json:"linkId"`   // iptv: the link's id; app: ""
	Name       string     `json:"name"`
	Client     string     `json:"client"` // ios|ipados|tvos|macos|android|androidtv|web|iptv|other
	ClientName string     `json:"clientName"`
	LastSeen   *time.Time `json:"lastSeen"`
	SetID      int64      `json:"setId"` // 0: «Все мои каналы» (the profile's default when it has one: defaultSetId)
	// DefaultSetID: the profile's default set (0: all my channels).
	DefaultSetID int64 `json:"defaultSetId"`
	// Playlist, Guide: an IPTV link's addresses.
	Playlist string `json:"playlist,omitempty"`
	Guide    string `json:"guide,omitempty"`
}

// clientOf guesses the app's platform from what it said at login.
func clientOf(name, model, client string) (code, text string) {
	s := strings.ToLower(name + " " + model + " " + client)
	switch {
	case strings.Contains(s, "apple tv") || strings.Contains(s, "appletv") || strings.Contains(s, "tvos"):
		return "tvos", "Apple TV"
	case strings.Contains(s, "ipad"):
		return "ipados", "iPad"
	case strings.Contains(s, "iphone") || strings.Contains(s, "ios"):
		return "ios", "iPhone"
	case strings.Contains(s, "mac"):
		return "macos", "Mac"
	case strings.Contains(s, "android") && strings.Contains(s, "tv"):
		return "androidtv", "Android TV"
	case strings.Contains(s, "android"):
		return "android", "Android"
	}
	return "other", strings.TrimSpace(model)
}

func linkID(token string) string {
	h := sha256.Sum256([]byte("iptv-link:" + token))
	return hex.EncodeToString(h[:8])
}

// GET /api/tv/devices: the profiles' app devices and IPTV links with
// their sets.
func (a *iptvAdmin) devices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asg, err := a.svc.Store.Assignments(ctx)
	if err != nil {
		internalErr(w, r, "iptv devices", err)
		return
	}
	out := []deviceJSON{}
	seen := map[string]bool{}
	rows, err := a.svc.Store.DB.QueryContext(ctx, `SELECT s.user_id, u.name, s.device_id, s.device_name, s.device_model, s.last_seen
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.kind=? AND s.device_id<>'' ORDER BY s.last_seen DESC`, auth.KindNative)
	if err != nil {
		internalErr(w, r, "iptv devices", err)
		return
	}
	for rows.Next() {
		var d deviceJSON
		var model, seenAt string
		if err := rows.Scan(&d.UserID, &d.Profile, &d.DeviceID, &d.Name, &model, &seenAt); err != nil {
			rows.Close()
			internalErr(w, r, "iptv devices", err)
			return
		}
		key := strconv.FormatInt(d.UserID, 10) + "/" + d.DeviceID
		if seen[key] {
			continue
		}
		seen[key] = true
		d.Kind = "app"
		d.Client, d.ClientName = clientOf(d.Name, model, "")
		if t, err := time.Parse(time.RFC3339, seenAt); err == nil {
			d.LastSeen = optTime(t)
		}
		d.SetID, d.DefaultSetID = asg[d.UserID][d.DeviceID], asg[d.UserID][""]
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		internalErr(w, r, "iptv devices", err)
		return
	}
	rows, err = a.svc.Store.DB.QueryContext(ctx, `SELECT t.token, t.user_id, u.name, t.device, t.last_seen, COALESCE(t.set_id, 0)
		FROM app_tokens t JOIN users u ON u.id=t.user_id WHERE t.proto=? ORDER BY t.last_seen DESC`, iptvProto)
	if err != nil {
		internalErr(w, r, "iptv devices", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var d deviceJSON
		var tok, seenAt string
		if err := rows.Scan(&tok, &d.UserID, &d.Profile, &d.Name, &seenAt, &d.SetID); err != nil {
			internalErr(w, r, "iptv devices", err)
			return
		}
		d.Kind, d.LinkID, d.Client, d.ClientName = "iptv", linkID(tok), "iptv", "IPTV"
		if t, err := time.Parse(time.RFC3339, seenAt); err == nil {
			d.LastSeen = optTime(t)
		}
		d.DefaultSetID = asg[d.UserID][""]
		base := a.ca.iptvBase(r, tok)
		d.Playlist, d.Guide = base+"/playlist.m3u", base+"/epg.xml"
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		internalErr(w, r, "iptv devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// PUT /api/tv/devices/assign {userId, deviceId, setId} (deviceId "": the
// profile's default) or {linkId, setId}: setId 0 is «Все мои каналы».
func (a *iptvAdmin) assign(w http.ResponseWriter, r *http.Request) {
	var b struct {
		UserID   int64  `json:"userId"`
		DeviceID string `json:"deviceId"`
		LinkID   string `json:"linkId"`
		SetID    int64  `json:"setId"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	if b.SetID != 0 {
		if _, err := a.svc.Store.Set(ctx, b.SetID); err != nil {
			writeErr(w, r, http.StatusBadRequest, msgSetNotFound)
			return
		}
	}
	if b.LinkID != "" {
		tok, err := a.findLink(ctx, b.LinkID)
		if err != nil {
			internalErr(w, r, "iptv assign", err)
			return
		}
		if tok == "" {
			writeErr(w, r, http.StatusNotFound, msgUnknownDevice)
			return
		}
		if err := a.svc.Store.SetTokenSet(ctx, tok, b.SetID); err != nil {
			internalErr(w, r, "iptv assign", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := a.ca.users.Get(ctx, b.UserID); err != nil {
		writeErr(w, r, http.StatusBadRequest, msgUnknownProfile)
		return
	}
	if err := a.svc.Store.AssignSet(ctx, b.UserID, b.DeviceID, b.SetID); err != nil {
		internalErr(w, r, "iptv assign", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *iptvAdmin) findLink(ctx context.Context, id string) (string, error) {
	rows, err := a.svc.Store.DB.QueryContext(ctx, `SELECT token FROM app_tokens WHERE proto=?`, iptvProto)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var tok string
		if err := rows.Scan(&tok); err != nil {
			return "", err
		}
		if linkID(tok) == id {
			return tok, nil
		}
	}
	return "", rows.Err()
}

// POST /api/tv/links {userId, setId, name}: a new IPTV link of the
// profile with that set → {linkId, playlist, guide}.
func (a *iptvAdmin) createLink(w http.ResponseWriter, r *http.Request) {
	if !a.links {
		writeErr(w, r, http.StatusConflict, msgLinksOff)
		return
	}
	var b struct {
		UserID int64  `json:"userId"`
		SetID  int64  `json:"setId"`
		Name   string `json:"name"`
	}
	if !decode(w, r, &b) {
		return
	}
	ctx := r.Context()
	if _, err := a.ca.users.Get(ctx, b.UserID); err != nil {
		writeErr(w, r, http.StatusBadRequest, msgUnknownProfile)
		return
	}
	if b.SetID != 0 {
		if _, err := a.svc.Store.Set(ctx, b.SetID); err != nil {
			writeErr(w, r, http.StatusBadRequest, msgSetNotFound)
			return
		}
	}
	name := clip(strings.TrimSpace(b.Name), 100)
	if name == "" {
		name = "IPTV"
	}
	// a device id of its own: links of one profile do not replace each other
	tok, err := a.ca.tokens.Issue(ctx, b.UserID, iptvProto, auth.Device{ID: "iptv-link-" + strconv.FormatInt(time.Now().UnixNano(), 36), Name: name, Client: "IPTV"})
	if err != nil {
		internalErr(w, r, "iptv link", err)
		return
	}
	if err := a.svc.Store.SetTokenSet(ctx, tok, b.SetID); err != nil {
		internalErr(w, r, "iptv link", err)
		return
	}
	base := a.ca.iptvBase(r, tok)
	writeJSON(w, http.StatusCreated, map[string]string{"linkId": linkID(tok), "playlist": base + "/playlist.m3u", "guide": base + "/epg.xml"})
}

// DELETE /api/tv/links/{linkId}: revokes an IPTV link (the TV's playlist
// and guide stop working).
func (a *iptvAdmin) deleteLink(w http.ResponseWriter, r *http.Request) {
	tok, err := a.findLink(r.Context(), chi.URLParam(r, "linkId"))
	if err != nil || tok == "" {
		writeErr(w, r, http.StatusNotFound, msgLinkNotFound)
		return
	}
	if err := a.ca.tokens.Revoke(r.Context(), tok); err != nil {
		internalErr(w, r, "iptv link", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET|PUT /api/tv/settings {xtreamEnabled}
func (a *iptvAdmin) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"xtreamEnabled": a.svc.Store.XtreamEnabled(r.Context())})
}

func (a *iptvAdmin) putSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		XtreamEnabled bool `json:"xtreamEnabled"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := a.svc.Store.SetXtreamEnabled(r.Context(), b.XtreamEnabled); err != nil {
		internalErr(w, r, "iptv settings", err)
		return
	}
	a.settings(w, r)
}

// POST /api/channels/opened: a client opened «ТВ» (refreshes «при
// открытии» in the background). 204.
func (a *iptvAdmin) opened(w http.ResponseWriter, r *http.Request) {
	a.svc.OnOpen(r.Context())
	w.WriteHeader(http.StatusNoContent)
}
