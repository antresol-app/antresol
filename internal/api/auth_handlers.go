package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/go-chi/chi/v5"
)

type authAPI struct {
	users    *auth.Store
	sessions *auth.Sessions
	net      *auth.Network
	throttle *auth.Throttle
	// loginSlots caps concurrent password checks: bcrypt is CPU-heavy and
	// login is open to the internet (it would starve HLS transcodes).
	loginSlots chan struct{}
	// caps fills the sources build's capability fields of /api/me (none in
	// the core build).
	caps meCaps
	// libraryOnly is me.libraryOnly.
	libraryOnly bool
	// requestsOpen says whether kids' requests are offered here (nil: no).
	requestsOpen func(context.Context) bool
}

// msgRemoteSetup: the first setup from outside the home network (the
// passwordless message is auth.MsgRemotePasswordless).
const msgRemoteSetup = "Первую настройку можно сделать только из домашней сети"

// me is the signed-in profile plus whether this client is outside the home
// network (the UI then hides one-tap sign-in).
type me struct {
	auth.User
	Remote bool `json:"remote"`
	// meExtra: the sources build's capability fields (me_full.go).
	meExtra
	// CanRequest: a kid profile may ask for titles («Попросить»).
	CanRequest bool `json:"canRequest"`
	// Requests: an admin has «Просьбы» to answer.
	Requests bool `json:"requests"`
	// LibraryOnly: the server serves its library only. The web hides the
	// screens of the sources build's other features.
	LibraryOnly bool `json:"libraryOnly"`
}

// patchMe: PATCH /api/me {"hideProgress": bool}, the settings a profile
// changes for itself («Скрывать мой прогресс от семьи»). A kid's is the
// parents' choice: only an admin changes it (PATCH /api/users/{id}).
func (a *authAPI) patchMe(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct {
		HideProgress *bool `json:"hideProgress"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.HideProgress != nil && u.IsKid {
		writeErr(w, r, http.StatusForbidden, "Это может изменить только администратор")
		return
	}
	if err := a.users.Update(r.Context(), u.ID, auth.UserPatch{HideProgress: in.HideProgress}); err != nil {
		storeErr(w, r, err)
		return
	}
	fresh, err := a.users.Get(r.Context(), u.ID)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.meOf(r, fresh))
}

// meOf is the /api/me answer for u on request r.
func (a *authAPI) meOf(r *http.Request, u auth.User) me {
	m := me{User: u, Remote: !a.net.IsTrusted(r), meExtra: a.caps.of(r.Context(), u), LibraryOnly: a.libraryOnly}
	if a.requestsOpen != nil && (u.IsKid || u.IsAdmin) && a.requestsOpen(r.Context()) {
		m.CanRequest, m.Requests = u.IsKid, u.IsAdmin && !u.IsKid
	}
	return m
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeErr answers {"error": msg} with msg in the request's language: msg
// is Russian (a catalog key, the rendering of one, or a user-facing
// error's text) and i18n.Localize translates it; text already translated
// passes through unchanged.
func writeErr(w http.ResponseWriter, r *http.Request, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": i18n.Localize(r.Context(), msg)})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, r, http.StatusBadRequest, "Не удалось разобрать запрос")
		return false
	}
	return true
}

// storeErr maps store errors to HTTP responses.
func storeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Профиль не найден")
	case errors.Is(err, auth.ErrExists):
		writeErr(w, r, http.StatusConflict, "Такое имя уже занято")
	case errors.Is(err, auth.ErrLastAdmin):
		writeErr(w, r, http.StatusConflict, "Нужен хотя бы один администратор")
	case errors.Is(err, auth.ErrPasswordTooLong):
		writeErr(w, r, http.StatusBadRequest, "Пароль слишком длинный (не более 72 байт)")
	case errors.Is(err, auth.ErrAdminPassShort):
		writeErr(w, r, http.StatusBadRequest, "Пароль администратора — не короче 6 символов")
	case errors.Is(err, auth.ErrPasswordShort):
		writeErr(w, r, http.StatusBadRequest, "Пароль — не короче 4 символов")
	case errors.Is(err, auth.ErrInvalid):
		writeErr(w, r, http.StatusBadRequest, "Проверьте имя и пароль (у администратора пароль обязателен)")
	default:
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
	}
}

func (a *authAPI) startSession(w http.ResponseWriter, r *http.Request, u auth.User) {
	browser, system := browserOf(r.UserAgent())
	tok, err := a.sessions.CreateBrowser(r.Context(), u.ID, browser, system)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "Что-то пошло не так")
		return
	}
	auth.SetCookie(w, r, tok)
	writeJSON(w, http.StatusOK, a.meOf(r, u))
}

// tooMany answers 429 with Retry-After and a message in the request language.
func tooMany(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	secs := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	ctx := r.Context()
	when := i18n.T(ctx, "%d сек.", secs)
	if secs >= 60 {
		when = i18n.T(ctx, "%d мин.", (secs+59)/60)
	}
	writeErr(w, r, http.StatusTooManyRequests, i18n.T(ctx, "Слишком много неудачных попыток входа. Попробуйте снова через %s", when))
}

// login signs a browser in: checkLogin, then a cookie session.
func (a *authAPI) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID   int64  `json:"userId"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if u, ok := a.checkLogin(w, r, in.UserID, in.Password); ok {
		a.startSession(w, r, u)
	}
}

// checkLogin checks the password with per-client and per-profile backoff
// (shared by the web login and device sign-in). Profile keys are split by
// home/remote so a stranger guessing from the internet cannot lock the
// family out at home. On false the response has been written.
func (a *authAPI) checkLogin(w http.ResponseWriter, r *http.Request, userID int64, password string) (auth.User, bool) {
	trusted := a.net.IsTrusted(r)
	client := a.net.ClientKey(r)
	where := "remote"
	if trusted {
		where = "home"
	}
	keys := []string{"ip:" + client, fmt.Sprintf("user:%d:%s", userID, where)}
	if wait := a.throttle.Wait(keys...); wait > 0 {
		tooMany(w, r, wait)
		return auth.User{}, false
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	case <-r.Context().Done():
		return auth.User{}, false
	}
	if wait := a.throttle.Wait(keys...); wait > 0 { // others failed while we queued
		tooMany(w, r, wait)
		return auth.User{}, false
	}
	if !trusted {
		if u, err := a.users.Get(r.Context(), userID); err == nil && !u.HasPassword {
			log.Printf("auth: passwordless login for profile %d refused from %s (not the home network)", userID, client)
			writeErr(w, r, http.StatusForbidden, auth.MsgRemotePasswordless)
			return auth.User{}, false
		}
	}
	u, err := a.users.Verify(r.Context(), userID, password)
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) || errors.Is(err, auth.ErrBadPassword) {
			a.throttle.Fail(keys...)
			log.Printf("auth: failed login for profile %d from %s", userID, client)
			writeErr(w, r, http.StatusUnauthorized, "Неверный пароль")
			return auth.User{}, false
		}
		storeErr(w, r, err)
		return auth.User{}, false
	}
	a.throttle.Reset(keys...)
	return u, true
}

