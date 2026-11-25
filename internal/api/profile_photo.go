package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
)

// A profile's photo (auth.User.Photo is its address): an admin sets or
// removes it in «Профили → Изменить»; anyone, signed in or not, reads it,
// as they read the profile list the sign-in screen draws it from.
//
//	GET    /api/profiles/{id}/photo   the JPEG (404: none)
//	POST   /api/users/{id}/photo      multipart «file» (X-Vault-Upload), admin; answers the profile
//	DELETE /api/users/{id}/photo      back to the letter, admin; answers the profile
//	POST   /api/me/photo, DELETE /api/me/photo   the same for the signed-in profile itself,
//	                                  as /api/me/app-password is its own; a kid's is the
//	                                  parents' (403), as «Скрывать прогресс» is

// servePhoto: GET /api/profiles/{id}/photo. The address carries the upload's
// version (?v=): a match is kept for good, a bare or stale one is asked again.
func (a *authAPI) servePhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, version, err := a.users.Photo(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		internalErr(w, r, "profile photo", err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == strconv.FormatInt(version, 10) {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	w.Write(data)
}

// uploadPhoto: POST /api/users/{id}/photo.
func (a *authAPI) uploadPhoto(w http.ResponseWriter, r *http.Request) {
	if id, ok := idParam(w, r, "id"); ok {
		if u, ok := a.savePhoto(w, r, id); ok {
			writeJSON(w, http.StatusOK, u)
		}
	}
}

// uploadMyPhoto: POST /api/me/photo; answers as /api/me does (the web app
// puts the answer in its session). A kid's photo is the parents' choice
// (an admin sets it in «Профили»): 403.
func (a *authAPI) uploadMyPhoto(w http.ResponseWriter, r *http.Request) {
	if !kidLocked(w, r) {
		if fresh, ok := a.savePhoto(w, r, userOf(r).ID); ok {
			writeJSON(w, http.StatusOK, a.meOf(r, fresh))
		}
	}
}

// savePhoto reads the multipart upload and keeps it as profile id's photo;
// the profile as it is now, or false with the answer already written.
func (a *authAPI) savePhoto(w http.ResponseWriter, r *http.Request, id int64) (auth.User, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, auth.MaxPhotoBytes+64<<10)
	file, _, err := r.FormFile("file")
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Фото больше 8 МБ")
		return auth.User{}, false
	}
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Загрузите картинку PNG, JPEG или WebP")
		return auth.User{}, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, auth.MaxPhotoBytes+1))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Загрузите картинку PNG, JPEG или WebP")
		return auth.User{}, false
	}
	if len(data) > auth.MaxPhotoBytes {
		writeErr(w, r, http.StatusRequestEntityTooLarge, "Фото больше 8 МБ")
		return auth.User{}, false
	}
	out, err := auth.FitPhoto(data)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, "Загрузите картинку PNG, JPEG или WebP")
		return auth.User{}, false
	}
	u, err := a.users.SetPhoto(r.Context(), id, out)
	if err != nil {
		storeErr(w, r, err)
		return auth.User{}, false
	}
	return u, true
}

// deletePhoto: DELETE /api/users/{id}/photo.
func (a *authAPI) deletePhoto(w http.ResponseWriter, r *http.Request) {
	if id, ok := idParam(w, r, "id"); ok {
		if u, ok := a.dropPhoto(w, r, id); ok {
			writeJSON(w, http.StatusOK, u)
		}
	}
}

// deleteMyPhoto: DELETE /api/me/photo.
func (a *authAPI) deleteMyPhoto(w http.ResponseWriter, r *http.Request) {
	if !kidLocked(w, r) {
		if fresh, ok := a.dropPhoto(w, r, userOf(r).ID); ok {
			writeJSON(w, http.StatusOK, a.meOf(r, fresh))
		}
	}
}

// dropPhoto forgets profile id's photo; the profile as it is now, or false
// with the answer already written.
func (a *authAPI) dropPhoto(w http.ResponseWriter, r *http.Request, id int64) (auth.User, bool) {
	if _, err := a.users.Get(r.Context(), id); err != nil {
		storeErr(w, r, err)
		return auth.User{}, false
	}
	u, err := a.users.RemovePhoto(r.Context(), id)
	if err != nil {
		storeErr(w, r, err)
		return auth.User{}, false
	}
	return u, true
}

// userPhotoPath: /api/users/{id}/photo or /api/me/photo.
func userPhotoPath(p string) bool {
	if p == "/api/me/photo" {
		return true
	}
	id, ok := strings.CutPrefix(p, "/api/users/")
	if !ok {
		return false
	}
	if id, ok = strings.CutSuffix(id, "/photo"); !ok || id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
