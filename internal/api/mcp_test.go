package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

// rpc posts one JSON-RPC message to /mcp with key.
func (e *libEnv) rpc(key, body string) (int, map[string]any) {
	e.t.Helper()
	w := e.do("POST", "/mcp", body, nil, append(bearer(key), "Accept", "application/json, text/event-stream")...)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("mcp body %d %q", w.Code, w.Body)
		}
	}
	return w.Code, out
}

func toolNames(res map[string]any) []string {
	var names []string
	for _, t := range res["result"].(map[string]any)["tools"].([]any) {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	return names
}

func toolText(t *testing.T, res map[string]any) (string, bool) {
	t.Helper()
	r, ok := res["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", res)
	}
	content := r["content"].([]any)
	isErr, _ := r["isError"].(bool)
	return content[0].(map[string]any)["text"].(string), isErr
}

func TestMCPInitializeListCall(t *testing.T) {
	e := newAPIKeyEnv(t)
	id, _ := e.movie("Матрица", "", "x")
	key := e.newKey(e.admin, auth.ScopeAct)

	code, res := e.rpc(key, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if code != 200 {
		t.Fatalf("initialize %d %v", code, res)
	}
	init := res["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" || init["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize result %v", init)
	}
	if code, _ := e.rpc(key, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification %d", code)
	}

	_, res = e.rpc(key, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	names := strings.Join(toolNames(res), ",")
	for _, want := range []string{"search_library", "get_item", "set_watched", "tv_now", "server_status"} {
		if !strings.Contains(names, want) {
			t.Fatalf("tools/list lacks %s: %s", want, names)
		}
	}
	if strings.Contains(names, "scan_library") || strings.Contains(names, "download_title") {
		t.Fatalf("act key without the addon sees %s", names)
	}

	_, res = e.rpc(key, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_library","arguments":{"query":"Матр"}}}`)
	text, isErr := toolText(t, res)
	if isErr || !strings.Contains(text, "Матрица") || !strings.Contains(text, `"id":`+itoa(id)) {
		t.Fatalf("search: %v %s", isErr, text)
	}
	if strings.Contains(text, "poster") {
		t.Fatalf("pictures not dropped: %s", text)
	}

	_, res = e.rpc(key, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"set_watched","arguments":{"id":`+itoa(id)+`}}}`)
	if text, isErr := toolText(t, res); isErr {
		t.Fatalf("set_watched: %s", text)
	}
	// the tool's write went through the guard: in the log, by the tool's name
	w := e.do("GET", "/api/me/api-keys/log", "", e.admin)
	if !strings.Contains(w.Body.String(), `"via":"mcp:set_watched"`) {
		t.Fatalf("log %s", w.Body)
	}

	_, res = e.rpc(key, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"server_status","arguments":{}}}`)
	if text, isErr := toolText(t, res); isErr || !strings.Contains(text, `"version"`) {
		t.Fatalf("server_status: %s", text)
	}

	_, res = e.rpc(key, `{"jsonrpc":"2.0","id":6,"method":"nope"}`)
	if res["error"].(map[string]any)["code"].(float64) != rpcMethodNotFound {
		t.Fatalf("unknown method %v", res)
	}
	_, res = e.rpc(key, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"scan_library"}}`)
	if res["error"] == nil {
		t.Fatalf("a hidden tool ran: %v", res)
	}
}

func TestMCPScopes(t *testing.T) {
	e := newAPIKeyEnv(t)
	id, _ := e.movie("Матрица", "", "x")
	read, admin := e.newKey(e.admin, auth.ScopeRead), e.newKey(e.admin, auth.ScopeAdmin)

	_, res := e.rpc(read, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	names := strings.Join(toolNames(res), ",")
	if strings.Contains(names, "set_watched") || !strings.Contains(names, "search_library") {
		t.Fatalf("read key tools %s", names)
	}
	_, res = e.rpc(read, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"set_watched","arguments":{"id":`+itoa(id)+`}}}`)
	if res["error"] == nil {
		t.Fatalf("read key ran set_watched: %v", res)
	}

	_, res = e.rpc(admin, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	names = strings.Join(toolNames(res), ",")
	for _, want := range []string{"scan_library", "restart_addon", "list_profiles"} {
		if !strings.Contains(names, want) {
			t.Fatalf("admin key lacks %s: %s", want, names)
		}
	}
	_, res = e.rpc(admin, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_profiles"}}`)
	if text, isErr := toolText(t, res); isErr || !strings.Contains(text, "Папа") || strings.Contains(text, "password\"") {
		t.Fatalf("list_profiles: %s", text)
	}
}

func TestMCPNeedsAKey(t *testing.T) {
	e := newAPIKeyEnv(t)
	// a browser session is not a key
	w := e.do("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, e.admin)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie on /mcp: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil); w.Code != 401 {
		t.Fatalf("no auth: %d", w.Code)
	}
	key := e.newKey(e.admin, auth.ScopeRead)
	if w := e.do("GET", "/mcp", "", nil, bearer(key)...); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp: %d", w.Code)
	}
}

// The download tools go to the addon that fulfils the kids' requests,
// through the addon proxy, only when one is on.
func TestMCPDownloadToolsNeedTheAddon(t *testing.T) {
	e := newAddonEnv(t)
	k := &libEnv{env: e.env, admin: e.admin}
	key := k.newKey(e.user, auth.ScopeAct)
	_, res := k.rpc(key, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if names := strings.Join(toolNames(res), ","); strings.Contains(names, "download_title") {
		t.Fatalf("no downloading addon, yet %s", names)
	}
	e.fake.mu.Lock()
	h := e.fake.installed["hello"]
	h.Manifest.Hooks, h.Manifest.Access = []string{mcpDownloaderHook}, "adults"
	e.fake.installed["hello"] = h
	e.fake.mu.Unlock()
	_, res = k.rpc(key, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if names := strings.Join(toolNames(res), ","); !strings.Contains(names, "download_title") || !strings.Contains(names, "discover_titles") {
		t.Fatalf("downloading addon on, tools %s", names)
	}
	// the call reaches the addon (the test addon has no such route: its 404)
	_, res = k.rpc(key, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"discover_titles","arguments":{"query":"Matrix"}}}`)
	if text, isErr := toolText(t, res); !isErr || !strings.Contains(text, "HTTP 404") {
		t.Fatalf("discover through the proxy: %v %s", isErr, text)
	}
}
