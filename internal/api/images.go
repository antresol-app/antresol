package api

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"

	"github.com/antresol-app/antresol/internal/imgcache"
	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

func mountImages(r chi.Router, h *lib) {
	r.Get("/api/images/{name}", h.cachedImage)
	r.Get("/api/images/local/{id}/{slot}", h.localImage)
	r.Get("/api/images/cast/{name}", h.castImage)
	r.Get("/api/images/art/{name}", h.artImage)
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
	serveImage(w, r, filepath.Join(p.Images.Dir, file), p.Images.Dir)
}

func imageHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// SVG logos must not run scripts in our origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
}

// imageWidths are the widths ?w= may ask for: the web app's srcset steps
// (a card's poster at 1×, 2× and 3×, a backdrop across a phone or a
// window). Anything else is the original, so no URL can fill the disk
// with copies.
var imageWidths = map[int]bool{160: true, 240: true, 320: true, 480: true, 640: true, 960: true, 1280: true, 1920: true}

// resizeSlots bounds the decodes at once: a cold page asks for thirty
// images together, and a 2 MB JPEG takes ~100 ms of a core to shrink.
var resizeSlots = make(chan struct{}, max(2, runtime.NumCPU()/2))

// resizing: one shrink per image and width, however many ask together.
var resizing singleflight.Group

// sized is the copy of the image at p no wider than ?w= (kept in
// cacheDir/resized), or p itself: no or an unknown width, an image
// already that small, a format it does not shrink, a failure.
func sized(r *http.Request, p, cacheDir string) string {
	wantW, err := strconv.Atoi(r.URL.Query().Get("w"))
	if err != nil || !imageWidths[wantW] || cacheDir == "" {
		return p
	}
	out, err, _ := resizing.Do(p+"|"+strconv.Itoa(wantW), func() (any, error) {
		resizeSlots <- struct{}{}
		defer func() { <-resizeSlots }()
		return imgcache.Resized(cacheDir, p, wantW, 0)
	})
	if err != nil {
		log.Printf("api: shrink %s to %d: %v", filepath.Base(p), wantW, err)
		return p
	}
	return out.(string)
}

// serveImage serves the image at p, shrunk to ?w= (copies kept in
// cacheDir).
func serveImage(w http.ResponseWriter, r *http.Request, p, cacheDir string) {
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
	imageHeaders(w)
	http.ServeContent(w, r, filepath.Base(fh.Name()), st.ModTime(), fh)
}

// cachedImage serves a TMDB cache file (<sha1>.<ext>) from DataDir/images.
func (h *lib) cachedImage(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	p, err := imgcache.Resolve(filepath.Join(h.DataDir, "images"), name)
	if err != nil || !cacheName.MatchString(name) { // a URL names cache files only, never a path
		writeErr(w, r, http.StatusNotFound, "Картинка не найдена")
		return
	}
	serveImage(w, r, p, filepath.Join(h.DataDir, "images"))
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
	serveImage(w, r, p, filepath.Join(h.DataDir, "images"))
}
