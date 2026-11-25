package api

import "net/http"

// Gzip is compressText for the handlers mounted beside the router
// (Subsonic, OPDS, the Jellyfin API): the same types, the same
// Vary: Accept-Encoding. skip says which paths are binary or streamed
// (a stream, a download, a picture): they pass through unwrapped, which
// keeps net/http's sendfile. Range requests and upgrades pass through too.
func Gzip(next http.Handler, skip func(path string) bool) http.Handler {
	z := compressText(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" || skip(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		z.ServeHTTP(w, r)
	})
}
