package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/transcript"
)

// AISettings is «ИИ-помощник»: the language models Antresol's AI tasks ask,
// one ordered list (List), the first that answers wins and a failure goes
// on to the next (channels.Chain). An entry is a LiteLLM or another
// OpenAI-compatible endpoint (its URL ends in /v1: Gemini's, OpenAI's,
// Groq's, OpenRouter's — /openai for Gemini), Anthropic's Messages API
// (https://api.anthropic.com, channels.IsAnthropicURL) or an Ollama (its
// address); each says which weights of task it serves (Tiers, see
// channels.Tier: light, medium, powerful).
// An entry's key never leaves the server: settingsAPI.load drops it from
// answers (KeySet says it is there).
// Off is the explicit «Выключено»: no model is asked, not even the server's
// own (VAULT_LLM_*), which is otherwise the list's entry «Сервер» (Env).
//
// URL, Model, Models and Key are the one-endpoint, three-tiers settings of
// before the list: a stored row of them is migrated on read (migrated), a
// PUT without a list is read as them (merged), and answers carry the first
// own model in them for one release, so a client that knows no list still
// reads one model.
//
// An entry is of a kind: a language model (the default) or speech recognition
// (Kind "speech": a speech-to-text service the transcripts of audiobooks ask,
// transcript.Speech — Groq's, OpenAI's or Gemini's own address, or an
// OpenAI-compatible /audio/transcriptions of one's own). The two kinds share
// the list and its rules (order, «Выключена», status, a check) but never each
// other's work: a text task asks the text entries only, the transcripts the
// speech ones only, each in its own order. The server's own speech service
// (VAULT_STT_*) is the speech entry «Сервер» (Env) like the LiteLLM is the
// text one. The stored «stt» of before is migrated into a speech entry on read
// (migrateSTT).
//
// Features switch what the assistant does, one by one (channels.Features):
// the id is on unless it is false here (a missing id is on, so a feature
// added later starts on). A feature that is off is never given a model
// (llmSource, SpeechSource) and goes on as it does with none.
type AISettings struct {
	Off      bool            `json:"off,omitempty"`
	List     []AIModel       `json:"list"`
	Features map[string]bool `json:"features,omitempty"`

	// legacy: see above
	URL    string   `json:"url"`
	Model  string   `json:"model"`
	Models AIModels `json:"models"`
	Key    string   `json:"key,omitempty"`

	// sent: decoded from a request or a row (not left out of it)
	sent bool
}

// UnmarshalJSON notes that the settings were there: a client of before the
// list clears its model by sending nothing but empty fields, which is not
// the same as a client that has no «ИИ-помощник» at all.
func (a *AISettings) UnmarshalJSON(b []byte) error {
	type plain AISettings
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*a = AISettings(p)
	a.sent = true
	return nil
}

// AIModel is one entry of the list.
type AIModel struct {
	// ID is made by the server (8 hex) and stays with the entry; "env" is the
	// server's own model.
	ID string `json:"id"`
	// Kind: "" a language model, "speech" speech recognition.
	Kind string `json:"kind,omitempty"`
	// Env: the server's own model (VAULT_LLM_*, of a speech entry VAULT_STT_*):
	// its address, model and key are the environment's, so the entry can be
	// reordered and switched off but not changed or deleted (it comes back).
	Env bool   `json:"env,omitempty"`
	URL string `json:"url"`
	// Model: the server's own entry keeps the admin's choice among the
	// endpoint's models here ("": the environment's VAULT_LLM_MODEL, which is
	// only its default; answers carry the one in force).
	Model string `json:"model"`
	// ModelDefault (answers only, the server's own entry): the environment's
	// model, the one the entry asks while none is chosen.
	ModelDefault string `json:"modelDefault,omitempty"`
	// Key is write-only: a request's empty one keeps the stored key of the
	// same entry (while its address stays on the same host); answers never
	// carry it, KeySet says there is one.
	Key    string `json:"key,omitempty"`
	KeySet bool   `json:"keySet"`
	// KeyFromEnv: an entry on the server's own address (a LiteLLM added from
	// it) asks with the server's key (VAULT_LLM_KEY), which is never copied
	// here. It holds only while the address stays on the server's host.
	KeyFromEnv bool            `json:"keyFromEnv,omitempty"`
	Tiers      []channels.Tier `json:"tiers"`
	// Off: kept in the list, skipped («Выключена»).
	Off bool `json:"off,omitempty"`
	// Status is what the server last learned of the entry, by a check or a
	// real task call (answers: State is also computed, see aiRuntime.status).
	Status AIStatus `json:"status"`
}

