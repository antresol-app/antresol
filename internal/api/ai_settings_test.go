package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

// «ИИ-помощник»: saved, its key never answered back and kept when the
// field comes empty; «Проверить» asks the model; «Опишите канал» uses it.
func TestAISettings(t *testing.T) {
	var asked []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path+" "+r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer sk-test" {
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
	defer model.Close()
	t.Setenv("VAULT_LLM_URL", "")
	e, _ := newChannelsEnvWithSettings(t)

	// as the web app does: the whole settings, «ИИ-помощник» changed
	put := func(ai string) *httptest.ResponseRecorder {
		var cur map[string]any
		_ = json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &cur)
		var a any
		_ = json.Unmarshal([]byte(ai), &a)
		cur["ai"] = a.(map[string]any)["ai"]
		cur["streamCacheGB"], cur["quality"] = 50, 1080 // this test server has no defaults
		b, _ := json.Marshal(cur)
		return e.do("PUT", "/api/settings", string(b), e.admin)
	}
	if w := put(`{"ai":{"url":"ftp://x","model":"m"}}`); w.Code != 400 {
		t.Fatalf("bad url %d %s", w.Code, w.Body)
	}
	if w := put(`{"ai":{"url":"` + model.URL + `/v1/","model":"gemini-flash-lite","key":"sk-test"}}`); w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/api/settings", "", e.admin)
	if strings.Contains(w.Body.String(), "sk-test") || !strings.Contains(w.Body.String(), `"aiKeySet":true`) || !strings.Contains(w.Body.String(), `"url":"`+model.URL+`/v1"`) {
		t.Fatalf("get %s", w.Body)
	}
	// the key is kept when the field comes back empty
	if w := put(`{"ai":{"url":"` + model.URL + `/v1","model":"gemini-flash-lite"}}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = e.do("POST", "/api/settings/ai/test", `{"url":"`+model.URL+`/v1","model":"gemini-flash-lite"}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"reply":"ok"`) {
		t.Fatalf("test %d %s", w.Code, w.Body)
	}
	w = e.do("POST", "/api/settings/ai/test", `{"url":"`+model.URL+`/v1","model":"m","key":"sk-wrong"}`, e.admin)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "401") {
		t.Fatalf("wrong key %s", w.Body)
	}
	// «Опишите канал» asks it
	w = e.do("GET", "/api/channels/options", "", e.admin)
	if !strings.Contains(w.Body.String(), `"aiAvailable":true`) {
		t.Fatalf("options %s", w.Body)
	}
	w = e.do("POST", "/api/channels/suggest", `{"text":"старое кино"}`, e.admin)
	if !strings.Contains(w.Body.String(), `"via":"ai"`) || !strings.Contains(w.Body.String(), `"countries":["SU"]`) {
		t.Fatalf("suggest %s", w.Body)
	}
	// no address: no model, the key gone
	if w := put(`{"ai":{"url":"","model":""}}`); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := e.do("GET", "/api/settings", "", e.admin); !strings.Contains(w.Body.String(), `"aiKeySet":false`) {
		t.Fatalf("cleared %s", w.Body)
	}
}

func newChannelsEnvWithSettings(t *testing.T) (*libEnv, *sql.DB) {
	var db *sql.DB
	e := newLibEnvWith(t, func(d *Deps, x *sql.DB) {
		db = x
		d.DB = x
		d.Channels = channels.New(x, nil)
	})
	return e, db
}

// Anthropic's API is not asked without a key: the SDK would otherwise run
// the request on whatever credential the host has.
func TestAISettingsAnthropicNeedsKey(t *testing.T) {
	t.Setenv("VAULT_LLM_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	e, _ := newChannelsEnvWithSettings(t)
	w := e.do("POST", "/api/settings/ai/test", `{"url":"https://api.anthropic.com","model":"claude-opus-5-5"}`, e.admin)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "ключ") {
		t.Fatalf("test without a key %d %s", w.Code, w.Body)
	}
	var cur map[string]any
	_ = json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &cur)
	cur["ai"] = map[string]any{"url": "https://api.anthropic.com/v1", "model": "claude-opus-5-5"}
	cur["streamCacheGB"], cur["quality"] = 50, 1080
	b, _ := json.Marshal(cur)
	if w := e.do("PUT", "/api/settings", string(b), e.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "ключ") {
		t.Fatalf("save without a key %d %s", w.Code, w.Body)
	}
	cur["ai"] = map[string]any{"url": "https://api.anthropic.com/v1", "model": "claude-opus-5-5", "key": "sk-ant-test"}
	b, _ = json.Marshal(cur)
	if w := e.do("PUT", "/api/settings", string(b), e.admin); w.Code != 200 {
		t.Fatalf("save with a key %d %s", w.Code, w.Body)
	}
}

// A row of one model (before the tiers) becomes three: the same model on
// every tier, except on Anthropic, where it keeps its family's tier.
func TestAISettingsTiersFromOneModel(t *testing.T) {
	for _, c := range []struct {
		in   AISettings
		want AIModels
	}{
		{AISettings{URL: "http://127.0.0.1:4000/v1", Model: "local"}, AIModels{"local", "local", "local"}},
		{AISettings{URL: channels.AnthropicURL, Model: "claude-opus-5-5"}, AIModels{"claude-haiku-5-5", "claude-sonnet-5-5", "claude-opus-5-5"}},
		{AISettings{URL: channels.AnthropicURL, Model: "claude-haiku-4-5"}, AIModels{"claude-haiku-4-5", "claude-sonnet-5-5", "claude-opus-5-5"}},
		{AISettings{URL: channels.AnthropicURL, Model: "custom"}, AIModels{"custom", "custom", "custom"}},
		{AISettings{URL: "http://x/v1", Models: AIModels{Light: "a"}}, AIModels{"a", "a", "a"}},
		{AISettings{URL: "http://x/v1", Models: AIModels{Light: "a", Medium: "b"}}, AIModels{"a", "b", "b"}},
	} {
		got := c.in.withTiers()
		if got.Models != c.want || got.Model != c.want.Medium {
			t.Errorf("%+v → %+v", c.in, got)
		}
	}
	a := AISettings{URL: "http://x/v1", Models: AIModels{"l", "m", "p"}}
	for tier, want := range map[channels.Tier]string{channels.TierLight: "l", channels.TierMedium: "m", channels.TierPowerful: "p"} {
		if got := a.llm(tier).Model; got != want {
			t.Errorf("%s: %s", tier, got)
		}
	}
}
