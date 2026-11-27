package transcript

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func piece() Audio {
	return Audio{Data: []byte("mp3 bytes"), Name: "piece.mp3", MIME: "audio/mp3", DurationMs: 60_000}
}

func client(url string, c Config) *Client {
	c.URL = url
	return &Client{Config: c}
}

func custom(url string) *Client {
	return client(url+"/v1", Config{Provider: ProviderCustom, Model: "whisper-x", Key: "sk-secret"})
}

func answer(t *testing.T, status int, body string, hdr ...string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(hdr); i += 2 {
			w.Header().Set(hdr[i], hdr[i+1])
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestConfigResolve(t *testing.T) {
	for _, c := range []struct {
		in         Config
		url, model string
		ready      bool
	}{
		{Config{Provider: ProviderGroq, Key: "k", URL: "http://elsewhere"}, "https://api.groq.com/openai/v1", "whisper-large-v3-turbo", true},
		{Config{Provider: ProviderOpenAI, Key: "k"}, "https://api.openai.com/v1", "whisper-1", true},
		{Config{Provider: ProviderGemini, Key: "k"}, "https://generativelanguage.googleapis.com/v1beta", "gemini-3.5-flash-lite", true},
		{Config{Provider: ProviderGroq, Model: "whisper-large-v3"}, "https://api.groq.com/openai/v1", "whisper-large-v3", false},
		{Config{Provider: ProviderCustom, URL: "http://box:9000/v1/", Model: "w"}, "http://box:9000/v1", "w", true},
		{Config{Provider: ProviderCustom, URL: "http://box:9000/v1"}, "http://box:9000/v1", "", false},
		{Config{Provider: ProviderCustom, Model: "w"}, "", "w", false},
		{Config{Provider: "nobody", URL: "http://x", Model: "w", Key: "k"}, "", "w", false},
		{Config{}, "", "", false},
	} {
		got := c.in.Resolve()
		if got.URL != c.url || got.Model != c.model || got.Ready() != c.ready {
			t.Errorf("%+v → url %q model %q ready %v", c.in, got.URL, got.Model, got.Ready())
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	set := func(provider, url, model, key string) {
		t.Setenv("VAULT_STT_PROVIDER", provider)
		t.Setenv("VAULT_STT_URL", url)
		t.Setenv("VAULT_STT_MODEL", model)
		t.Setenv("VAULT_STT_KEY", key)
	}
	set("", "", "m", "k")
	if c, ok := ConfigFromEnv(); ok {
		t.Fatalf("nothing set: %+v", c)
	}
	set("groq", "", "", "gsk")
	if c, ok := ConfigFromEnv(); !ok || c != (Config{Provider: ProviderGroq, URL: "https://api.groq.com/openai/v1", Model: "whisper-large-v3-turbo", Key: "gsk"}) {
		t.Fatalf("groq: %+v %v", c, ok)
	}
	// an address alone is a server of one's own
	set("", "http://proxy:4000/v1/", "stt", "")
	if c, ok := ConfigFromEnv(); !ok || c != (Config{Provider: ProviderCustom, URL: "http://proxy:4000/v1", Model: "stt"}) {
		t.Fatalf("custom: %+v %v", c, ok)
	}
	// a fixed provider's key never goes to another address
	set("openai", "http://proxy:4000/v1", "", "sk")
	if c, ok := ConfigFromEnv(); !ok || c.URL != "https://api.openai.com/v1" {
		t.Fatalf("openai: %+v %v", c, ok)
	}
	set("groq", "", "", "")
	if c, ok := ConfigFromEnv(); ok {
		t.Fatalf("no key: %+v", c)
	}
}

// An OpenAI-compatible /audio/transcriptions: the multipart request and
// the verbose_json answer.
func TestTranscribeOpenAICompatible(t *testing.T) {
	var got map[string]string
	var file, auth, path string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		got = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			got[k] = strings.Join(v, ",")
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		b, _ := io.ReadAll(f)
		file = h.Filename + " " + h.Header.Get("Content-Type") + " " + string(b)
		_, _ = io.WriteString(w, `{"task":"transcribe","language":"Russian","duration":60,"text":"Старик. Море.",
			"segments":[{"id":1,"start":4.2,"end":8,"text":" Море. "},{"id":0,"start":0,"end":4.2,"text":" Старик."},
			{"id":2,"start":9,"end":9.5,"text":"  "},{"id":3,"start":58,"end":75,"text":"Конец."},{"id":4,"start":80,"end":81,"text":"за краем"}]}`)
	}))
	defer s.Close()
	res, err := custom(s.URL).Transcribe(context.Background(), piece())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/audio/transcriptions" || auth != "Bearer sk-secret" || file != "piece.mp3 audio/mp3 mp3 bytes" {
		t.Errorf("request: %q %q %q", path, auth, file)
	}
	if want := (map[string]string{"model": "whisper-x", "response_format": "verbose_json", "temperature": "0"}); !reflect.DeepEqual(got, want) {
		t.Errorf("fields: %v", got)
	}
	want := Result{Language: "ru", Timed: true, Segments: []Segment{{0, 4200, "Старик."}, {4200, 8000, "Море."}, {58000, 60000, "Конец."}}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result: %+v", res)
	}
}

