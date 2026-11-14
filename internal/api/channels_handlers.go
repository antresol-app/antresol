package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/imgcache"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
	"github.com/antresol-app/antresol/internal/screentime"
	"github.com/go-chi/chi/v5"
)

// «ТВ-каналы»: the apps' channel list, guide, editor and live stream
// (/api/channels…, session), and — only with VAULT_IPTV=on — the IPTV
// players' playlist, guide, streams and logos (/iptv/{token}/…, an app
// token in the path).

const iptvProto = "iptv"

type channelsAPI struct {
	svc     *channels.Service
	ext     *iptv.Service // external channels (nil: none)
	lib     *library.Repo
	tokens  *auth.AppTokens
	network *auth.Network
	users   *auth.Store
	screen  *screentime.Service
	dataDir string
	// cards translates programme titles into the viewer's language like
	// the library's cards (nil: the stored text).
	cards *lib
}

func newChannelsAPI(deps Deps, cards *lib) *channelsAPI {
	if cards != nil && deps.Channels.ExternalAudio == nil {
		// the encoder takes a file's external voice-overs as the players
		// get them: probed (cached), fitted, the cleaned ones placed
		deps.Channels.ExternalAudio = func(ctx context.Context, fileID int64, path string, p media.ProbeResult) []media.ExternalAudio {
			var out []media.ExternalAudio
			for _, e := range cards.externalAudio(ctx, library.File{ID: fileID, Path: path}, p.DurationMs, p.StartMs) {
				out = append(out, e.media())
			}
			return out
		}
	}
	if deps.Channels.DataDir == "" {
		deps.Channels.DataDir = deps.DataDir
	}
	return &channelsAPI{svc: deps.Channels, ext: deps.ExtIPTV, lib: deps.Library, tokens: auth.NewAppTokens(deps.DB), network: deps.Network,
		users: deps.Users, screen: deps.ScreenTime, dataDir: deps.DataDir, cards: cards}
}

func viewerOf(u auth.User) channels.Viewer {
	return channels.Viewer{ID: u.ID, MaxRating: u.RatingLimit(), Admin: u.IsAdmin}
}

// mountChannels: the web app's routes (inside the session guard).
func mountChannels(r chi.Router, a *channelsAPI) {
	r.Get("/api/channels", a.list)
	r.Get("/api/channels/epg", a.epgGrid)
	r.Put("/api/channels/last", a.setLast)
	r.Put("/api/channels/{id}/favorite", a.favorite(true))
	r.Delete("/api/channels/{id}/favorite", a.favorite(false))
	r.Get("/api/channels/options", a.options)
	r.Get("/api/channels/options/search", a.optionsSearch)
	r.Get("/api/channels/options/folders", a.optionsFolders)
	r.Post("/api/channels/preview", a.preview)
	r.Post("/api/channels/suggest", a.suggest)
	r.With(auth.RequireAdmin).Post("/api/channels/generate", a.generate)
	r.Post("/api/channels", a.create)
	r.Put("/api/channels/{id}", a.update)
	r.Delete("/api/channels/{id}", a.remove)
	r.Get("/api/channels/{id}/guide", a.guide)
	r.Get("/api/channels/{id}/logo.png", a.webLogo)
	r.Post("/api/channels/{id}/logo", a.uploadLogo)
	r.Delete("/api/channels/{id}/logo", a.deleteLogo)
	r.Get("/api/channels/live/{id}/{name}", a.webLive)
	if a.ext != nil {
		r.Get("/api/channels/live/ext/{id}/{n}", a.extStream)
	}
}

// mountIPTVLinks: the profile's IPTV addresses (Deps.IPTV only).
func mountIPTVLinks(r chi.Router, a *channelsAPI) {
	r.Get("/api/channels/iptv", a.iptvLinks)
	r.Post("/api/channels/iptv/reset", a.iptvReset)
}

// mountIPTV: the IPTV players' routes (Deps.IPTV only) (a token in the path, no session).
func mountIPTV(r chi.Router, a *channelsAPI) {
	r.Get("/iptv/{token}/playlist.m3u", a.m3u)
	r.Get("/iptv/{token}/playlist.m3u8", a.m3u)
	r.Get("/iptv/{token}/epg.xml", a.epg)
	r.Get("/iptv/{token}/live/{id}/{name}", a.iptvLive)
	r.Get("/iptv/{token}/logo/{name}", a.iptvLogo)
	r.Get("/iptv/{token}/art/{item}", a.iptvArt)
	if a.ext != nil {
		r.Get("/iptv/{token}/ext/{id}", a.iptvExt)
	}
}

// ---- the web app ----

