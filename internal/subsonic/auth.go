package subsonic

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
)

// Messages of failed logins.
const (
	msgBadLogin      = "Неверное имя или пароль"
	msgNoAppPassword = "Задайте пароль для приложений в профиле Antresol"
	msgThrottled     = "Слишком много попыток, подождите"
	// as the web login says it (api.msgRemotePasswordless)
	msgRemotePasswordless = "Для входа не из дома у профиля должен быть пароль. Попросите администратора задать его в Управлении → Профили."
)

// authenticate checks u with t+s (t = md5(app password + s)) or p (the app
// password, in the clear or as enc:<hex>). Only the app password counts:
// the login password never travels to music apps, and a profile without
// a password logs in from the home network only, as on the web. Failures
// count in the shared throttle under the web login's keys. On failure the
// response is written and false returned.
func (c *call) authenticate() bool {
	name := c.p.Get("u")
	if name == "" {
		write(c.w, c.json, c.missing("u"))
		return false
	}
	token, salt := c.p.Get("t"), c.p.Get("s")
	if token == "" && !c.p.Has("p") {
		write(c.w, c.json, c.fail(codeMissing, "Не указан пароль (p или t и s)"))
		return false
	}
	if token != "" && salt == "" {
		write(c.w, c.json, c.missing("s"))
		return false
	}
	out, err := auth.AppLogin(c.ctx, c.s.Users, c.s.Network, c.s.th(), c.r, "subsonic", name,
		func(app string) bool { return c.checkPassword(app, token, salt) })
	if err != nil {
		write(c.w, c.json, c.internal("login", err))
		return false
	}
	switch out.Result {
	case auth.LoginOK:
		c.u = out.User
		return true
	case auth.LoginThrottled:
		write(c.w, c.json, c.fail(codeAuth, msgThrottled))
	case auth.LoginRemotePasswordless:
		write(c.w, c.json, c.fail(codeAuth, msgRemotePasswordless))
	case auth.LoginNoAppPassword:
		write(c.w, c.json, c.fail(codeAuth, msgNoAppPassword))
	default:
		write(c.w, c.json, c.fail(codeAuth, msgBadLogin))
	}
	return false
}

// checkPassword compares in constant time: the token with md5(app+salt),
// else p (enc:<hex> decoded) with app. An empty password never matches.
func (c *call) checkPassword(app, token, salt string) bool {
	if app == "" {
		return false
	}
	if token != "" {
		sum := md5.Sum([]byte(app + salt))
		want := hex.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(strings.ToLower(token)), []byte(want)) == 1
	}
	p := c.p.Get("p")
	if hx, ok := strings.CutPrefix(p, "enc:"); ok {
		b, err := hex.DecodeString(hx)
		if err != nil {
			return false
		}
		p = string(b)
	}
	if p == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(p), []byte(app)) == 1
}
