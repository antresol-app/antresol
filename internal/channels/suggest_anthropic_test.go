package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func anthropicServer(t *testing.T, reply string, got *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %q, want /v1/messages", r.URL.Path)
		}
		// the v2 SDK sends the key as a bearer token
		if k := r.Header.Get("Authorization"); k != "Bearer sk-test" && r.Header.Get("X-Api-Key") != "sk-test" {
			t.Errorf("no key sent: %q", k)
		}
		raw, _ := io.ReadAll(r.Body)
		if got != nil {
			if err := json.Unmarshal(raw, got); err != nil {
				t.Errorf("body: %v", err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIsAnthropicURL(t *testing.T) {
	for url, want := range map[string]bool{
		"https://api.anthropic.com":    true,
		"https://api.anthropic.com/v1": true,
		"http://127.0.0.1:4000/v1":     false,
		"https://api.openai.com/v1":    false,
		"https://anthropic.com.evil.x": false,
	} {
		if got := IsAnthropicURL(url); got != want {
			t.Errorf("IsAnthropicURL(%q) = %v", url, got)
		}
	}
}

func TestAnthropicSuggestMapping(t *testing.T) {
	var body map[string]any
	answer := `{"name":"Комедии 90-х","kinds":["movie"],"genres":["комедия"],"countries":[],"languages":[],"yearFrom":1990,"yearTo":1999,"keywords":[]}`
	reply, _ := json.Marshal(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
		"stop_reason": "end_turn",
		"content": []map[string]any{
			{"type": "thinking", "thinking": "", "signature": "sig"},
			{"type": "text", "text": "Here it is:\n```json\n" + answer + "\n```"},
		},
		"usage": map[string]int{"input_tokens": 10, "output_tokens": 20},
	})
	srv := anthropicServer(t, string(reply), &body)
	l := &LLM{URL: srv.URL, Model: "claude-opus-5-5", Key: "sk-test", Anthropic: true}
	s, err := l.suggest(context.Background(), "комедии девяностых", Vocabulary{Genres: map[string]bool{"комедия": true}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Комедии 90-х" || s.Rules.YearFrom != 1990 || len(s.Rules.Genres) != 1 {
		t.Fatalf("suggestion %+v", s)
	}
	if body["model"] != "claude-opus-5-5" || body["max_tokens"] != float64(16000) {
		t.Errorf("model/max_tokens: %v %v", body["model"], body["max_tokens"])
	}
	for _, k := range []string{"temperature", "thinking", "response_format"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s must not be sent", k)
		}
	}
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Errorf("output_config %v", body["output_config"])
	}
	sys, _ := body["system"].([]any)
	if len(sys) != 1 || !strings.Contains(sys[0].(map[string]any)["text"].(string), "Genres: комедия") {
		t.Errorf("system %v", body["system"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2*len(llmShots)+1 {
		t.Fatalf("%d messages", len(msgs))
	}
	if m := msgs[1].(map[string]any); m["role"] != "assistant" {
		t.Errorf("second message role %v", m["role"])
	}
	if m := msgs[len(msgs)-1].(map[string]any); m["role"] != "user" {
		t.Errorf("last message role %v", m["role"])
	}
}

func TestAnthropicRefusal(t *testing.T) {
	reply := `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"refusal",
		"stop_details":{"type":"refusal","category":null,"explanation":"policy"},
		"content":[],"usage":{"input_tokens":1,"output_tokens":0}}`
	srv := anthropicServer(t, reply, nil)
	l := &LLM{URL: srv.URL, Model: "claude-opus-5-5", Key: "sk-test", Anthropic: true}
	_, err := l.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("err %v", err)
	}
}

func TestAnthropicPingAndError(t *testing.T) {
	srv := anthropicServer(t, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`, nil)
	l := &LLM{URL: srv.URL, Model: "claude-opus-5-5", Key: "sk-test", Anthropic: true}
	if out, err := l.Ping(context.Background()); err != nil || out != "ok" {
		t.Fatalf("ping %q %v", out, err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer bad.Close()
	l = &LLM{URL: bad.URL, Model: "claude-opus-5-5", Key: "sk-test", Anthropic: true}
	if _, err := l.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "401") || err.Error() != "llm: 401 invalid x-api-key" {
		t.Fatalf("err %v", err)
	}
}
