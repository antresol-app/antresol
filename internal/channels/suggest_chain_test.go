package channels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// chainServer answers the chat requests by script and counts them.
func chainServer(t *testing.T, code int, content string, delay time.Duration) (*LLM, *int) {
	n := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*n++
		_, _ = io.Copy(io.Discard, r.Body)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if code != 200 {
			http.Error(w, "no", code)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	t.Cleanup(srv.Close)
	return &LLM{URL: srv.URL + "/v1", Model: "m"}, n
}

func TestChainRules(t *testing.T) {
	ctx := context.Background()
	t.Run("a failure goes to the next, at most three are asked", func(t *testing.T) {
		var members []ChainMember
		var counts []*int
		for _, id := range []string{"a", "b", "c", "d"} {
			l, n := chainServer(t, 500, "", 0)
			members, counts = append(members, ChainMember{ID: id, LLM: l}), append(counts, n)
		}
		var reported []string
		c := &Chain{Members: members, Report: func(id string, _ time.Duration, err error) {
			reported = append(reported, id+map[bool]string{true: "+", false: "-"}[err == nil])
		}}
		head := &LLM{Chain: c}
		if _, err := head.AskJSON(ctx, "s", "u"); err == nil {
			t.Fatal("all failed, yet no error")
		}
		if *counts[0] != 1 || *counts[2] != 1 || *counts[3] != 0 || strings.Join(reported, ",") != "a-,b-,c-" {
			t.Fatalf("asked %d %d %d %d, reported %v", *counts[0], *counts[1], *counts[2], *counts[3], reported)
		}
		// a skipped one does not count as an attempt
		c.Skip = func(id string) bool { return id == "a" }
		if _, err := head.AskJSON(ctx, "s", "u"); err == nil || *counts[0] != 1 || *counts[3] != 1 {
			t.Fatalf("with a skipped: %v, a %d d %d", err, *counts[0], *counts[3])
		}
	})
	t.Run("nothing to ask", func(t *testing.T) {
		l, _ := chainServer(t, 200, "{}", 0)
		head := &LLM{Chain: &Chain{Members: []ChainMember{{ID: "a", LLM: l}}, Skip: func(string) bool { return true }}}
		if _, err := head.AskJSON(ctx, "s", "u"); !errors.Is(err, ErrChainEmpty) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an answer that is no JSON goes on; plain text does not need to be JSON", func(t *testing.T) {
		bad, nb := chainServer(t, 200, "sorry", 0)
		good, _ := chainServer(t, 200, "```json\n{\"a\":1}\n```", 0)
		head := &LLM{Chain: &Chain{Members: []ChainMember{{ID: "a", LLM: bad}, {ID: "b", LLM: good}}}}
		if got, err := head.AskJSON(ctx, "s", "u"); err != nil || got != `{"a":1}` || *nb != 1 {
			t.Fatalf("got %q, %v", got, err)
		}
		if got, err := head.Ping(ctx); err != nil || got != "sorry" {
			t.Fatalf("ping %q, %v", got, err)
		}
	})
	t.Run("the budget ends the call", func(t *testing.T) {
		slow, ns := chainServer(t, 200, "{}", 5*time.Second)
		next, nn := chainServer(t, 200, "{}", 0)
		slow.Timeout = 5 * time.Second
		head := &LLM{Chain: &Chain{Members: []ChainMember{{ID: "a", LLM: slow}, {ID: "b", LLM: next}}, Budget: 100 * time.Millisecond}}
		start := time.Now()
		if _, err := head.AskJSON(ctx, "s", "u"); err == nil || time.Since(start) > 2*time.Second {
			t.Fatalf("budget: %v after %v", err, time.Since(start))
		}
		if *ns != 1 || *nn != 0 {
			t.Fatalf("asked %d, %d", *ns, *nn)
		}
	})
	t.Run("ErrorInfo", func(t *testing.T) {
		for _, c := range []struct {
			err  error
			kind string
			code int
		}{
			{httpStatusErr{code: 401}, ErrHTTP, 401},
			{errBadAnswer, ErrAnswer, 0},
			{errEmptyAnswer, ErrAnswer, 0},
			{errNoKey, ErrKey, 0},
			{context.DeadlineExceeded, ErrTimeout, 0},
			{errors.New("x"), ErrOther, 0},
		} {
			if k, code := ErrorInfo(c.err); k != c.kind || code != c.code {
				t.Errorf("%v: %s %d", c.err, k, code)
			}
		}
		// a refused connection
		_, err := (&LLM{URL: "http://127.0.0.1:1/v1", Model: "m"}).Ping(ctx)
		if k, _ := ErrorInfo(err); k != ErrNetwork {
			t.Errorf("unreachable: %v → %s", err, k)
		}
	})
}

func TestListModels(t *testing.T) {
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("Authorization")+r.Header.Get("x-api-key")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"models/gemini-b"},{"id":"auto"},{"id":"auto"},{"id":""}]}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3.5:2b"},{"name":"gemma"}]}`))
		default:
			http.Error(w, "nope", 404)
		}
	}))
	defer srv.Close()
	got, err := ListModels(context.Background(), nil, srv.URL+"/v1/", "k1")
	if err != nil || strings.Join(got, ",") != "auto,gemini-b" || path != "/v1/models" || key != "Bearer k1" {
		t.Fatalf("openai: %v %v %s %s", got, err, path, key)
	}
	got, err = ListModels(context.Background(), nil, srv.URL, "")
	if err != nil || strings.Join(got, ",") != "gemma,qwen3.5:2b" || path != "/api/tags" {
		t.Fatalf("ollama: %v %v %s", got, err, path)
	}
	if _, err = ListModels(context.Background(), nil, srv.URL+"/other/v1", ""); err == nil {
		t.Fatal("404 is an error")
	} else if k, code := ErrorInfo(err); k != ErrHTTP || code != 404 {
		t.Fatalf("%v → %s %d", err, k, code)
	}
	// Anthropic's: its host, x-api-key, newest first as listed
	cl := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		path, key = r.URL.Host+r.URL.Path, r.Header.Get("x-api-key")+"|"+r.Header.Get("anthropic-version")
		rec := httptest.NewRecorder()
		_, _ = rec.WriteString(`{"data":[{"id":"claude-opus-5-5"},{"id":"claude-haiku-5-5"}]}`)
		return rec.Result(), nil
	})}
	got, err = ListModels(context.Background(), cl, "https://api.anthropic.com/v1", "sk-ant")
	if err != nil || strings.Join(got, ",") != "claude-opus-5-5,claude-haiku-5-5" || path != "api.anthropic.com/v1/models" || key != "sk-ant|2023-06-01" {
		t.Fatalf("anthropic: %v %v %s %s", got, err, path, key)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