// AIStatus: the last outcome of asking a model. State: ok | error | noKey
// (computed: the provider needs a key and none is stored) | unchecked (added
// or changed, never asked) | off (computed).
type AIStatus struct {
	State string `json:"state"`
	MS    int64  `json:"ms,omitempty"`
	// Kind and Code sort an error for the page to word it (channels.ErrorInfo);
	// Error is the provider's own words, cut short.
	Kind   string    `json:"kind,omitempty"`
	Code   int       `json:"code,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at,omitzero"`
	Source string    `json:"source,omitempty"` // check | task
}

// Status states and sources.
const (
	aiOK        = "ok"
	aiError     = "error"
	aiNoKey     = "noKey"
	aiUnchecked = "unchecked"
	aiOff       = "off"

	aiByCheck = "check"
	aiByTask  = "task"

	aiEnvID       = "env"
	aiEnvSpeechID = "env-stt"

	kindSpeech = "speech"
	// maxAIModels: the longest list.
	maxAIModels = 20
)

var allTiers = []channels.Tier{channels.TierLight, channels.TierMedium, channels.TierPowerful}

// isZero: no «ИИ-помощник» in the request (a client that does not know it).
func (a AISettings) isZero() bool { return !a.sent }

// asksTheSame: a task would ask the same models, in the same order, as with
// b (what each last answered aside).
func (a AISettings) asksTheSame(b AISettings) bool {
	asked := func(s AISettings) []AIModel {
		list := slices.Clone(s.List)
		for i := range list {
			list[i].Status, list[i].KeySet = AIStatus{}, false
		}
		return list
	}
	return a.Off == b.Off && a.sameFeatures(b) && slices.EqualFunc(asked(a), asked(b), func(x, y AIModel) bool { return reflect.DeepEqual(x, y) })
}

// sameHost: both addresses name the same host and port.
func sameHost(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" && strings.EqualFold(ua.Host, ub.Host)
}

// needsKey: the provider an address belongs to asks for a key (Anthropic,
// Gemini, OpenAI, Groq, OpenRouter); a LiteLLM, an own server or an Ollama
// may have none.
func needsKey(raw string) bool {
	if channels.IsAnthropicURL(raw) {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "generativelanguage.googleapis.com", "api.openai.com", "api.groq.com", "openrouter.ai":
		return true
	}
	return false
}

// AIModels are the three models of the endpoint of before the list: light
// (quick, cheap, many small calls: naming channel categories), medium
// (reading a request: «Опишите канал»), powerful (the hardest tasks).
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

// detID is the id of an entry made from a stored row of the old shape: the
// same on every read, so statuses and the page's rows keep their entry
// until the first save writes it down.
func detID(url, model string) string {
	h := sha256.Sum256([]byte(url + "|" + model))
	return hex.EncodeToString(h[:4])
}

func newAIID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// legacyList: the list of an endpoint of before it — one entry per
// distinct model, in the order medium, light, powerful, each serving the
// tiers that pointed at it.
func legacyList(a AISettings) []AIModel {
	a = a.withTiers()
	var list []AIModel
	for _, t := range []channels.Tier{channels.TierMedium, channels.TierLight, channels.TierPowerful} {
		model := a.Models.model(t)
		if model == "" {
			continue
		}
		if i := slices.IndexFunc(list, func(e AIModel) bool { return e.Model == model }); i >= 0 {
			list[i].Tiers = append(list[i].Tiers, t)
			continue
		}
		list = append(list, AIModel{ID: detID(a.URL, model), URL: a.URL, Model: model, Key: a.Key, Tiers: []channels.Tier{t}})
	}
	for i := range list {
		list[i].Tiers = normalizeTiers(list[i].Tiers)
	}
	return list
}

