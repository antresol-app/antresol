package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/version"
	"github.com/go-chi/chi/v5"
)

// The MCP server (Model Context Protocol, Streamable HTTP transport) at
// /mcp: an AI assistant (Claude Desktop, Claude Code, any MCP client) uses
// the library through tools, signed in with a personal API key (Bearer).
//
// A minimal, stateless implementation of the transport: every JSON-RPC
// message is a POST; a request is answered with one application/json
// response, a notification or a response with 202 and no body. No
// session id is given and no server-sent stream is offered (GET and
// DELETE: 405), which the transport allows. Only the tools capability.
//
// A tool does nothing the key could not do through the /api routes: it
// calls those routes inside the server (mcpServer.call) with the request's
// own key, so the guard checks the key, its scope and the profile's rights
// again, and logs the tool's writes (auth.WithInternalCall names the tool).

// mcpProtocolVersions: the protocol revisions the server speaks, newest
// first. initialize answers the client's own when it is one of them.
var mcpProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// mcpMaxBody: the largest JSON-RPC message taken.
const mcpMaxBody = 1 << 20

// mcpMaxText: the largest tool result sent back (a longer one is cut).
const mcpMaxText = 60000

type mcpServer struct {
	// api is the whole router (set once it is built): the tools' calls
	api http.Handler
	// addons: the installed addons (nil: none)
	addons addonRuntime
}

// downloader is the id of the addon on that the download tools use (see
// mcpDownloaderHook), "" when there is none.
func (m *mcpServer) downloader(ctx context.Context) string {
	if m.addons == nil {
		return ""
	}
	list, err := m.addons.List(ctx)
	if err != nil {
		return ""
	}
	for _, in := range list {
		if in.Enabled && slices.Contains(in.Manifest.Hooks, mcpDownloaderHook) {
			return in.ID
		}
	}
	return ""
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

func mountMCP(r interface {
	Handle(string, http.Handler)
}, m *mcpServer) {
	r.Handle(auth.MCPPath, m)
}

func (m *mcpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.APIKeyFrom(r.Context()); !ok {
		writeErr(w, r, http.StatusUnauthorized, "Нужен ключ API: создайте его в «Настройки → Ключи API и ИИ»")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !slices.Contains(mcpProtocolVersions, v) {
		http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, mcpMaxBody+1))
	if err != nil || len(body) > mcpMaxBody {
		mcpWrite(w, http.StatusRequestEntityTooLarge, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{rpcInvalidRequest, "message too large"}})
		return
	}
	body = bytes.TrimSpace(body)
	// a batch (protocol 2025-03-26) is answered with an array
	if len(body) > 0 && body[0] == '[' {
		var msgs []rpcMessage
		if err := json.Unmarshal(body, &msgs); err != nil || len(msgs) == 0 {
			mcpWrite(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{rpcParseError, "parse error"}})
			return
		}
		var out []rpcResponse
		for _, msg := range msgs {
			if resp, ok := m.handle(r, msg); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mcpWrite(w, http.StatusOK, out)
		return
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		mcpWrite(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{rpcParseError, "parse error"}})
		return
	}
	resp, ok := m.handle(r, msg)
	if !ok {
		w.WriteHeader(http.StatusAccepted) // a notification or a client's response
		return
	}
	mcpWrite(w, http.StatusOK, resp)
}

