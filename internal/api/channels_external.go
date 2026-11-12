package api

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/iptv"
	"github.com/go-chi/chi/v5"
)

// «ТВ»: external channels (internal/iptv) in the viewer's list, guide,
// grid and IPTV links. A viewer sees those of its set (its IPTV link's,
// its app device's, its profile's default, else «all my channels»), never
// through the own channels' visibility; the stream is the provider's URL
// unless the source hides provider addresses (then a redirect of
// Antresol). Watching them writes nothing but favourites and the last
// channel, as with own channels.

// streamJSON: how a client plays an external channel. URL is the
// provider's (absolute), or Antresol's redirect (a path) when the source
// hides provider addresses. Unsupported: platforms that cannot play it as
// given ("web": headers a browser cannot send, or http from an https page).
type streamJSON struct {
	URL         string         `json:"url"`
	UserAgent   string         `json:"userAgent,omitempty"`
	Referrer    string         `json:"referrer,omitempty"`
	Fallbacks   []fallbackJSON `json:"fallbacks"`
	Unsupported []string       `json:"unsupported"`
}

type fallbackJSON struct {
	URL         string   `json:"url"`
	UserAgent   string   `json:"userAgent,omitempty"`
	Referrer    string   `json:"referrer,omitempty"`
	Unsupported []string `json:"unsupported"`
}

// extFor: the external channels u sees in the request's set (token: an
// IPTV link's; "": the session's device).
func (a *channelsAPI) extFor(r *http.Request, u auth.User, token string) map[int64]iptv.ExtChannel {
	if a.ext == nil {
		return nil
	}
	ctx := r.Context()
	dev := ""
	if token == "" {
		if s, ok := auth.SessionFrom(ctx); ok {
			dev = s.Device.ID
		}
	}
	m, err := a.ext.Visible(ctx, viewerOf(u), a.ext.SetFor(ctx, u.ID, dev, token))
	if err != nil {
		log.Printf("channels: external: %v", err)
		return nil
	}
	return m
}

// sees: u sees channel c (an own channel by its rules, an external one in
// its set).
func (a *channelsAPI) sees(r *http.Request, u auth.User, c channels.Channel) (iptv.ExtChannel, bool) {
	if !c.External() {
		return iptv.ExtChannel{}, channels.VisibleTo(c, viewerOf(u))
	}
	x, ok := a.extFor(r, u, "")[c.ID]
	return x, ok
}

// sortedExt: the channels of m by number.
func sortedExt(m map[int64]iptv.ExtChannel) []iptv.ExtChannel {
	out := make([]iptv.ExtChannel, 0, len(m))
	for _, x := range m {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel.Number < out[j].Channel.Number })
	return out
}

func unsupportedOf(r *http.Request, network *auth.Network, s iptv.Stream) []string {
	out := []string{}
	https := strings.HasPrefix(network.Origin(r), "https:")
	if s.UserAgent != "" || s.Referrer != "" || (https && strings.HasPrefix(strings.ToLower(s.URL), "http:") && !s.HideURL) {
		out = append(out, "web")
	}
	return out
}

// extStreamPath is the app's redirect to stream n of an external channel.
func extStreamPath(id int64, n int) string {
	return "/api/channels/live/ext/" + strconv.FormatInt(id, 10) + "/" + strconv.Itoa(n)
}

func (a *channelsAPI) streamOf(r *http.Request, x iptv.ExtChannel) *streamJSON {
	if len(x.Streams) == 0 {
		return nil
	}
	url := func(i int, s iptv.Stream) string {
		if s.HideURL {
			return extStreamPath(x.ChannelID, i)
		}
		return s.URL
	}
	p := x.Streams[0]
	j := &streamJSON{URL: url(0, p), UserAgent: p.UserAgent, Referrer: p.Referrer, Fallbacks: []fallbackJSON{}, Unsupported: unsupportedOf(r, a.network, p)}
	for i, s := range x.Streams[1:] {
		j.Fallbacks = append(j.Fallbacks, fallbackJSON{URL: url(i+1, s), UserAgent: s.UserAgent, Referrer: s.Referrer, Unsupported: unsupportedOf(r, a.network, s)})
	}
	return j
}

// extJSON is an external channel of the list.
func (a *channelsAPI) extJSON(r *http.Request, x iptv.ExtChannel, favs map[int64]bool) channelJSON {
	c := x.Channel
	j := channelJSON{ID: c.ID, Number: c.Number, Name: c.Name, Category: c.Category, Enabled: c.Enabled,
		Logo: channels.LogoIndex(c.ID), Favorite: favs[c.ID], LogoURL: extLogoURL(x), External: true, Stream: a.streamOf(r, x)}
	return j
}