// normalizeTiers: the known tiers, once each, in the order of allTiers;
// none left is every tier.
func normalizeTiers(ts []channels.Tier) []channels.Tier {
	out := make([]channels.Tier, 0, len(allTiers))
	for _, t := range allTiers {
		if slices.Contains(ts, t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return slices.Clone(allTiers)
	}
	return out
}

// serves: the entry takes tasks of weight t.
func (e AIModel) serves(t channels.Tier) bool { return slices.Contains(e.Tiers, t) }

// speech: the entry is speech recognition.
func (e AIModel) speech() bool { return e.Kind == kindSpeech }

// kindOf: a client's kind, as it is kept.
func kindOf(k string) string {
	if k == kindSpeech {
		return kindSpeech
	}
	return ""
}

// envID is the id of the server's own entry of a kind.
func envID(kind string) string {
	if kind == kindSpeech {
		return aiEnvSpeechID
	}
	return aiEnvID
}

// envOf is the server's own address, usual model and key for an own entry of
// a kind (ok: it has one — VAULT_LLM_* for a language model, VAULT_STT_* for
// speech).
func envOf(kind string, env *channels.LLM) (url, model, key string, ok bool) {
	if kind == kindSpeech {
		c, ok := transcript.ConfigFromEnv()
		return c.URL, c.Model, c.Key, ok
	}
	if env == nil {
		return "", "", "", false
	}
	return env.URL, env.Model, env.Key, true
}

// migrateSTT puts the stored «Распознавание речи» of before the list into a
// speech entry, last in the list: a provider of its own is that entry, and
// the server's own entry then stays off (the old setting did not ask it);
// «Выключено» is the server's own entry off. The written-back row has no
// «stt» (Load clears it).
func (a AISettings) migrateSTT(s STTSettings) AISettings {
	if s.Provider == "" {
		return a
	}
	off := AIModel{ID: aiEnvSpeechID, Kind: kindSpeech, Env: true, Off: true, Tiers: slices.Clone(allTiers)}
	if s.Provider != sttOff {
		c := transcript.Config{Provider: s.Provider, URL: s.URL, Model: s.Model, Key: s.Key}.Resolve()
		a.List = append(slices.Clone(a.List), AIModel{ID: detID("stt|"+c.URL, c.Model), Kind: kindSpeech, URL: c.URL, Model: c.Model, Key: c.Key, Tiers: slices.Clone(allTiers)})
	}
	if !slices.ContainsFunc(a.List, func(e AIModel) bool { return e.Env && e.speech() }) {
		a.List = append(slices.Clone(a.List), off)
	}
	return a
}

// migrated reads a stored row of the old shape (one endpoint, three
// models) as the list; the written-back row has no old fields. The server's
// own model is not stored here: resolved adds it.
func (a AISettings) migrated() AISettings {
	if a.URL != "" && len(a.List) == 0 {
		a.List = legacyList(a)
	}
	a.URL, a.Model, a.Models, a.Key = "", "", AIModels{}, ""
	return a
}

// resolved is the list as it runs: the server's own model (env, nil: none)
// is an entry — kept where the admin put it, last when new — with the
// environment's address, model and key; without VAULT_LLM_* there is none.
// Never saved (the key is the environment's).
func (a AISettings) resolved(env *channels.LLM) AISettings {
	list := make([]AIModel, 0, len(a.List)+2)
	has := map[string]bool{}
	for _, e := range a.List {
		if e.Env {
			e.Kind = kindOf(e.Kind)
			url, _, key, ok := envOf(e.Kind, env)
			if !ok || has[e.Kind] {
				continue
			}
			has[e.Kind] = true
			e.ID, e.URL, e.Model, e.Key = envID(e.Kind), url, e.modelOf(env), key
		}
		list = append(list, e)
	}
	for _, kind := range []string{"", kindSpeech} {
		if url, model, key, ok := envOf(kind, env); ok && !has[kind] {
			list = append(list, AIModel{ID: envID(kind), Kind: kind, Env: true, URL: url, Model: model, Key: key, Tiers: slices.Clone(allTiers)})
		}
	}
	a.List = list
	return a
}

// modelOf: the model an own entry asks — the admin's choice, else the
// environment's.
func (e AIModel) modelOf(env *channels.LLM) string {
	if m := strings.TrimSpace(e.Model); m != "" {
		return m
	}
	_, model, _, _ := envOf(e.Kind, env)
	return model
}

// key is the key an entry asks with: its own, or the server's (KeyFromEnv,
// while the address is still the server's).
func (e AIModel) key(env *channels.LLM) string {
	if e.Env {
		_, _, key, _ := envOf(e.Kind, env)
		return key
	}
	if e.KeyFromEnv && env != nil && sameHost(e.URL, env.URL) {
		return env.Key
	}
	return e.Key
}

// keyMissing: the provider wants a key and the entry has none.
func (e AIModel) keyMissing(env *channels.LLM) bool {
	if e.speech() {
		return !e.Env && transcript.ProviderOf(e.URL) != transcript.ProviderCustom && e.key(env) == ""
	}
	return !e.Env && needsKey(e.URL) && e.key(env) == ""
}

// mirror: the first own model that is on, in the old shape, for a client
// that knows no list (see AISettings).
func mirror(list []AIModel) (url, model string, models AIModels) {
	i := slices.IndexFunc(list, func(e AIModel) bool { return !e.Env && !e.Off && !e.speech() })
	if i < 0 {
		return "", "", AIModels{}
	}
	url = list[i].URL
	pick := func(t channels.Tier) string {
		for _, e := range list {
			if !e.Env && !e.Off && !e.speech() && e.URL == url && e.serves(t) {
				return e.Model
			}
		}
		return ""
	}
	a := AISettings{URL: url, Models: AIModels{Light: pick(channels.TierLight), Medium: pick(channels.TierMedium), Powerful: pick(channels.TierPowerful)}}.withTiers()
	return url, a.Model, a.Models
}

// validate returns a Russian message for an invalid value.
func (a AISettings) validate() string {
	if len(a.List) > maxAIModels {
		return "Слишком много моделей"
	}
	for _, e := range a.List {
		if e.Env {
			if utf8.RuneCountInString(e.Model) > 120 || strings.ContainsAny(e.Model, "\r\n") {
				return "Слишком длинное название модели"
			}
			continue
		}
		switch {
		case e.URL == "":
			return "Укажите адрес модели"
		case e.Model == "":
			return "Укажите модель"
		case len(e.Key) > 500 || strings.ContainsAny(e.Key, "\r\n "):
			return "Неверный ключ"
		case utf8.RuneCountInString(e.Model) > 120 || strings.ContainsAny(e.Model, "\r\n"):
			return "Слишком длинное название модели"
		case len(e.URL) > 500:
			return "Адрес модели — http:// или https://"
		}
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "Адрес модели — http:// или https://"
		}
	}
	return ""
}

