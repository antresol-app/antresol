package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/db"
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
	mu     sync.Mutex
	auths  []string
	files  []string
	models []string
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
		s.models = append(s.models, r.FormValue("model"))
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

func (s *speechServer) lastModel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.models) == 0 {
		return "(not asked)"
	}
	return s.models[len(s.models)-1]
}

func (s *speechServer) lastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.auths) == 0 {
		return "(not asked)"
	}
	return s.auths[len(s.auths)-1]
}

// speechList is the settings' list as the speech tests read it (a key never
// comes back).
type speechEntry struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Env        bool     `json:"env"`
	URL        string   `json:"url"`
	Model      string   `json:"model"`
	ModelUsual string   `json:"modelDefault"`
	KeySet     bool     `json:"keySet"`
	Off        bool     `json:"off"`
	Tiers      []string `json:"tiers"`
	Status     AIStatus `json:"status"`
}

type sttView struct {
	AI struct {
		Off  bool          `json:"off"`
		List []speechEntry `json:"list"`
	} `json:"ai"`
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
	for _, k := range []string{"VAULT_STT_PROVIDER", "VAULT_STT_URL", "VAULT_STT_MODEL", "VAULT_STT_KEY", "VAULT_LLM_URL", "VAULT_LLM_MODEL", "VAULT_LLM_KEY"} {
		t.Setenv(k, "")
	}
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

// putList saves the settings with the list as given (the page's PUT).
func (e *sttEnv) putList(list string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":50,"ai":{"list":`+list+`}}`, e.admin)
}

func (e *sttEnv) saveList(list string) sttView {
	e.t.Helper()
	if w := e.putList(list); w.Code != 200 {
		e.t.Fatalf("put %s: %d %s", list, w.Code, w.Body)
	}
	return e.view()
}

// speech is what the worker is given now: the members, first to last.
func (e *sttEnv) speech() []transcript.Member {
	sp, _ := SpeechSource(e.db, AppSettings{})(context.Background())
	return sp.Members
}

func models(ms []transcript.Member) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Config.Model)
	}
	return out
}

func (e *sttEnv) storedRow() map[string]json.RawMessage {
	e.t.Helper()
	raw, _, err := db.GetSetting(context.Background(), e.db, appSettingsKey)
	if err != nil {
		e.t.Fatal(err)
	}
	var row map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &row)
	return row
}

// groqSpeech saves the one Groq speech entry (with its key), on or off.
func (e *sttEnv) groqSpeech(off bool) {
	e.t.Helper()
	id := ""
	if sp := speechOf(e.view()); len(sp) > 0 {
		id = sp[0].ID
	}
	e.saveList(fmt.Sprintf(`[{"id":%q,"kind":"speech","url":"https://api.groq.com/openai/v1","model":"whisper-large-v3-turbo","key":"gsk-secret","off":%v,"tiers":[]}]`, id, off))
}

func speechOf(v sttView) []speechEntry {
	var out []speechEntry
	for _, e := range v.AI.List {
		if e.Kind == "speech" {
			out = append(out, e)
		}
	}
	return out
}

// The stored «Распознавание речи» of before the list is a speech entry on
// read, with its key; the old fields of an answer still say what it was;
// the first save writes the entry and an empty «stt».
func TestSpeechMigratesStoredSTT(t *testing.T) {
	noSpeechEnv(t)
	s := newSpeechServer(t, "gsk-secret")
	e := newSTTEnv(t)
	row := `{"stt":{"provider":"custom","url":"` + s.URL + `/v1/","model":"m1","key":"gsk-secret"}}`
	if err := db.PutSetting(context.Background(), e.db, appSettingsKey, row); err != nil {
		t.Fatal(err)
	}
	v := e.view()
	sp := speechOf(v)
	if len(sp) != 1 || sp[0].URL != s.URL+"/v1" || sp[0].Model != "m1" || !sp[0].KeySet || sp[0].Env || len(sp[0].Tiers) != 3 {
		t.Fatalf("%+v", sp)
	}
	if v.STT.Provider != "custom" || v.STT.Model != "m1" || v.STT.URL != s.URL+"/v1" || !v.STTKeySet {
		t.Fatalf("the old fields: %+v %v", v.STT, v.STTKeySet)
	}
	// the same entry on every read (its status belongs to its id)
	if again := speechOf(e.view()); again[0].ID != sp[0].ID {
		t.Fatalf("the id moved: %s %s", sp[0].ID, again[0].ID)
	}
	if got := models(e.speech()); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("asked %v", got)
	}
	// saved by the page with an empty key: the key is kept, «stt» is written empty
	body := `[{"id":"` + sp[0].ID + `","kind":"speech","url":"` + s.URL + `/v1","model":"m1","key":"","tiers":[]}]`
	if w := e.putList(body); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var stt STTSettings
	if err := json.Unmarshal(e.storedRow()["stt"], &stt); err != nil || stt != (STTSettings{}) {
		t.Fatalf("stt left in the row: %s", e.storedRow()["stt"])
	}
	after := speechOf(e.view())
	if len(after) != 1 || after[0].ID != sp[0].ID || !after[0].KeySet {
		t.Fatalf("%+v", after)
	}
	if w := e.do("POST", "/api/settings/ai/test", `{"ids":["`+sp[0].ID+`"]}`, e.admin); !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("the migrated key does not work: %s", w.Body)
	}
}

