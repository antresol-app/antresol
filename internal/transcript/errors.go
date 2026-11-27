package transcript

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is what a failed request says to do.
type Kind int

const (
	// KindInput: this piece was refused or answered badly: the book's
	// failure.
	KindInput Kind = iota
	// KindRateLimit: wait (RetryAfter) and ask again: nothing failed.
	KindRateLimit
	// KindProvider: the service cannot be asked now (the key, the address,
	// the model, an outage): no book's fault.
	KindProvider
)

// Error is a failed request to the speech-to-text service.
type Error struct {
	Kind   Kind
	Status int
	// RetryAfter is how long the service asks to wait (0: it did not say).
	RetryAfter time.Duration
	// Shorter: a shorter piece may get through (the answer was cut short or
	// the content blocked).
	Shorter bool
	msg     string
}

func (e *Error) Error() string { return e.msg }

// errorf is an error in the service's words: cut short, the key blanked.
func (c *Client) errorf(kind Kind, status int, format string, args ...any) *Error {
	s := fmt.Sprintf(format, args...)
	if c.Key != "" {
		s = strings.ReplaceAll(s, c.Key, "***")
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return &Error{Kind: kind, Status: status, msg: "stt: " + s}
}

var (
	// reTryAgain: «Please try again in 7m12.5s» (Groq, OpenAI).
	reTryAgain = regexp.MustCompile(`(?i)try again in ((?:\d+(?:\.\d+)?(?:ms|h|m|s))+)`)
	// reProviderFault: a 400 that is about the service's setup, not about
	// the audio: a key, a model or a budget.
	reProviderFault = regexp.MustCompile(`(?i)api[ _-]?key[ _]?(?:not valid|invalid)|API_KEY_INVALID|invalid model|unknown model|model[^"{}]{0,80}(?:not found|does not exist|not supported)|budget`)
)

// httpError reads an answer other than 200.
func (c *Client) httpError(resp *http.Response, raw []byte) *Error {
	detail := strings.TrimSpace(string(raw))
	var body struct {
		Error struct {
			Message string `json:"message"`
			Details []struct {
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	retryDelay := ""
	if json.Unmarshal(raw, &body) == nil {
		if body.Error.Message != "" {
			detail = body.Error.Message // the service's own words, not its JSON
		}
		for _, d := range body.Error.Details {
			if d.RetryDelay != "" {
				retryDelay = d.RetryDelay
			}
		}
	}
	after := retryAfter(resp.Header.Get("Retry-After"))
	if after == 0 {
		after, _ = time.ParseDuration(retryDelay)
	}
	if m := reTryAgain.FindStringSubmatch(detail); after == 0 && m != nil {
		after, _ = time.ParseDuration(m[1])
	}
	status := resp.StatusCode
	kind := KindInput
	switch {
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable && after > 0:
		kind = KindRateLimit
	case status < 400 || status >= 500, status == http.StatusUnauthorized, status == http.StatusPaymentRequired,
		status == http.StatusForbidden, status == http.StatusNotFound, status == http.StatusMethodNotAllowed,
		status == http.StatusRequestTimeout, reProviderFault.Match(raw):
		kind = KindProvider
	}
	if loc := resp.Header.Get("Location"); loc != "" && status < 400 {
		detail = "redirected to " + loc + " (use that address)"
	}
	e := c.errorf(kind, status, "%s %s", resp.Status, detail)
	if kind == KindRateLimit {
		e.RetryAfter = after
	}
	return e
}

// retryAfter reads a Retry-After header: seconds (a fraction too) or a
// date.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil {
		return max(time.Duration(s*float64(time.Second)), 0)
	}
	if at, err := http.ParseTime(v); err == nil {
		return max(time.Until(at).Round(time.Second), 0)
	}
	return 0
}