// entryPrint says an entry is still what it was: the same address, model and
// key. A status, a cool-down belongs to one print; an edit starts afresh.
func entryPrint(e AIModel, env *channels.LLM) string {
	if e.Env {
		url, _, key, ok := envOf(e.Kind, env)
		if !ok {
			return ""
		}
		return url + "|" + e.modelOf(env) + "|" + key
	}
	return e.URL + "|" + e.Model + "|" + e.key(env)
}

// cleanEntry is a client's entry as it is kept: trimmed, the tiers whole;
// an empty key keeps the stored one of the same entry while the address
// stays on the same host (a key belongs to its provider, so moving from
// Gemini to Groq never sends Gemini's key to Groq); the status is the
// stored one while the entry is as it was, and unchecked once it changed.
func cleanEntry(e AIModel, stored *AIModel, env *channels.LLM) AIModel {
	e.Tiers = normalizeTiers(e.Tiers)
	e.KeySet, e.ModelDefault = false, ""
	e.Kind = kindOf(e.Kind)
	if e.speech() {
		e.Tiers, e.KeyFromEnv = slices.Clone(allTiers), false
	}
	if e.Env {
		out := AIModel{ID: envID(e.Kind), Kind: e.Kind, Env: true, Tiers: e.Tiers, Off: e.Off}
		// the choice is kept only when it is not the environment's own model
		// (that one follows the environment)
		if _, usual, _, ok := envOf(e.Kind, env); ok {
			if m := strings.TrimSpace(e.Model); m != usual {
				out.Model = m
			}
		}
		if stored != nil && stored.Env && stored.Kind == e.Kind {
			out.Status = stored.Status
		}
		return out
	}
	e.URL = strings.TrimRight(strings.TrimSpace(e.URL), "/")
	e.Model = strings.TrimSpace(e.Model)
	e.Key = strings.TrimSpace(e.Key)
	e.KeyFromEnv = e.KeyFromEnv && e.Key == "" && env != nil && sameHost(e.URL, env.URL)
	if e.Key == "" && !e.KeyFromEnv && stored != nil && !stored.Env && stored.Kind == e.Kind && sameHost(e.URL, stored.URL) {
		e.Key = stored.Key
	}
	e.Status = AIStatus{}
	if stored != nil && entryPrint(*stored, env) == entryPrint(e, env) {
		e.Status = stored.Status
	}
	return e
}

