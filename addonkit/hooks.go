package addonkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// maxHookBody bounds a hook's request body (the core's bodies are far
// smaller; a hold set's keys are at most 20 000).
const maxHookBody = 64 << 20

// HookFunc answers one hook. u is the profile the hook runs for (nil when
// it runs for none); in is the body ({} when the core sends nothing).
// It returns the 200 answer (marshalled as JSON), or nil, nil for 204;
// a *Refusal for 409 with its text; any other error is a failure (500).
// Only an untyped nil is 204: a typed nil (a nil slice, map or pointer in
// the any) is 200 with the body null; return nil itself for "nothing to
// add".
type HookFunc func(ctx context.Context, u *User, in json.RawMessage) (any, error)

// HookMux serves POST /hooks/<name> (contract: "Hooks"). Mount it at
// /hooks/ (mux.Handle("/hooks/", m)) or serve it as the whole handler.
// A hook with no handler answers 404: the core treats that as "not
// implemented", except for a declared files.* hook, where 404 is a
// failure. Declare in the manifest exactly the hooks handled here.
type HookMux struct {
	mu sync.RWMutex
	h  map[string]HookFunc
}

// Handle sets the handler of hook name (a second call replaces it).
func (m *HookMux) Handle(name string, fn func(ctx context.Context, u *User, in json.RawMessage) (any, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.h == nil {
		m.h = map[string]HookFunc{}
	}
	m.h[name] = fn
}

// DefaultRefusal is the text of a refusal given none: the core takes a
// 409 without a text for a failure, not a refusal.
const DefaultRefusal = "Расширение не разрешило это действие"

// Refusal is a hook's "no": the core shows Message to the user (409). An
// empty Message is sent as DefaultRefusal.
type Refusal struct{ Message string }

func (r *Refusal) Error() string { return r.text() }

func (r *Refusal) text() string {
	if r == nil || strings.TrimSpace(r.Message) == "" {
		return DefaultRefusal
	}
	return r.Message
}

// Refuse is a *Refusal with msg (DefaultRefusal when msg is empty).
func Refuse(msg string) error { return &Refusal{Message: msg} }

// Answer is a hook answer with a status of its own, for the hooks whose
// contract has more answers than 200 and 204: 202 with Body ("still
// working", trailers.fetch), 200 with Body, 204, or 404 ("not this
// addon"; Body ignored). A HookFunc returns it as a *Answer; a 200 or 202
// without a Body, and any other Status, is a programming error answered
// as a failure (500).
type Answer struct {
	Status int
	Body   any
}

func (m *HookMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, "/hooks/")
	if !ok || name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		WriteError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	m.mu.RLock()
	fn := m.h[name]
	m.mu.RUnlock()
	if fn == nil {
		WriteError(w, http.StatusNotFound, "no such hook")
		return
	}
	var u *User
	if r.Header.Get(UserHeader) != "" {
		var ok bool
		if u, ok = UserFrom(r); !ok {
			WriteError(w, http.StatusBadRequest, "bad "+UserHeader)
			return
		}
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad body")
		return
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	if !json.Valid(b) {
		WriteError(w, http.StatusBadRequest, "the body is not JSON")
		return
	}
	out, err := fn(r.Context(), u, json.RawMessage(b))
	var ref *Refusal
	status := http.StatusOK
	if a, ok := out.(*Answer); ok && err == nil {
		switch {
		case a == nil:
			out = nil
		case a.Status == http.StatusNotFound:
			WriteError(w, http.StatusNotFound, "not this addon")
			return
		case a.Status == http.StatusNoContent:
			out = nil
		case (a.Status == http.StatusOK || a.Status == http.StatusAccepted) && a.Body == nil:
			WriteError(w, http.StatusInternalServerError, "a hook answer of status "+strconv.Itoa(a.Status)+" without a body")
			return
		case a.Status == http.StatusOK, a.Status == http.StatusAccepted:
			status, out = a.Status, a.Body
		default:
			WriteError(w, http.StatusInternalServerError, "a hook answer of status "+strconv.Itoa(a.Status))
			return
		}
	}
	switch {
	case errors.As(err, &ref):
		WriteError(w, http.StatusConflict, ref.text())
	case err != nil:
		WriteError(w, http.StatusInternalServerError, err.Error())
	case out == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		body, err := json.Marshal(out)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	}
}