// mountAuth mounts setup, login, device sign-in and profiles. guard is the
// router's one session check. throttle counts failed password checks; it is
// shared with Subsonic and OPDS (nil: a new one).
func mountAuth(r chi.Router, users *auth.Store, guard *auth.Guard, throttle *auth.Throttle, caps meCaps, libraryOnly bool, requestsOpen func(context.Context) bool) {
	if throttle == nil {
		throttle = auth.NewThrottle()
	}
	sessions, network := guard.Sessions, guard.Network
	a := &authAPI{users: users, sessions: sessions, net: network, throttle: throttle, loginSlots: make(chan struct{}, 2), caps: caps,
		libraryOnly: libraryOnly, requestsOpen: requestsOpen}
	r.Get("/api/setup", func(w http.ResponseWriter, r *http.Request) {
		need, err := users.NeedsSetup(r.Context())
		if err != nil {
			storeErr(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"needsSetup": need, "remote": !network.IsTrusted(r)})
	})
	r.Post("/api/setup", func(w http.ResponseWriter, r *http.Request) {
		if !network.IsTrusted(r) {
			log.Printf("auth: setup refused from %s (not the home network)", network.ClientKey(r))
			writeErr(w, r, http.StatusForbidden, msgRemoteSetup)
			return
		}
		var in struct{ Name, Password string }
		if !decode(w, r, &in) {
			return
		}
		u, err := users.CreateFirstAdmin(r.Context(), in.Name, in.Password)
		if errors.Is(err, auth.ErrSetupDone) {
			writeErr(w, r, http.StatusForbidden, "Сервер уже настроен")
			return
		}
		if err != nil {
			storeErr(w, r, err)
			return
		}
		a.startSession(w, r, u)
	})
	r.Get("/api/profiles", func(w http.ResponseWriter, r *http.Request) {
		list, err := users.List(r.Context())
		if err != nil {
			storeErr(w, r, err)
			return
		}
		type pub struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Avatar      string `json:"avatar"`
			HasPassword bool   `json:"hasPassword"`
		}
		out := make([]pub, 0, len(list))
		for _, u := range list {
			out = append(out, pub{u.ID, u.Name, u.Avatar, u.HasPassword})
		}
		writeJSON(w, 200, out)
	})
	r.Post("/api/login", a.login)
	r.Post("/api/auth/token", a.deviceToken)
	r.Post("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if tok := auth.RequestToken(r); tok != "" {
			if err := sessions.Revoke(r.Context(), tok); err != nil {
				log.Printf("auth: logout revoke: %v", err)
				writeErr(w, r, http.StatusInternalServerError, "Не удалось выйти, попробуйте ещё раз")
				return
			}
		}
		auth.ClearCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	})

	r.Group(func(r chi.Router) {
		r.Use(guard.Require)
		r.Get("/api/me", func(w http.ResponseWriter, r *http.Request) {
			u, _ := auth.UserFrom(r.Context())
			writeJSON(w, 200, a.meOf(r, u))
		})
		r.Patch("/api/me", a.patchMe)
		// «Общая медиатека / Моя медиатека»: the choice the profile's web
		// pages ask the library with (?scope=); kept on the server so it
		// follows the profile to every browser
		r.Put("/api/me/library-scope", func(w http.ResponseWriter, r *http.Request) {
			u, _ := auth.UserFrom(r.Context())
			var in struct {
				Scope string `json:"scope"`
			}
			if !decode(w, r, &in) {
				return
			}
			if u.IsKid && in.Scope != auth.ScopeAll {
				writeErr(w, r, http.StatusForbidden, "Детский профиль видит общую медиатеку")
				return
			}
			if in.Scope != auth.ScopeAll && in.Scope != auth.ScopeMine {
				writeErr(w, r, http.StatusBadRequest, "Неизвестная медиатека")
				return
			}
			if err := users.SetLibraryScope(r.Context(), u.ID, in.Scope); err != nil {
				storeErr(w, r, err)
				return
			}
			u, err := users.Get(r.Context(), u.ID)
			if err != nil {
				storeErr(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, a.meOf(r, u))
		})
		mountAppPassword(r, users)
		mountDevices(r, sessions, guard.Keys)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Get("/api/users", func(w http.ResponseWriter, r *http.Request) {
				list, err := users.List(r.Context())
				if err != nil {
					storeErr(w, r, err)
					return
				}
				writeJSON(w, 200, list)
			})
			r.Post("/api/users", func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Name      string `json:"name"`
					Avatar    string `json:"avatar"`
					Password  string `json:"password"`
					IsAdmin   bool   `json:"isAdmin"`
					IsKid     bool   `json:"isKid"`
					MaxRating string `json:"maxRating"`
				}
				if !decode(w, r, &in) {
					return
				}
				u, err := users.CreateUser(r.Context(), auth.NewUser(in))
				if err != nil {
					storeErr(w, r, err)
					return
				}
				writeJSON(w, http.StatusCreated, u)
			})
			r.Patch("/api/users/{id}", func(w http.ResponseWriter, r *http.Request) {
				id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
				if err != nil {
					writeErr(w, r, http.StatusBadRequest, "Неверный id")
					return
				}
				var in struct {
					Name      *string `json:"name"`
					Avatar    *string `json:"avatar"`
					Password  *string `json:"password"`
					IsAdmin   *bool   `json:"isAdmin"`
					IsKid     *bool   `json:"isKid"`
					MaxRating *string `json:"maxRating"`
					// the admin also decides it for a kid profile
					HideProgress *bool   `json:"hideProgress"`
					KindleEmail  *string `json:"kindleEmail"`
				}
				if !decode(w, r, &in) {
					return
				}
				if in.KindleEmail != nil {
					*in.KindleEmail = strings.TrimSpace(*in.KindleEmail)
					if *in.KindleEmail != "" && !validKindleEmail(*in.KindleEmail) {
						writeErr(w, r, http.StatusBadRequest, "Адрес Kindle заканчивается на @kindle.com")
						return
					}
				}
				if err := users.Update(r.Context(), id, auth.UserPatch(in)); err != nil {
					storeErr(w, r, err)
					return
				}
				if in.Password != nil && *in.Password != "" {
					// a new password locks out sessions (web and devices)
					// opened without it, except the caller's own
					if err := sessions.RevokeUser(r.Context(), id, auth.TokenFrom(r.Context())); err != nil {
						log.Printf("auth: revoke sessions of profile %d: %v", id, err)
					}
				}
				u, err := users.Get(r.Context(), id)
				if err != nil {
					storeErr(w, r, err)
					return
				}
				writeJSON(w, 200, u)
			})
			// «Сбросить пароль для приложений»: the admin ends another
			// profile's app password (it may have been made from a session
			// that is no longer theirs).
			r.Delete("/api/users/{id}/app-password", func(w http.ResponseWriter, r *http.Request) {
				id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
				if err != nil {
					writeErr(w, r, http.StatusBadRequest, "Неверный id")
					return
				}
				if err := users.ClearAppPassword(r.Context(), id); err != nil {
					storeErr(w, r, err)
					return
				}
				log.Printf("auth: admin %d reset the app password of profile %d", userOf(r).ID, id)
				w.WriteHeader(http.StatusNoContent)
			})
			r.Delete("/api/users/{id}", func(w http.ResponseWriter, r *http.Request) {
				id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
				if err != nil {
					writeErr(w, r, http.StatusBadRequest, "Неверный id")
					return
				}
				if err := users.Delete(r.Context(), id); err != nil {
					storeErr(w, r, err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})
	})
}