// «Выключено» of before: no speech at all, the server's own included — its
// entry is there and off. A provider of its own did not ask the server's
// either.
func TestSpeechMigratesOffAndProvider(t *testing.T) {
	noSpeechEnv(t)
	s := newSpeechServer(t, "env-key")
	t.Setenv("VAULT_STT_URL", s.URL+"/v1")
	t.Setenv("VAULT_STT_MODEL", "server-model")
	t.Setenv("VAULT_STT_KEY", "env-key")
	e := newSTTEnv(t)
	if err := db.PutSetting(context.Background(), e.db, appSettingsKey, `{"stt":{"provider":"off"}}`); err != nil {
		t.Fatal(err)
	}
	sp := speechOf(e.view())
	if len(sp) != 1 || !sp[0].Env || !sp[0].Off || sp[0].ID != aiEnvSpeechID {
		t.Fatalf("%+v", sp)
	}
	if got := e.speech(); len(got) != 0 {
		t.Fatalf("off, and asked %v", got)
	}
	if v := e.view(); v.STT.Provider != "off" {
		t.Fatalf("%+v", v.STT)
	}
	// a provider of its own: it is asked, the server's own entry is left off
	other := newSpeechServer(t, "")
	if err := db.PutSetting(context.Background(), e.db, appSettingsKey, `{"stt":{"provider":"custom","url":"`+other.URL+`/v1","model":"mine"}}`); err != nil {
		t.Fatal(err)
	}
	if got := models(e.speech()); len(got) != 1 || got[0] != "mine" {
		t.Fatalf("asked %v", got)
	}
	if sp := speechOf(e.view()); len(sp) != 2 || !sp[1].Env || !sp[1].Off {
		t.Fatalf("%+v", sp)
	}
}

// The server's own speech service (VAULT_STT_*) is the entry «Сервер»: its
// address and key are the environment's, its model the admin's choice (kept
// only when it is not the environment's own), it can be switched off and
// moved but not deleted.
func TestSpeechEnvEntry(t *testing.T) {
	noSpeechEnv(t)
	s := newSpeechServer(t, "env-key")
	t.Setenv("VAULT_STT_URL", s.URL+"/v1")
	t.Setenv("VAULT_STT_MODEL", "server-model")
	t.Setenv("VAULT_STT_KEY", "env-key")
	e := newSTTEnv(t)
	v := e.view()
	sp := speechOf(v)
	if len(sp) != 1 || !sp[0].Env || sp[0].ID != "env-stt" || sp[0].Model != "server-model" || sp[0].ModelUsual != "server-model" || !sp[0].KeySet || sp[0].URL != s.URL+"/v1" {
		t.Fatalf("%+v", sp)
	}
	if v.STTFromEnv != "server-model" || v.STT.Provider != "" {
		t.Fatalf("the old fields: %+v %q", v.STT, v.STTFromEnv)
	}
	if got := models(e.speech()); len(got) != 1 || got[0] != "server-model" {
		t.Fatalf("asked %v", got)
	}
	// a model of the admin's choice; the address and key stay the environment's
	n := e.worker.count()
	v = e.saveList(`[{"id":"env-stt","kind":"speech","env":true,"url":"http://evil.invalid","model":"alt","key":"mine","tiers":[]}]`)
	if sp = speechOf(v); len(sp) != 1 || sp[0].Model != "alt" || sp[0].URL != s.URL+"/v1" || sp[0].ModelUsual != "server-model" {
		t.Fatalf("%+v", sp)
	}
	if e.worker.count() != n+1 {
		t.Errorf("the worker told %d times", e.worker.count()-n)
	}
	if w := e.do("POST", "/api/settings/ai/test", `{"ids":["env-stt"]}`, e.admin); !strings.Contains(w.Body.String(), `"ok":true`) || s.lastModel() != "alt" || s.lastAuth() != "Bearer env-key" {
		t.Fatalf("%s %q %q", w.Body, s.lastModel(), s.lastAuth())
	}
	// the environment's own model is not stored (it follows the environment)
	e.saveList(`[{"id":"env-stt","kind":"speech","env":true,"model":"server-model","tiers":[]}]`)
	if raw := string(e.storedRow()["ai"]); strings.Contains(raw, "server-model") {
		t.Fatalf("stored %s", raw)
	}
	// «Выключена», and moved behind another entry
	other := newSpeechServer(t, "")
	v = e.saveList(`[{"id":"env-stt","kind":"speech","env":true,"off":true,"tiers":[]},{"id":"","kind":"speech","url":"` + other.URL + `/v1","model":"mine","tiers":[]}]`)
	if sp = speechOf(v); len(sp) != 2 || !sp[0].Off || sp[1].Model != "mine" {
		t.Fatalf("%+v", sp)
	}
	if got := models(e.speech()); len(got) != 1 || got[0] != "mine" {
		t.Fatalf("asked %v", got)
	}
	// not deletable: it comes back
	if sp = speechOf(e.saveList(`[]`)); len(sp) != 1 || !sp[0].Env {
		t.Fatalf("%+v", sp)
	}
}

