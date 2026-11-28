package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
)

func newChannelsEnvWithSettings(t *testing.T) (*libEnv, *sql.DB) {
	var db *sql.DB
	e := newLibEnvWith(t, func(d *Deps, x *sql.DB) {
		db = x
		d.DB = x
		d.Channels = channels.New(x, nil)
	})
	return e, db
}

// putAI is what the web app does: the whole settings, «ИИ-помощник» replaced.
func putAI(t *testing.T, e *libEnv, ai map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	var cur map[string]any
	_ = json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &cur)
	cur["ai"] = ai
	cur["streamCacheGB"], cur["quality"] = 50, 1080 // this test server has no defaults
	b, _ := json.Marshal(cur)
	return e.do("PUT", "/api/settings", string(b), e.admin)
}

// aiView is the settings' «ИИ-помощник» as an answer has it.
type aiView struct {
	Off  bool `json:"off"`
	List []struct {
		ID     string   `json:"id"`
		Env    bool     `json:"env"`
		URL    string   `json:"url"`
		Model  string   `json:"model"`
		Key    string   `json:"key"`
		KeySet bool     `json:"keySet"`
		Tiers  []string `json:"tiers"`
		Off    bool     `json:"off"`
		Status AIStatus `json:"status"`
	} `json:"list"`
	// the old shape, mirrored for one release
	URL    string   `json:"url"`
	Model  string   `json:"model"`
	Models AIModels `json:"models"`
}

func getAI(t *testing.T, e *libEnv) aiView {
	t.Helper()
	var got struct {
		AI        aiView `json:"ai"`
		AIKeySet  bool   `json:"aiKeySet"`
		AIFromEnv string `json:"aiFromEnv"`
	}
	w := e.do("GET", "/api/settings", "", e.admin)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err, w.Body)
	}
	return got.AI
}

