package api

import (
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mailer"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/go-chi/chi/v5"
)

// «Уведомления на почту» (internal/notify):
//
//   - GET|PUT /api/me/notify: the profile's address and topics; a kid's are
//     the admin's, through /api/users/{id}/notify (as «Звук и субтитры»).
//   - POST /api/me/notify/verify: «Отправить проверочное письмо» (a code,
//     and a link with a public address); POST /api/me/notify/confirm
//     {code} confirms the address.
//   - GET /api/notify/link/verify?t=, GET|POST /api/notify/link/unsubscribe?t=:
//     the letters' links, no login (a signed token); GET of unsubscribe
//     only asks (mail scanners open links), POST (its form, or a mail
//     app's one-click List-Unsubscribe-Post) turns the topic off.
func mountNotify(r chi.Router, h *lib) {
	r.Get("/api/me/notify", h.myNotify)
	r.Put("/api/me/notify", h.kidLockedNotify(h.saveNotify))
	r.Post("/api/me/notify/verify", h.kidLockedNotify(h.verifyNotify))
	r.Post("/api/me/notify/confirm", h.kidLockedNotify(h.confirmNotify))
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/api/users/{id}/notify", h.kidNotify(h.notifyOut))
		r.Put("/api/users/{id}/notify", h.kidNotify(h.saveNotify))
		r.Post("/api/users/{id}/notify/verify", h.kidNotify(h.verifyNotify))
		r.Post("/api/users/{id}/notify/confirm", h.kidNotify(h.confirmNotify))
	})
}

// kidNotify runs next for the kid profile named by {id}: an adult's
// address and topics are its own, not even an admin reads or changes them.
func (h *lib) kidNotify(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return h.userPlaybackPrefs(func(w http.ResponseWriter, r *http.Request, uid int64) {
		u, err := h.Users.Get(r.Context(), uid)
		if err != nil {
			storeErr(w, r, err)
			return
		}
		if !u.IsKid {
			writeErr(w, r, http.StatusForbidden, "Уведомления взрослого профиля настраивает только он сам")
			return
		}
		next(w, r, uid)
	})
}

// notifyLinkPrefix: the letters' links (token, no session; no CSRF check:
// see csrf).
const notifyLinkPrefix = "/api/notify/link/"

func mountNotifyLinks(r chi.Router, n *notify.Service) {
	r.Get(notifyLinkPrefix+"verify", notifyVerifyLink(n))
	r.Get(notifyLinkPrefix+"unsubscribe", notifyUnsubscribeLink(n, false))
	r.Post(notifyLinkPrefix+"unsubscribe", notifyUnsubscribeLink(n, true))
}

// notifyView is the answer of /api/me/notify.
type notifyView struct {
	notify.Prefs
	// Topics: what this profile may turn on (by role and edition).
	Topics []string `json:"topics"`
	// MailReady: the server has a mail account (Настройки → почта).
	MailReady bool `json:"mailReady"`
	// PublicLinks: letters carry links (VAULT_PUBLIC_URL is set).
	PublicLinks bool `json:"publicLinks"`
}

func (h *lib) kidLockedNotify(next func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if kidLocked(w, r) {
			return
		}
		next(w, r, userOf(r).ID)
	}
}

func (h *lib) myNotify(w http.ResponseWriter, r *http.Request) { h.notifyOut(w, r, userOf(r).ID) }

func (h *lib) notifyOut(w http.ResponseWriter, r *http.Request, uid int64) {
	ctx := r.Context()
	u, err := h.Users.Get(ctx, uid)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	p, err := h.Notify.Prefs(ctx, uid)
	if err != nil {
		internalErr(w, r, "notify prefs", err)
		return
	}
	_, ready := h.Notify.MailReady(ctx)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, notifyView{Prefs: p, Topics: h.Notify.TopicsFor(ctx, u), MailReady: ready, PublicLinks: h.Notify.Links()})
}

func (h *lib) saveNotify(w http.ResponseWriter, r *http.Request, uid int64) {
	var in notify.Prefs
	if !decode(w, r, &in) {
		return
	}
	u, err := h.Users.Get(r.Context(), uid)
	if err != nil {
		storeErr(w, r, err)
		return
	}
	if _, err := h.Notify.SetPrefs(r.Context(), u, in); err != nil {
		if errors.Is(err, notify.ErrBadEmail) {
			writeErr(w, r, http.StatusBadRequest, "Укажите адрес почты, например name@example.com")
			return
		}
		internalErr(w, r, "save notify prefs", err)
		return
	}
	h.notifyOut(w, r, uid)
}

func (h *lib) verifyNotify(w http.ResponseWriter, r *http.Request, uid int64) {
	err := h.Notify.SendVerification(r.Context(), uid)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, notify.ErrNoEmail):
		writeErr(w, r, http.StatusConflict, "Сначала укажите и сохраните адрес почты")
	case errors.Is(err, notify.ErrNoMail):
		writeErr(w, r, http.StatusConflict, "Почта сервера не настроена: её задаёт администратор в настройках")
	case errors.Is(err, notify.ErrTooSoon):
		writeErr(w, r, http.StatusTooManyRequests, "Письмо только что отправлено — подождите минуту")
	case errors.Is(err, notify.ErrTooMany):
		writeErr(w, r, http.StatusTooManyRequests, "Сегодня проверочных писем уже достаточно — попробуйте завтра")
	case errors.Is(err, mailer.ErrAuth):
		writeErr(w, r, http.StatusBadGateway, "Почтовый сервер не принял логин или пароль — проверьте настройки")
	case errors.Is(err, mailer.ErrRecipient):
		writeErr(w, r, http.StatusBadGateway, "Почтовый сервер не принял этот адрес")
	default:
		log.Printf("notify: verification letter to profile %d: %v", uid, err)
		writeErr(w, r, http.StatusBadGateway, "Не удалось отправить письмо, попробуйте ещё раз")
	}
}

