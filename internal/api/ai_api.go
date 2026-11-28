package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

// llmSource is the model a task of tier t asks now: the list's entries that
// are on, serve the tier and have their key, first to last, as one
// channels.Chain (nil: none — «Опишите канал» reads the words alone). The
// server's own model (VAULT_LLM_*, env) is an entry of the list like the
// others; «Выключено» (Off) is none, whatever the environment has.
func llmSource(db *sql.DB, defaults AppSettings, env *channels.LLM) func(context.Context, channels.Tier) *channels.LLM {
	rt := aiRuntimeFor(db)
	return func(ctx context.Context, t channels.Tier) *channels.LLM {
		if db != nil {
			if app, err := LoadAppSettings(ctx, db, defaults); err == nil {
				if app.AI.Off {
					return nil
				}
				return rt.chain(db, app.AI, env, t)
			}
		}
		return env
	}
}

// aiChecked: one entry's check, as the answer has it.
type aiChecked struct {
	ID     string   `json:"id"`
	OK     bool     `json:"ok"`
	MS     int64    `json:"ms"`
	Reply  string   `json:"reply,omitempty"`
	Status AIStatus `json:"status"`
}

// POST /api/settings/ai/test {ids?: [], entry?: {…}}: asks the entries a
// one-word question — {ok, results: [{id, ok, ms, reply?, status}]}; ok:
// every one answered. ids: those entries (on or off); none: every one that
// is on, in parallel, 15 s each. A provider that needs a key and has none is
// not asked (its status is noKey). The statuses are stored, and an entry
// that answers is no longer cooling down. entry: a draft not in the list
// yet (an empty key is the stored one of the entry with its id) — asked,
// nothing saved.
func (s *settingsAPI) testAI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs   []string `json:"ids"`
		Entry *AIModel `json:"entry"`
	}
	if !decode(w, r, &body) {
		return
	}
	stored, err := LoadAppSettings(r.Context(), s.db, s.defaults)
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	env := channels.LLMFromEnv()
	list := stored.AI.resolved(env).List
	byID := map[string]*AIModel{}
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	const wait = 15 * time.Second
	var todo []AIModel
	draft := body.Entry != nil
	switch {
	case draft:
		e := cleanEntry(*body.Entry, byID[body.Entry.ID], env)
		if e.Env {
			if byID[aiEnvID] == nil {
				writeErr(w, r, http.StatusNotFound, "Модель не найдена")
				return
			}
			e = *byID[aiEnvID]
		}
		if msg := (AISettings{List: []AIModel{e}}).validate(); msg != "" {
			writeErr(w, r, http.StatusBadRequest, msg)
			return
		}
		if e.URL == "" || e.Model == "" {
			writeErr(w, r, http.StatusBadRequest, "Укажите адрес модели")
			return
		}
		if !e.Env && needsKey(e.URL) && e.Key == "" {
			writeErr(w, r, http.StatusBadRequest, "Нужен ключ API")
			return
		}
		todo = []AIModel{e}
	case len(body.IDs) > 0:
		for _, id := range body.IDs {
			e := byID[id]
			if e == nil {
				writeErr(w, r, http.StatusNotFound, "Модель не найдена")
				return
			}
			todo = append(todo, *e)
		}
	default:
		for _, e := range list {
			if !e.Off {
				todo = append(todo, e)
			}
		}
	}
	rt := aiRuntimeFor(s.db)
	results := make([]aiChecked, len(todo))
	var wg sync.WaitGroup
	for i, e := range todo {
		results[i] = aiChecked{ID: e.ID}
		if !draft {
			// no key, no request
			if st := rt.status(e, env); st.State == aiNoKey {
				results[i].Status = st
				continue
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := e.llm(env, wait)
			l.Timeout = wait
			start := time.Now()
			reply, err := l.Ping(r.Context())
			took := time.Since(start)
			results[i].OK, results[i].MS, results[i].Reply = err == nil, took.Milliseconds(), reply
			results[i].Status = outcome(took, err, aiByCheck, time.Now())
			if !draft && r.Context().Err() == nil {
				rt.record(s.db, env, e, took, err, aiByCheck)
			}
		}()
	}
	wg.Wait()
	ok := true
	for _, c := range results {
		ok = ok && c.OK
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "results": results})
}

// POST /api/settings/ai/models {url?, key?, id?}: the models an endpoint
// lists (channels.ListModels; nothing is billed, nothing saved) —
// {models: [{id}], error?}. An empty key is the stored one of the entry with
// that id (the server's own: the environment's) while the address stays on
// the same host; no address is the entry's. A failing endpoint is an
// answer too: {models: [], error, kind, code} — the page then offers a
// free-text name.
func (s *settingsAPI) modelsAI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
		Key string `json:"key"`
		ID  string `json:"id"`
	}
	if !decode(w, r, &body) {
		return
	}
	stored, err := LoadAppSettings(r.Context(), s.db, s.defaults)
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	base, key := strings.TrimRight(strings.TrimSpace(body.URL), "/"), strings.TrimSpace(body.Key)
	for _, e := range stored.AI.resolved(channels.LLMFromEnv()).List {
		if e.ID != body.ID {
			continue
		}
		if base == "" || e.Env {
			base = e.URL
		}
		if key == "" && sameHost(base, e.URL) {
			key = e.Key
		}
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		writeErr(w, r, http.StatusBadRequest, "Адрес модели — http:// или https://")
		return
	}
	names, err := channels.ListModels(r.Context(), nil, base, key)
	out := map[string]any{"models": []map[string]string{}}
	if err != nil {
		st := outcome(0, err, "", time.Time{})
		out["error"], out["kind"], out["code"] = st.Error, st.Kind, st.Code
		writeJSON(w, http.StatusOK, out)
		return
	}
	models := make([]map[string]string, len(names))
	for i, n := range names {
		models[i] = map[string]string{"id": n}
	}
	out["models"] = models
	writeJSON(w, http.StatusOK, out)
}
