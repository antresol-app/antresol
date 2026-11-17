package api

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/notify"
	"github.com/go-chi/chi/v5"
)

// The core API's letters (notify): an addon tells profiles about something
// of one of the topics its manifest declares; the notify service puts it
// in their next digests (notify.QueueAddon).

const (
	msgAddonNotifyOff      = "Уведомления недоступны"
	msgAddonNoTopic        = "Расширение не объявило эту тему уведомлений"
	msgAddonBadAudience    = "Неверная аудитория уведомления"
	msgAddonKidRecipient   = "Детскому профилю это недоступно"
	msgAddonBadNotifyLink  = "Ссылка уведомления должна вести на страницу приложения"
	msgAddonBadNotifyTexts = "Неверный текст уведомления"

	addonNotifyBody = 64 << 10
	// the limits of a notification, in characters
	notifyKeyMax     = 200
	notifySubjectMax = 200
	notifyBodyMax    = 2000
)

func mountAddonCoreNotify(r chi.Router, a *addonsAPI) {
	r.Post("/notify", a.coreNotify)
}

// textOK: t has no part over max characters; need: at least one is more
// than blanks.
func textOK(t addonkit.Text, max int, need bool) bool {
	if utf8.RuneCountInString(t.RU) > max || utf8.RuneCountInString(t.EN) > max {
		return false
	}
	return !need || strings.TrimSpace(t.RU) != "" || strings.TrimSpace(t.EN) != ""
}

func textMap(t *addonkit.Text) map[string]string {
	if t == nil || (t.RU == "" && t.EN == "") {
		return nil
	}
	m := map[string]string{}
	if t.RU != "" {
		m["ru"] = t.RU
	}
	if t.EN != "" {
		m["en"] = t.EN
	}
	return m
}

// coreNotify: POST addonkit.NotifyRequest → 202 {queued: n}, the profiles
// it was queued for (those with an address and the topic on).
func (a *addonsAPI) coreNotify(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "notify")
	if !ok {
		return
	}
	if a.notify == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgAddonNotifyOff)
		return
	}
	var body addonkit.NotifyRequest
	if !decodeMax(w, r, &body, addonNotifyBody) {
		return
	}
	if strings.TrimSpace(body.Key) == "" || utf8.RuneCountInString(body.Key) > notifyKeyMax || !textOK(body.Subject, notifySubjectMax, true) ||
		(body.Body != nil && !textOK(*body.Body, notifyBodyMax, false)) {
		writeErr(w, r, http.StatusBadRequest, msgAddonBadNotifyTexts)
		return
	}
	n, err := a.notify.QueueAddon(r.Context(), notify.AddonEvent{Addon: in.ID, Topic: body.Topic, Key: body.Key,
		Link: body.Link, Poster: body.Poster, UserIDs: body.UserIDs, Audience: body.Audience,
		Subject: textMap(&body.Subject), Body: textMap(body.Body)})
	switch {
	case errors.Is(err, notify.ErrNoTopic):
		writeErr(w, r, http.StatusBadRequest, msgAddonNoTopic)
	case errors.Is(err, notify.ErrBadAudience):
		writeErr(w, r, http.StatusBadRequest, msgAddonBadAudience)
	case errors.Is(err, notify.ErrKidRecipient):
		writeErr(w, r, http.StatusBadRequest, msgAddonKidRecipient)
	case errors.Is(err, notify.ErrBadLink):
		writeErr(w, r, http.StatusBadRequest, msgAddonBadNotifyLink)
	case err != nil:
		log.Printf("api: addon %s notify: %v", in.ID, err)
		writeErr(w, r, http.StatusInternalServerError, msgAddonFailed)
	default:
		writeJSON(w, http.StatusAccepted, map[string]int{"queued": n})
	}
}