func mcpWrite(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// handle answers one message; ok is false when it needs no answer.
func (m *mcpServer) handle(r *http.Request, msg rpcMessage) (rpcResponse, bool) {
	if len(msg.ID) == 0 || string(msg.ID) == "null" {
		return rpcResponse{}, false // notifications (initialized, cancelled…) and responses
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: msg.ID}
	if msg.JSONRPC != "2.0" || msg.Method == "" {
		resp.Error = &rpcError{rpcInvalidRequest, "invalid request"}
		return resp, true
	}
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		v := mcpProtocolVersions[0]
		if slices.Contains(mcpProtocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "antresol", "title": "Antresol", "version": version.Version},
			"instructions": "Antresol is a home media server (films, series, music, books, TV channels). " +
				"Find titles with search_library, then use their ids with get_item and set_watched. " +
				"Titles not in the library can be found with discover_titles and downloaded with download_title (when the server has a download addon).",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		list := []map[string]any{}
		for _, t := range m.tools(r) {
			list = append(list, t.describe())
		}
		resp.Result = map[string]any{"tools": list}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			resp.Error = &rpcError{rpcInvalidParams, "invalid params"}
			return resp, true
		}
		var tool *mcpTool
		for _, t := range m.tools(r) {
			if t.name == p.Name {
				tool = &t
				break
			}
		}
		if tool == nil {
			resp.Error = &rpcError{rpcInvalidParams, "unknown tool: " + p.Name}
			return resp, true
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		out, err := tool.run(&toolCall{m: m, r: r, tool: tool.name}, mcpArgs(p.Arguments))
		resp.Result = toolResult(out, err)
	default:
		resp.Error = &rpcError{rpcMethodNotFound, "method not found: " + msg.Method}
	}
	return resp, true
}

// toolResult is a tools/call result: the value as JSON text, or the error
// (isError: the model sees it and may try otherwise).
func toolResult(v any, err error) map[string]any {
	if err != nil {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}
	}
	b, jerr := json.Marshal(v)
	if jerr != nil {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": jerr.Error()}}, "isError": true}
	}
	text := string(b)
	if len(text) > mcpMaxText {
		text = text[:mcpMaxText] + " …(cut)"
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

// toolCall is one tool's run: the request it came with.
type toolCall struct {
	m    *mcpServer
	r    *http.Request
	tool string
}

// apiError is a /api route's refusal, as the tool reports it.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// call runs method path on the router as the request's key and decodes
// the JSON answer into out (nil: dropped). A status of 400 and above is
// an *apiError with the route's message.
func (c *toolCall) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	ctx := auth.WithInternalCall(c.r.Context(), "mcp:"+c.tool)
	// a fresh routing: the router would reuse the outer request's route context
	ctx = context.WithValue(ctx, chi.RouteCtxKey, nil)
	req, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return err
	}
	req.RemoteAddr, req.Host, req.TLS = c.r.RemoteAddr, c.r.Host, c.r.TLS
	for _, h := range []string{"Authorization", "X-Forwarded-For", "X-Real-IP", "Forwarded", "X-Forwarded-Proto", "User-Agent"} {
		if v := c.r.Header.Values(h); len(v) > 0 {
			req.Header[h] = v
		}
	}
	req.Header.Set("Accept-Language", i18n.FromContext(c.r.Context()))
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &bufferWriter{h: http.Header{}}
	c.m.api.ServeHTTP(rec, req)
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	if rec.code >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.buf.Bytes(), &e)
		return &apiError{Status: rec.code, Message: e.Error}
	}
	if out == nil || rec.buf.Len() == 0 {
		return nil
	}
	if err := json.Unmarshal(rec.buf.Bytes(), out); err != nil {
		log.Printf("mcp: %s %s: %v", method, path, err)
		return errors.New("the server's answer could not be read")
	}
	return nil
}

// bufferWriter is the in-process ResponseWriter of toolCall.call.
type bufferWriter struct {
	h    http.Header
	code int
	buf  bytes.Buffer
}

func (b *bufferWriter) Header() http.Header { return b.h }
func (b *bufferWriter) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}
func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.code == 0 {
		b.code = http.StatusOK
	}
	if b.buf.Len() > 8<<20 {
		return 0, errors.New("mcp: answer too large")
	}
	return b.buf.Write(p)
}
func (b *bufferWriter) Flush() {}

// mcpArgs are a tool's arguments with typed getters.
type mcpArgs map[string]any

func (a mcpArgs) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprint(int64(v))
	}
	return ""
}

func (a mcpArgs) num(k string) (int64, bool) {
	switch v := a[k].(type) {
	case float64:
		return int64(v), v == float64(int64(v))
	case string:
		var n int64
		_, err := fmt.Sscan(strings.TrimSpace(v), &n)
		return n, err == nil
	}
	return 0, false
}

func (a mcpArgs) boolean(k string, def bool) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return def
}