// A server that knows no verbose_json is asked for plain JSON; its text
// has no times of its own, so the piece's are used.
func TestTranscribeWithoutTimes(t *testing.T) {
	var formats []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := r.FormValue("response_format")
		formats = append(formats, f)
		if f == "verbose_json" {
			http.Error(w, `{"error":{"message":"response_format 'verbose_json' is not compatible with this model"}}`, 400)
			return
		}
		_, _ = io.WriteString(w, `{"text":" Весь кусок. "}`)
	}))
	defer s.Close()
	res, err := custom(s.URL).Transcribe(context.Background(), piece())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(formats, []string{"verbose_json", "json"}) {
		t.Errorf("formats %v", formats)
	}
	if res.Timed || !reflect.DeepEqual(res.Segments, []Segment{{0, 60000, "Весь кусок."}}) {
		t.Errorf("result %+v", res)
	}
}

// Silence is a timed answer of nothing.
func TestTranscribeSilence(t *testing.T) {
	s := answer(t, 200, `{"language":"english","text":"","segments":[]}`)
	res, err := custom(s.URL).Transcribe(context.Background(), piece())
	if err != nil || !res.Timed || len(res.Segments) != 0 || res.Language != "en" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTranscribeErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		hdr    []string
		kind   Kind
		after  time.Duration
	}{
		{"rate limit with a header", 429, `{"error":{"message":"slow down"}}`, []string{"Retry-After", "430"}, KindRateLimit, 430 * time.Second},
		{"rate limit with a fraction", 429, `{}`, []string{"Retry-After", "1.5"}, KindRateLimit, 1500 * time.Millisecond},
		{"rate limit in words", 429, `{"error":{"message":"Limit reached. Please try again in 7m12.5s. Visit…"}}`, nil, KindRateLimit, 7*time.Minute + 12500*time.Millisecond},
		{"rate limit without a time", 429, `{}`, nil, KindRateLimit, 0},
		{"unavailable until", 503, `busy`, []string{"Retry-After", "20"}, KindRateLimit, 20 * time.Second},
		{"bad key", 401, `{"error":{"message":"Incorrect API key provided: sk-secret"}}`, nil, KindProvider, 0},
		{"forbidden", 403, `{}`, nil, KindProvider, 0},
		{"no such address", 404, `not found`, nil, KindProvider, 0},
		{"server error", 502, `bad gateway`, nil, KindProvider, 0},
		{"unknown model", 400, `{"error":{"message":"Invalid model name passed in model=whisper-x"}}`, nil, KindProvider, 0},
		{"too large", 413, `{"error":{"message":"file too large"}}`, nil, KindInput, 0},
		{"bad audio", 400, `{"error":{"message":"could not decode the audio"}}`, nil, KindInput, 0},
		{"not an answer", 200, `<html>`, nil, KindProvider, 0},
		{"redirect", 302, ``, []string{"Location", "http://elsewhere.invalid/"}, KindProvider, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := answer(t, c.status, c.body, c.hdr...)
			_, err := custom(s.URL).Transcribe(context.Background(), piece())
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%v", err)
			}
			if e.Kind != c.kind || e.RetryAfter != c.after {
				t.Errorf("kind %v after %v: %v", e.Kind, e.RetryAfter, err)
			}
			if strings.Contains(err.Error(), "sk-secret") {
				t.Errorf("the key in the message: %v", err)
			}
		})
	}
}

