// Package telegram is the small part of the Telegram Bot API the server
// uses: getMe (whose bot is this token), getUpdates (long polling, so a home
// server behind NAT needs no public webhook) and sendMessage (plain text).
//
// The token is part of every request's address. Nothing here puts it in an
// error or a log line: a failed call says which method failed and why, never
// where it went.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is the Bot API's address.
const DefaultBase = "https://api.telegram.org"

// Client calls the Bot API. The zero value works.
type Client struct {
	// Base is the API's address ("" : DefaultBase; tests: a fake server).
	Base string
	// HTTP makes the requests (nil: a client with no timeout of its own,
	// the call's context bounds it).
	HTTP *http.Client
}

// Me is the bot a token belongs to.
type Me struct {
	ID       int64
	Username string // without "@"
	Name     string
}

// Update is one thing that happened to the bot; only messages are read.
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

// Message is a text sent to the bot.
type Message struct {
	Text string `json:"text"`
	Chat Chat   `json:"chat"`
	From *User  `json:"from"`
}

// Chat is where a message came from; Type is "private" for a person's own
// chat with the bot.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// User is a Telegram account.
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LanguageCode string `json:"language_code"`
}

// Error is an answer of the API that says no.
type Error struct {
	Method      string
	Code        int // the API's error_code (else the HTTP status)
	Description string
	// RetryAfter: how long to wait before asking again (429).
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	s := "telegram " + e.Method + ": " + strconv.Itoa(e.Code)
	if e.Description != "" {
		s += " " + e.Description
	}
	return s
}

// netError is a failure before an answer: the connection, the deadline.
type netError struct {
	method string
	msg    string
	err    error
}

func (e *netError) Error() string { return "telegram " + e.method + ": " + e.msg }
func (e *netError) Unwrap() error { return e.err }

// IsUnauthorized: the token is wrong or was revoked.
func IsUnauthorized(err error) bool { return hasCode(err, http.StatusUnauthorized) }

// IsConflict: another getUpdates is running for the token, or a webhook is set.
func IsConflict(err error) bool { return hasCode(err, http.StatusConflict) }

// IsGone: the chat cannot be written to any more — the person blocked the
// bot, deleted the account, or the chat is unknown to this bot (the token is
// another bot's). Sending again is pointless.
func IsGone(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	if e.Code == http.StatusForbidden {
		return true
	}
	d := strings.ToLower(e.Description)
	return e.Code == http.StatusBadRequest && (strings.Contains(d, "chat not found") || strings.Contains(d, "user not found"))
}

// RetryAfter is how long the API asked to wait (429).
func RetryAfter(err error) (time.Duration, bool) {
	var e *Error
	if errors.As(err, &e) && e.Code == http.StatusTooManyRequests {
		return e.RetryAfter, true
	}
	return 0, false
}

func hasCode(err error, code int) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

var tokenRe = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{1,100}$`)

// ValidToken: a bot token has the shape BotFather gives (digits, a colon,
// letters, digits, - and _). It also keeps what is not safe in an address
// out of the request.
func ValidToken(s string) bool { return tokenRe.MatchString(s) }

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return DefaultBase
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// envelope is the shape of every answer.
type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call posts params to method and decodes the result into out (nil: none).
func (c *Client) call(ctx context.Context, token, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return &netError{method: method, msg: "bad request", err: nil}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return redact(method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return redact(method, err)
	}
	var env envelope
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		// not the API's own answer (a proxy's page): the status says what it can
		if resp.StatusCode == http.StatusOK {
			return &netError{method: method, msg: "unreadable answer"}
		}
		return &Error{Method: method, Code: resp.StatusCode}
	}
	if !env.OK {
		e := &Error{Method: method, Code: env.ErrorCode, Description: env.Description,
			RetryAfter: time.Duration(env.Parameters.RetryAfter) * time.Second}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		if e.Code == http.StatusTooManyRequests && e.RetryAfter == 0 {
			if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				e.RetryAfter = time.Duration(n) * time.Second
			}
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return &netError{method: method, msg: "unreadable answer"}
	}
	return nil
}

// redact makes err of a failed request one that never shows the request's
// address (it holds the token): the cause stays for errors.Is.
func redact(method string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return &netError{method: method, msg: err.Error(), err: err}
}

// GetMe tells whose bot the token is (and so whether it works).
func (c *Client) GetMe(ctx context.Context, token string) (Me, error) {
	var r struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
	}
	if err := c.call(ctx, token, "getMe", struct{}{}, &r); err != nil {
		return Me{}, err
	}
	return Me{ID: r.ID, Username: r.Username, Name: r.FirstName}, nil
}

// GetUpdates waits up to timeoutSec seconds (0: no wait) for updates with an
// id of offset or more; ctx bounds the wait too.
func (c *Client) GetUpdates(ctx context.Context, token string, offset int64, timeoutSec int) ([]Update, error) {
	var out []Update
	err := c.call(ctx, token, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeoutSec, "allowed_updates": []string{"message"},
	}, &out)
	return out, err
}

// SendMessage sends plain text (no formatting, no link preview) to a chat.
func (c *Client) SendMessage(ctx context.Context, token string, chatID int64, text string) error {
	return c.call(ctx, token, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "link_preview_options": map[string]any{"is_disabled": true},
	}, nil)
}
