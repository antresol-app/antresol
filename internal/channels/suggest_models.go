package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// maxListedModels: the longest list of models an endpoint's answer is cut to
// (OpenRouter has hundreds).
const maxListedModels = 1000

// ListModels asks an endpoint which models it has (nothing is billed): an
// OpenAI-compatible one (LiteLLM, OpenAI, Groq, OpenRouter, Gemini's layer)
// GET {url}/models, Anthropic's GET /v1/models with x-api-key, an Ollama
// GET /api/tags. The names, the provider's own order for Anthropic (newest
// first) and alphabetical otherwise. client nil: the default one.
func ListModels(ctx context.Context, client *http.Client, base, key string) ([]string, error) {
	return listModels(ctx, client, base, key, false)
}

// ListSpeechModels is ListModels for speech recognition: of a big provider's
// list (OpenAI, Groq) only the speech models — Whisper and the transcribe
// ones; of Gemini's, the Gemini models, which take audio themselves (the
// speech service asks them through their multimodal API, so the address may
// be the native /v1beta one: its OpenAI layer is listed); a LiteLLM's or
// another server's list is its admin's and is listed whole.
func ListSpeechModels(ctx context.Context, client *http.Client, base, key string) ([]string, error) {
	return listModels(ctx, client, base, key, true)
}

// geminiHost: the address is Gemini's.
func geminiHost(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Hostname(), "generativelanguage.googleapis.com")
}

func listModels(ctx context.Context, client *http.Client, base, key string, speech bool) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	l := &LLM{URL: strings.TrimRight(strings.TrimSpace(base), "/"), Key: key}
	if speech && geminiHost(l.URL) && !strings.HasSuffix(l.URL, "/openai") {
		l.URL += "/openai" // the speech address is the native one; the list is the layer's
	}
	var path string
	switch {
	case l.anthropic():
		path = anthropicBase(l.URL) + "/v1/models?limit=1000"
	case l.openAI():
		path = l.URL + "/models"
	default:
		path = l.URL + "/api/tags"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case l.anthropic():
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case key != "":
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatusErr{code: resp.StatusCode, text: fmt.Sprintf("llm: %s %s", resp.Status, clipRunes(strings.TrimSpace(string(raw)), 200, "…"))}
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errEmptyAnswer
	}
	seen := map[string]bool{}
	var names []string
	own := ownList(l.URL)
	// what a big provider's list is cut to: its chat models, or for speech
	// Whisper and the transcribe ones (Gemini's chat models hear too)
	keep := chatModelID
	if speech && !geminiHost(l.URL) {
		keep = speechModelID
	}
	add := func(n string) {
		n = strings.TrimPrefix(strings.TrimSpace(n), "models/") // Gemini's layer names them so
		if own && !keep(n) {
			return
		}
		if n != "" && !seen[n] && len(names) < maxListedModels {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, m := range out.Data {
		add(m.ID)
	}
	for _, m := range out.Models {
		add(m.Name)
	}
	if !l.anthropic() {
		sort.Strings(names)
	}
	return names, nil
}

// ownList: the address is of a big provider whose list holds every kind of
// model (embeddings, speech, images, moderation) beside the chat ones — the
// others (a LiteLLM's aliases, an Ollama's tags, OpenRouter's) are what
// their admin put there and are listed whole.
func ownList(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.openai.com", "api.groq.com", "generativelanguage.googleapis.com":
		return true
	}
	return false
}

// notChat: a word in a model's id that says it does not answer chat
// completions (OpenAI's embeddings, Whisper, TTS, DALL·E, moderation; Groq's
// guard models; Gemini's imagen, veo, embedding and aqa).
var notChat = []string{"embed", "whisper", "tts", "dall-e", "moderation", "transcribe", "imagen", "veo", "image", "sora", "aqa", "guard", "realtime", "codex", "davinci", "babbage", "search-preview"}

func chatModelID(id string) bool {
	l := strings.ToLower(id)
	for _, w := range notChat {
		if strings.Contains(l, w) {
			return false
		}
	}
	return true
}

// speechModelID: a word in a model's id that says it transcribes speech
// (OpenAI's whisper-1 and gpt-4o-transcribe, Groq's whisper-large-v3 and
// distil-whisper).
func speechModelID(id string) bool {
	l := strings.ToLower(id)
	return strings.Contains(l, "whisper") || strings.Contains(l, "transcribe")
}
