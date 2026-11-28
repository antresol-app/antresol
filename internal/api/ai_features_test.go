package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/db"
)

// capModel is a LiteLLM that wants the server's key: it lists two models,
// answers «ok» and notes the model and the key of its last chat request.
type capModel struct {
	*httptest.Server
	mu          sync.Mutex
	model, auth string
	chats       int
}

func newCapModel(t *testing.T, key string) *capModel {
	m := &capModel{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, `{"error":"bad key"}`, 401)
			return
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"server-model"},{"id":"alt-model"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var body struct {
				Model    string
				Messages []struct{ Content string }
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.mu.Lock()
			m.model, m.auth = body.Model, r.Header.Get("Authorization")
			m.chats++
			m.mu.Unlock()
			content := `{"ok":true}`
			if strings.Contains(body.Messages[0].Content, "TV channel") {
				content = `{"name":"Советское кино","kinds":["movie"],"genres":[],"countries":["SU"],"keywords":[]}`
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *capModel) last() (string, string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.model, m.auth, m.chats
}

// features is the settings' «features» as an answer has it.
func featuresOf(t *testing.T, e *libEnv) map[string]bool {
	t.Helper()
	var got struct {
		AI struct {
			Features map[string]bool `json:"features"`
		} `json:"ai"`
	}
	if err := json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.AI.Features
}

// llmFeatures: the features a language model serves (transcripts: speech).
var llmFeatures = []channels.Feature{channels.FeatureDescribe, channels.FeatureFranchises, channels.FeatureIPTVCategories, channels.FeatureIPTVVariants, channels.FeatureBookNames, channels.FeatureChapters}

// A feature that is off is never given a model — whichever call site asks —
// and the others still are; «ИИ-помощник» off takes all of them.
func TestAIFeaturesGateEveryCallSite(t *testing.T) {
	m := newCapModel(t, "env-secret")
	t.Setenv("VAULT_LLM_URL", m.URL+"/v1")
	t.Setenv("VAULT_LLM_MODEL", "server-model")
	t.Setenv("VAULT_LLM_KEY", "env-secret")
	e, conn := newChannelsEnvWithSettings(t)
	ctx := context.Background()
	src := llmSource(conn, AppSettings{}, channels.LLMFromEnv())
	put := func(features map[string]bool, off bool) {
		t.Helper()
		ai := getAI(t, e)
		body := map[string]any{"list": ai.List, "off": off}
		if features != nil {
			body["features"] = features
		}
		if w := putAI(t, e, body); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}

	if f := featuresOf(t, e); len(f) != 0 {
		t.Fatalf("a new server has switched something off: %v", f)
	}
	for _, f := range llmFeatures {
		if src(ctx, f, channels.TierMedium) == nil {
			t.Fatalf("%s is off by default", f)
		}
	}
	for _, off := range llmFeatures {
		put(map[string]bool{string(off): false}, false)
		if got := featuresOf(t, e); len(got) != 1 || got[string(off)] {
			t.Fatalf("stored %v", got)
		}
		for _, f := range llmFeatures {
			for _, tier := range []channels.Tier{channels.TierLight, channels.TierMedium, channels.TierPowerful} {
				if on := src(ctx, f, tier) != nil; on == (f == off) {
					t.Errorf("%s off: %s asks a model %v", off, f, on)
				}
			}
		}
	}

	// «Опишите канал»: off, the words alone answer and no request is made
	put(map[string]bool{"describe": false}, false)
	_, _, before := m.last()
	w := e.do("POST", "/api/channels/suggest", `{"text":"старое кино"}`, e.admin)
	if !strings.Contains(w.Body.String(), `"via":"words"`) {
		t.Fatalf("suggest with the feature off %s", w.Body)
	}
	if _, _, n := m.last(); n != before {
		t.Fatalf("the model was asked %d times", n-before)
	}
	if o := e.do("GET", "/api/channels/options", "", e.admin).Body.String(); !strings.Contains(o, `"aiAvailable":false`) {
		t.Fatalf("the editor still offers the assistant: %s", o)
	}
	// and on again
	put(map[string]bool{}, false)
	if w := e.do("POST", "/api/channels/suggest", `{"text":"старое кино"}`, e.admin); !strings.Contains(w.Body.String(), `"via":"ai"`) {
		t.Fatalf("suggest with it on %s", w.Body)
	}
	if f := featuresOf(t, e); len(f) != 0 {
		t.Fatalf("an empty object keeps something off: %v", f)
	}

	// the franchises' pass asks through the service's own model: the feature's
	svc := channels.New(conn, nil)
	svc.LLMSource = src
	put(map[string]bool{"franchises": false}, false)
	if svc.Franchises.Model(ctx) != nil {
		t.Error("franchises asked a model while off")
	}
	put(nil, false)
	if f := featuresOf(t, e); len(f) != 1 || f["franchises"] {
		t.Fatalf("a PUT without «features» changed them: %v", f)
	}
	put(map[string]bool{}, false)
	if svc.Franchises.Model(ctx) == nil {
		t.Error("franchises ask no model while on")
	}

	// the master switch
	put(nil, true)
	for _, f := range llmFeatures {
		if src(ctx, f, channels.TierLight) != nil {
			t.Errorf("%s: a model with the assistant off", f)
		}
	}
}

// What a PUT may say of features: ids nobody knows are dropped, «on» is the
// default and not stored, a PUT that has none keeps the stored ones.
func TestAIFeaturesStored(t *testing.T) {
	t.Setenv("VAULT_LLM_URL", "")
	e, conn := newChannelsEnvWithSettings(t)
	if w := putAI(t, e, map[string]any{"list": []any{}, "features": map[string]bool{"chapters": false, "describe": true, "nonsense": false}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if f := featuresOf(t, e); len(f) != 1 || f["chapters"] {
		t.Fatalf("features %v", f)
	}
	app, err := LoadAppSettings(context.Background(), conn, AppSettings{})
	if err != nil || len(app.AI.Features) != 1 || app.AI.Features["chapters"] {
		t.Fatalf("stored %v %v", app.AI.Features, err)
	}
	// a client of before the list (url, models) keeps them too
	if w := putAI(t, e, map[string]any{"url": "", "model": ""}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if f := featuresOf(t, e); len(f) != 1 {
		t.Fatalf("an old client lost them: %v", f)
	}
	// a row stored before there were features loads with all on
	if err := db.PutSetting(context.Background(), conn, appSettingsKey, `{"ai":{"list":[{"id":"env","env":true,"url":"","model":"","tiers":["light"]}]}}`); err != nil {
		t.Fatal(err)
	}
	if f := featuresOf(t, e); len(f) != 0 {
		t.Fatalf("an old row: %v", f)
	}
}

// The transcripts' speech recognition is a feature too: off, no service is
// given, whatever the speech settings or the environment have.
func TestAIFeatureTranscriptsGatesSpeech(t *testing.T) {
	t.Setenv("VAULT_LLM_URL", "")
	t.Setenv("VAULT_STT_PROVIDER", "groq")
	t.Setenv("VAULT_STT_KEY", "env-stt")
	e, conn := newChannelsEnvWithSettings(t)
	speech := func() bool {
		_, on := SpeechSource(conn, AppSettings{})(context.Background())
		return on
	}
	if !speech() {
		t.Fatal("the server's own speech service is not asked by default")
	}
	for name, ai := range map[string]map[string]any{
		"feature": {"list": []any{}, "features": map[string]bool{"transcripts": false}},
		"master":  {"list": []any{}, "off": true},
	} {
		if w := putAI(t, e, ai); w.Code != 200 {
			t.Fatal(w.Body)
		}
		if speech() {
			t.Errorf("%s off: speech is still asked", name)
		}
	}
	// other features off leave it alone
	if w := putAI(t, e, map[string]any{"list": []any{}, "features": map[string]bool{"chapters": false}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if !speech() {
		t.Error("chapters off turned the speech off")
	}
	// the stored provider is gated the same way
	if err := saveAppSettings(context.Background(), conn, AppSettings{STT: STTSettings{Provider: "groq", Key: "gsk"}, AI: AISettings{Features: map[string]bool{"transcripts": false}}}); err != nil {
		t.Fatal(err)
	}
	if c, on := SpeechSource(conn, AppSettings{})(context.Background()); on {
		t.Errorf("speech with transcripts off: %+v", c)
	}
}

// The server's own entry: its model is the admin's choice among the
// endpoint's models, kept in the settings; the environment's is only the
// default. Its address and key stay the environment's.
func TestAIServerModelChoice(t *testing.T) {
	m := newCapModel(t, "env-secret")
	t.Setenv("VAULT_LLM_URL", m.URL+"/v1")
	t.Setenv("VAULT_LLM_MODEL", "server-model")
	t.Setenv("VAULT_LLM_KEY", "env-secret")
	e, conn := newChannelsEnvWithSettings(t)
	ctx := context.Background()
	env := func() *channels.LLM { return channels.LLMFromEnv() }
	asked := func(f func()) (string, string) {
		t.Helper()
		_, _, n := m.last()
		f()
		model, auth, after := m.last()
		if after != n+1 {
			t.Fatalf("%d requests", after-n)
		}
		return model, auth
	}
	ask := func() {
		l := llmSource(conn, AppSettings{}, env())(ctx, channels.FeatureDescribe, channels.TierMedium)
		if l == nil {
			t.Fatal("no model")
		}
		if _, err := l.AskJSON(ctx, "sys", "user"); err != nil {
			t.Fatal(err)
		}
	}
	if ai := getAI(t, e); len(ai.List) != 1 || ai.List[0].Model != "server-model" {
		t.Fatalf("default %+v", ai.List)
	}
	// the endpoint's models, with the environment's key
	w := e.do("POST", "/api/settings/ai/models", `{"id":"env"}`, e.admin)
	if !strings.Contains(w.Body.String(), `"alt-model"`) || strings.Contains(w.Body.String(), "env-secret") {
		t.Fatalf("models %s", w.Body)
	}
	// chosen: kept, shown, asked — with the environment's key
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "env", "env": true, "model": "alt-model", "url": "http://evil", "key": "evil"}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	ai := getAI(t, e)
	if len(ai.List) != 1 || ai.List[0].Model != "alt-model" || ai.List[0].URL != m.URL+"/v1" || ai.List[0].Status.State != aiUnchecked || !ai.List[0].KeySet {
		t.Fatalf("chosen %+v", ai.List)
	}
	if model, auth := asked(ask); model != "alt-model" || auth != "Bearer env-secret" {
		t.Fatalf("asked %q with %q", model, auth)
	}
	app, err := LoadAppSettings(ctx, conn, AppSettings{})
	if err != nil || len(app.AI.List) != 1 || app.AI.List[0].Model != "alt-model" || app.AI.List[0].Key != "" || app.AI.List[0].URL != "" {
		t.Fatalf("stored %+v %v", app.AI.List, err)
	}
	// «Проверить» a draft asks the model being chosen, a check of the entry the saved one
	if model, _ := asked(func() {
		if w := e.do("POST", "/api/settings/ai/test", `{"entry":{"id":"env","env":true,"model":"draft-model"}}`, e.admin); !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatal(w.Body)
		}
	}); model != "draft-model" {
		t.Fatalf("draft asked %q", model)
	}
	if model, _ := asked(func() { e.do("POST", "/api/settings/ai/test", `{"ids":["env"]}`, e.admin) }); model != "alt-model" {
		t.Fatalf("check asked %q", model)
	}
	// the choice survives the environment changing its default…
	t.Setenv("VAULT_LLM_MODEL", "server-model-2")
	if ai = getAI(t, e); ai.List[0].Model != "alt-model" {
		t.Fatalf("after a restart %+v", ai.List)
	}
	if model, _ := asked(ask); model != "alt-model" {
		t.Fatalf("after a restart asked %q", model)
	}
	// …and choosing the default again follows the environment
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "env", "env": true, "model": "server-model-2"}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if app, _ = LoadAppSettings(ctx, conn, AppSettings{}); app.AI.List[0].Model != "" {
		t.Fatalf("the default is stored: %+v", app.AI.List)
	}
	t.Setenv("VAULT_LLM_MODEL", "server-model-3")
	if ai = getAI(t, e); ai.List[0].Model != "server-model-3" {
		t.Fatalf("following the environment %+v", ai.List)
	}
	// a model name that is no name
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "env", "env": true, "model": strings.Repeat("x", 200)}}}); w.Code != 400 {
		t.Fatalf("a 200-letter model %d", w.Code)
	}
}

