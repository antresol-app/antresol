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

// toServer sends everything the client asks for to srv (the address stays
// the Anthropic API's, the way the settings store it).
type toServer struct{ srv *httptest.Server }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = "http", t.srv.Listener.Addr().String()
	return http.DefaultTransport.RoundTrip(r)
}

// anthropicLLM: the model of the settings «https://api.anthropic.com» with
// key, answered by srv.
func anthropicLLM(srv *httptest.Server, key string) *LLM {
	return &LLM{URL: AnthropicURL, Model: "claude-opus-5-5", Key: key, Client: &http.Client{Transport: toServer{srv}}}
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
	l := anthropicLLM(srv, "sk-test")
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
	l := anthropicLLM(srv, "sk-test")
	_, err := l.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("err %v", err)
	}
}

func TestAnthropicPingAndError(t *testing.T) {
	srv := anthropicServer(t, `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`, nil)
	l := anthropicLLM(srv, "sk-test")
	if out, err := l.Ping(context.Background()); err != nil || out != "ok" {
		t.Fatalf("ping %q %v", out, err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer bad.Close()
	l = anthropicLLM(bad, "sk-test")
	if _, err := l.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "401") || err.Error() != "llm: 401 invalid x-api-key" {
		t.Fatalf("err %v", err)
	}
}

const okReply = `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`

// People copy /v1 out of habit (the OpenAI-compatible addresses end in it):
// the SDK adds /v1/messages itself, so only the host is used.
func TestAnthropicAddressWithPathStillCallsV1Messages(t *testing.T) {
	srv := anthropicServer(t, okReply, nil) // fails on any path but /v1/messages
	for _, u := range []string{"https://api.anthropic.com", "https://api.anthropic.com/", "https://api.anthropic.com/v1", "https://api.anthropic.com/v1/",
		"https://api.anthropic.com/v1/messages", "HTTPS://API.ANTHROPIC.COM/v1?x=1"} {
		l := anthropicLLM(srv, "sk-test")
		l.URL = u
		if out, err := l.Ping(context.Background()); err != nil || out != "ok" {
			t.Errorf("%s: %q %v", u, out, err)
		}
	}
}

// The model of the settings is the one the admin set: nothing from the
// server's environment or the user's profile directory joins the request.
func TestAnthropicIgnoresEnvironment(t *testing.T) {
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okReply)
	}))
	defer srv.Close()
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-From-Env: 1")
	t.Setenv("ANTHROPIC_PROFILE", "nope")
	t.Setenv("HOME", t.TempDir())
	l := anthropicLLM(srv, "sk-test")
	if out, err := l.Ping(context.Background()); err != nil || out != "ok" {
		t.Fatalf("ping %q %v", out, err)
	}
	if hdr.Get("X-From-Env") != "" {
		t.Errorf("a header of the environment was sent: %v", hdr)
	}
	if a := hdr.Get("Authorization"); a != "Bearer sk-test" && hdr.Get("X-Api-Key") != "sk-test" {
		t.Errorf("credential %q", a)
	}
	for k, v := range hdr {
		for _, x := range v {
			if strings.Contains(x, "env-key") || strings.Contains(x, "env-token") {
				t.Errorf("the environment's credential was sent in %s", k)
			}
		}
	}
}

// Without a key the request is not sent at all: the SDK would otherwise
// run it on whatever credential the host has.
func TestAnthropicRefusesAnEmptyKey(t *testing.T) {
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked++ }))
	defer srv.Close()
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	for _, key := range []string{"", "  "} {
		_, err := anthropicLLM(srv, key).Ping(context.Background())
		if err == nil || !strings.Contains(err.Error(), "key") {
			t.Errorf("key %q: %v", key, err)
		}
	}
	if asked != 0 {
		t.Fatalf("%d requests went out without a key", asked)
	}
}

// AskSchema holds the answer to the schema with structured outputs
// (output_config.format), beside the effort.
func TestAnthropicAskSchema(t *testing.T) {
	var got map[string]any
	srv := anthropicServer(t, `{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[{"type":"text","text":"{\"entries\":[]}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, &got)
	schema := map[string]any{"type": "object", "properties": map[string]any{"entries": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}},
		"required": []string{"entries"}, "additionalProperties": false}
	out, err := anthropicLLM(srv, "sk-test").AskSchema(context.Background(), "sys", "user", schema)
	if err != nil || out != `{"entries":[]}` {
		t.Fatalf("answer %q, %v", out, err)
	}
	oc, _ := got["output_config"].(map[string]any)
	f, _ := oc["format"].(map[string]any)
	if f["type"] != "json_schema" || f["schema"] == nil || oc["effort"] != "low" {
		t.Errorf("output_config: %v", got["output_config"])
	}
}

// An OpenAI-compatible endpoint gets a strict json_schema response format;
// one that refuses it (400) is asked again for a plain JSON object.
func TestOpenAIAskSchemaFallsBack(t *testing.T) {
	var formats []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		formats = append(formats, body["response_format"])
		if f, _ := body["response_format"].(map[string]any); f["type"] == "json_schema" {
			http.Error(w, `{"error":"response_format json_schema not supported"}`, http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"entries\":[]}"}}]}`)
	}))
	t.Cleanup(srv.Close)
	l := &LLM{URL: srv.URL + "/v1", Model: "m"}
	out, err := l.AskSchema(context.Background(), "sys", "user", map[string]any{"type": "object"})
	if err != nil || out != `{"entries":[]}` || len(formats) != 2 {
		t.Fatalf("answer %q, %v, formats %v", out, err, formats)
	}
	if f, _ := formats[1].(map[string]any); f["type"] != "json_object" {
		t.Errorf("fallback format %v", formats[1])
	}
}