// a model server answering «ok» (or the JSON of «Опишите канал») to the one
// key it knows; asked counts its chat requests.
func modelServer(t *testing.T, key string) (srv *httptest.Server, asked *atomic.Int32) {
	asked = new(atomic.Int32)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		asked.Add(1)
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, `{"error":"bad key"}`, 401)
			return
		}
		var body struct{ Messages []struct{ Content string } }
		_ = json.NewDecoder(r.Body).Decode(&body)
		content := "ok"
		if strings.Contains(body.Messages[0].Content, "TV channel") {
			content = `{"name":"Советское кино","kinds":["movie"],"genres":[],"countries":["SU"],"keywords":[]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, asked
}

// «ИИ-помощник»: a list saved, an entry's key never answered back and kept
// when the field comes empty for the same entry, «Проверить» asks the
// entries and stores what it learns, «Опишите канал» uses the list.
func TestAISettingsList(t *testing.T) {
	model, _ := modelServer(t, "sk-test")
	other, _ := modelServer(t, "")
	t.Setenv("VAULT_LLM_URL", "")
	e, _ := newChannelsEnvWithSettings(t)

	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"url": "ftp://x", "model": "m"}}}); w.Code != 400 {
		t.Fatalf("bad url %d %s", w.Code, w.Body)
	}
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"url": model.URL + "/v1", "model": ""}}}); w.Code != 400 {
		t.Fatalf("no model %d %s", w.Code, w.Body)
	}
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"url": model.URL + "/v1/", "model": "gemini-flash-lite", "key": "sk-test", "tiers": []string{"light"}}}}); w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/api/settings", "", e.admin)
	if strings.Contains(w.Body.String(), "sk-test") || !strings.Contains(w.Body.String(), `"aiKeySet":true`) {
		t.Fatalf("get %s", w.Body)
	}
	ai := getAI(t, e)
	if len(ai.List) != 1 {
		t.Fatalf("list %+v", ai.List)
	}
	first := ai.List[0]
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(first.ID) || first.URL != model.URL+"/v1" || !first.KeySet || first.Key != "" ||
		len(first.Tiers) != 1 || first.Tiers[0] != "light" || first.Status.State != aiUnchecked {
		t.Fatalf("entry %+v", first)
	}
	// for a client that knows no list: the first model in the old shape
	if ai.URL != model.URL+"/v1" || ai.Model != "gemini-flash-lite" || ai.Models.Light != "gemini-flash-lite" {
		t.Fatalf("mirror %+v", ai)
	}

	// the key is kept when the field comes back empty for the same entry
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": first.ID, "url": model.URL + "/v1", "model": "gemini-flash-lite", "tiers": []string{"light", "medium"}}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = e.do("POST", "/api/settings/ai/test", `{"ids":["`+first.ID+`"]}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"reply":"ok"`) || strings.Contains(w.Body.String(), "sk-test") {
		t.Fatalf("test %d %s", w.Code, w.Body)
	}
	// ... and what the check learned is stored (and the tiers changed, the id stays)
	ai = getAI(t, e)
	if ai.List[0].ID != first.ID || ai.List[0].Status.State != aiOK || ai.List[0].Status.Source != aiByCheck || len(ai.List[0].Tiers) != 2 {
		t.Fatalf("after the check %+v", ai.List[0])
	}
	// a draft with a wrong key: asked, not saved; the answer says why
	w = e.do("POST", "/api/settings/ai/test", `{"entry":{"url":"`+model.URL+`/v1","model":"m","key":"sk-wrong"}}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), `"code":401`) || !strings.Contains(w.Body.String(), `"kind":"http"`) {
		t.Fatalf("wrong key %s", w.Body)
	}
	if got := getAI(t, e); len(got.List) != 1 || got.List[0].Status.State != aiOK {
		t.Fatalf("a draft changed the list: %+v", got.List)
	}
	// a draft of a stored entry with no key typed asks with the stored key
	w = e.do("POST", "/api/settings/ai/test", `{"entry":{"id":"`+first.ID+`","url":"`+model.URL+`/v1","model":"other-model"}}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("draft with the stored key %s", w.Body)
	}
	// an unknown id is not found
	if w := e.do("POST", "/api/settings/ai/test", `{"ids":["nope"]}`, e.admin); w.Code != 404 {
		t.Fatalf("unknown id %d", w.Code)
	}

	// «Опишите канал» asks the list
	w = e.do("GET", "/api/channels/options", "", e.admin)
	if !strings.Contains(w.Body.String(), `"aiAvailable":true`) {
		t.Fatalf("options %s", w.Body)
	}
	w = e.do("POST", "/api/channels/suggest", `{"text":"старое кино"}`, e.admin)
	if !strings.Contains(w.Body.String(), `"via":"ai"`) || !strings.Contains(w.Body.String(), `"countries":["SU"]`) {
		t.Fatalf("suggest %s", w.Body)
	}

	// the entry moves to another host: its key does not follow; a new entry
	// on the first host has no key of its own either
	if w := putAI(t, e, map[string]any{"list": []any{
		map[string]any{"id": first.ID, "url": other.URL + "/v1", "model": "m"},
		map[string]any{"url": model.URL + "/v1", "model": "second"},
	}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	ai = getAI(t, e)
	if len(ai.List) != 2 || ai.List[0].ID != first.ID || ai.List[0].KeySet || ai.List[1].KeySet || ai.List[1].ID == first.ID || ai.List[0].Status.State != aiUnchecked {
		t.Fatalf("moved %+v", ai.List)
	}
	// a client's own id for a new entry is not kept
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "abc", "url": other.URL + "/v1", "model": "m"}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); len(ai.List) != 1 || ai.List[0].ID == "abc" || ai.List[0].ID == first.ID {
		t.Fatalf("new id %+v", ai.List)
	}
	// no list left: no model, no key
	if w := putAI(t, e, map[string]any{"list": []any{}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := e.do("GET", "/api/settings", "", e.admin); !strings.Contains(w.Body.String(), `"aiKeySet":false`) || len(getAI(t, e).List) != 0 {
		t.Fatalf("cleared %s", w.Body)
	}
	// a request that does not know «ИИ-помощник» keeps what is stored
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"url": other.URL + "/v1", "model": "kept"}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	var cur map[string]any
	_ = json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &cur)
	delete(cur, "ai")
	b, _ := json.Marshal(cur)
	if w := e.do("PUT", "/api/settings", string(b), e.admin); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); len(ai.List) != 1 || ai.List[0].Model != "kept" {
		t.Fatalf("no ai in the request: %+v", ai.List)
	}
	if w := putAI(t, e, map[string]any{"list": make([]any, maxAIModels+1)}); w.Code != 400 {
		t.Fatalf("too long %d", w.Code)
	}
}

// A client of before the list (its PUT has the one endpoint and its three
// models) still works: a changed endpoint is migrated into the list, a PUT
// that is only what it read (the mirror) changes nothing.
func TestAISettingsOldShapePut(t *testing.T) {
	model, _ := modelServer(t, "sk-test")
	t.Setenv("VAULT_LLM_URL", "")
	e, _ := newChannelsEnvWithSettings(t)
	url := model.URL + "/v1"
	if w := putAI(t, e, map[string]any{"url": url, "model": "m", "models": map[string]string{"light": "l", "medium": "m", "powerful": "m"}, "key": "sk-test"}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	ai := getAI(t, e)
	if len(ai.List) != 2 || ai.List[0].Model != "m" || ai.List[1].Model != "l" || !ai.List[0].KeySet || !ai.List[1].KeySet {
		t.Fatalf("migrated %+v", ai.List)
	}
	if strings.Join(ai.List[0].Tiers, ",") != "medium,powerful" || strings.Join(ai.List[1].Tiers, ",") != "light" {
		t.Fatalf("tiers %+v", ai.List)
	}
	// a stale tab's PUT of what it read before: nothing changes (the ids stay)
	if w := putAI(t, e, map[string]any{"url": ai.URL, "model": ai.Model, "models": ai.Models}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if again := getAI(t, e); len(again.List) != 2 || again.List[0].ID != ai.List[0].ID || !again.List[0].KeySet {
		t.Fatalf("a stale PUT changed the list: %+v", again.List)
	}
	// another model on the same host keeps the key
	if w := putAI(t, e, map[string]any{"url": url, "model": "n"}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if again := getAI(t, e); len(again.List) != 1 || again.List[0].Model != "n" || !again.List[0].KeySet {
		t.Fatalf("old PUT of one model: %+v", again.List)
	}
	// no address: no models
	if w := putAI(t, e, map[string]any{"url": "", "model": ""}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if again := getAI(t, e); len(again.List) != 0 {
		t.Fatalf("old clear: %+v", again.List)
	}
}

// A stored row of the old shape (one endpoint, three models) is read as the
// list: one entry per distinct model, in the order medium, light, powerful,
// each with the tiers that pointed at it; the ids are the same on every read.
func TestAISettingsMigration(t *testing.T) {
	old := AISettings{URL: "http://x/v1", Key: "k", Models: AIModels{Light: "a", Medium: "b", Powerful: "a"}}
	got := old.migrated()
	if got.URL != "" || got.Key != "" || got.Models != (AIModels{}) {
		t.Errorf("old fields stay: %+v", got)
	}
	if len(got.List) != 2 || got.List[0].Model != "b" || got.List[1].Model != "a" || got.List[0].Key != "k" {
		t.Fatalf("list %+v", got.List)
	}
	if want := []channels.Tier{"light", "powerful"}; len(got.List[1].Tiers) != 2 || got.List[1].Tiers[0] != want[0] || got.List[1].Tiers[1] != want[1] {
		t.Errorf("tiers %+v", got.List[1].Tiers)
	}
	if again := old.migrated(); again.List[0].ID != got.List[0].ID || again.List[1].ID != got.List[1].ID {
		t.Errorf("ids move between reads")
	}
	// one model only: every tier
	one := AISettings{URL: "http://x/v1", Model: "only"}.migrated()
	if len(one.List) != 1 || len(one.List[0].Tiers) != 3 {
		t.Errorf("one model %+v", one.List)
	}
	// Anthropic's one model keeps its family's tier, the others are Anthropic's
	cl := AISettings{URL: channels.AnthropicURL, Model: "claude-opus-5-5", Key: "k"}.migrated()
	if len(cl.List) != 3 || cl.List[0].Model != "claude-sonnet-5-5" || cl.List[2].Model != "claude-opus-5-5" {
		t.Errorf("anthropic %+v", cl.List)
	}
	// a list wins over stale old fields
	both := AISettings{URL: "http://x/v1", Model: "m", List: []AIModel{{ID: "keep", URL: "http://y/v1", Model: "z"}}}.migrated()
	if len(both.List) != 1 || both.List[0].ID != "keep" || both.URL != "" {
		t.Errorf("both %+v", both)
	}
	// nothing stored: nothing; the server's own model joins on resolving, last
	env := &channels.LLM{URL: "http://env/v1", Model: "srv", Key: "ek"}
	if n := (AISettings{}).migrated().resolved(env).List; len(n) != 1 || !n[0].Env || n[0].Model != "srv" || len(n[0].Tiers) != 3 {
		t.Errorf("env only %+v", n)
	}
	r := old.migrated().resolved(env).List
	if len(r) != 3 || !r[2].Env {
		t.Errorf("env last %+v", r)
	}
	// without VAULT_LLM_* a stored env entry goes
	if n := (AISettings{List: []AIModel{{ID: "env", Env: true}}}).resolved(nil).List; len(n) != 0 {
		t.Errorf("stale env entry %+v", n)
	}
	// off: no list, the server's own model stays in it
	offv := AISettings{Off: true}.migrated().resolved(env)
	if !offv.Off || len(offv.List) != 1 || !offv.List[0].Env {
		t.Errorf("off %+v", offv)
	}
}

// The server's own model (VAULT_LLM_*) is the list's first entry when it is
// the only one: it can be reordered and switched off but not deleted, «Проверить»
// asks it, and «Выключено» really turns AI off for every task although the
// environment has it.
func TestAISettingsServerModel(t *testing.T) {
	model, asked := modelServer(t, "")
	t.Setenv("VAULT_LLM_URL", model.URL+"/v1")
	t.Setenv("VAULT_LLM_MODEL", "server-model")
	t.Setenv("VAULT_LLM_KEY", "env-secret")
	e, _ := newChannelsEnvWithSettings(t)
	available := func() bool {
		return strings.Contains(e.do("GET", "/api/channels/options", "", e.admin).Body.String(), `"aiAvailable":true`)
	}

	ai := getAI(t, e)
	if len(ai.List) != 1 || !ai.List[0].Env || ai.List[0].ID != "env" || ai.List[0].Model != "server-model" || !ai.List[0].KeySet || ai.Off || !available() {
		t.Fatalf("default %+v", ai)
	}
	if w := e.do("GET", "/api/settings", "", e.admin); strings.Contains(w.Body.String(), "env-secret") {
		t.Fatalf("the environment's key is answered: %s", w.Body)
	}
	// nothing typed: «Проверить» all asks the server's model
	w := e.do("POST", "/api/settings/ai/test", `{}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"id":"env"`) || strings.Contains(w.Body.String(), "env-secret") {
		t.Fatalf("test of the server's model %d %s", w.Code, w.Body)
	}
	// an own model joins; the order is the admin's
	own := map[string]any{"url": model.URL + "/v1", "model": "mine"}
	if w := putAI(t, e, map[string]any{"list": []any{own, map[string]any{"id": "env", "env": true}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); len(ai.List) != 2 || ai.List[0].Model != "mine" || !ai.List[1].Env || ai.List[1].Model != "server-model" {
		t.Fatalf("own first %+v", ai.List)
	}
	// reordered: the server's first
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "env", "env": true, "url": "http://evil", "model": "evil"}, own}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); !ai.List[0].Env || ai.List[0].URL != model.URL+"/v1" || ai.List[0].Model != "server-model" {
		t.Fatalf("env first, still the environment's %+v", ai.List)
	}
	// switched off: not asked, but listed
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": "env", "env": true, "off": true}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); len(ai.List) != 1 || !ai.List[0].Env || !ai.List[0].Off || ai.List[0].Status.State != aiOff || available() {
		t.Fatalf("switched off %+v available %v", ai.List, available())
	}
	// deleted: it comes back (last), as it was left: here, off
	if w := putAI(t, e, map[string]any{"list": []any{own}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); len(ai.List) != 2 || !ai.List[1].Env || ai.List[1].Off {
		t.Fatalf("deleted env entry %+v", ai.List)
	}
	// «Выключено»: no task asks any model, the list stays
	asked.Store(0)
	if w := putAI(t, e, map[string]any{"off": true, "list": ai.List}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); !ai.Off || len(ai.List) != 2 || available() {
		t.Fatalf("off %+v available %v", ai, available())
	}
	if l := llmSource(e.db, AppSettings{}, channels.LLMFromEnv())(context.Background(), channels.TierMedium); l != nil {
		t.Fatalf("off, yet a model: %+v", l)
	}
	// and the old shape's «Выключено» (a client of before the list)
	if w := putAI(t, e, map[string]any{"off": false, "list": ai.List}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := putAI(t, e, map[string]any{"url": "", "model": "", "off": true}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if ai = getAI(t, e); !ai.Off || len(ai.List) != 1 || !ai.List[0].Env || available() {
		t.Fatalf("old off %+v", ai)
	}
}

// Anthropic's API is not asked without a key: the SDK would otherwise run
// the request on whatever credential the host has. Saved without one it is
// listed as «Нет ключа» and no task asks it.
func TestAISettingsNoKey(t *testing.T) {
	t.Setenv("VAULT_LLM_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	e, _ := newChannelsEnvWithSettings(t)
	w := e.do("POST", "/api/settings/ai/test", `{"entry":{"url":"https://api.anthropic.com","model":"claude-opus-5-5"}}`, e.admin)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "ключ") {
		t.Fatalf("test without a key %d %s", w.Code, w.Body)
	}
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"url": "https://api.anthropic.com/v1", "model": "claude-opus-5-5"}}}); w.Code != 200 {
		t.Fatalf("save without a key %d %s", w.Code, w.Body)
	}
	ai := getAI(t, e)
	if ai.List[0].KeySet || ai.List[0].Status.State != aiNoKey {
		t.Fatalf("no key %+v", ai.List)
	}
	// «Проверить все» asks nothing of it and says so
	w = e.do("POST", "/api/settings/ai/test", `{}`, e.admin)
	if !strings.Contains(w.Body.String(), `"noKey"`) || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("check all %s", w.Body)
	}
	if l := llmSource(e.db, AppSettings{}, nil)(context.Background(), channels.TierMedium); l != nil {
		t.Fatalf("an entry without its key is asked: %+v", l)
	}
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"id": ai.List[0].ID, "url": "https://api.anthropic.com/v1", "model": "claude-opus-5-5", "key": "sk-ant-test"}}}); w.Code != 200 {
		t.Fatalf("save with a key %d %s", w.Code, w.Body)
	}
	if ai = getAI(t, e); !ai.List[0].KeySet || ai.List[0].Status.State != aiUnchecked {
		t.Fatalf("with a key %+v", ai.List)
	}
}

// What the models endpoint lists is the endpoint's own list, with the stored
// key for an entry that is not retyped; the key is never answered.
func TestAISettingsListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "no", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": "pin-b"}, map[string]string{"id": "auto"}}})
	}))
	defer srv.Close()
	t.Setenv("VAULT_LLM_URL", "")
	e, _ := newChannelsEnvWithSettings(t)
	body := func(s string) string { return e.do("POST", "/api/settings/ai/models", s, e.admin).Body.String() }
	if got := body(`{"url":"` + srv.URL + `/v1","key":"sk-test"}`); got != `{"models":[{"id":"auto"},{"id":"pin-b"}]}`+"\n" {
		t.Fatalf("typed key: %s", got)
	}
	if got := body(`{"url":"` + srv.URL + `/v1"}`); !strings.Contains(got, `"code":401`) || !strings.Contains(got, `"models":[]`) {
		t.Fatalf("no key: %s", got)
	}
	if w := putAI(t, e, map[string]any{"list": []any{map[string]any{"url": srv.URL + "/v1", "model": "auto", "key": "sk-test"}}}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	id := getAI(t, e).List[0].ID
	if got := body(`{"id":"` + id + `"}`); !strings.Contains(got, `"id":"auto"`) || strings.Contains(got, "sk-test") {
		t.Fatalf("stored key: %s", got)
	}
	// another host: the stored key does not follow
	if got := body(`{"id":"` + id + `","url":"http://127.0.0.1:1/v1"}`); strings.Contains(got, `"id":"auto"`) {
		t.Fatalf("key went to another host: %s", got)
	}
	if w := e.do("POST", "/api/settings/ai/models", `{"url":"ftp://x"}`, e.admin); w.Code != 400 {
		t.Fatalf("bad url %d", w.Code)
	}
	if w := e.do("POST", "/api/settings/ai/models", `{"url":"`+srv.URL+`/v1"}`, nil); w.Code != 403 && w.Code != 401 {
		t.Fatalf("not for everyone: %d", w.Code)
	}
}

// ---------- the chain: what happens when a model fails ----------

// scripted is a model server whose answers are the script's: one status and
// content per request.
type scripted struct {
	srv   *httptest.Server
	asked atomic.Int32
	mu    sync.Mutex
	reply func(n int) (int, string)
}

func newScripted(t *testing.T, reply func(n int) (int, string)) *scripted {
	s := &scripted{reply: reply}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.asked.Add(1))
		s.mu.Lock()
		fn := s.reply
		s.mu.Unlock()
		code, content := fn(n)
		if code != 200 {
			http.Error(w, `{"error":"scripted"}`, code)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func always(code int, content string) func(int) (int, string) {
	return func(int) (int, string) { return code, content }
}

// chainEnv: a database with the list stored and a runtime on a clock the test moves.
type chainEnv struct {
	t     *testing.T
	db    *sql.DB
	rt    *aiRuntime
	clock time.Time
	mu    sync.Mutex
}

func newChainEnv(t *testing.T, list ...AIModel) *chainEnv {
	_, conn := newChannelsEnvWithSettings(t)
	c := &chainEnv{t: t, db: conn, rt: aiRuntimeFor(conn), clock: time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)}
	c.rt.now = func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.clock }
	c.set(list...)
	return c
}

func (c *chainEnv) set(list ...AIModel) {
	c.t.Helper()
	if err := saveAppSettings(context.Background(), c.db, AppSettings{AI: AISettings{List: list}}); err != nil {
		c.t.Fatal(err)
	}
}

func (c *chainEnv) advance(d time.Duration) { c.mu.Lock(); c.clock = c.clock.Add(d); c.mu.Unlock() }

func (c *chainEnv) ask(tier channels.Tier) (string, error) {
	l := llmSource(c.db, AppSettings{}, nil)(context.Background(), tier)
	if l == nil {
		return "", context.Canceled
	}
	return l.AskJSON(context.Background(), "sys", "user")
}

func (c *chainEnv) stored() map[string]AIStatus {
	app, err := LoadAppSettings(context.Background(), c.db, AppSettings{})
	if err != nil {
		c.t.Fatal(err)
	}
	out := map[string]AIStatus{}
	for _, e := range app.AI.List {
		out[e.ID] = e.Status
	}
	return out
}

func entry(id string, s *scripted, tiers ...channels.Tier) AIModel {
	return AIModel{ID: id, URL: s.srv.URL + "/v1", Model: "m-" + id, Tiers: normalizeTiers(tiers)}
}

const goodJSON = `{"ok":true}`

// A model that fails hands the task to the next one — for errors another
// model may fix: 5xx and 429, a refused key or model, a bad request, an
// answer that is no JSON — and the failure is what the entry's status shows.
func TestAIChainFallsBack(t *testing.T) {
	for name, fail := range map[string]func(int) (int, string){
		"5xx":      always(503, ""),
		"429":      always(429, ""),
		"401":      always(401, ""),
		"403":      always(403, ""),
		"404":      always(404, ""),
		"400":      always(400, ""),
		"not JSON": always(200, "sorry, I cannot"),
	} {
		t.Run(name, func(t *testing.T) {
			bad, good := newScripted(t, fail), newScripted(t, always(200, goodJSON))
			c := newChainEnv(t, entry("a", bad), entry("b", good))
			got, err := c.ask(channels.TierMedium)
			if err != nil || got != goodJSON {
				t.Fatalf("got %q, %v", got, err)
			}
			if bad.asked.Load() != 1 || good.asked.Load() != 1 {
				t.Fatalf("asked %d, %d", bad.asked.Load(), good.asked.Load())
			}
			st := c.stored()
			if st["a"].State != aiError || st["a"].Source != aiByTask || st["b"].State != aiOK || st["b"].Source != aiByTask {
				t.Fatalf("statuses %+v", st)
			}
		})
	}
}

// 401/403/404 sit the entry out until it is edited or checked well; a
// timeout, 5xx or 429 for two minutes; a 400 or a bad answer not at all.
func TestAIChainCoolDown(t *testing.T) {
	t.Run("a refused key waits for an edit", func(t *testing.T) {
		bad, good := newScripted(t, always(401, "")), newScripted(t, always(200, goodJSON))
		c := newChainEnv(t, entry("a", bad), entry("b", good))
		for i := 0; i < 3; i++ {
			if _, err := c.ask(channels.TierMedium); err != nil {
				t.Fatal(err)
			}
			c.advance(time.Hour)
		}
		if bad.asked.Load() != 1 {
			t.Fatalf("a refused key was asked %d times", bad.asked.Load())
		}
		// edited (another key): asked again
		edited := entry("a", bad)
		edited.Key = "new"
		c.set(edited, entry("b", good))
		if _, err := c.ask(channels.TierMedium); err != nil || bad.asked.Load() != 2 {
			t.Fatalf("after the edit: %d, %v", bad.asked.Load(), err)
		}
		// a check that finds it well frees it
		bad.mu.Lock()
		bad.reply = always(200, "ok")
		bad.mu.Unlock()
		c.rt.record(c.db, nil, edited, time.Millisecond, nil, aiByCheck)
		if _, err := c.ask(channels.TierMedium); err != nil || bad.asked.Load() != 3 {
			t.Fatalf("after a good check: %d, %v", bad.asked.Load(), err)
		}
	})
	t.Run("a timeout or 5xx waits two minutes", func(t *testing.T) {
		bad, good := newScripted(t, always(500, "")), newScripted(t, always(200, goodJSON))
		c := newChainEnv(t, entry("a", bad), entry("b", good))
		_, _ = c.ask(channels.TierMedium)
		c.advance(aiCoolDown - time.Second)
		_, _ = c.ask(channels.TierMedium)
		if bad.asked.Load() != 1 {
			t.Fatalf("asked %d times inside the cool-down", bad.asked.Load())
		}
		c.advance(2 * time.Second)
		_, _ = c.ask(channels.TierMedium)
		if bad.asked.Load() != 2 {
			t.Fatalf("asked %d times after it", bad.asked.Load())
		}
	})
	t.Run("a bad request or answer does not", func(t *testing.T) {
		bad, good := newScripted(t, always(400, "")), newScripted(t, always(200, goodJSON))
		c := newChainEnv(t, entry("a", bad), entry("b", good))
		for i := 0; i < 3; i++ {
			_, _ = c.ask(channels.TierMedium)
		}
		if bad.asked.Load() != 3 {
			t.Fatalf("asked %d times", bad.asked.Load())
		}
	})
	t.Run("a model that does not answer in time", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(hang))
		defer slow.Close()
		good := newScripted(t, always(200, goodJSON))
		c := newChainEnv(t, AIModel{ID: "a", URL: slow.URL + "/v1", Model: "m", Tiers: normalizeTiers(nil)}, entry("b", good))
		// the slow entry's one ask is cut after 50 ms
		l := llmSource(c.db, AppSettings{}, nil)(context.Background(), channels.TierMedium)
		l.Chain.Members[0].LLM.Timeout = 50 * time.Millisecond
		if got, err := l.AskJSON(context.Background(), "s", "u"); err != nil || got != goodJSON {
			t.Fatalf("got %q, %v", got, err)
		}
		if st := c.stored()["a"]; st.State != aiError || st.Kind != channels.ErrTimeout {
			t.Fatalf("status %+v", st)
		}
		if !c.rt.cooling("a", entryPrint(AIModel{URL: slow.URL + "/v1", Model: "m"}, nil)) {
			t.Fatal("a timeout is not cooling down")
		}
	})
}

// A call asks at most three models, whatever the list's length; the entries
// that are off, lack a key or serve another weight of task are not asked.
func TestAIChainBudgetAndEligibility(t *testing.T) {
	var bad []*scripted
	var list []AIModel
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		s := newScripted(t, always(500, ""))
		bad = append(bad, s)
		list = append(list, entry(id, s))
	}
	c := newChainEnv(t, list...)
	if _, err := c.ask(channels.TierMedium); err == nil {
		t.Fatal("every model failed, yet an answer")
	}
	n := 0
	for _, s := range bad {
		n += int(s.asked.Load())
	}
	if n != channels.ChainAttempts || bad[3].asked.Load() != 0 {
		t.Fatalf("asked %d models of 5", n)
	}
	// the next call goes on with the ones not cooling down
	if _, err := c.ask(channels.TierMedium); err == nil || bad[3].asked.Load() != 1 || bad[4].asked.Load() != 1 {
		t.Fatalf("second call: d %d e %d, %v", bad[3].asked.Load(), bad[4].asked.Load(), err)
	}
	// every one is cooling down: an error at once, nothing asked
	before := bad[0].asked.Load()
	if _, err := c.ask(channels.TierMedium); err == nil || bad[0].asked.Load() != before {
		t.Fatalf("all cooling: %v", err)
	}

	light, all, off := newScripted(t, always(200, `{"who":"light"}`)), newScripted(t, always(200, `{"who":"all"}`)), newScripted(t, always(200, `{"who":"off"}`))
	lightOnly := entry("l", light, channels.TierLight)
	disabled := entry("o", off)
	disabled.Off = true
	keyless := AIModel{ID: "k", URL: "https://api.groq.com/openai/v1", Model: "m", Tiers: normalizeTiers(nil)}
	c = newChainEnv(t, keyless, disabled, lightOnly, entry("x", all))
	if got, _ := c.ask(channels.TierMedium); got != `{"who":"all"}` {
		t.Fatalf("medium: %s", got)
	}
	if got, _ := c.ask(channels.TierLight); got != `{"who":"light"}` {
		t.Fatalf("light: %s", got)
	}
	if off.asked.Load() != 0 {
		t.Fatal("a switched-off entry was asked")
	}
	if l := llmSource(c.db, AppSettings{}, nil)(context.Background(), channels.TierPowerful); l == nil || l.Model != "m-x" {
		t.Fatalf("powerful: %+v", l)
	}
	c.set(keyless, disabled)
	if l := llmSource(c.db, AppSettings{}, nil)(context.Background(), channels.TierMedium); l != nil {
		t.Fatalf("nothing eligible, yet %+v", l)
	}
}

// A caller that goes away is no model's failure: no status, no cool-down.
func TestAIChainCallerCancelled(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(hang))
	defer slow.Close()
	good := newScripted(t, always(200, goodJSON))
	c := newChainEnv(t, AIModel{ID: "a", URL: slow.URL + "/v1", Model: "m", Tiers: normalizeTiers(nil)}, entry("b", good))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	l := llmSource(c.db, AppSettings{}, nil)(context.Background(), channels.TierMedium)
	if _, err := l.AskJSON(ctx, "s", "u"); err == nil {
		t.Fatal("cancelled, yet an answer")
	}
	if good.asked.Load() != 0 || c.stored()["a"].State != "" || c.rt.cooling("a", entryPrint(AIModel{URL: slow.URL + "/v1", Model: "m"}, nil)) {
		t.Fatalf("a cancelled caller counted against the model: %+v", c.stored())
	}
}

// Real task calls keep the stored status current without a write storm: it
// is written when its state changes and otherwise once a minute.
func TestAIChainStatusWritesAreThrottled(t *testing.T) {
	good := newScripted(t, always(200, goodJSON))
	c := newChainEnv(t, entry("a", good))
	at := func() time.Time { return c.stored()["a"].At }
	_, _ = c.ask(channels.TierMedium)
	first := at()
	if first.IsZero() || c.stored()["a"].State != aiOK {
		t.Fatalf("first call is not stored: %+v", c.stored())
	}
	for i := 0; i < 20; i++ {
		c.advance(2 * time.Second)
		_, _ = c.ask(channels.TierMedium)
	}
	if !at().Equal(first) {
		t.Fatalf("written during the minute: %v → %v", first, at())
	}
	// ... but the page already sees the newest, from memory
	if st := c.rt.status(AIModel{ID: "a", URL: good.srv.URL + "/v1", Model: "m-a"}, nil); !st.At.After(first) {
		t.Fatalf("the live status is stale: %+v", st)
	}
	c.advance(time.Minute)
	_, _ = c.ask(channels.TierMedium)
	if !at().After(first) {
		t.Fatal("not written after the minute")
	}
	// a change of state is written at once
	good.mu.Lock()
	good.reply = always(500, "")
	good.mu.Unlock()
	c.advance(time.Second)
	_, _ = c.ask(channels.TierMedium)
	if st := c.stored()["a"]; st.State != aiError || st.Code != 500 {
		t.Fatalf("a failure is not stored at once: %+v", st)
	}
	// the stored settings besides the status are untouched
	if app, _ := LoadAppSettings(context.Background(), c.db, AppSettings{}); len(app.AI.List) != 1 || app.AI.List[0].Model != "m-a" {
		t.Fatalf("list changed: %+v", app.AI.List)
	}
}

// A status belongs to the entry as it was asked: an entry edited meanwhile
// does not take the old answer's.
func TestAIChainStatusKeepsToItsEntry(t *testing.T) {
	good := newScripted(t, always(200, goodJSON))
	c := newChainEnv(t, entry("a", good))
	l := llmSource(c.db, AppSettings{}, nil)(context.Background(), channels.TierMedium)
	edited := entry("a", good)
	edited.Model = "changed"
	c.set(edited)
	if _, err := l.AskJSON(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if st := c.stored()["a"]; st.State != "" {
		t.Fatalf("the old model's answer on the edited entry: %+v", st)
	}
}

func TestAISettingsKeyStaysWithItsHost(t *testing.T) {
	gem := "https://generativelanguage.googleapis.com/v1beta/openai"
	stored := AISettings{List: []AIModel{{ID: "a1", URL: gem, Model: "g", Key: "AIza-old", Tiers: normalizeTiers(nil)}}}
	merge := func(e AIModel) AIModel { return AISettings{List: []AIModel{e}}.merged(stored, nil).List[0] }
	if got := merge(AIModel{ID: "a1", URL: gem, Model: "g2"}); got.Key != "AIza-old" || got.ID != "a1" {
		t.Errorf("same host: %+v, want the stored key", got)
	}
	if got := merge(AIModel{ID: "a1", URL: "https://api.groq.com/openai/v1", Model: "q"}); got.Key != "" {
		t.Errorf("another host: key %q carried over", got.Key)
	}
	if got := merge(AIModel{ID: "a1", URL: "https://api.groq.com/openai/v1", Model: "q", Key: " gsk-new "}); got.Key != "gsk-new" {
		t.Errorf("a typed key: %q", got.Key)
	}
	if got := merge(AIModel{URL: gem, Model: "g"}); got.Key != "" || got.ID == "a1" {
		t.Errorf("a new entry on the same host: %+v", got)
	}
	// the same id twice: the second is a new entry
	two := AISettings{List: []AIModel{{ID: "a1", URL: gem, Model: "g"}, {ID: "a1", URL: gem, Model: "h"}}}.merged(stored, nil).List
	if two[0].ID != "a1" || two[1].ID == "a1" || two[1].Key != "" {
		t.Errorf("duplicate id: %+v", two)
	}
	// the status is kept while the entry is as it was and starts over when it changes
	stored.List[0].Status = AIStatus{State: aiOK, MS: 5}
	if got := merge(AIModel{ID: "a1", URL: gem + "/", Model: "g"}); got.Status.State != aiOK {
		t.Errorf("unchanged entry lost its status: %+v", got.Status)
	}
	if got := merge(AIModel{ID: "a1", URL: gem, Model: "other"}); got.Status.State != "" {
		t.Errorf("changed entry kept its status: %+v", got.Status)
	}
	if got := merge(AIModel{ID: "a1", URL: gem, Model: "g", Key: "new"}); got.Status.State != "" {
		t.Errorf("a new key kept the status: %+v", got.Status)
	}
	// the tiers are kept whole
	if got := merge(AIModel{ID: "a1", URL: gem, Model: "g", Tiers: []channels.Tier{"powerful", "bogus", "light", "light"}}); len(got.Tiers) != 2 || got.Tiers[0] != "light" || got.Tiers[1] != "powerful" {
		t.Errorf("tiers %v", got.Tiers)
	}
}

// hang never answers: it reads the request (so the server notices the client
// leaving) and waits for that.
func hang(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	<-r.Context().Done()
}
