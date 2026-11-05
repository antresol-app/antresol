package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/go-chi/chi/v5"
)

// adminEvents are not sent to other profiles. The sources build adds its own
// admin screens' events (events_full.go).
var adminEvents = map[string]bool{
	events.DiskWarning:     true,
	events.MigrateProgress: true,
	events.ImportProgress:  true,
}

// eventVisible says whether a subscriber gets an event at all:
// request.changed (a kid's request made or answered) reaches the admins
// and the kid whose request it is only.
func eventVisible(u auth.User, typ string, data any) bool {
	if typ != events.RequestChanged || u.IsAdmin {
		return true
	}
	m, ok := data.(map[string]int64)
	return ok && m["userId"] == u.ID
}

// sseHeartbeat keeps proxies from closing an idle stream.
var sseHeartbeat = 25 * time.Second

func mountEvents(r chi.Router, bus *events.Bus) {
	r.Get("/api/events", func(w http.ResponseWriter, r *http.Request) { serveEvents(w, r, bus) })
}

// serveEvents streams the bus as server-sent events: "event: <type>" and
// "data: <json>" per message.
func serveEvents(w http.ResponseWriter, r *http.Request, bus *events.Bus) {
	rc := http.NewResponseController(w)
	u := userOf(r)
	// library.updated names what was added; a rating-limited profile could
	// learn titles it may not see, so it gets a bare "reload" instead.
	limited := ratingLimit(u) != ""
	lang := i18n.FromContext(r.Context())
	var filter []func(string) bool
	if !u.IsAdmin {
		filter = append(filter, func(typ string) bool { return !adminEvents[typ] })
	}
	ch, cancel := bus.Subscribe(filter...)
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // nginx (NPM): do not buffer
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 5000\n: connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}
	beat := time.NewTicker(sseHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !eventVisible(u, ev.Type, ev.Data) {
				continue
			}
			payload := ev.Data
			if limited && ev.Type == events.LibraryUpdated {
				payload = struct{}{}
			}
			payload = eventPayload(lang, u, ev.Type, payload)
			data, err := json.Marshal(payload)
			if err != nil {
				log.Printf("api: event %s: %v", ev.Type, err)
				continue
			}
			data = localizeEvent(lang, data)
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// localizeEvent translates the display text of an event's payload (a
// follow's lastError, a download's status, a source's message…), made in
// Russian by background work, into the subscriber's language: every
// string value of the JSON goes through i18n.LocalizeL, which leaves
// unknown text (titles, paths) as it is. Russian, or a payload without
// Cyrillic, is sent untouched.
func localizeEvent(lang string, data []byte) []byte {
	if lang == i18n.RU || !bytes.ContainsFunc(data, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) {
		return data
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return data
	}
	out, err := json.Marshal(localizeJSON(lang, v))
	if err != nil {
		return data
	}
	return out
}

func localizeJSON(lang string, v any) any {
	switch t := v.(type) {
	case string:
		return i18n.LocalizeL(lang, t)
	case map[string]any:
		for k, x := range t {
			t[k] = localizeJSON(lang, x)
		}
	case []any:
		for i, x := range t {
			t[i] = localizeJSON(lang, x)
		}
	}
	return v
}
