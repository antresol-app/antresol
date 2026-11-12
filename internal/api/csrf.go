package api

import (
	"mime"
	"net/http"
	"strings"
)

// csrf guards every state-changing request. The session cookie is
// SameSite=Lax, which does not stop requests from sibling subdomains (they
// are "same-site"), so:
//   - Sec-Fetch-Site must be same-origin/none, or, from browsers that do not
//     send it, Origin must match Host (http.CrossOriginProtection);
//   - a request with a body must be application/json, which a cross-origin
//     page can only send after a CORS preflight that we never answer.
//
// Safe methods (GET, HEAD, OPTIONS) pass through, and so do the links of
// notification letters (notifyLinkPrefix): they use no cookie — a signed
// token in the address is the whole authority — and a mail app's
// one-click unsubscribe is a cross-site form POST by design (RFC 8058).
func csrf(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, r, http.StatusForbidden, "Запрос с другого сайта отклонён")
	}))
	guarded := csrfGuarded(next, cop)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, notifyLinkPrefix) {
			r.Header.Del("Cookie") // the token alone decides
			next.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

func csrfGuarded(next http.Handler, cop *http.CrossOriginProtection) http.Handler {
	return cop.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.ContentLength != 0 || r.Header.Get("Content-Type") != "" {
				mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || (mt != "application/json" && !fileUpload(r, mt)) {
					writeErr(w, r, http.StatusForbidden, "Запрос с другого сайта отклонён")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	}))
}

// fileUpload: the «Файл» import (POST /api/import/files), «Добавить книги»
// (POST /api/books/upload) and a channel's logo (POST /api/channels/{id}/logo)
// are multipart, a type a cross-origin form may send without a preflight, so
// they must also carry X-Vault-Upload, a header only a same-origin script can
// set.
func fileUpload(r *http.Request, mt string) bool {
	return mt == "multipart/form-data" && r.Method == http.MethodPost &&
		(r.URL.Path == "/api/import/files" || r.URL.Path == "/api/books/upload" || channelLogoPath(r.URL.Path)) &&
		r.Header.Get("X-Vault-Upload") != ""
}

// channelLogoPath: /api/channels/{id}/logo.
func channelLogoPath(p string) bool {
	id, ok := strings.CutPrefix(p, "/api/channels/")
	if !ok {
		return false
	}
	if id, ok = strings.CutSuffix(id, "/logo"); !ok || id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