type programJSON struct {
	// ID is "<channel>-<seq>" of the programme's first schedule entry.
	ID       string    `json:"id"`
	ItemID   int64     `json:"itemId"`
	Kind     string    `json:"kind"` // movie | episode
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle,omitempty"`
	Overview string    `json:"overview,omitempty"`
	Year     int       `json:"year,omitempty"`
	Rating   string    `json:"rating,omitempty"`
	Genres   []string  `json:"genres,omitempty"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Poster   string    `json:"poster,omitempty"`
	Backdrop string    `json:"backdrop,omitempty"`
	// External: a programme from an online source (not in the library).
	External bool `json:"external,omitempty"`
	// Audio and Subtitles: the live stream's renditions this programme
	// really has (nil: unknown; filled for the one on air and the next).
	Audio     []trackJSON `json:"audio,omitempty"`
	Subtitles []trackJSON `json:"subtitles,omitempty"`
	fileID    int64
}

// trackJSON is a rendition of the live stream as one programme fills it:
// Slot is its place in the run's set ("a<n>" audio-<n>.m3u8, "s<n>"
// subs-<n>.m3u8), Name is the HLS rendition's NAME (clients pick by it),
// Label describes the programme's own track («Русский — LostFilm»).
type trackJSON struct {
	Slot  string `json:"slot"`
	Name  string `json:"name"`
	Label string `json:"label"`
	Lang  string `json:"lang"`
}

type channelJSON struct {
	ID       int64           `json:"id"`
	Number   int             `json:"number"`
	Name     string          `json:"name"`
	Category string          `json:"category"`
	Personal bool            `json:"personal"`
	Auto     bool            `json:"auto"`
	Enabled  bool            `json:"enabled"`
	CanEdit  bool            `json:"canEdit"`
	Favorite bool            `json:"favorite"`
	Logo     int             `json:"logo"` // the gradient (0…6), IPTV only
	Rules    *channels.Rules `json:"rules,omitempty"`
	Now      *programJSON    `json:"now,omitempty"`
	Next     *programJSON    `json:"next,omitempty"`

	// LogoURL: the uploaded logo (absent: none); PickInfo: the picks
	// described, for the editor (channels_fine.go)
	LogoURL  string              `json:"logoUrl,omitempty"`
	PickInfo []channels.PickInfo `json:"pickInfo,omitempty"`

	// External: a channel of an external playlist (internal/iptv); Stream
	// is how to play it (channels_external.go). Absent for own channels.
	External bool        `json:"external,omitempty"`
	Stream   *streamJSON `json:"stream,omitempty"`
}

func (a *channelsAPI) toJSON(ctx context.Context, c channels.Channel, v channels.Viewer, favs map[int64]bool) channelJSON {
	name, cat := a.display(ctx, c)
	j := channelJSON{ID: c.ID, Number: c.Number, Name: name, Category: cat, Personal: c.UserID != 0, Auto: c.AutoKey != "",
		Enabled: c.Enabled, CanEdit: channels.CanEdit(c, v), Logo: channels.LogoIndex(c.ID), Favorite: favs[c.ID], LogoURL: logoURL(c)}
	if j.CanEdit {
		r := c.Rules
		j.Rules = &r
	}
	return j
}

// display is c's name and category in the viewer's language; a series'
// own channel takes the series' title as the library's cards show it.
func (a *channelsAPI) display(ctx context.Context, c channels.Channel) (string, string) {
	name, cat := channels.Display(i18n.FromContext(ctx), c)
	if k, v, ok := strings.Cut(c.AutoKey, ":"); ok && k == "series" && !c.Edited && a.cards != nil && a.lib != nil {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if it, err := a.lib.Item(ctx, id); err == nil {
				if t := a.cards.programTranslation(ctx, it); t != nil {
					name = translatedTitle(t, name)
				}
			}
		}
	}
	return name, cat
}

// programs merges a channel's entries into programmes (the parts of a
// movie are one) with their descriptions.
func (a *channelsAPI) programs(ctx context.Context, es []channels.Entry) ([]programJSON, error) {
	ids := make([]int64, len(es))
	for i, e := range es {
		ids[i] = e.ItemID
	}
	info, err := a.svc.Describe(ctx, ids)
	if err != nil {
		return nil, err
	}
	var out []programJSON
	for i := 0; i < len(es); i++ {
		e := es[i]
		end := e.End
		for i+1 < len(es) && es[i+1].ItemID == e.ItemID && (e.FileID == 0 || es[i+1].FileID != e.FileID) && es[i+1].Start.Equal(end) { // its first file again: a repeat
			i++
			end = es[i].End
		}
		x, ok := info[e.ItemID]
		if !ok {
			continue
		}
		p := programJSON{ID: strconv.FormatInt(e.Channel, 10) + "-" + strconv.FormatInt(e.Seq, 10), ItemID: e.ItemID, Kind: x.Kind,
			Year: x.Year, Rating: x.Rating, Start: e.Start, End: end,
			Poster: library.ImageURL(x.ArtItem, "poster", x.ArtPoster), fileID: e.FileID}
		for _, g := range x.Genres {
			p.Genres = append(p.Genres, i18n.Genre(ctx, g))
		}
		if x.External {
			// a title outside the library: its TMDB art through the image cache
			p.External, p.Poster, p.Backdrop = true, tmdbArtURL(x.Poster), tmdbArtURL(x.Backdrop)
		} else if it, err := a.lib.Item(ctx, x.ArtItem); err == nil {
			p.Backdrop = library.ImageURL(it.ID, "backdrop", it.Backdrop)
			a.cards.localizeProgram(ctx, &x, it) // the viewer's language
		}
		p.Title, p.Overview = x.Title, x.Overview
		if x.Kind == library.KindEpisode {
			p.Subtitle = channels.EpisodeLabel(x)
		}
		out = append(out, p)
	}
	return out, nil
}

// withTracks fills the renditions of channel ch's programme on air at now
// and the next one (the first two from now on), labelled in the viewer's
// language: those of the run's track set (or the set it would have now)
// the programme really has.
func (a *channelsAPI) withTracks(ctx context.Context, ch int64, ps []programJSON, now time.Time) {
	lang := i18n.FromContext(ctx)
	n := 0
	for i := range ps {
		p := &ps[i]
		if !p.End.After(now) {
			continue
		}
		if n++; n > 2 {
			return
		}
		audio, subs, ok, err := a.svc.ProgrammeTracks(ctx, ch, p.fileID, lang)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("channels: tracks of file %d: %v", p.fileID, err)
			}
			continue
		}
		if !ok {
			continue // unknown: absent
		}
		p.Audio, p.Subtitles = make([]trackJSON, 0, len(audio)), make([]trackJSON, 0, len(subs))
		for _, l := range audio {
			p.Audio = append(p.Audio, trackJSON{Slot: l.Slot, Name: l.Name, Label: l.Label, Lang: l.Lang})
		}
		for _, l := range subs {
			p.Subtitles = append(p.Subtitles, trackJSON{Slot: l.Slot, Name: l.Name, Label: l.Label, Lang: l.Lang})
		}
	}
}

// GET /api/channels: the channels the profile sees with what is on now
// and next; ?manage=1: the channels it may edit (disabled ones too).
func (a *channelsAPI) list(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	v := viewerOf(u)
	manage := r.URL.Query().Get("manage") == "1"
	var cs []channels.Channel
	var err error
	if manage {
		var all []channels.Channel
		all, err = a.svc.Store.All(ctx)
		for _, c := range all {
			// external channels have their own editor (/api/tv/…)
			if !c.External() && channels.CanEdit(c, v) && (c.UserID == 0 || c.UserID == u.ID) {
				cs = append(cs, c)
			}
		}
	} else {
		cs, err = a.svc.Store.Visible(ctx, v)
	}
	if err != nil {
		internalErr(w, r, "channels", err)
		return
	}
	favs, err := a.svc.Store.Favorites(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "channels", err)
		return
	}
	last, err := a.svc.Store.LastChannel(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "channels", err)
		return
	}
	now := time.Now()
	out := make([]channelJSON, 0, len(cs))
	var lastChannel any // null unless the last channel is still in the list
	for _, c := range cs {
		if c.ID == last && !manage {
			lastChannel = c.ID
		}
		j := a.toJSON(ctx, c, v, favs)
		if !manage {
			es, err := a.svc.Store.Range(ctx, c.ID, now, now.Add(4*time.Hour))
			if err != nil {
				internalErr(w, r, "channels", err)
				return
			}
			ps, err := a.programs(ctx, es)
			if err != nil {
				internalErr(w, r, "channels", err)
				return
			}
			a.withTracks(ctx, c.ID, ps, now)
			if len(ps) > 0 {
				j.Now = &ps[0]
			}
			if len(ps) > 1 {
				j.Next = &ps[1]
			}
		} else {
			j = a.withPicks(ctx, j, c)
		}
		out = append(out, j)
	}
	if !manage {
		xs := sortedExt(a.extFor(r, u, ""))
		progs := a.extProgrammes(ctx, xs, now, now.Add(4*time.Hour))
		for _, x := range xs {
			if x.ChannelID == last {
				lastChannel = x.ChannelID
			}
			j := a.extJSON(r, x, favs)
			if ps := progs[x.ChannelID]; len(ps) > 0 {
				j.Now = &ps[0]
				if len(ps) > 1 {
					j.Next = &ps[1]
				}
			}
			out = append(out, j)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out, "lastChannel": lastChannel, "canManage": u.IsAdmin, "canCreate": !u.IsKid})
}

func (a *channelsAPI) channelParam(w http.ResponseWriter, r *http.Request) (channels.Channel, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return channels.Channel{}, false
	}
	c, err := a.svc.Store.Get(r.Context(), id)
	if errors.Is(err, channels.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return c, false
	}
	if err != nil {
		internalErr(w, r, "channel", err)
		return c, false
	}
	return c, true
}

// GET /api/channels/{id}/guide?hours=: the schedule from now on.
func (a *channelsAPI) guide(w http.ResponseWriter, r *http.Request) {
	c, ok := a.channelParam(w, r)
	if !ok {
		return
	}
	u := userOf(r)
	x, ok := a.sees(r, u, c)
	if !ok {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 48 {
		hours = 24
	}
	now := time.Now()
	if c.External() {
		favs, err := a.svc.Store.Favorites(r.Context(), u.ID)
		if err != nil {
			internalErr(w, r, "guide", err)
			return
		}
		ps := a.extProgrammes(r.Context(), []iptv.ExtChannel{x}, now, now.Add(time.Duration(hours)*time.Hour))[c.ID]
		if ps == nil {
			ps = []programJSON{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"channel": a.extJSON(r, x, favs), "programs": ps})
		return
	}
	es, err := a.svc.Store.Range(r.Context(), c.ID, now, now.Add(time.Duration(hours)*time.Hour))
	if err != nil {
		internalErr(w, r, "guide", err)
		return
	}
	ps, err := a.programs(r.Context(), es)
	if err != nil {
		internalErr(w, r, "guide", err)
		return
	}
	a.withTracks(r.Context(), c.ID, ps, now)
	if ps == nil {
		ps = []programJSON{}
	}
	favs, err := a.svc.Store.Favorites(r.Context(), u.ID)
	if err != nil {
		internalErr(w, r, "guide", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": a.toJSON(r.Context(), c, viewerOf(u), favs), "programs": ps})
}

type optionTitle struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Episodes int    `json:"episodes,omitempty"`
}

type optionProfile struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// GET /api/channels/options: what the editor offers — genres, decades,
// movies and series, and (for admins) the profiles whose library a
// channel may play.
func (a *channelsAPI) options(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	cat := a.svc.Builder.Catalog
	units, err := cat.Units(ctx)
	if err != nil {
		internalErr(w, r, "channel options", err)
		return
	}
	limit := u.RatingLimit()
	var visible []channels.Unit
	for _, x := range units {
		if library.RatingAllowed(limit, library.Item{Kind: x.Kind, Rating: x.Rating}) {
			visible = append(visible, x)
		}
	}
	progs, err := cat.Progs(ctx, visible)
	if err != nil {
		internalErr(w, r, "channel options", err)
		return
	}
	genres := map[string]bool{}
	decades := map[int]bool{}
	titles := []optionTitle{}
	for _, x := range visible {
		if len(progs[x.ID]) == 0 {
			continue
		}
		for _, g := range x.Genres {
			genres[g] = true
		}
		if x.Year >= 1900 {
			decades[x.Year/10*10] = true
		}
		t := optionTitle{ID: x.ID, Kind: x.Kind, Title: x.Title, Year: x.Year}
		if x.Kind == library.KindSeries {
			t.Episodes = len(progs[x.ID])
		}
		titles = append(titles, t)
	}
	sort.Slice(titles, func(i, j int) bool { return strings.ToLower(titles[i].Title) < strings.ToLower(titles[j].Title) })
	type genre struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	gs := []genre{}
	for g := range genres {
		gs = append(gs, genre{Key: g, Name: i18n.Genre(ctx, g)})
	}
	sort.Slice(gs, func(i, j int) bool { return gs[i].Name < gs[j].Name })
	ds := []int{}
	for d := range decades {
		ds = append(ds, d)
	}
	sort.Ints(ds)
	profiles := []optionProfile{}
	if u.IsAdmin && a.users != nil {
		us, err := a.users.List(ctx)
		if err != nil {
			internalErr(w, r, "channel options", err)
			return
		}
		for _, p := range us {
			if !p.IsKid {
				profiles = append(profiles, optionProfile{ID: p.ID, Name: p.Name})
			}
		}
	}
	// extrasAvailable: the editor offers «titles outside the library»
	// (Rules.Extras) only where this server can play them
	// the tags beyond genres and decades (channels_tags.go); aiAvailable:
	// «Опишите канал» has a language model behind it, not the words alone
	countries, languages, keywords, yearMin, yearMax := tagOptions(visible)
	writeJSON(w, http.StatusOK, map[string]any{"genres": gs, "decades": ds, "titles": titles, "profiles": profiles,
		"extrasAvailable": !u.IsKid && a.svc.ExtrasReady(ctx),
		"countries":       countries, "languages": languages, "keywords": keywords, "yearMin": yearMin, "yearMax": yearMax,
		"aiAvailable": a.svc.LLM != nil})
}

type channelBody struct {
	Name     string         `json:"name"`
	Number   int            `json:"number"`
	Category string         `json:"category"`
	Personal bool           `json:"personal"`
	Enabled  *bool          `json:"enabled"`
	Rules    channels.Rules `json:"rules"`
}

func (a *channelsAPI) storeErr(w http.ResponseWriter, r *http.Request, err error) {
	var inv *channels.Invalid
	switch {
	case errors.As(err, &inv):
		writeErr(w, r, http.StatusBadRequest, inv.Msg)
	case errors.Is(err, channels.ErrNumberTaken):
		writeErr(w, r, http.StatusConflict, "Этот номер уже занят другим каналом")
	default:
		internalErr(w, r, "channel", err)
	}
}

// apply puts the body into c for profile u: a non-admin's channel is its
// own and plays its library or the whole one.
func (b channelBody) apply(c *channels.Channel, u auth.User) {
	c.Name, c.Number, c.Category, c.Rules = b.Name, b.Number, b.Category, b.Rules
	if b.Enabled != nil {
		c.Enabled = *b.Enabled
	}
	if !u.IsAdmin || b.Personal {
		c.UserID = u.ID
	} else {
		c.UserID = 0
	}
}

// POST /api/channels
func (a *channelsAPI) create(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.IsKid {
		writeErr(w, r, http.StatusForbidden, "Детский профиль не создаёт каналы")
		return
	}
	var b channelBody
	if !decode(w, r, &b) {
		return
	}
	c := channels.Channel{Enabled: true}
	b.apply(&c, u)
	c, err := a.svc.Store.Create(r.Context(), c)
	if err != nil {
		a.storeErr(w, r, err)
		return
	}
	if err := a.svc.BuildChannel(r.Context(), c); err != nil {
		log.Printf("channels: build %d: %v", c.ID, err)
	}
	writeJSON(w, http.StatusCreated, a.withPicks(r.Context(), a.toJSON(r.Context(), c, viewerOf(u), nil), c))
}

// PUT /api/channels/{id}
func (a *channelsAPI) update(w http.ResponseWriter, r *http.Request) {
	c, ok := a.channelParam(w, r)
	if !ok {
		return
	}
	u := userOf(r)
	if !channels.CanEdit(c, viewerOf(u)) {
		writeErr(w, r, http.StatusForbidden, "Этот канал может менять только администратор")
		return
	}
	if c.External() {
		writeErr(w, r, http.StatusBadRequest, msgExternalEdit)
		return
	}
	var b channelBody
	if !decode(w, r, &b) {
		return
	}
	owner := c.UserID
	b.apply(&c, u)
	if u.IsAdmin && owner != 0 && owner != u.ID {
		c.UserID = owner // another profile's channel stays theirs
	}
	if c.AutoKey != "" {
		c.Edited = true
	}
	c, err := a.svc.Store.Update(r.Context(), c)
	if err != nil {
		a.storeErr(w, r, err)
		return
	}
	if !c.Enabled && a.svc.Live != nil {
		a.svc.Live.Stop(c.ID)
	}
	if c.Enabled {
		if err := a.svc.BuildChannel(r.Context(), c); err != nil {
			log.Printf("channels: build %d: %v", c.ID, err)
		}
	}
	writeJSON(w, http.StatusOK, a.withPicks(r.Context(), a.toJSON(r.Context(), c, viewerOf(u), nil), c))
}

// DELETE /api/channels/{id} (a generated channel is disabled)
func (a *channelsAPI) remove(w http.ResponseWriter, r *http.Request) {
	c, ok := a.channelParam(w, r)
	if !ok {
		return
	}
	if !channels.CanEdit(c, viewerOf(userOf(r))) {
		writeErr(w, r, http.StatusForbidden, "Этот канал может менять только администратор")
		return
	}
	if c.External() {
		writeErr(w, r, http.StatusBadRequest, msgExternalEdit) // the source's editor hides it
		return
	}
	if err := a.svc.Store.Delete(r.Context(), c.ID); err != nil {
		internalErr(w, r, "channel delete", err)
		return
	}
	if err := a.svc.RemoveLogo(r.Context(), c.ID); err != nil { // its uploaded logo goes with it
		log.Printf("channels: logo of %d: %v", c.ID, err)
	}
	if a.svc.Live != nil {
		a.svc.Live.Stop(c.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/channels/generate (admin): the generated channels now.
func (a *channelsAPI) generate(w http.ResponseWriter, r *http.Request) {
	res, err := a.svc.Generator.Sync(r.Context())
	if err != nil {
		internalErr(w, r, "channels generate", err)
		return
	}
	if err := a.svc.Refresh(r.Context()); err != nil {
		log.Printf("channels: refresh: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]int{"created": res.Created, "updated": res.Updated, "disabled": res.Disabled})
}

func (a *channelsAPI) iptvBase(r *http.Request, tok string) string {
	return a.network.Origin(r) + "/iptv/" + tok
}

func (a *channelsAPI) iptvAnswer(w http.ResponseWriter, r *http.Request, tok string) {
	base := a.iptvBase(r, tok)
	writeJSON(w, http.StatusOK, map[string]string{"playlist": base + "/playlist.m3u", "guide": base + "/epg.xml"})
}

// GET /api/channels/iptv: the profile's M3U and XMLTV addresses (the
// token is made on first use).
func (a *channelsAPI) iptvLinks(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	tok, err := a.tokens.Current(r.Context(), u.ID, iptvProto, iptvProto)
	if err == nil && tok == "" {
		tok, err = a.tokens.Issue(r.Context(), u.ID, iptvProto, auth.Device{ID: iptvProto, Name: "IPTV", Client: "IPTV"})
	}
	if err != nil {
		internalErr(w, r, "iptv token", err)
		return
	}
	a.iptvAnswer(w, r, tok)
}

// POST /api/channels/iptv/reset: a new token; the old links stop working.
func (a *channelsAPI) iptvReset(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	tok, err := a.tokens.Issue(r.Context(), u.ID, iptvProto, auth.Device{ID: iptvProto, Name: "IPTV", Client: "IPTV"})
	if err != nil {
		internalErr(w, r, "iptv token", err)
		return
	}
	a.iptvAnswer(w, r, tok)
}

// GET /api/channels/{id}/logo.png
func (a *channelsAPI) webLogo(w http.ResponseWriter, r *http.Request) {
	c, ok := a.channelParam(w, r)
	if !ok {
		return
	}
	if _, ok := a.sees(r, userOf(r), c); !ok && !channels.CanEdit(c, viewerOf(userOf(r))) {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	a.serveLogo(w, r, c)
}

// GET /api/channels/live/{id}/{name}: the web player's stream.
func (a *channelsAPI) webLive(w http.ResponseWriter, r *http.Request) {
	a.live(w, r, userOf(r))
}

// live serves a channel's playlist (index.m3u8) or a segment to u.
func (a *channelsAPI) live(w http.ResponseWriter, r *http.Request, u auth.User) {
	if a.svc.Live == nil {
		writeErr(w, r, http.StatusServiceUnavailable, "Трансляция каналов недоступна")
		return
	}
	c, ok := a.channelParam(w, r)
	if !ok {
		return
	}
	if !channels.VisibleTo(c, viewerOf(u)) {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	ctx := r.Context()
	name := chi.URLParam(r, "name")
	sig := screentime.Signal{Category: screentime.Video, Key: "channel:" + strconv.FormatInt(c.ID, 10)}
	if strings.HasSuffix(name, ".m3u8") {
		if name == "index.m3u8" {
			if ref := a.screen.Allow(ctx, u, screentime.Video); ref != nil {
				w.Header().Set("X-Vault-Error", "screen-time")
				writeErr(w, r, http.StatusForbidden, ref.Message)
				return
			}
			a.screen.Opened(ctx, u, screentime.Video, sig.Key, 0)
		}
		b, err := a.svc.Live.Playlist(ctx, c.ID, name)
		if errors.Is(err, channels.ErrNoSegment) {
			writeErr(w, r, http.StatusNotFound, "Фрагмент не найден")
			return
		}
		if err != nil {
			a.liveErr(w, r, err)
			return
		}
		if key, keyed := auth.ViaMediaKey(ctx); keyed {
			// AVPlayer fetches the renditions and segments without the
			// app's headers: each one carries the key
			b = withSegmentQuery(b, "mk="+url.QueryEscape(key))
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Write(b)
		return
	}
	if strings.HasSuffix(name, ".vtt") {
		b, err := a.svc.Live.VTT(c.ID, name)
		if err != nil {
			writeErr(w, r, http.StatusNotFound, "Фрагмент не найден")
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(b)
		return
	}
	p, dur, err := a.svc.Live.Segment(c.ID, name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Фрагмент не найден")
		return
	}
	if strings.HasPrefix(name, "seg") {
		// screen time counts the picture (each audio rendition would count it again)
		sig.Media = time.Duration(dur * float64(time.Second))
		a.screen.Signal(ctx, u, sig)
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Фрагмент не найден")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Фрагмент не найден")
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// withSegmentQuery adds q to every URI of a live playlist: the segment and
// rendition lines and the URI="…" attributes of the master.
func withSegmentQuery(playlist []byte, q string) []byte {
	lines := strings.Split(string(playlist), "\n")
	for i, l := range lines {
		switch {
		case l != "" && !strings.HasPrefix(l, "#"):
			lines[i] = withQuery(l, q)
		case strings.HasPrefix(l, "#EXT-X-MEDIA:"):
			if a, rest, ok := strings.Cut(l, `URI="`); ok {
				if uri, tail, ok := strings.Cut(rest, `"`); ok {
					lines[i] = a + `URI="` + withQuery(uri, q) + `"` + tail
				}
			}
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func (a *channelsAPI) liveErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, channels.ErrBusy):
		w.Header().Set("Retry-After", "30")
		writeErr(w, r, http.StatusServiceUnavailable, "Сейчас идёт слишком много каналов — попробуйте позже")
	case errors.Is(err, channels.ErrNotReady):
		w.Header().Set("Retry-After", "2")
		writeErr(w, r, http.StatusServiceUnavailable, "Канал запускается — повторите через пару секунд")
	case errors.Is(err, context.Canceled):
	default:
		log.Printf("channels: live: %v", err)
		writeErr(w, r, http.StatusServiceUnavailable, "На канале сейчас нечего показывать")
	}
}

