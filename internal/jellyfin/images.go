package jellyfin

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/imgcache"
	"github.com/antresol-app/antresol/internal/library"
)

// itemImage serves GET|HEAD /Items/{id}/Images/{type}[/{idx}]: Primary is
// the poster (an episode's own still), Backdrop and Thumb the backdrop,
// Logo the logo; anything else, an item without that image or one the
// profile may not see is 404. maxWidth/maxHeight (fill*, width, height)
// give a resized copy.
//
// The token is optional: Swiftfin loads images with no token at all
// (jellyfin-sdk-swift client.url(with:) adds no ApiKey, its image session
// no headers), as Jellyfin allows. With a token the profile's rating
// limit applies (library.Repo.Visible); without one the request must
// carry the image's tag, which only a BaseItemDto the client was allowed
// to see gives it. A bad token is 401, as everywhere.
func (s *Server) itemImage(w http.ResponseWriter, r *http.Request) {
	if ParseAuth(r).Token != "" {
		s.requireToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := userOf(r)
			s.serveItemImage(w, r, &u)
		})).ServeHTTP(w, r)
		return
	}
	s.serveItemImage(w, r, nil)
}

func (s *Server) serveItemImage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	notFound := func() { w.WriteHeader(http.StatusNotFound) }
	k, id, ok := ParseID(chi.URLParam(r, "id"))
	if !ok || k != kindItem {
		notFound()
		return
	}
	c := s.conv(r.Context(), auth.User{}, nil)
	if u != nil {
		c = s.conv(r.Context(), *u, nil)
	}
	it, err := c.item(id)
	if errors.Is(err, library.ErrNotFound) || err == nil && jfTypes[it.Kind] == "" {
		notFound()
		return
	}
	if err != nil {
		s.internal(w, r, "image", err)
		return
	}
	var ref string
	switch chi.URLParam(r, "type") {
	case "primary":
		ref = it.Poster
	case "backdrop", "thumb":
		ref = it.Backdrop
	case "logo":
		ref = metaOf(it).Logo
	}
	p, err := imgcache.Resolve(s.ImageDir, ref)
	if err != nil {
		notFound()
		return
	}
	if u != nil {
		ok, err := c.visible(it)
		if err != nil {
			s.internal(w, r, "image", err)
			return
		}
		if !ok {
			notFound()
			return
		}
	} else if qv(r, "tag") != imageTag(ref) {
		notFound()
		return
	}
	maxW, maxH := qInt(r, "maxWidth", 0), qInt(r, "maxHeight", 0)
	for _, k := range []string{"fillWidth", "width"} {
		if maxW <= 0 {
			maxW = qInt(r, k, 0)
		}
	}
	for _, k := range []string{"fillHeight", "height"} {
		if maxH <= 0 {
			maxH = qInt(r, k, 0)
		}
	}
	if maxW > 0 || maxH > 0 {
		if p, err = imgcache.Resized(s.ImageDir, p, min(max(maxW, 0), 4096), min(max(maxH, 0), 4096)); err != nil {
			notFound()
			return
		}
	}
	fh, err := os.Open(p)
	if err != nil {
		notFound()
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil || !st.Mode().IsRegular() {
		notFound()
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "public, max-age=31536000") // the tag is in the URL
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox") // SVG logos
	http.ServeContent(w, r, strings.ToLower(filepath.Base(p)), st.ModTime(), fh)
}

// noUserImage: Vault avatars are gradients (grad-1..8), not pictures.
func (s *Server) noUserImage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
