package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/transcript"
)

// STTSettings is «Распознавание речи» of «ИИ-помощник»: the speech-to-text
// service that makes the audiobooks' transcripts. Provider: groq, openai or
// gemini (their own addresses), custom (an OpenAI-compatible server at
// URL), "off" (none, the server's own included), or "": the server's own
// (VAULT_STT_*), none without it. Model "" is the provider's usual one.
// Key never leaves the server (settingsAPI.load drops it from answers) and
// is only ever sent to the address it was saved with.
type STTSettings struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Model    string `json:"model"`
	Key      string `json:"key,omitempty"`
}

const sttOff = "off"

// UnmarshalJSON takes «stt» whole: a field a request leaves out is empty,
// not the one of another provider that was stored.
func (s *STTSettings) UnmarshalJSON(b []byte) error {
	type plain STTSettings
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = STTSettings(p)
	return nil
}

// config is the settings' service as it is asked.
func (s STTSettings) config() transcript.Config {
	return transcript.Config{Provider: s.Provider, URL: s.URL, Model: s.Model, Key: s.Key}.Resolve()
}

// merged: what a PUT stores — trimmed, an address only for a server of
// one's own; an empty key keeps the stored one (answers never carry it)
// while the provider and the address are the same.
func (s STTSettings) merged(stored STTSettings) STTSettings {
	s.Provider, s.Model, s.Key = strings.TrimSpace(s.Provider), strings.TrimSpace(s.Model), strings.TrimSpace(s.Key)
	s.URL = strings.TrimRight(strings.TrimSpace(s.URL), "/")
	if s.Provider == "" || s.Provider == sttOff {
		return STTSettings{Provider: s.Provider}
	}
	if s.Provider != transcript.ProviderCustom {
		s.URL = ""
	}
	if s.Key == "" && s.Provider == stored.Provider && s.config().URL == stored.config().URL {
		s.Key = stored.Key
	}
	return s
}

// validate returns a Russian message for an invalid value. A key may still
// be missing: the provider is chosen first, then its key typed.
func (s STTSettings) validate() string {
	switch s.Provider {
	case "", sttOff:
		return ""
	case transcript.ProviderGroq, transcript.ProviderOpenAI, transcript.ProviderGemini:
	case transcript.ProviderCustom:
		if s.URL == "" {
			return "Укажите адрес сервера распознавания речи"
		}
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "Адрес распознавания речи — http:// или https://"
		}
		if s.Model == "" {
			return "Укажите модель распознавания речи"
		}
	default:
		return "Неизвестный провайдер распознавания речи"
	}
	switch {
	case utf8.RuneCountInString(s.Model) > 120 || strings.ContainsAny(s.Model, "\r\n"):
		return "Слишком длинное название модели"
	case len(s.Key) > 500 || strings.ContainsAny(s.Key, "\r\n\t "):
		return "Неверный ключ распознавания речи"
	}
	return ""
}

// SpeechSource is the speech-to-text service asked now (false: none, the
// transcripts are off): the settings' when one is chosen there, else the
// server's VAULT_STT_*; «Выключено» is none, whatever the environment has.
func SpeechSource(db *sql.DB, defaults AppSettings) func(context.Context) (transcript.Config, bool) {
	return func(ctx context.Context) (transcript.Config, bool) {
		app, err := LoadAppSettings(ctx, db, defaults)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("api: speech settings: %v", err)
			}
			return transcript.Config{}, false
		}
		// the assistant off, or the transcripts switched off on its page: none,
		// whatever the speech settings or the environment have
		if app.AI.Off || !app.AI.featureOn(channels.FeatureTranscripts) {
			return transcript.Config{}, false
		}
		switch app.STT.Provider {
		case "":
			return transcript.ConfigFromEnv()
		case sttOff:
			return transcript.Config{}, false
		}
		c := app.STT.config()
		if !c.Ready() {
			return transcript.Config{}, false
		}
		return c, true
	}
}

// sttTestTimeout bounds «Проверить» of the speech service.
const sttTestTimeout = time.Minute

// POST /api/settings/stt/test {provider, url, model, key}: sends a second
// of silence to be transcribed — {ok, ms, model, error?}. An empty key is
// the stored one (for the stored address only); no provider: the server's
// own. Nothing is saved.
func (s *settingsAPI) testSTT(w http.ResponseWriter, r *http.Request) {
	var body STTSettings
	if !decode(w, r, &body) {
		return
	}
	stored, err := LoadAppSettings(r.Context(), s.db, s.defaults)
	if err != nil {
		internalErr(w, r, "settings", err)
		return
	}
	a := body.merged(stored.STT)
	var cfg transcript.Config
	msg := a.validate()
	switch {
	case msg != "":
	case a.Provider == sttOff:
		msg = "Распознавание речи выключено"
	case a.Provider == "":
		env, ok := transcript.ConfigFromEnv()
		if cfg = env; !ok {
			msg = "Выберите провайдера распознавания речи"
		}
	default:
		if cfg = a.config(); !cfg.Ready() {
			msg = "Для этого провайдера нужен ключ API"
		}
	}
	if msg != "" {
		writeErr(w, r, http.StatusBadRequest, msg)
		return
	}
	start := time.Now()
	client := &transcript.Client{Config: cfg, Timeout: sttTestTimeout}
	_, err = client.Transcribe(r.Context(), transcript.Silence(time.Second))
	out := map[string]any{"ok": err == nil, "ms": time.Since(start).Milliseconds(), "model": cfg.Model}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