// Speech and text entries share the list and never each other's work: the
// transcripts ask the speech ones in the list's order, a text task the text
// ones, and each kind's discovery and «Проверить» is its own.
func TestSpeechAndTextAreSeparate(t *testing.T) {
	noSpeechEnv(t)
	good, bad := newSpeechServer(t, "k"), newSpeechServer(t, "other")
	text, asked := modelServer(t, "t")
	e := newSTTEnv(t)
	list := `[
	  {"id":"","url":"` + text.URL + `/v1","model":"chat","key":"t","tiers":["light","medium","powerful"]},
	  {"id":"","kind":"speech","url":"` + bad.URL + `/v1","model":"first","key":"k","tiers":[]},
	  {"id":"","kind":"speech","url":"` + good.URL + `/v1","model":"second","key":"k","tiers":[]},
	  {"id":"","kind":"speech","url":"` + good.URL + `/v1","model":"third","key":"k","off":true,"tiers":[]},
	  {"id":"","url":"` + text.URL + `/v1","model":"chat2","key":"t","tiers":["light"]}
	]`
	v := e.saveList(list)
	if got := models(e.speech()); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("speech asked %v (a text model or an entry that is off among them)", got)
	}
	// the text chain holds no speech entry: its head and members are the text ones
	rt := aiRuntimeFor(e.db)
	app, _ := LoadAppSettings(context.Background(), e.db, AppSettings{})
	for _, tier := range []channels.Tier{channels.TierLight, channels.TierMedium, channels.TierPowerful} {
		head := rt.chain(e.db, app.AI, nil, tier)
		if head == nil || head.Chain == nil || head.Model != "chat" {
			t.Fatalf("tier %s: %+v", tier, head)
		}
		for _, m := range head.Chain.Members {
			if m.LLM.URL == bad.URL+"/v1" || m.LLM.URL == good.URL+"/v1" {
				t.Fatalf("a speech entry in the text chain: %+v", m.LLM)
			}
		}
	}
	// a speech entry is not the old one-model mirror either
	if w := e.do("GET", "/api/settings", "", e.admin); strings.Contains(w.Body.String(), `"model":"first"`) && !strings.Contains(w.Body.String(), `"kind":"speech"`) {
		t.Fatal("a speech entry leaked")
	}
	// «Проверить все» asks every one that is on, each in its own way
	w := e.do("POST", "/api/settings/ai/test", `{}`, e.admin)
	var res struct {
		Results []struct {
			ID string
			OK bool
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	ok := map[string]bool{}
	for _, r := range res.Results {
		ok[r.ID] = r.OK
	}
	if len(res.Results) != 4 || ok[v.AI.List[1].ID] || !ok[v.AI.List[2].ID] || !ok[v.AI.List[0].ID] {
		t.Fatalf("%s", w.Body)
	}
	if bad.files[0] != "silence.wav RIFF" || asked.Load() != 2 {
		t.Errorf("sent %q, the text model asked %d times", bad.files, asked.Load())
	}
	// the speech entry that failed shows its error, the other is well
	st := map[string]AIStatus{}
	for _, en := range e.view().AI.List {
		st[en.Model] = en.Status
	}
	if st["first"].State != aiError || st["first"].Code != 401 || st["second"].State != aiOK || st["third"].State != aiOff {
		t.Fatalf("%+v", st)
	}
}

// A speech entry's key is its own: a new address does not take the stored
// key, and a missing key of Groq, OpenAI or Gemini is «Нет ключа» and the
// entry is not asked.
func TestSpeechKeys(t *testing.T) {
	noSpeechEnv(t)
	e := newSTTEnv(t)
	v := e.saveList(`[{"id":"","kind":"speech","url":"https://api.groq.com/openai/v1","model":"whisper-large-v3-turbo","tiers":[]}]`)
	sp := speechOf(v)
	if len(sp) != 1 || sp[0].KeySet || sp[0].Status.State != aiNoKey || len(e.speech()) != 0 {
		t.Fatalf("%+v %v", sp, e.speech())
	}
	v = e.saveList(`[{"id":"` + sp[0].ID + `","kind":"speech","url":"https://api.groq.com/openai/v1","model":"whisper-large-v3-turbo","key":" gsk-secret ","tiers":[]}]`)
	if sp = speechOf(v); !sp[0].KeySet || len(e.speech()) != 1 {
		t.Fatalf("%+v", sp)
	}
	if m := e.speech()[0]; m.Config != (transcript.Config{Provider: "groq", URL: "https://api.groq.com/openai/v1", Model: "whisper-large-v3-turbo", Key: "gsk-secret"}) {
		t.Fatalf("%+v", m.Config)
	}
	// the same key stays with an empty one, not with another provider's address
	v = e.saveList(`[{"id":"` + sp[0].ID + `","kind":"speech","url":"https://api.groq.com/openai/v1","model":"whisper-large-v3","tiers":[]}]`)
	if sp = speechOf(v); !sp[0].KeySet {
		t.Fatalf("%+v", sp)
	}
	v = e.saveList(`[{"id":"` + sp[0].ID + `","kind":"speech","url":"https://api.openai.com/v1","model":"whisper-1","tiers":[]}]`)
	if sp = speechOf(v); sp[0].KeySet || len(e.speech()) != 0 {
		t.Fatalf("the key moved to another provider: %+v", sp)
	}
	// a server of one's own needs none; a text entry cannot be made speech by an edit
	own := newSpeechServer(t, "")
	v = e.saveList(`[{"id":"","kind":"speech","url":"` + own.URL + `/v1","model":"m","tiers":[]}]`)
	if sp = speechOf(v); len(sp) != 1 || len(e.speech()) != 1 {
		t.Fatalf("%+v", sp)
	}
	for body, why := range map[string]string{
		`[{"id":"","kind":"speech","url":"","model":"m"}]`:                                                "адрес",
		`[{"id":"","kind":"speech","url":"http://a.invalid/v1","model":""}]`:                              "модель",
		`[{"id":"","kind":"speech","url":"ftp://a.invalid","model":"m"}]`:                                 "http",
		`[{"id":"","kind":"speech","url":"http://u:p@a.invalid","model":"m"}]`:                            "http",
		`[{"id":"","kind":"speech","url":"http://a.invalid","model":"m","key":"a b"}]`:                    "ключ",
		`[{"id":"","kind":"speech","url":"http://a.invalid","model":"` + strings.Repeat("m", 200) + `"}]`: "модел",
	} {
		w := e.putList(body)
		if w.Code != 400 || !strings.Contains(strings.ToLower(w.Body.String()), why) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	// the old test endpoint is gone; another kind of an entry never changes
	if w := e.do("POST", "/api/settings/stt/test", `{}`, e.admin); w.Code == 200 {
		t.Errorf("stt/test: %d", w.Code)
	}
}

// Discovery of a speech entry lists speech models only (kind "speech");
// a text entry's list still holds the chat ones.
func TestSpeechModelsDiscovery(t *testing.T) {
	noSpeechEnv(t)
	e := newSTTEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"whisper-1"},{"id":"my-chat"}]}`)
	}))
	t.Cleanup(srv.Close)
	list := func(kind string) string {
		w := e.do("POST", "/api/settings/ai/models", `{"url":"`+srv.URL+`/v1","kind":"`+kind+`"}`, e.admin)
		return w.Body.String()
	}
	// a server of one's own is its admin's list, whole, for both kinds
	if got := list("speech"); !strings.Contains(got, "whisper-1") || !strings.Contains(got, "my-chat") {
		t.Fatal(got)
	}
	if got := list(""); !strings.Contains(got, "whisper-1") {
		t.Fatal(got)
	}
}