// An entry stored before the server's model could be chosen has none: it
// follows the environment, and the row without «model» (or «features») loads.
func TestAIServerModelMigration(t *testing.T) {
	m := newCapModel(t, "")
	t.Setenv("VAULT_LLM_URL", m.URL+"/v1")
	t.Setenv("VAULT_LLM_MODEL", "from-env")
	t.Setenv("VAULT_LLM_KEY", "")
	e, conn := newChannelsEnvWithSettings(t)
	ctx := context.Background()
	for name, row := range map[string]string{
		"no model":  `{"ai":{"list":[{"id":"env","env":true,"tiers":["light","medium","powerful"]}]}}`,
		"empty":     `{"ai":{"list":[{"id":"env","env":true,"model":"","tiers":["medium"],"off":true}]}}`,
		"legacy":    `{"ai":{"url":"` + m.URL + `/v1","model":"legacy-m"}}`,
		"nothing":   `{}`,
		"model set": `{"ai":{"list":[{"id":"env","env":true,"model":"chosen","tiers":["medium"]}]}}`,
	} {
		if err := db.PutSetting(ctx, conn, appSettingsKey, row); err != nil {
			t.Fatal(err)
		}
		ai := getAI(t, e)
		var env *struct{ Model string }
		for _, x := range ai.List {
			if x.Env {
				env = &struct{ Model string }{x.Model}
			}
		}
		want := "from-env"
		if name == "model set" {
			want = "chosen"
		}
		if env == nil || env.Model != want {
			t.Errorf("%s: %+v", name, ai.List)
		}
	}
}