// visibleChannel is the {id} channel when u sees it (else a 404 written).
func (a *channelsAPI) visibleChannel(w http.ResponseWriter, r *http.Request, u auth.User) (channels.Channel, bool) {
	c, ok := a.channelParam(w, r)
	if !ok {
		return c, false
	}
	if _, ok := a.sees(r, u, c); !ok {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return c, false
	}
	return c, true
}

// PUT|DELETE /api/channels/{id}/favorite: star or unstar a channel.
func (a *channelsAPI) favorite(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userOf(r)
		c, ok := a.visibleChannel(w, r, u)
		if !ok {
			return
		}
		if err := a.svc.Store.SetFavorite(r.Context(), u.ID, c.ID, on); err != nil {
			internalErr(w, r, "channel favorite", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PUT /api/channels/last {channelId}: the channel the profile tuned in
// (a TV's «last channel»; never media progress or history).
func (a *channelsAPI) setLast(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var b struct {
		ChannelID int64 `json:"channelId"`
	}
	if !decode(w, r, &b) {
		return
	}
	c, err := a.svc.Store.Get(r.Context(), b.ChannelID)
	if errors.Is(err, channels.ErrNotFound) || (err == nil && !a.seesOK(r, u, c)) {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	if err != nil {
		internalErr(w, r, "last channel", err)
		return
	}
	if err := a.svc.Store.SetLastChannel(r.Context(), u.ID, c.ID); err != nil {
		internalErr(w, r, "last channel", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type channelEPGJSON struct {
	ID       int64         `json:"id"`
	Number   int           `json:"number"`
	Name     string        `json:"name"`
	Category string        `json:"category"`
	Favorite bool          `json:"favorite"`
	Programs []programJSON `json:"programs"`

	LogoURL string `json:"logoUrl,omitempty"` // the uploaded logo (absent: none); an external channel's tvg-logo
	// External: a channel of an external playlist (its stream: the list's).
	External bool `json:"external,omitempty"`
}

// GET /api/channels/epg?from=<RFC3339>&hours=<1…24>: the guide grid —
// every channel the profile sees with the programmes overlapping
// [from, from+hours). from is clamped to now − 12 h … now + 47 h.
func (a *channelsAPI) epgGrid(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	v := viewerOf(u)
	now := time.Now()
	from := now.Add(-time.Hour).Truncate(30 * time.Minute)
	if s := r.URL.Query().Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, "Неверное время начала")
			return
		}
		from = t
	}
	if lo := now.Add(-12 * time.Hour); from.Before(lo) {
		from = lo
	}
	if hi := now.Add(47 * time.Hour); from.After(hi) {
		from = hi
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 {
		hours = 6
	}
	hours = min(hours, 24)
	to := from.Add(time.Duration(hours) * time.Hour)
	cs, err := a.svc.Store.Visible(ctx, v)
	if err != nil {
		internalErr(w, r, "epg", err)
		return
	}
	favs, err := a.svc.Store.Favorites(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "epg", err)
		return
	}
	out := make([]channelEPGJSON, 0, len(cs))
	for _, c := range cs {
		es, err := a.svc.Store.Range(ctx, c.ID, from, to)
		if err != nil {
			internalErr(w, r, "epg", err)
			return
		}
		ps, err := a.programs(ctx, es)
		if err != nil {
			internalErr(w, r, "epg", err)
			return
		}
		a.withTracks(ctx, c.ID, ps, now)
		if ps == nil {
			ps = []programJSON{}
		}
		name, cat := a.display(ctx, c)
		out = append(out, channelEPGJSON{ID: c.ID, Number: c.Number, Name: name, Category: cat, Favorite: favs[c.ID], Programs: ps, LogoURL: logoURL(c)})
	}
	xs := sortedExt(a.extFor(r, u, ""))
	if len(xs) > 0 {
		progs := a.extProgrammes(ctx, xs, from, to)
		for _, x := range xs {
			ps := progs[x.ChannelID]
			if ps == nil {
				ps = []programJSON{}
			}
			c := x.Channel
			out = append(out, channelEPGJSON{ID: c.ID, Number: c.Number, Name: c.Name, Category: c.Category, Favorite: favs[c.ID], Programs: ps,
				LogoURL: extLogoURL(x), External: true})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from.UTC(), "to": to.UTC(), "channels": out})
}

// ---- IPTV players ----

// iptvUser is the profile of the path's token, or an answer already
// written: an unknown token is 404 (as a path that does not exist), a
// passwordless profile outside the home network 403.
func (a *channelsAPI) iptvUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	tok := chi.URLParam(r, "token")
	u, err := a.tokens.LookupProto(r.Context(), tok, iptvProto)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return u, false
	}
	if err != nil {
		internalErr(w, r, "iptv token", err)
		return u, false
	}
	if !u.HasPassword && !a.network.IsTrusted(r) {
		writeErr(w, r, http.StatusForbidden, auth.MsgRemotePasswordless)
		return u, false
	}
	return u, true
}

// GET /iptv/{token}/playlist.m3u
func (a *channelsAPI) m3u(w http.ResponseWriter, r *http.Request) {
	u, ok := a.iptvUser(w, r)
	if !ok {
		return
	}
	cs, err := a.svc.Store.Visible(r.Context(), viewerOf(u))
	if err != nil {
		internalErr(w, r, "m3u", err)
		return
	}
	a.localizeChannels(i18n.WithLanguage(r.Context(), a.guideLanguage(r.Context(), u.ID)), cs)
	tok := chi.URLParam(r, "token")
	ext := a.iptvExternals(r, u, tok, time.Time{}, time.Time{})
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := channels.WriteM3UWith(w, a.iptvBase(r, tok), cs, ext); err != nil {
		log.Printf("channels: m3u: %v", err)
	}
}

// GET /iptv/{token}/epg.xml: now − 12 h … now + 48 h.
func (a *channelsAPI) epg(w http.ResponseWriter, r *http.Request) {
	u, ok := a.iptvUser(w, r)
	if !ok {
		return
	}
	// in the link profile's interface language
	lang := a.guideLanguage(r.Context(), u.ID)
	ctx := i18n.WithLanguage(r.Context(), lang)
	cs, err := a.svc.Store.Visible(ctx, viewerOf(u))
	if err != nil {
		internalErr(w, r, "epg", err)
		return
	}
	a.localizeChannels(ctx, cs)
	now := time.Now()
	g := channels.Guide{Base: a.iptvBase(r, chi.URLParam(r, "token")), Channels: cs, Entries: map[int64][]channels.Entry{}, Lang: lang}
	var ids []int64
	for _, c := range cs {
		es, err := a.svc.Store.Range(ctx, c.ID, now.Add(-12*time.Hour), now.Add(48*time.Hour))
		if err != nil {
			internalErr(w, r, "epg", err)
			return
		}
		g.Entries[c.ID] = es
		for _, e := range es {
			ids = append(ids, e.ItemID)
		}
	}
	if g.Info, err = a.svc.Describe(ctx, ids); err != nil {
		internalErr(w, r, "epg", err)
		return
	}
	a.localizeInfo(ctx, g.Info)
	// external channels: what their guides have, up to a week ahead
	g.External = a.iptvExternals(r, u, chi.URLParam(r, "token"), now.Add(-12*time.Hour), now.Add(iptv.EPGAfter))
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := channels.WriteXMLTV(w, g); err != nil {
		log.Printf("channels: epg: %v", err)
	}
}

// GET /iptv/{token}/live/{id}/{name}
func (a *channelsAPI) iptvLive(w http.ResponseWriter, r *http.Request) {
	u, ok := a.iptvUser(w, r)
	if !ok {
		return
	}
	a.live(w, r, u)
}

// GET /iptv/{token}/logo/{id}.png
func (a *channelsAPI) iptvLogo(w http.ResponseWriter, r *http.Request) {
	u, ok := a.iptvUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(chi.URLParam(r, "name"), ".png"), 10, 64)
	c, gerr := a.svc.Store.Get(r.Context(), id)
	if err == nil && gerr == nil && c.External() {
		_, ok := a.extFor(r, u, chi.URLParam(r, "token"))[c.ID]
		if !ok {
			gerr = channels.ErrNotFound
		}
	} else if err == nil && gerr == nil && !channels.VisibleTo(c, viewerOf(u)) {
		gerr = channels.ErrNotFound
	}
	if err != nil || gerr != nil {
		http.NotFound(w, r)
		return
	}
	a.serveLogo(w, r, c)
}

// GET /iptv/{token}/art/{item}: a programme's poster (the guide's icon),
// 320 px wide.
func (a *channelsAPI) iptvArt(w http.ResponseWriter, r *http.Request) {
	u, ok := a.iptvUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "item"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, err := a.lib.Item(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ok, err := a.lib.Visible(r.Context(), u.RatingLimit(), it); err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	p := posterPath(a.dataDir, it.Poster)
	if p == "" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	q.Set("w", "320")
	r.URL.RawQuery = q.Encode()
	serveImage(w, r, p, imagesDir(a.dataDir), false)
}

// tmdbArtURL is the address of a TMDB image path ("/abc.jpg") in the image
// cache (/api/images/art/{name}: fetched on its first view); "" for
// anything else.
func tmdbArtURL(path string) string {
	name, ok := strings.CutPrefix(path, "/")
	if !ok || !tmdb.ArtFile.MatchString(name) {
		return ""
	}
	return "/api/images/art/" + name
}

func imagesDir(dataDir string) string { return filepath.Join(dataDir, "images") }

// posterPath is the file of a stored poster value ("" when there is none).
func posterPath(dataDir, v string) string {
	p, err := imgcache.Resolve(imagesDir(dataDir), v)
	if err != nil {
		return ""
	}
	return p
}
