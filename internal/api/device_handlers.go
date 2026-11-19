package api

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/go-chi/chi/v5"
)

// deviceIDRe: the id the native app keeps for this install.
var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// clip trims s and cuts it to n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = strings.TrimSpace(string(r[:n]))
	}
	return s
}

// deviceToken signs the native app in: the same password checks as the web
// login, but the answer carries a device token instead of a cookie.
func (a *authAPI) deviceToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID   int64  `json:"userId"`
		Password string `json:"password"`
		Device   struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Model   string `json:"model"`
			Version string `json:"version"`
		} `json:"device"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !deviceIDRe.MatchString(in.Device.ID) {
		writeErr(w, r, http.StatusBadRequest, "Не указано устройство")
		return
	}
	u, ok := a.checkLogin(w, r, in.UserID, in.Password)
	if !ok {
		return
	}
	tok, err := a.sessions.CreateDevice(r.Context(), u.ID, auth.DeviceInfo{
		ID: in.Device.ID, Name: clip(in.Device.Name, 100), Model: clip(in.Device.Model, 64), Version: clip(in.Device.Version, 64),
	})
	if err != nil {
		log.Printf("auth: device token for profile %d: %v", u.ID, err)
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Token string `json:"token"`
		User  me     `json:"user"`
	}{tok, a.meOf(r, u)})
}

// mediaKeyTTL is how long an app's media key works: a long film with
// pauses, or a night of AirPlay, without asking again.
const mediaKeyTTL = 12 * time.Hour

// webMediaKeyTTL: a browser's key is for one AirPlay sitting. The player
// asks for a new one with at least an hour left on every session it starts
// on a receiver (a seek, the next part, a restart after a refused segment),
// so a key that runs out mid-film costs one restart at the same place.
const webMediaKeyTTL = 4 * time.Hour

// mountDevices: «Устройства», the native apps signed in to the caller's
// profile, and media keys (inside the session guard; keys nil: no media
// keys) for the app's player and for AirPlay from the web player: the
// receiver fetches the stream itself, without the browser's cookie. A key
// is asked for with the session itself (a cookie or a Bearer token, never
// another key) and ends with that session (sign-out, a revoked device).
func mountDevices(r chi.Router, sessions *auth.Sessions, keys *auth.MediaKeys) {
	if keys != nil {
		r.Post("/api/auth/media-key", func(w http.ResponseWriter, r *http.Request) {
			sess, _ := auth.SessionFrom(r.Context())
			tok := auth.TokenFrom(r.Context())
			if tok == "" {
				writeErr(w, r, http.StatusUnauthorized, "Нужно войти")
				return
			}
			ttl := mediaKeyTTL
			if sess.Kind == auth.KindWeb {
				ttl = webMediaKeyTTL
			}
			key, exp := keys.Issue(sess, tok, ttl)
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]string{"key": key, "expiresAt": exp.UTC().Format(time.RFC3339)})
		})
	}
	// «Устройства» of the profile: its own devices, an admin's too (the
	// server's list of every profile's devices is /api/admin/devices, in
	// «Профили»; a profile's settings show only what is the profile's)
	r.Get("/api/me/devices", func(w http.ResponseWriter, r *http.Request) {
		u := userOf(r)
		cur, _ := auth.SessionFrom(r.Context())
		list, err := sessions.Devices(r.Context(), u.ID)
		if err != nil {
			internalErr(w, r, "devices", err)
			return
		}
		out := make([]deviceView, 0, len(list))
		for _, s := range list {
			out = append(out, deviceViewOf(s, cur.ID, u.Name, u.Avatar, u.Photo))
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		// every profile's devices, by the profile signed in on each
		r.Get("/api/admin/devices", func(w http.ResponseWriter, r *http.Request) {
			cur, _ := auth.SessionFrom(r.Context())
			list, err := sessions.AllDevices(r.Context())
			if err != nil {
				internalErr(w, r, "devices", err)
				return
			}
			out := make([]deviceView, 0, len(list))
			for _, d := range list {
				out = append(out, deviceViewOf(d.Session, cur.ID, d.UserName, d.UserAvatar, d.UserPhoto))
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"items": out})
		})
		// an admin signs out any profile's device
		r.Delete("/api/admin/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
			if err == nil {
				err = sessions.RevokeAnyDevice(r.Context(), id)
			} else {
				err = auth.ErrNotFound
			}
			deviceRevoked(w, r, err)
		})
	})
	r.Delete("/api/me/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err == nil {
			// the caller's own device, an admin's too (any profile's: /api/admin/devices)
			err = sessions.RevokeDevice(r.Context(), userOf(r).ID, id)
		} else {
			err = auth.ErrNotFound
		}
		deviceRevoked(w, r, err)
	})
}

// deviceView is a device of GET /api/me/devices and /api/admin/devices.
type deviceView struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"` // native | web (a browser)
	Name      string `json:"name"`
	Model     string `json:"model"`
	Version   string `json:"version"`
	CreatedAt string `json:"createdAt"`
	LastSeen  string `json:"lastSeen"`
	Current   bool   `json:"current"`
	// the profile signed in on it
	UserID     int64  `json:"userId"`
	UserName   string `json:"userName"`
	UserAvatar string `json:"userAvatar"`
	UserPhoto  string `json:"userPhoto,omitempty"`
	// History: the managed account the device writes its history
	// into (null: the profile's own)
	History *historyRef `json:"history"`
}

func deviceViewOf(s auth.Session, current int64, name, avatar, photo string) deviceView {
	return deviceView{s.ID, s.Kind, s.Device.Name, s.Device.Model, s.Device.Version,
		s.CreatedAt.UTC().Format(time.RFC3339), s.LastSeen.UTC().Format(time.RFC3339), s.ID == current, s.UserID, name, avatar, photo, historyOf(s)}
}

// deviceRevoked answers a sign-out of a device: 204, or 404 when there is none to sign out.
func deviceRevoked(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, auth.ErrNotFound) {
		writeErr(w, r, http.StatusNotFound, "Устройство не найдено")
		return
	}
	if err != nil {
		internalErr(w, r, "revoke device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