// extLogoURL: the uploaded logo, else the playlist's tvg-logo, else none.
func extLogoURL(x iptv.ExtChannel) string {
	if u := logoURL(x.Channel); u != "" {
		return u
	}
	return x.Logo
}

// extProgrammes: the programmes of chans overlapping [from, to), as the
// apps get them.
func (a *channelsAPI) extProgrammes(ctx context.Context, chans []iptv.ExtChannel, from, to time.Time) map[int64][]programJSON {
	keys := map[int64][]iptv.EPGKey{}
	for _, x := range chans {
		if len(x.EPGKeys) > 0 {
			keys[x.ChannelID] = x.EPGKeys
		}
	}
	out := map[int64][]programJSON{}
	if len(keys) == 0 {
		return out
	}
	ps, err := a.ext.Programmes(ctx, keys, from, to)
	if err != nil {
		log.Printf("channels: external programmes: %v", err)
		return out
	}
	for ch, list := range ps {
		for _, p := range list {
			out[ch] = append(out[ch], extProgramJSON(p))
		}
	}
	return out
}

func extProgramJSON(p iptv.Programme) programJSON {
	j := programJSON{ID: "x" + strconv.FormatInt(p.EPGURLID, 10) + "-" + strconv.FormatInt(p.Start.UnixMilli(), 10), Kind: "external",
		Title: p.Title, Subtitle: p.SubTitle, Overview: p.Desc, Start: p.Start, End: p.End, Poster: p.Icon}
	if p.Category != "" {
		j.Genres = []string{p.Category}
	}
	return j
}

// iptvExternals: the external channels of an IPTV link with their stream
// lines (from, to: the programmes for epg.xml; zero: none).
func (a *channelsAPI) iptvExternals(r *http.Request, u auth.User, tok string, from, to time.Time) []channels.External {
	m := a.extFor(r, u, tok)
	xs := sortedExt(m)
	var progs map[int64][]iptv.Programme
	if !from.IsZero() {
		keys := map[int64][]iptv.EPGKey{}
		for _, x := range xs {
			keys[x.ChannelID] = x.EPGKeys
		}
		var err error
		if progs, err = a.ext.Programmes(r.Context(), keys, from, to); err != nil {
			log.Printf("channels: external programmes: %v", err)
		}
	}
	base := a.iptvBase(r, tok)
	out := make([]channels.External, 0, len(xs))
	for _, x := range xs {
		if len(x.Streams) == 0 {
			continue
		}
		s := x.Streams[0]
		e := channels.External{Channel: x.Channel, TvgID: x.TvgID, Logo: x.Logo, URL: s.URL, UserAgent: s.UserAgent, Referrer: s.Referrer}
		if s.HideURL {
			e.URL = base + "/ext/" + strconv.FormatInt(x.ChannelID, 10)
		}
		for _, p := range progs[x.ChannelID] {
			e.Programmes = append(e.Programmes, channels.ExternalProgramme{Start: p.Start, End: p.End, Title: p.Title, SubTitle: p.SubTitle,
				Desc: p.Desc, Category: p.Category, Icon: p.Icon})
		}
		out = append(out, e)
	}
	return out
}

// redirectStream answers a 302 to stream n of external channel {id} of
// m (404 when the viewer's set does not have it).
func redirectStream(w http.ResponseWriter, r *http.Request, m map[int64]iptv.ExtChannel, n int) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	x, ok := m[id]
	if err != nil || !ok || n < 0 || n >= len(x.Streams) {
		writeErr(w, r, http.StatusNotFound, "Канал не найден")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, x.Streams[n].URL, http.StatusFound)
}

// GET /api/channels/live/ext/{id}/{n}: an app's stream n of an external
// channel whose source hides provider addresses (302).
func (a *channelsAPI) extStream(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "n"))
	if err != nil {
		n = -1
	}
	redirectStream(w, r, a.extFor(r, userOf(r), ""), n)
}

// GET /iptv/{token}/ext/{id}: an IPTV player's stream of an external
// channel whose source hides provider addresses (302 to the primary).
func (a *channelsAPI) iptvExt(w http.ResponseWriter, r *http.Request) {
	u, ok := a.iptvUser(w, r)
	if !ok {
		return
	}
	m := a.extFor(r, u, chi.URLParam(r, "token"))
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if _, ok := m[id]; !ok {
		writeErr(w, r, http.StatusForbidden, "Канал не найден")
		return
	}
	redirectStream(w, r, m, 0)
}

func (a *channelsAPI) seesOK(r *http.Request, u auth.User, c channels.Channel) bool {
	_, ok := a.sees(r, u, c)
	return ok
}
