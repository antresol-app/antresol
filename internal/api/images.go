package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/imgcache"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

func mountImages(r chi.Router, h *lib) {
	r.Get("/api/images/{name}", h.cachedImage)
	r.Get("/api/images/local/{id}/{slot}", h.localImage)
	r.Get("/api/images/cast/{name}", h.castImage)
	r.Get("/api/images/art/{name}", h.artImage)
	r.Get("/api/images/frame/{itemId}", h.frameImage)
}

// castImage serves a cast member's TMDB photo from the image cache; one
// not downloaded yet (a title enriched before photos were kept, a cast in
// another language) is fetched once and kept. Without the network only
// the kept ones show.
func (h *lib) castImage(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !tmdb.ProfileFile.MatchString(name) {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	serveTMDBImage(w, r, h.CastPhotos, name, "cast photo")
}

// artImage serves a title's TMDB poster or logo in a language the library
// does not store (Card.Poster and Logo from tmdb.Translation) the same
// way: fetched on its first view, then from the image cache.
func (h *lib) artImage(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !tmdb.ArtFile.MatchString(name) {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	serveTMDBImage(w, r, h.Artwork, name, "artwork")
}

// serveTMDBImage serves the TMDB image /name (a checked file name, never
// a path) from p's cache, downloading it once.
func serveTMDBImage(w http.ResponseWriter, r *http.Request, p *tmdb.Photos, name, what string) {
	if p == nil {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	file, ok := p.Cached("/" + name)
	if !ok {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		var err error
		file, err = p.Fetch(ctx, "/"+name)
		cancel()
		if err != nil {
			log.Printf("api: %s %s: %v", what, name, err)
			writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
			return
		}
	}
	serveImage(w, r, filepath.Join(p.Images.Dir, file), p.Images.Dir, false)
}

// imageHeaders: a week in the client's cache; a TMDB cache file (long:
// named by the sha1 of its URL, which rarely gets other content) 30 days,
// then revalidated (If-Modified-Since).
func imageHeaders(w http.ResponseWriter, long bool) {
	cc := "private, max-age=604800"
	if long {
		cc = "private, max-age=2592000"
	}
	w.Header().Set("Cache-Control", cc)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// SVG logos must not run scripts in our origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
}

// sized is the copy of the image at p no wider than ?w= (kept in
// cacheDir/resized), or p itself: no width or one that is not a step of
// imgcache.Widths (so no URL can fill the disk with copies), an image
// already that small, a format it does not shrink, a failure.
func sized(r *http.Request, p, cacheDir string) string {
	wantW, err := strconv.Atoi(r.URL.Query().Get("w"))
	if err != nil || !slices.Contains(imgcache.Widths, wantW) || cacheDir == "" {
		return p
	}
	out, err := imgcache.Resized(cacheDir, p, wantW, 0)
	if err != nil {
		log.Printf("api: shrink %s to %d: %v", filepath.Base(p), wantW, err)
		return p
	}
	return out
}

// serveImage serves the image at p, shrunk to ?w= (copies kept in
// cacheDir); long: a TMDB cache file (imageHeaders).
func serveImage(w http.ResponseWriter, r *http.Request, p, cacheDir string, long bool) {
	fh, err := os.Open(sized(r, p, cacheDir))
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || st.IsDir() {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	imageHeaders(w, long)
	http.ServeContent(w, r, filepath.Base(fh.Name()), st.ModTime(), fh)
}

// cachedImage serves a TMDB cache file (<sha1>.<ext>) from DataDir/images.
func (h *lib) cachedImage(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !cacheName.MatchString(name) { // a URL names cache files only, never a path
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	p, err := imgcache.Resolve(filepath.Join(h.DataDir, "images"), name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	serveImage(w, r, p, filepath.Join(h.DataDir, "images"), true)
}

// localImage serves artwork found next to the media ("file:" values) by item id.
func (h *lib) localImage(w http.ResponseWriter, r *http.Request) {
	it, ok := h.itemParam(w, r, "id")
	if !ok {
		return
	}
	var v string
	switch chi.URLParam(r, "slot") {
	case "poster":
		v = it.Poster
	case "backdrop":
		v = it.Backdrop
	case "logo":
		v = metaOf(it).Logo
	}
	p, err := imgcache.Resolve("", v) // "file:" values only (no cache directory)
	if err != nil || !strings.HasPrefix(v, "file:") {
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	serveImage(w, r, p, filepath.Join(h.DataDir, "images"), false)
}
