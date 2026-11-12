package api

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/channels"
)

// «ТВ» round 5: the editor's picks (search, folder browse) and uploaded
// channel logos.

// GET /api/channels/options/search?q=: movies, series and episodes to
// pick, within the editor's age limit.
func (a *channelsAPI) optionsSearch(w http.ResponseWriter, r *http.Request) {
	hits, err := a.svc.Builder.Catalog.Search(r.Context(), r.URL.Query().Get("q"), userOf(r).RatingLimit())
	if err != nil {
		internalErr(w, r, "channel search", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": hits})
}

// GET /api/channels/options/folders?path=: a library folder's sub-folders
// and files ("": the library roots), within the editor's age limit.
func (a *channelsAPI) optionsFolders(w http.ResponseWriter, r *http.Request) {
	f, err := a.svc.Builder.Catalog.Browse(r.Context(), r.URL.Query().Get("path"), userOf(r).RatingLimit())
	switch {
	case errors.Is(err, channels.ErrBadPath):
		writeErr(w, r, http.StatusBadRequest, "Недопустимый путь")
	case errors.Is(err, channels.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Папка не найдена")
	case err != nil:
		internalErr(w, r, "channel folders", err)
	default:
		writeJSON(w, http.StatusOK, f)
	}
}

// editable is the channel of the URL when the viewer may edit it.
func (a *channelsAPI) editable(w http.ResponseWriter, r *http.Request) (channels.Channel, bool) {
	c, ok := a.channelParam(w, r)
	if !ok {
		return c, false
	}
	if !channels.CanEdit(c, viewerOf(userOf(r))) {
		writeErr(w, r, http.StatusForbidden, "Этот канал может менять только администратор")
		return c, false
	}
	return c, true
}

// POST /api/channels/{id}/logo: multipart field "file", PNG, JPEG or
// WebP of at most 2 MB; answers the channel.
func (a *channelsAPI) uploadLogo(w http.ResponseWriter, r *http.Request) {
	c, ok := a.editable(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, channels.MaxLogoBytes+64<<10)
	file, _, err := r.FormFile("file")
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Картинка больше 2 МБ")
		return
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Загрузите картинку PNG, JPEG или WebP")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, channels.MaxLogoBytes+1))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Загрузите картинку PNG, JPEG или WebP")
		return
	}
	if len(data) > channels.MaxLogoBytes {
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Картинка больше 2 МБ")
		return
	}
	out, err := channels.FitLogo(data)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Загрузите картинку PNG, JPEG или WebP")
		return
	}
	if c, err = a.svc.SaveLogo(r.Context(), c, out); err != nil {
		internalErr(w, r, "channel logo", err)
		return
	}
	writeJSON(w, http.StatusOK, a.withPicks(r.Context(), a.toJSON(r.Context(), c, viewerOf(userOf(r)), nil), c))
}

// DELETE /api/channels/{id}/logo: back to the generated tile.
func (a *channelsAPI) deleteLogo(w http.ResponseWriter, r *http.Request) {
	c, ok := a.editable(w, r)
	if !ok {
		return
	}
	if err := a.svc.RemoveLogo(r.Context(), c.ID); err != nil {
		internalErr(w, r, "channel logo", err)
		return
	}
	c.LogoVersion = 0
	writeJSON(w, http.StatusOK, a.withPicks(r.Context(), a.toJSON(r.Context(), c, viewerOf(userOf(r)), nil), c))
}

// logoURL is the web app's address of c's uploaded logo ("": none).
func logoURL(c channels.Channel) string {
	if c.LogoVersion == 0 {
		return ""
	}
	return "/api/channels/" + strconv.FormatInt(c.ID, 10) + "/logo.png" + channels.LogoQuery(c)
}

// serveLogo answers c's logo: the uploaded one, else the generated tile.
// The URL of an upload carries its version: that one is kept for good,
// any other (none, an old version) is checked every time.
func (a *channelsAPI) serveLogo(w http.ResponseWriter, r *http.Request, c channels.Channel) {
	b, uploaded, err := a.svc.LogoPNG(c)
	if err != nil {
		internalErr(w, r, "channel logo", err)
		return
	}
	v := r.URL.Query().Get("v")
	w.Header().Set("Content-Type", "image/png")
	switch {
	case uploaded && v == strconv.FormatInt(c.LogoVersion, 10):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	case uploaded || v != "":
		w.Header().Set("Cache-Control", "no-cache")
	default:
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	w.Write(b)
}

// withPicks describes j's picks for its editor (the editor's list and
// the answers to its writes; not the viewers' list).
func (a *channelsAPI) withPicks(ctx context.Context, j channelJSON, c channels.Channel) channelJSON {
	if j.Rules == nil || len(c.Rules.Picks) == 0 {
		return j
	}
	info, err := a.svc.Builder.Catalog.DescribePicks(ctx, c.Rules)
	if err != nil {
		log.Printf("channels: picks of %d: %v", c.ID, err)
	}
	j.PickInfo = info
	return j
}
