package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/channels"
)

// AISettings is «ИИ-помощник»: the language model «Опишите канал» asks — a
// LiteLLM or another OpenAI-compatible endpoint (its URL ends in /v1:
// Gemini's, OpenAI's, Groq's, OpenRouter's), or an Ollama (its address).
// Key never leaves the server: settingsAPI.load drops it from answers.
type AISettings struct {
	URL   string `json:"url"`
	Model string `json:"model"`
	Key   string `json:"key,omitempty"`
}

// validate returns a Russian message for an invalid value.
func (a AISettings) validate() string {
	switch {
	case a.URL == "" && a.Model == "":
		return ""
	case a.URL == "":
		return "Укажите адрес модели"
	case a.Model == "":
		return "Укажите модель"
	case utf8.RuneCountInString(a.Model) > 120 || strings.ContainsAny(a.Model, "\r\n"):
		return "Слишком длинное название модели"
	case len(a.Key) > 500 || strings.ContainsAny(a.Key, "\r\n "):
		return "Неверный ключ"
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "Адрес модели — http:// или https://"
	}
	return ""
}

// merged: what a PUT stores — trimmed; an empty key keeps the stored one
// (answers never carry it); no address clears the model and the key.
func (a AISettings) merged(stored AISettings) AISettings {
	a.URL = strings.TrimRight(strings.TrimSpace(a.URL), "/")
	a.Model, a.Key = strings.TrimSpace(a.Model), strings.TrimSpace(a.Key)
	if a.Key == "" {
		a.Key = stored.Key
	}
	if a.URL == "" {
		return AISettings{}
	}
	return a
}

// llm is the model of the settings (nil when none is set).
func (a AISettings) llm() *channels.LLM {
	if a.URL == "" || a.Model == "" {
		return nil
	}
	return &channels.LLM{URL: a.URL, Model: a.Model, Key: a.Key, Timeout: 30 * time.Second}
}

// llmSource is the model «Опишите канал» asks now: the settings' when set,
// else the server's VAULT_LLM_* (env, nil: the words alone).
func llmSource(db *sql.DB, defaults AppSettings, env *channels.LLM) func(context.Context) *channels.LLM {
	return func(ctx context.Context) *channels.LLM {
		if db != nil {
			if app, err := LoadAppSettings(ctx, db, defaults); err == nil {
				if l := app.AI.llm(); l != nil {
					return l
				}
			}
		}
		return env
	}
}

// POST /api/settings/ai/test {url, model, key}: asks the model a one-word
// question — {ok, ms, reply} or {ok:false, error}. An empty key is the
// stored one; nothing is saved.
func (s *settingsAPI) testAI(w http.ResponseWriter, r *http.Request) {
	var body AISettings
	if !decode(w, r, &body) {
		return
	}
	stored, err := LoadAppSettings(r.Context(), s.db, s.defaults)
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	a := body.merged(stored.AI)
	if a.URL == "" {
		// nothing typed: the model the server uses now
		if env := channels.LLMFromEnv(); env != nil {
			a = AISettings{URL: env.URL, Model: env.Model, Key: env.Key}
		}
	}
	if msg := a.validate(); msg != "" || a.URL == "" {
		if msg == "" {
			msg = "Укажите адрес модели"
		}
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	start := time.Now()
	reply, err := a.llm().Ping(r.Context())
	ms := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "ms": ms, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ms": ms, "reply": reply})
}