// The key goes to the configured address only: a redirect is not followed.
func TestTranscribeNeverFollowsRedirects(t *testing.T) {
	var asked bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { asked = true }))
	defer other.Close()
	s := answer(t, 307, ``, "Location", other.URL)
	for _, p := range []string{ProviderCustom, ProviderGemini} {
		c := client(s.URL, Config{Provider: p, Model: "m", Key: "k"})
		c.HTTP = &http.Client{}
		if _, err := c.Transcribe(context.Background(), piece()); err == nil {
			t.Errorf("%s: no error", p)
		}
	}
	if asked {
		t.Fatal("the redirect was followed")
	}
}

func TestTranscribeUnreachableAndCancelled(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	s.Close()
	_, err := custom(s.URL).Transcribe(context.Background(), piece())
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindProvider {
		t.Fatalf("unreachable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := custom(s.URL).Transcribe(ctx, piece()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func gemini(url string) *Client {
	return client(url+"/v1beta", Config{Provider: ProviderGemini, Model: "gemini-3.5-flash-lite", Key: "AIza-secret"})
}

func geminiAnswer(text, finish string) string {
	b, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{
		"content":      map[string]any{"parts": []any{map[string]any{"text": "thinking…", "thought": true}, map[string]any{"text": text}}},
		"finishReason": finish}}})
	return string(b)
}

// Gemini: generateContent with the audio inline and the answer held to a
// JSON schema.
func TestTranscribeGemini(t *testing.T) {
	var path, key string
	var body struct {
		Contents []struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"contents"`
		GenerationConfig struct {
			ResponseFormat struct {
				Text struct {
					MIMEType string         `json:"mimeType"`
					Schema   map[string]any `json:"schema"`
				} `json:"text"`
			} `json:"responseFormat"`
		} `json:"generationConfig"`
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("x-goog-api-key")
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("the key elsewhere: %q", r.URL.RawQuery)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, geminiAnswer(`{"language":"ru","segments":[{"start":"00:00","end":"00:04.5","text":"Старик."},
			{"start":"0:00:04.5","end":"00:08","text":"Море."},{"start":"00:59","end":"01:02","text":"Конец."}]}`, "STOP"))
	}))
	defer s.Close()
	res, err := gemini(s.URL).Transcribe(context.Background(), piece())
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1beta/models/gemini-3.5-flash-lite:generateContent" || key != "AIza-secret" {
		t.Errorf("request %q %q", path, key)
	}
	if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 2 || body.Contents[0].Parts[0].Text == "" {
		t.Fatalf("contents %+v", body.Contents)
	}
	d := body.Contents[0].Parts[1].InlineData
	if d == nil || d.MIMEType != "audio/mp3" || d.Data != base64.StdEncoding.EncodeToString([]byte("mp3 bytes")) {
		t.Errorf("audio %+v", d)
	}
	if f := body.GenerationConfig.ResponseFormat.Text; f.MIMEType != "APPLICATION_JSON" || f.Schema["type"] != "object" {
		t.Errorf("format %+v", f)
	}
	want := Result{Language: "ru", Timed: true, Segments: []Segment{{0, 4500, "Старик."}, {4500, 8000, "Море."}, {59000, 60000, "Конец."}}}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result %+v", res)
	}
}

// An endpoint that does not know responseFormat is asked with
// responseMimeType and responseJsonSchema.
func TestTranscribeGeminiOlderSchemaFields(t *testing.T) {
	var configs []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			GenerationConfig map[string]any `json:"generationConfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body.GenerationConfig["responseFormat"]; ok {
			configs = append(configs, "format")
			http.Error(w, `{"error":{"code":400,"message":"Invalid JSON payload received. Unknown name \"responseFormat\" at 'generation_config': Cannot find field.","status":"INVALID_ARGUMENT"}}`, 400)
			return
		}
		if body.GenerationConfig["responseMimeType"] == "application/json" && body.GenerationConfig["responseJsonSchema"] != nil {
			configs = append(configs, "mime")
		}
		_, _ = io.WriteString(w, geminiAnswer(`{"language":"en","segments":[{"start":1,"end":2.5,"text":"Hi."}]}`, "STOP"))
	}))
	defer s.Close()
	res, err := gemini(s.URL).Transcribe(context.Background(), piece())
	if err != nil || !reflect.DeepEqual(res.Segments, []Segment{{1000, 2500, "Hi."}}) || !reflect.DeepEqual(configs, []string{"format", "mime"}) {
		t.Fatalf("%+v %v %v", res, configs, err)
	}
}