// A LiteLLM added from the server's own: an entry on the server's address
// that asks with the server's key without ever holding it.
func TestAIEntryWithTheServersKey(t *testing.T) {
	m := newCapModel(t, "env-secret")
	t.Setenv("VAULT_LLM_URL", m.URL+"/v1")
	t.Setenv("VAULT_LLM_MODEL", "server-model")
	t.Setenv("VAULT_LLM_KEY", "env-secret")
	e, conn := newChannelsEnvWithSettings(t)
	ctx := context.Background()
	other := newCapModel(t, "other-key")

	// the models of the address, found with the server's key, only for the server's host
	if w := e.do("POST", "/api/settings/ai/models", `{"url":"`+m.URL+`/v1","serverKey":true}`, e.admin); !strings.Contains(w.Body.String(), `"alt-model"`) {
		t.Fatalf("models with the server's key %s", w.Body)
	}
	if w := e.do("POST", "/api/settings/ai/models", `{"url":"`+m.URL+`/v1"}`, e.admin); strings.Contains(w.Body.String(), `"alt-model"`) || !strings.Contains(w.Body.String(), `"code":401`) {
		t.Fatalf("models without a key %s", w.Body)
	}
	if w := e.do("POST", "/api/settings/ai/models", `{"url":"`+other.URL+`/v1","serverKey":true}`, e.admin); strings.Contains(w.Body.String(), `"alt-model"`) || strings.Contains(w.Body.String(), "env-secret") {
		t.Fatalf("the server's key went to another host %s", w.Body)
	}
	// a draft is asked with it
	if w := e.do("POST", "/api/settings/ai/test", `{"entry":{"url":"`+m.URL+`/v1","model":"alt-model","keyFromEnv":true}}`, e.admin); !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("draft %s", w.Body)
	}
	// saved after the server's own, asked first when it is put first
	own := map[string]any{"url": m.URL + "/v1", "model": "alt-model", "keyFromEnv": true}
	if w := putAI(t, e, map[string]any{"list": []any{own, map[string]any{"id": "env", "env": true}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	w := e.do("GET", "/api/settings", "", e.admin)
	if strings.Contains(w.Body.String(), "env-secret") {
		t.Fatalf("the server's key is answered: %s", w.Body)
	}
	var got struct {
		AI struct {
			List []struct {
				ID         string   `json:"id"`
				KeySet     bool     `json:"keySet"`
				KeyFromEnv bool     `json:"keyFromEnv"`
				Status     AIStatus `json:"status"`
			} `json:"list"`
		} `json:"ai"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.AI.List) != 2 || !got.AI.List[0].KeyFromEnv || !got.AI.List[0].KeySet || got.AI.List[0].Status.State != aiUnchecked {
		t.Fatalf("entry %+v %v", got.AI.List, err)
	}
	app, _ := LoadAppSettings(ctx, conn, AppSettings{})
	if app.AI.List[0].Key != "" || !app.AI.List[0].KeyFromEnv {
		t.Fatalf("stored %+v", app.AI.List[0])
	}
	l := llmSource(conn, AppSettings{}, channels.LLMFromEnv())(ctx, channels.FeatureDescribe, channels.TierMedium)
	if _, err := l.AskJSON(ctx, "sys", "user"); err != nil {
		t.Fatal(err)
	}
	if model, auth, _ := m.last(); model != "alt-model" || auth != "Bearer env-secret" {
		t.Fatalf("asked %q with %q", model, auth)
	}
	// moved to another host, it does not take the server's key along
	id := got.AI.List[0].ID
	moved := map[string]any{"id": id, "url": other.URL + "/v1", "model": "alt-model", "keyFromEnv": true}
	if w := putAI(t, e, map[string]any{"list": []any{moved}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if app, _ = LoadAppSettings(ctx, conn, AppSettings{}); len(app.AI.List) < 1 || app.AI.List[0].KeyFromEnv || app.AI.List[0].Key != "" {
		t.Fatalf("moved %+v", app.AI.List)
	}
}

// The page lists the features it knows by id (web/src/pages/manage/aiFeatureList.ts):
// it must be exactly the server's.
func TestAIFeatureIDsAreThePages(t *testing.T) {
	src, err := os.ReadFile("../../web/src/pages/manage/aiFeatureList.ts")
	if err != nil {
		t.Skip("the web sources are not here")
	}
	m := regexp.MustCompile(`AI_FEATURES = \[([^\]]*)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no AI_FEATURES in the page's list")
	}
	var page []string
	for _, id := range regexp.MustCompile(`'([A-Za-z]+)'`).FindAllSubmatch(m[1], -1) {
		page = append(page, string(id[1]))
	}
	var server []string
	for _, f := range channels.Features {
		server = append(server, string(f))
	}
	if !slices.Equal(page, server) {
		t.Fatalf("the page lists %v, the server has %v", page, server)
	}
}

// The server's own entry says what its default model is, for the page to name it.
func TestAIServerEntryNamesItsDefault(t *testing.T) {
	m := newCapModel(t, "")
	t.Setenv("VAULT_LLM_URL", m.URL+"/v1")
	t.Setenv("VAULT_LLM_MODEL", "from-env")
	e, _ := newChannelsEnvWithSettings(t)
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "env", "env": true, "model": "chosen"}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	body := e.do("GET", "/api/settings", "", e.admin).Body.String()
	if !strings.Contains(body, `"model":"chosen"`) || !strings.Contains(body, `"modelDefault":"from-env"`) {
		t.Fatalf("%s", body)
	}
}