func (h *lib) confirmNotify(w http.ResponseWriter, r *http.Request, uid int64) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.Notify.ConfirmCode(r.Context(), uid, in.Code); err != nil {
		if errors.Is(err, notify.ErrBadCode) {
			writeErr(w, r, http.StatusBadRequest, "Неверный или устаревший код")
			return
		}
		internalErr(w, r, "confirm notify code", err)
		return
	}
	h.notifyOut(w, r, uid)
}

// linkPage is the small page a letter's link opens.
var linkPage = template.Must(template.New("link").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>body{margin:0;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;color:#1d1d1f}
main{max-width:480px;margin:64px auto;padding:24px 16px}h1{font-size:22px;margin:0 0 12px}p{color:#6e6e73;line-height:1.5}
button{font:inherit;font-weight:600;color:#fff;background:#0071e3;border:0;border-radius:10px;padding:12px 20px;cursor:pointer}
@media (prefers-color-scheme:dark){body{background:#000;color:#f5f5f7}p{color:#a1a1a6}}</style></head>
<body><main><h1>{{.Title}}</h1>{{if .Text}}<p>{{.Text}}</p>{{end}}
{{if .Button}}<form method="post" action="{{.Action}}"><button type="submit">{{.Button}}</button></form>{{end}}</main></body></html>
`))

type linkView struct {
	Lang, Title, Text, Button string
	Action                    string
}

func writeLinkPage(w http.ResponseWriter, status int, v linkView) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer") // the token is in the address
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	if err := linkPage.Execute(w, v); err != nil {
		log.Printf("notify: link page: %v", err)
	}
}

// linkLang: the token's profile's language, else the request's.
func linkLang(r *http.Request, info notify.LinkInfo) string {
	if info.Lang != "" {
		return info.Lang
	}
	return i18n.FromContext(r.Context())
}

func badLinkPage(w http.ResponseWriter, r *http.Request, info notify.LinkInfo) {
	l := linkLang(r, info)
	writeLinkPage(w, http.StatusBadRequest, linkView{Lang: l, Title: i18n.TL(l, "Ссылка недействительна"),
		Text: i18n.TL(l, "Ссылка устарела или относится к другому адресу. Откройте настройки уведомлений в меню профиля.")})
}

func notifyVerifyLink(n *notify.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := n.Confirm(r.Context(), r.URL.Query().Get("t"))
		if err != nil {
			if !errors.Is(err, notify.ErrBadToken) && !errors.Is(err, notify.ErrStaleToken) {
				log.Printf("notify: verify link: %v", err)
			}
			badLinkPage(w, r, info)
			return
		}
		l := linkLang(r, info)
		writeLinkPage(w, http.StatusOK, linkView{Lang: l, Title: i18n.TL(l, "Адрес подтверждён"),
			Text: i18n.TL(l, "Уведомления, которые вы включили, будут приходить на этот адрес.")})
	}
}

func notifyUnsubscribeLink(n *notify.Service, apply bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("t")
		var info notify.LinkInfo
		var err error
		if apply {
			info, err = n.Unsubscribe(r.Context(), tok)
		} else {
			info, err = n.CheckUnsubscribe(r.Context(), tok)
		}
		if err != nil {
			if !errors.Is(err, notify.ErrBadToken) && !errors.Is(err, notify.ErrStaleToken) {
				log.Printf("notify: unsubscribe link: %v", err)
			}
			badLinkPage(w, r, info)
			return
		}
		l := linkLang(r, info)
		topic := notify.Heading(l, info.Topic)
		if !apply {
			writeLinkPage(w, http.StatusOK, linkView{Lang: l, Title: i18n.TL(l, "Больше не присылать «%s»?", topic),
				Text:   i18n.TL(l, "Остальные уведомления не изменятся."),
				Button: i18n.TL(l, "Отписаться"), Action: notifyLinkPrefix + "unsubscribe?t=" + url.QueryEscape(tok)})
			return
		}
		writeLinkPage(w, http.StatusOK, linkView{Lang: l, Title: i18n.TL(l, "Вы отписались от «%s»", topic),
			Text: i18n.TL(l, "Остальные уведомления не изменились. Включить снова можно в меню профиля: «Уведомления на почту».")})
	}
}

// MailConfig is the SMTP account of the settings as the mailer takes it.
func (m MailSettings) MailConfig() mailer.Config {
	return mailer.Config{Host: m.Host, Port: m.Port, Username: m.Username, Password: m.Password, From: m.From}
}

// NotifyMail is notify.Service.Mail over the saved settings: the account of
// «Отправка на Kindle» is the one letters leave from.
func NotifyMail(load func(ctx context.Context) (AppSettings, error)) func(ctx context.Context) (mailer.Config, bool) {
	return func(ctx context.Context) (mailer.Config, bool) {
		s, err := load(ctx)
		if err != nil {
			log.Printf("notify: settings: %v", err)
			return mailer.Config{}, false
		}
		m := s.Mail.normalized()
		return m.MailConfig(), m.Configured()
	}
}
