package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/antresol-app/antresol/internal/metadata"
	"github.com/go-chi/chi/v5"
)

// The core API's metadata pass-through (metadata): an addon asks TMDB,
// MusicBrainz, Open Library or Wikidata through the server's own clients,
// with their keys, User-Agents, limits and retries, and never sees a key.

const (
	msgAddonSourceOff    = "Источник выключен"
	msgAddonSourceFailed = "Источник не ответил"
)

// RawSource is a metadata client the pass-through calls (tmdb.Client,
// musicbrainz.Client, openlibrary.Client, wikidata.Client): a GET of path
// (percent-encoded, below the client's base URL) with q and the client's
// own credentials, answering with any status as it is.
type RawSource interface {
	Raw(ctx context.Context, path string, q url.Values) (status int, contentType string, body []byte, err error)
}

// AddonMetadata are the sources of the pass-through; a nil one is off
// (404). Never set a field to a typed nil pointer.
type AddonMetadata struct {
	TMDB, MusicBrainz, OpenLibrary, Wikidata RawSource
}

// source is the pass-through source named in the path; known is false for
// a name the core has none by.
func (m AddonMetadata) source(name string) (src RawSource, known bool) {
	switch name {
	case "tmdb":
		return m.TMDB, true
	case "musicbrainz":
		return m.MusicBrainz, true
	case "openlibrary":
		return m.OpenLibrary, true
	case "wikidata":
		return m.Wikidata, true
	}
	return nil, false
}

func mountAddonCoreMetadata(r chi.Router, a *addonsAPI) {
	r.Get("/metadata/{source}/*", a.coreMetadata)
	r.Head("/metadata/{source}/*", a.coreMetadata)
	r.Get("/metadata/{source}", a.coreMetadata)
	r.Head("/metadata/{source}", a.coreMetadata)
}

// coreMetadata: GET|HEAD /metadata/{source}/<path>?<query>. The addon's
// token has been read (requireToken); its Authorization, cookies and any
// api_key of the query never reach the provider. The path is checked
// undecoded (metadata.SafeRawPath): no «..» in any encoding, no backslash,
// no «//host». Only the upstream status, Content-Type and body (at most
// metadata.MaxRaw) come back; TMDB's images are not proxied (their host is
// not the API's).
func (a *addonsAPI) coreMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.callingAddon(w, r, "metadata"); !ok {
		return
	}
	r.Header.Del("Authorization")
	r.Header.Del("Cookie")
	name := chi.URLParam(r, "source")
	src, known := a.meta.source(name)
	if !known || src == nil {
		writeErr(w, r, http.StatusNotFound, msgAddonSourceOff)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/addon-core/v1/metadata/"+name+"/")
	if !ok || rest == "" || !metadata.SafeRawPath("/"+rest) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadPath)
		return
	}
	q := r.URL.Query()
	for k := range q {
		if strings.EqualFold(k, "api_key") {
			delete(q, k)
		}
	}
	status, ctype, body, err := src.Raw(r.Context(), "/"+rest, q)
	if err != nil {
		if r.Context().Err() == nil {
			// the service and path only: a transport error's URL carries
			// the addon's query (and, for TMDB, would carry the key)
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			log.Printf("api: addon metadata %s /%s: %v", name, rest, err)
		}
		writeErr(w, r, http.StatusBadGateway, msgAddonSourceFailed)
		return
	}
	h := w.Header()
	if ctype != "" {
		h.Set("Content-Type", ctype)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}
