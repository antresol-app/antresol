package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/transcript"
)

// STTSettings is «Распознавание речи» as it was stored before it became
// entries of the model list (AIModel, Kind "speech"): provider groq, openai,
// gemini or custom (an OpenAI-compatible server at URL), "off", or "": the
// server's own (VAULT_STT_*). A stored row is migrated into a speech entry on
// read (AISettings.migrateSTT) and the field is empty from then on; answers
// still carry it for one release, made from the entries (legacySTT), for a
// client that does not know the list's speech entries. A request's is
// ignored.
type STTSettings struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Model    string `json:"model"`
	Key      string `json:"key,omitempty"`
}

const sttOff = "off"

// UnmarshalJSON takes «stt» whole: a field a request leaves out is empty.
func (s *STTSettings) UnmarshalJSON(b []byte) error {
	type plain STTSettings
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = STTSettings(p)
	return nil
}

// legacySTT is the old «stt» of answers, read off the list: the first speech
// entry that is on (its provider; the address only for a server of one's
// own), else «off» when there are speech entries and none is on, else the
// server's own («»). The key flag and the server's own model go beside it.
func legacySTT(list []AIModel) (stt STTSettings, keySet bool, fromEnv string) {
	seen := false
	for _, e := range list {
		if !e.speech() {
			continue
		}
		seen = true
		if e.Env {
			fromEnv = e.ModelDefault
		}
		if e.Off || stt.Provider != "" {
			continue
		}
		if e.Env {
			stt.Provider, stt.Model = "", ""
			continue
		}
		stt = STTSettings{Provider: transcript.ProviderOf(e.URL), Model: e.Model}
		if stt.Provider == transcript.ProviderCustom {
			stt.URL = e.URL
		}
		keySet = e.KeySet
	}
	if seen && stt.Provider == "" {
		on := false
		for _, e := range list {
			on = on || e.speech() && !e.Off
		}
		if !on {
			stt.Provider = sttOff
		}
	}
	return stt, keySet, fromEnv
}

// SpeechSource is the speech-to-text services asked now, in the list's order
// (false: none, the transcripts are off): the speech entries that are on and
// have their key — the server's own (VAULT_STT_*) one of them. The assistant
// off, or the transcripts feature switched off on its page, is none, whatever
// the list or the environment have.
func SpeechSource(db *sql.DB, defaults AppSettings) func(context.Context) (transcript.Speech, bool) {
	rt := aiRuntimeFor(db)
	return func(ctx context.Context) (transcript.Speech, bool) {
		app, err := LoadAppSettings(ctx, db, defaults)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("api: speech settings: %v", err)
			}
			return transcript.Speech{}, false
		}
		if app.AI.Off || !app.AI.featureOn(channels.FeatureTranscripts) {
			return transcript.Speech{}, false
		}
		return rt.speechChain(db, app.AI, channels.LLMFromEnv())
	}
}
