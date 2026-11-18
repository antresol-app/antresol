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

// AISettings is «ИИ-помощник»: the language models Antresol's AI tasks
// ask — a LiteLLM or another OpenAI-compatible endpoint (its URL ends in
// /v1: Gemini's, OpenAI's, Groq's, OpenRouter's), Anthropic's Messages API
// (https://api.anthropic.com, channels.IsAnthropicURL), or an Ollama (its
// address). One endpoint, three models by weight (Models): each task asks
// the tier that fits it (channels.Tier).
// Key never leaves the server: settingsAPI.load drops it from answers.
type AISettings struct {
	URL string `json:"url"`
	// Model is the medium tier, kept for clients and builds that know one
	// model only (a stored row with Model alone is read as every tier).
	Model  string   `json:"model"`
	Models AIModels `json:"models"`
	Key    string   `json:"key,omitempty"`
}

// AIModels are the three models of the endpoint: light (quick, cheap,
// many small calls: naming channel categories), medium (reading a
// request: «Опишите канал»), powerful (the hardest tasks).
type AIModels struct {
	Light    string `json:"light"`
	Medium   string `json:"medium"`
	Powerful string `json:"powerful"`
}

// Anthropic's models by tier: what a single stored Claude model becomes.
var anthropicTiers = AIModels{Light: "claude-haiku-5-5", Medium: "claude-sonnet-5-5", Powerful: "claude-opus-5-5"}

// withTiers fills the tiers from what is stored: a row of one model only
// (before the tiers) becomes that model on every tier, except on
// Anthropic, where it keeps its own family's tier (an Opus stays
// «powerful») and the others take Anthropic's models; an empty tier is the
// medium one (or any set one). Model mirrors Medium.
func (a AISettings) withTiers() AISettings {
	m := &a.Models
	m.Light, m.Medium, m.Powerful = strings.TrimSpace(m.Light), strings.TrimSpace(m.Medium), strings.TrimSpace(m.Powerful)
	a.Model = strings.TrimSpace(a.Model)
	if *m == (AIModels{}) && a.Model != "" {
		if channels.IsAnthropicURL(a.URL) {
			*m = anthropicTiers
			switch l := strings.ToLower(a.Model); {
			case strings.Contains(l, "haiku"):
				m.Light = a.Model
			case strings.Contains(l, "opus"):
				m.Powerful = a.Model
			case strings.Contains(l, "sonnet"):
				m.Medium = a.Model
			default:
				*m = AIModels{a.Model, a.Model, a.Model}
			}
		} else {
			*m = AIModels{a.Model, a.Model, a.Model}
		}
	}
	fallback := m.Medium
	for _, v := range []string{m.Light, m.Powerful} {
		if fallback == "" {
			fallback = v
		}
	}
	for _, v := range []*string{&m.Light, &m.Medium, &m.Powerful} {
		if *v == "" {
			*v = fallback
		}
	}
	a.Model = m.Medium
	return a
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
	case len(a.Key) > 500 || strings.ContainsAny(a.Key, "\r\n "):
		return "Неверный ключ"
	}
	for _, m := range []string{a.Models.Light, a.Models.Medium, a.Models.Powerful} {
		if utf8.RuneCountInString(m) > 120 || strings.ContainsAny(m, "\r\n") {
			return "Слишком длинное название модели"
		}
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "Адрес модели — http:// или https://"
	}
	if channels.IsAnthropicURL(a.URL) && a.Key == "" {
		return "Для Anthropic нужен ключ API"
	}
	return ""
}

// merged: what a PUT stores — trimmed, the tiers filled; an empty key
// keeps the stored one (answers never carry it); no address clears the
// models and the key.
func (a AISettings) merged(stored AISettings) AISettings {
	a.URL = strings.TrimRight(strings.TrimSpace(a.URL), "/")
	a.Key = strings.TrimSpace(a.Key)
	if a.Key == "" {
		a.Key = stored.Key
	}
	if a.URL == "" {
		return AISettings{}
	}
	if a.Models == (AIModels{}) && a.Model != "" && a.Model == stored.Model && a.URL == stored.URL {
		a.Models = stored.Models // a client of one model that did not change it
	}
	return a.withTiers()
}

// model is the tier's model.
func (m AIModels) model(t channels.Tier) string {
	switch t {
	case channels.TierLight:
		return m.Light
	case channels.TierPowerful:
		return m.Powerful
	}
	return m.Medium
}

// llm is the tier's model of the settings (nil when none is set).
func (a AISettings) llm(t channels.Tier) *channels.LLM {
	a = a.withTiers()
	model := a.Models.model(t)
	if a.URL == "" || model == "" {
		return nil
	}
	return &channels.LLM{URL: a.URL, Model: model, Key: a.Key, Timeout: 30 * time.Second}
}

// llmSource is the model a task of tier t asks now: the settings' when
// set, else the server's VAULT_LLM_* (env: one model for every tier; nil:
// none — «Опишите канал» reads the words alone).
func llmSource(db *sql.DB, defaults AppSettings, env *channels.LLM) func(context.Context, channels.Tier) *channels.LLM {
	return func(ctx context.Context, t channels.Tier) *channels.LLM {
		if db != nil {
			if app, err := LoadAppSettings(ctx, db, defaults); err == nil {
				if l := app.AI.llm(t); l != nil {
					return l
				}
			}
		}
		return env
	}
}

// POST /api/settings/ai/test {url, models, key}: asks each tier's model
// (the distinct ones) a one-word question — {ok, ms, reply, tiers: [{tier,
// model, ok, ms, reply|error}]}; ok: every one answered. An empty key is
// the stored one; nothing is saved.
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
			a = AISettings{URL: env.URL, Model: env.Model, Key: env.Key}.withTiers()
		}
	}
	if msg := a.validate(); msg != "" || a.URL == "" {
		if msg == "" {
			msg = "Укажите адрес модели"
		}
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	type tierJSON struct {
		Tier  channels.Tier `json:"tier"`
		Model string        `json:"model"`
		OK    bool          `json:"ok"`
		MS    int64         `json:"ms"`
		Reply string        `json:"reply,omitempty"`
		Error string        `json:"error,omitempty"`
	}
	var tiers []tierJSON
	done := map[string]tierJSON{}
	ok, total := true, int64(0)
	for _, t := range []channels.Tier{channels.TierLight, channels.TierMedium, channels.TierPowerful} {
		model := a.Models.model(t)
		if d, seen := done[model]; seen {
			d.Tier = t
			tiers = append(tiers, d)
			continue
		}
		start := time.Now()
		reply, err := a.llm(t).Ping(r.Context())
		j := tierJSON{Tier: t, Model: model, OK: err == nil, MS: time.Since(start).Milliseconds(), Reply: reply}
		if err != nil {
			j.Error, ok = err.Error(), false
		}
		total += j.MS
		done[model] = j
		tiers = append(tiers, j)
	}
	out := map[string]any{"ok": ok, "ms": total, "tiers": tiers}
	if ok {
		out["reply"] = tiers[1].Reply
	} else {
		for _, t := range tiers {
			if !t.OK {
				out["error"] = t.Error
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
