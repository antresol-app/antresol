package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/transcript"
)

// fakeTranscripts counts the worker's «the services changed».
type fakeTranscripts struct {
	mu sync.Mutex
	n  int
}

func (f *fakeTranscripts) Reconfigured(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return nil
}

func (f *fakeTranscripts) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// speechServer answers a transcription when the key is want.
type speechServer struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
	files []string
}

func newSpeechServer(t *testing.T, want string) *speechServer {
	s := &speechServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		f, h, err := r.FormFile("file")
		var file string
		if err == nil {
			b, _ := io.ReadAll(f)
			file = h.Filename + " " + string(b[:min(4, len(b))])
		}
		s.mu.Lock()
		s.auths, s.files = append(s.auths, auth), append(s.files, file)
		s.mu.Unlock()
		if want != "" && auth != "Bearer "+want {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"message":"bad key `+strings.TrimPrefix(auth, "Bearer ")+`"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"text":"","segments":[]}`)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *speechServer) lastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.auths) == 0 {
		return "(not asked)"
	}
	return s.auths[len(s.auths)-1]
}

type sttView struct {
	STT struct {
		Provider, URL, Model, Key string
	} `json:"stt"`
	STTKeySet  bool   `json:"sttKeySet"`
	STTFromEnv string `json:"sttFromEnv"`
}

type sttEnv struct {
	*libEnv
	db     *sql.DB
	worker *fakeTranscripts
}

func newSTTEnv(t *testing.T) *sttEnv {
	e := &sttEnv{worker: &fakeTranscripts{}}
	e.libEnv = newLibEnvWith(t, func(d *Deps, x *sql.DB) {
		e.db, d.DB, d.Transcripts = x, x, e.worker
	})
	return e
}

func noSpeechEnv(t *testing.T) {
	for _, k := range []string{"VAULT_STT_PROVIDER", "VAULT_STT_URL", "VAULT_STT_MODEL", "VAULT_STT_KEY"} {
		t.Setenv(k, "")
	}
}

// put saves the settings with «stt» as given ("" leaves it out).
func (e *sttEnv) put(stt string) *httptest.ResponseRecorder {
	e.t.Helper()
	body := `{"quality":1080,"voice":"dub","streamCacheGB":50`
	if stt != "" {
		body += `,"stt":` + stt
	}
	return e.do("PUT", "/api/settings", body+`}`, e.admin)
}

func (e *sttEnv) save(stt string) sttView {
	e.t.Helper()
	if w := e.put(stt); w.Code != 200 {
		e.t.Fatalf("put %s: %d %s", stt, w.Code, w.Body)
	}
	return e.view()
}

func (e *sttEnv) view() sttView {
	e.t.Helper()
	w := e.do("GET", "/api/settings", "", e.admin)
	var v sttView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		e.t.Fatal(err, w.Body)
	}
	if v.STT.Key != "" || strings.Contains(w.Body.String(), "gsk-secret") {
		e.t.Fatalf("a key in the answer: %s", w.Body)
	}
	return v
}

// stored is the configuration the worker is given.
func (e *sttEnv) config() (transcript.Config, bool) {
	return SpeechSource(e.db, AppSettings{})(context.Background())
}

func TestSTTSettings(t *testing.T) {
	noSpeechEnv(t)
	e := newSTTEnv(t)
	if v := e.view(); v.STT.Provider != "" || v.STTKeySet || v.STTFromEnv != "" {
		t.Fatalf("default %+v", v)
	}
	if c, on := e.config(); on {
		t.Fatalf("off by default: %+v", c)
	}
	// a provider chosen before its key is typed saves, and asks nothing yet
	if v := e.save(`{"provider":"groq"}`); v.STT.Provider != "groq" || v.STTKeySet {
		t.Fatalf("%+v", v)
	}
	if _, on := e.config(); on {
		t.Fatal("on without a key")
	}
	n := e.worker.count()
	if v := e.save(`{"provider":"groq","url":"http://elsewhere.invalid","key":" gsk-secret "}`); !v.STTKeySet || v.STT.URL != "" {
		t.Fatalf("%+v", v)
	}
	if c, on := e.config(); !on || c != (transcript.Config{Provider: "groq", URL: "https://api.groq.com/openai/v1", Model: "whisper-large-v3-turbo", Key: "gsk-secret"}) {
		t.Fatalf("%+v %v", c, on)
	}
	if e.worker.count() != n+1 {
		t.Errorf("the worker told %d times", e.worker.count()-n)
	}
	// saved without «stt», or without the key: kept, the worker not told
	for _, body := range []string{"", `{"provider":"groq"}`} {
		if v := e.save(body); !v.STTKeySet || v.STT.Provider != "groq" {
			t.Fatalf("%q: %+v", body, v)
		}
	}
	if e.worker.count() != n+1 {
		t.Errorf("told of no change")
	}
	// a key never moves to another provider, nor to another address
	for _, body := range []string{`{"provider":"openai"}`, `{"provider":"gemini"}`} {
		e.save(`{"provider":"groq","key":"gsk-secret"}`)
		if v := e.save(body); v.STTKeySet {
			t.Fatalf("%s: the key moved: %+v", body, v)
		}
		if _, on := e.config(); on {
			t.Fatalf("%s: on", body)
		}
	}
	e.save(`{"provider":"custom","url":"http://a.invalid:4000/v1","model":"stt","key":"gsk-secret"}`)
	if v := e.save(`{"provider":"custom","url":"http://a.invalid:4000/v1/","model":"other"}`); !v.STTKeySet {
		t.Fatalf("the same address: %+v", v)
	}
	if v := e.save(`{"provider":"custom","url":"http://b.invalid:4000/v1","model":"stt"}`); v.STTKeySet || v.STT.URL != "http://b.invalid:4000/v1" {
		t.Fatalf("another address: %+v", v)
	}
	if c, on := e.config(); !on || c.Key != "" {
		t.Fatalf("a server of one's own needs no key: %+v %v", c, on)
	}
	for body, why := range map[string]string{
		`{"provider":"nobody"}`:                                          "провайдер",
		`{"provider":"custom","model":"m"}`:                              "адрес",
		`{"provider":"custom","url":"http://a.invalid/v1"}`:              "модель",
		`{"provider":"custom","url":"ftp://a.invalid","model":"m"}`:      "http",
		`{"provider":"custom","url":"http://u:p@a.invalid","model":"m"}`: "http",
		`{"provider":"groq","key":"a b"}`:                                "ключ",
		`{"provider":"groq","model":"` + strings.Repeat("m", 200) + `"}`: "модел",
	} {
		w := e.put(body)
		if w.Code != 400 || !strings.Contains(strings.ToLower(w.Body.String()), why) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if v := e.save(`{"provider":"off","url":"http://b.invalid","model":"stt","key":"gsk-secret"}`); v.STT.Provider != "off" || v.STT.URL != "" || v.STT.Model != "" || v.STTKeySet {
		t.Fatalf("off %+v", v)
	}
}

// VAULT_STT_* is the server's own service: used while none is chosen here,
// listed to come back to, and «Выключено» really turns it off.
func TestSTTSettingsFromEnv(t *testing.T) {
	noSpeechEnv(t)
	s := newSpeechServer(t, "env-key")
	t.Setenv("VAULT_STT_URL", s.URL+"/v1")
	t.Setenv("VAULT_STT_MODEL", "server-model")
	t.Setenv("VAULT_STT_KEY", "env-key")
	e := newSTTEnv(t)
	if v := e.view(); v.STTFromEnv != "server-model" || v.STT.Provider != "" {
		t.Fatalf("%+v", v)
	}
	if c, on := e.config(); !on || c.Model != "server-model" || c.Provider != "custom" {
		t.Fatalf("%+v %v", c, on)
	}
	w := e.do("POST", "/api/settings/stt/test", `{}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"model":"server-model"`) || s.lastAuth() != "Bearer env-key" {
		t.Fatalf("%d %s %s", w.Code, w.Body, s.lastAuth())
	}
	// the server's key does not follow an address typed here
	other := newSpeechServer(t, "")
	e.do("POST", "/api/settings/stt/test", `{"provider":"custom","url":"`+other.URL+`/v1","model":"m"}`, e.admin)
	if other.lastAuth() != "" {
		t.Fatalf("the server's key sent on: %q", other.lastAuth())
	}
	e.save(`{"provider":"off"}`)
	if _, on := e.config(); on {
		t.Fatal("on although off")
	}
	if w := e.do("POST", "/api/settings/stt/test", `{"provider":"off"}`, e.admin); w.Code != 400 {
		t.Fatalf("test of off %d %s", w.Code, w.Body)
	}
	if v := e.view(); v.STTFromEnv != "server-model" {
		t.Fatalf("%+v", v)
	}
	e.save(`{"provider":""}`)
	if _, on := e.config(); !on {
		t.Fatal("back to the server's")
	}
}

// «Проверить» sends a silent recording; an empty key is the stored one only
// for the stored address.
func TestSTTSettingsCheck(t *testing.T) {
	noSpeechEnv(t)
	e := newSTTEnv(t)
	a, b := newSpeechServer(t, "gsk-secret"), newSpeechServer(t, "")
	check := func(body string) string {
		t.Helper()
		w := e.do("POST", "/api/settings/stt/test", body, e.admin)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
		return w.Body.String()
	}
	if got := check(`{"provider":"custom","url":"` + a.URL + `/v1","model":"m","key":"gsk-secret"}`); !strings.Contains(got, `"ok":true`) || !strings.Contains(got, `"ms":`) {
		t.Fatal(got)
	}
	if a.files[0] != "silence.wav RIFF" {
		t.Errorf("sent %q", a.files[0])
	}
	if got := check(`{"provider":"custom","url":"` + a.URL + `/v1","model":"m","key":"gsk-wrong"}`); !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "401") || strings.Contains(got, "gsk-wrong") {
		t.Fatal(got)
	}
	if v := e.view(); v.STT.Provider != "" {
		t.Fatalf("a check saved something: %+v", v)
	}
	e.save(`{"provider":"custom","url":"` + a.URL + `/v1","model":"m","key":"gsk-secret"}`)
	if got := check(`{"provider":"custom","url":"` + a.URL + `/v1","model":"m"}`); !strings.Contains(got, `"ok":true`) {
		t.Fatal(got)
	}
	check(`{"provider":"custom","url":"` + b.URL + `/v1","model":"m"}`)
	if b.lastAuth() != "" {
		t.Fatalf("the stored key sent elsewhere: %q", b.lastAuth())
	}
	w := e.do("POST", "/api/settings/stt/test", `{"provider":"groq"}`, e.admin)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "ключ") {
		t.Fatalf("no key: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/settings/stt/test", `{}`, e.kid("")); w.Code != 403 {
		t.Fatalf("not an admin: %d", w.Code)
	}
}