// merged: what a PUT stores. The list is the client's whole list in order;
// an id that is gone is deleted (the server's own model comes back from the
// environment), an id the server did not make is a new entry with its own.
// A PUT of the old shape (no list: one endpoint, three models) is its
// migration — or, when it is only what the page was reading (the mirror),
// nothing changes. No list and no address clears the models and keeps only
// Off and the server's own entry.
func (a AISettings) merged(stored AISettings, env *channels.LLM) AISettings {
	byID := map[string]*AIModel{}
	for i := range stored.List {
		byID[stored.List[i].ID] = &stored.List[i]
	}
	out := AISettings{Off: a.Off, Features: stored.Features}
	if a.Features != nil {
		out.Features = cleanFeatures(a.Features)
	}
	switch {
	case a.List != nil:
		out.List = a.List
	case a.URL != "":
		a.URL = strings.TrimRight(strings.TrimSpace(a.URL), "/")
		a = a.withTiers()
		if url, _, models := mirror(stored.List); a.URL == url && a.Models == models && strings.TrimSpace(a.Key) == "" {
			stored.Off = false
			if a.Features != nil {
				stored.Features = out.Features
			}
			return stored
		}
		if a.Key = strings.TrimSpace(a.Key); a.Key == "" {
			for _, e := range stored.List {
				if !e.Env && sameHost(a.URL, e.URL) {
					a.Key = e.Key
					break
				}
			}
		}
		out.List = legacyList(a)
		for _, e := range stored.List {
			if e.Env {
				out.List = append(out.List, e)
			}
		}
		out.Off = false // an address is a model chosen
	default:
		for _, e := range stored.List {
			if e.Env {
				out.List = append(out.List, e)
			}
		}
	}
	seen := map[string]bool{}
	list := make([]AIModel, 0, len(out.List))
	hasEnv := map[string]bool{}
	for _, e := range out.List {
		var old *AIModel
		switch o := byID[e.ID]; {
		case e.Env:
			e.Kind = kindOf(e.Kind)
			if _, _, _, ok := envOf(e.Kind, env); !ok || hasEnv[e.Kind] {
				continue
			}
			hasEnv[e.Kind], e.ID, old = true, envID(e.Kind), byID[envID(e.Kind)]
		case o != nil && !o.Env && !seen[e.ID]:
			// the kind of an entry is not edited: a language model stays one
			old, e.Kind = o, o.Kind
		default:
			// a new entry: its id is the server's
			for e.ID = newAIID(); byID[e.ID] != nil || seen[e.ID]; e.ID = newAIID() {
			}
		}
		e = cleanEntry(e, old, env)
		seen[e.ID] = true
		list = append(list, e)
	}
	out.List = list
	return out
}
