package channels

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// fakeRT is a client whose every request is answered by fn: no network.
type fakeRT func(*http.Request) *http.Response

func (f fakeRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func answer(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

// The shapes below are what each provider's list endpoint answers (cut
// short), each asked the way its provider wants.
func TestListModelsProviders(t *testing.T) {
	cases := []struct {
		name, base, key, path, body string
		check                       func(*testing.T, *http.Request)
		want                        []string
	}{
		{
			name: "anthropic: x-api-key and anthropic-version, the provider's own order", base: "https://api.anthropic.com", key: "sk-ant-1",
			path: "/v1/models",
			body: `{"data":[{"type":"model","id":"claude-opus-5-5","display_name":"Claude Opus 5.5","created_at":"2026-09-01T00:00:00Z"},{"type":"model","id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","created_at":"2026-08-01T00:00:00Z"},{"type":"model","id":"claude-haiku-5-5","display_name":"Claude Haiku 5.5","created_at":"2026-07-01T00:00:00Z"}],"has_more":false,"first_id":"claude-opus-5-5","last_id":"claude-haiku-5-5"}`,
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("x-api-key") != "sk-ant-1" || r.Header.Get("anthropic-version") == "" || r.Header.Get("Authorization") != "" {
					t.Errorf("anthropic headers %v", r.Header)
				}
			},
			want: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"},
		},
		{
			name: "anthropic: an address copied with /v1 is the same endpoint", base: "https://api.anthropic.com/v1/", key: "k",
			path: "/v1/models",
			body: `{"data":[{"id":"claude-haiku-5-5"}]}`,
			want: []string{"claude-haiku-5-5"},
		},
		{
			name: "gemini: the OpenAI layer names models/…, the prefix goes, embeddings and images stay out", base: "https://generativelanguage.googleapis.com/v1beta/openai", key: "AIza",
			path: "/v1beta/openai/models",
			body: `{"object":"list","data":[{"id":"models/gemini-3.5-flash","object":"model","owned_by":"google"},{"id":"models/gemini-3.5-flash-lite","object":"model","owned_by":"google"},{"id":"models/text-embedding-004","object":"model","owned_by":"google"},{"id":"models/gemini-embedding-001","object":"model","owned_by":"google"},{"id":"models/imagen-4.0-generate-001","object":"model","owned_by":"google"},{"id":"models/veo-3.0-generate-001","object":"model","owned_by":"google"},{"id":"models/aqa","object":"model","owned_by":"google"},{"id":"models/gemini-3.5-flash-preview-tts","object":"model","owned_by":"google"}]}`,
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer AIza" {
					t.Errorf("gemini auth %q", r.Header.Get("Authorization"))
				}
			},
			want: []string{"gemini-3.5-flash", "gemini-3.5-flash-lite"},
		},
		{
			name: "openai: chat models only, alphabetical", base: "https://api.openai.com/v1", key: "sk-o",
			path: "/v1/models",
			body: `{"object":"list","data":[{"id":"whisper-1","object":"model","owned_by":"openai-internal"},{"id":"gpt-5-mini","object":"model","owned_by":"system"},{"id":"text-embedding-3-small","object":"model","owned_by":"system"},{"id":"dall-e-3","object":"model","owned_by":"system"},{"id":"gpt-5","object":"model","owned_by":"system"},{"id":"tts-1","object":"model","owned_by":"openai-internal"},{"id":"omni-moderation-latest","object":"model","owned_by":"system"},{"id":"gpt-5-codex","object":"model","owned_by":"system"}]}`,
			want: []string{"gpt-5", "gpt-5-mini"},
		},
		{
			name: "groq: its models sit under /openai/v1, speech and guard models stay out", base: "https://api.groq.com/openai/v1", key: "gsk",
			path: "/openai/v1/models",
			body: `{"object":"list","data":[{"id":"whisper-large-v3-turbo","object":"model","active":true,"owned_by":"OpenAI","context_window":448},{"id":"openai/gpt-oss-120b","object":"model","active":true,"owned_by":"OpenAI","context_window":131072},{"id":"meta-llama/llama-guard-4-12b","object":"model","active":true,"owned_by":"Meta"},{"id":"llama-3.3-70b-versatile","object":"model","active":true,"owned_by":"Meta"}]}`,
			want: []string{"llama-3.3-70b-versatile", "openai/gpt-oss-120b"},
		},
		{
			name: "openrouter: listed whole, ids with a slash", base: "https://openrouter.ai/api/v1", key: "",
			path: "/api/v1/models",
			body: `{"data":[{"id":"openai/gpt-5-image","name":"OpenAI: GPT-5 Image"},{"id":"google/gemini-3.5-flash-lite","name":"Google: Gemini 3.5 Flash Lite"}]}`,
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Errorf("a key was invented: %q", r.Header.Get("Authorization"))
				}
			},
			want: []string{"google/gemini-3.5-flash-lite", "openai/gpt-5-image"},
		},
		{
			name: "litellm: its aliases, exactly as the admin named them", base: "http://127.0.0.1:4000/v1", key: "sk-master",
			path: "/v1/models",
			body: `{"data":[{"id":"gemini-flash-lite","object":"model","created":1677610602,"owned_by":"openai"},{"id":"auto","object":"model","owned_by":"openai"},{"id":"local-embedding","object":"model","owned_by":"openai"},{"id":"gemini-flash-lite","object":"model","owned_by":"openai"}],"object":"list"}`,
			check: func(t *testing.T, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer sk-master" {
					t.Errorf("litellm auth %q", r.Header.Get("Authorization"))
				}
			},
			want: []string{"auto", "gemini-flash-lite", "local-embedding"},
		},
		{
			name: "ollama: /api/tags, names with their tags", base: "http://localhost:11434", key: "",
			path: "/api/tags",
			body: `{"models":[{"name":"qwen3.5:2b","model":"qwen3.5:2b","size":1}, {"name":"gemma4:26b","model":"gemma4:26b","size":2}]}`,
			want: []string{"gemma4:26b", "qwen3.5:2b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got *http.Request
			client := &http.Client{Transport: fakeRT(func(r *http.Request) *http.Response {
				got = r
				if r.URL.Path != c.path {
					return answer(404, `{"error":"not found"}`)
				}
				return answer(200, c.body)
			})}
			names, err := ListModels(context.Background(), client, c.base, c.key)
			if err != nil {
				t.Fatalf("%v (asked %v)", err, got.URL)
			}
			if !reflect.DeepEqual(names, c.want) {
				t.Errorf("names %v, want %v", names, c.want)
			}
			if got.Method != http.MethodGet {
				t.Errorf("method %s", got.Method)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

// A refused key is an error with the provider's code (the page words it), an
// answer that is no list is an empty one.
func TestListModelsFailures(t *testing.T) {
	client := func(code int, body string) *http.Client {
		return &http.Client{Transport: fakeRT(func(*http.Request) *http.Response { return answer(code, body) })}
	}
	_, err := ListModels(context.Background(), client(401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`), "https://api.anthropic.com", "bad")
	if kind, code := ErrorInfo(err); err == nil || kind != ErrHTTP || code != 401 {
		t.Errorf("401: %v %v %v", err, kind, code)
	}
	if _, err := ListModels(context.Background(), client(200, `<html>`), "http://x/v1", ""); err == nil {
		t.Error("no JSON, no error")
	}
	names, err := ListModels(context.Background(), client(200, `{"data":[]}`), "http://x/v1", "")
	if err != nil || len(names) != 0 {
		t.Errorf("empty: %v %v", names, err)
	}
}
