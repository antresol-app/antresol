package telegram

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

const testToken = "123:test-token"

// fakeAPI answers /bot<token>/<method> with what the test sets for the method.
type fakeAPI struct {
	t      *testing.T
	status map[string]int
	body   map[string]string
	got    map[string]map[string]any
}

func newFake(t *testing.T) (*fakeAPI, *Client) {
	t.Helper()
	f := &fakeAPI{t: t, status: map[string]int{}, body: map[string]string{}, got: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+testToken+"/") {
			http.Error(w, "wrong token", http.StatusNotFound)
			return
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/")
		raw, _ := io.ReadAll(r.Body)
		in := map[string]any{}
		_ = json.Unmarshal(raw, &in)
		f.got[method] = in
		if st := f.status[method]; st != 0 {
			w.WriteHeader(st)
		}
		_, _ = io.WriteString(w, f.body[method])
	}))
	t.Cleanup(srv.Close)
	return f, &Client{Base: srv.URL}
}

func TestGetMe(t *testing.T) {
	f, c := newFake(t)
	f.body["getMe"] = `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Home Media","username":"home_media_bot"}}`
	me, err := c.GetMe(context.Background(), testToken)
	if err != nil || me.ID != 42 || me.Username != "home_media_bot" || me.Name != "Home Media" {
		t.Fatalf("getMe: %+v %v", me, err)
	}
}

func TestRejectedTokenIsUnauthorized(t *testing.T) {
	f, c := newFake(t)
	f.status["getMe"] = http.StatusUnauthorized
	f.body["getMe"] = `{"ok":false,"error_code":401,"description":"Unauthorized"}`
	_, err := c.GetMe(context.Background(), testToken)
	if !IsUnauthorized(err) {
		t.Fatalf("want unauthorized, got %v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the error carries the token: %v", err)
	}
}

func TestTooManyRequestsCarriesRetryAfter(t *testing.T) {
	f, c := newFake(t)
	f.status["sendMessage"] = http.StatusTooManyRequests
	f.body["sendMessage"] = `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`
	err := c.SendMessage(context.Background(), testToken, 5, "hi")
	d, ok := RetryAfter(err)
	if !ok || d != 7*time.Second {
		t.Fatalf("retry after: %v %v (%v)", d, ok, err)
	}
}

func TestBlockedAndConflict(t *testing.T) {
	f, c := newFake(t)
	f.status["sendMessage"] = http.StatusForbidden
	f.body["sendMessage"] = `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`
	if err := c.SendMessage(context.Background(), testToken, 5, "hi"); !IsGone(err) {
		t.Fatalf("a blocked bot is a gone chat: %v", err)
	}
	f.status["sendMessage"] = http.StatusBadRequest
	f.body["sendMessage"] = `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`
	if err := c.SendMessage(context.Background(), testToken, 5, "hi"); !IsGone(err) {
		t.Fatalf("an unknown chat is gone: %v", err)
	}
	f.body["sendMessage"] = `{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`
	if err := c.SendMessage(context.Background(), testToken, 5, "hi"); err == nil || IsGone(err) {
		t.Fatalf("a long message is not a gone chat: %v", err)
	}
	f.status["getUpdates"] = http.StatusConflict
	f.body["getUpdates"] = `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`
	if _, err := c.GetUpdates(context.Background(), testToken, 0, 0); !IsConflict(err) {
		t.Fatalf("conflict: %v", err)
	}
}

func TestGetUpdatesAsksFromTheOffsetAndReadsMessages(t *testing.T) {
	f, c := newFake(t)
	f.body["getUpdates"] = `{"ok":true,"result":[
		{"update_id":10,"message":{"message_id":1,"from":{"id":7,"first_name":"Аня","username":"anya","language_code":"ru"},"chat":{"id":7,"type":"private"},"text":"/start abc"}},
		{"update_id":11,"edited_message":{"message_id":2}}]}`
	ups, err := c.GetUpdates(context.Background(), testToken, 10, 25)
	if err != nil || len(ups) != 2 {
		t.Fatalf("updates: %v %v", ups, err)
	}
	m := ups[0].Message
	if ups[0].ID != 10 || m == nil || m.Text != "/start abc" || m.Chat.ID != 7 || m.Chat.Type != "private" || m.From.Username != "anya" || m.From.LanguageCode != "ru" {
		t.Fatalf("message: %+v", ups[0])
	}
	if ups[1].Message != nil {
		t.Fatalf("an edited message is no message: %+v", ups[1])
	}
	got := f.got["getUpdates"]
	if got["offset"] != float64(10) || got["timeout"] != float64(25) {
		t.Fatalf("request: %v", got)
	}
}

func TestSendMessageIsPlainTextWithoutPreview(t *testing.T) {
	f, c := newFake(t)
	f.body["sendMessage"] = `{"ok":true,"result":{"message_id":1}}`
	if err := c.SendMessage(context.Background(), testToken, -100, "a <b> & c"); err != nil {
		t.Fatal(err)
	}
	got := f.got["sendMessage"]
	if got["chat_id"] != float64(-100) || got["text"] != "a <b> & c" || got["parse_mode"] != nil {
		t.Fatalf("request: %v", got)
	}
	if lp, _ := got["link_preview_options"].(map[string]any); lp["is_disabled"] != true {
		t.Fatalf("link preview: %v", got)
	}
}

func TestTransportErrorsNeverCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing listens
	c := &Client{Base: base}
	_, err := c.GetMe(context.Background(), testToken)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("the error carries the token: %v", err)
	}
}

func TestAnswerThatIsNotJSON(t *testing.T) {
	f, c := newFake(t)
	f.status["getMe"] = http.StatusBadGateway
	f.body["getMe"] = `<html>bad gateway</html>`
	_, err := c.GetMe(context.Background(), testToken)
	var e *Error
	if !errors.As(err, &e) || e.Code != http.StatusBadGateway {
		t.Fatalf("want a 502 error, got %v", err)
	}
}

func TestContextEndsTheCall(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (&Client{Base: srv.URL}).GetMe(ctx, testToken); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a deadline error, got %v", err)
	}
}

func TestValidToken(t *testing.T) {
	for tok, want := range map[string]bool{
		testToken:                       true,
		"123456:ABC-def_":               true,
		"":                              false,
		"no-colon":                      false,
		"12:a/b":                        false,
		"12:a b":                        false,
		"x:abc":                         false,
		strings.Repeat("1", 200) + ":a": false,
	} {
		if got := ValidToken(tok); got != want {
			t.Errorf("ValidToken(%q) = %v", tok, got)
		}
	}
}
