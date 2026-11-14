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