// Times a model got wrong are no times: the piece's are used.
func TestTranscribeGeminiBadTimes(t *testing.T) {
	for name, segs := range map[string]string{
		"beyond the recording": `[{"start":"00:01","end":"00:02","text":"a"},{"start":"05:00","end":"05:02","text":"b"}]`,
		"all at zero":          `[{"start":"00:00","end":"00:00","text":"a"},{"start":"00:00","end":"00:00","text":"b"}]`,
		"not times":            `[{"start":"soon","end":"later","text":"a"},{"start":"","end":"","text":"b"}]`,
		"going back":           `[{"start":"00:40","end":"00:41","text":"a"},{"start":"00:02","end":"00:03","text":"b"}]`,
	} {
		s := answer(t, 200, geminiAnswer(`{"language":"en","segments":`+segs+`}`, "STOP"))
		res, err := gemini(s.URL).Transcribe(context.Background(), piece())
		if err != nil || res.Timed || !reflect.DeepEqual(res.Segments, []Segment{{0, 60000, "a b"}}) {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
}

func TestTranscribeGeminiErrors(t *testing.T) {
	for _, c := range []struct {
		name    string
		status  int
		body    string
		kind    Kind
		after   time.Duration
		shorter bool
	}{
		{"rate limit", 429, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"37s"}]}}`, KindRateLimit, 37 * time.Second, false},
		{"bad key", 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`, KindProvider, 0, false},
		{"too long", 200, geminiAnswer(`{"language":"en","segm`, "MAX_TOKENS"), KindInput, 0, true},
		{"recitation", 200, geminiAnswer(``, "RECITATION"), KindInput, 0, true},
		{"blocked", 200, `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`, KindInput, 0, true},
		{"not JSON", 200, geminiAnswer(`I cannot do that`, "STOP"), KindInput, 0, false},
	} {
		s := answer(t, c.status, c.body)
		_, err := gemini(s.URL).Transcribe(context.Background(), piece())
		var e *Error
		if !errors.As(err, &e) || e.Kind != c.kind || e.RetryAfter != c.after || e.Shorter != c.shorter {
			t.Errorf("%s: %v (%+v)", c.name, err, e)
		}
	}
}

func TestLanguageCode(t *testing.T) {
	for in, want := range map[string]string{
		"russian": "ru", "English": "en", "ru": "ru", "RU": "ru", "ru-RU": "ru", "ukrainian": "uk", "german": "de", "": "", "klingon": "",
	} {
		if got := languageCode(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestSilentWAV(t *testing.T) {
	a := Silence(time.Second)
	if a.MIME != "audio/wav" || a.DurationMs != 1000 || len(a.Data) != 44+32000 || string(a.Data[:4]) != "RIFF" || string(a.Data[8:12]) != "WAVE" {
		t.Fatalf("%q %d %d", a.MIME, a.DurationMs, len(a.Data))
	}
	for _, b := range a.Data[44:] {
		if b != 0 {
			t.Fatal("not silent")
		}
	}
}
