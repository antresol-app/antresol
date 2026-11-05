package opds

import (
	"context"
	"crypto/subtle"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
)

type ctxKey struct{}

func userOf(ctx context.Context) auth.User { u, _ := ctx.Value(ctxKey{}).(auth.User); return u }

func unauthorized(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Vault"`)
	http.Error(w, i18n.T(r.Context(), msg), http.StatusUnauthorized)
}

// Messages of failed logins (as Subsonic says them).
const (
	msgNoCredentials      = "Нужны имя профиля и пароль для приложений"
	msgBadLogin           = "Неверное имя или пароль"
	msgNoAppPassword      = "Задайте пароль для приложений в профиле Antresol"
	msgRemotePasswordless = "Для входа не из дома у профиля должен быть пароль. Попросите администратора задать его в Управлении → Профили."
)

// authenticate checks HTTP Basic: the profile name and its app password,
// through auth.AppLogin (the login of Subsonic too): never the login
// password, a profile without a password from the home network only, the
// shared throttle (a locked key gets 429). Missing credentials or an empty
// password give 401 without a strike.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	name, pass, ok := r.BasicAuth()
	if !ok || name == "" || pass == "" {
		unauthorized(w, r, msgNoCredentials)
		return auth.User{}, false
	}
	out, err := auth.AppLogin(r.Context(), h.Users, h.Network, h.Throttle, r, "opds", name,
		func(app string) bool { return subtle.ConstantTimeCompare([]byte(pass), []byte(app)) == 1 })
	if err != nil {
		if r.Context().Err() == nil {
			log.Printf("opds: login: %v", err)
			http.Error(w, i18n.T(r.Context(), "Внутренняя ошибка"), http.StatusInternalServerError)
		}
		return auth.User{}, false
	}
	switch out.Result {
	case auth.LoginOK:
		return out.User, true
	case auth.LoginThrottled:
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(out.Wait.Seconds()))))
		http.Error(w, i18n.T(r.Context(), "Слишком много попыток, подождите"), http.StatusTooManyRequests)
	case auth.LoginRemotePasswordless:
		unauthorized(w, r, msgRemotePasswordless)
	case auth.LoginNoAppPassword:
		unauthorized(w, r, msgNoAppPassword)
	default:
		unauthorized(w, r, msgBadLogin)
	}
	return auth.User{}, false
}
