package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Gemini's OpenAI-compatible address ends in /v1beta/openai, not /v1: it
// must be asked at /chat/completions, not Ollama's /api/chat.
func TestOpenAIEndpointKinds(t *testing.T) {
	for url, want := range map[string]bool{
		"https://api.openai.com/v1":                                true,
		"https://api.groq.com/openai/v1":                           true,
		"https://openrouter.ai/api/v1/":                            true,
		"https://generativelanguage.googleapis.com/v1beta/openai":  true,
		"https://generativelanguage.googleapis.com/v1beta/openai/": true,
		"http://127.0.0.1:4000/v1":                                 true,
		"http://localhost:11434":                                   false,
	} {
		if got := (&LLM{URL: url}).openAI(); got != want {
			t.Errorf("%s: openAI %v, want %v", url, got, want)
		}
	}
}

func TestGeminiAddressAsksChatCompletions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/openai/chat/completions" || r.Header.Get("Authorization") != "Bearer AIza-k" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}}})
	}))
	defer srv.Close()
	l := &LLM{URL: srv.URL + "/v1beta/openai", Model: "gemini-3.5-flash-lite", Key: "AIza-k"}
	got, err := l.chatHTTP(context.Background(), []map[string]string{{"role": "user", "content": "hi"}}, false, nil)
	if err != nil || got != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
}
