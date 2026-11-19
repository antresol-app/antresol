package addonkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HelperSettings: the helper is on an address the admin typed in the
// addon's own settings page (HelperState.Source).
const HelperSettings = "settings"

// The problems of HelperState.Problem: why a helper does not answer, for
// the server's «Состояние» page.
const (
	HelperProblemDNS     = "dns"     // no such name on the network
	HelperProblemRefused = "refused" // nothing listens there
	HelperProblemTimeout = "timeout" // no answer in time
	HelperProblemStatus  = "status"  // it answers with an error status
	HelperProblemAuth    = "auth"    // it refuses the addon's credentials (401/403)
	HelperProblemUnset   = "unset"   // no address at all (Source HelperNone)
	HelperProblemOther   = "other"
)

// HelperState is one helper as the addon itself uses it: the answer of
// the helpers.status hook ({"helpers": [...]}, HelpersStatus). The server's
// «Состояние» page shows it instead of probing the helper itself, since
// only the addon knows the address it really goes to (its settings page,
// the admin's variable, or the compose name).
type HelperState struct {
	Name string `json:"name"`
	// Source is where the address comes from: HelperOverride (the admin's
	// variable), HelperSettings (the addon's settings page), HelperCompose
	// (the compose name) or HelperNone.
	Source string `json:"source"`
	// Address is the compose address (Source HelperCompose or HelperNone);
	// never an address the admin gave, which may carry a secret.
	Address string `json:"address,omitempty"`
	OK      bool   `json:"ok"`
	// Problem is why it does not answer (HelperProblem*), "" when OK.
	Problem string `json:"problem,omitempty"`
	// CheckedAt is when OK and Problem were learnt (zero: never).
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	// Live: OK and Problem come from the addon's own last real request to
	// the helper, not from a probe.
	Live bool `json:"live,omitempty"`
}

// HelpersStatus is the answer of the helpers.status hook.
type HelpersStatus struct {
	Helpers []HelperState `json:"helpers"`
}

// HelperProblem sorts an error of a request to a helper (HelperProblem*);
// "" for nil.
func HelperProblem(err error) string {
	if err == nil {
		return ""
	}
	var dns *net.DNSError
	var st *HelperStatusError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		if dns.IsTimeout {
			return HelperProblemTimeout
		}
		return HelperProblemDNS
	case errors.As(err, &st):
		if st.Code == http.StatusUnauthorized || st.Code == http.StatusForbidden {
			return HelperProblemAuth
		}
		return HelperProblemStatus
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return HelperProblemTimeout
	case strings.Contains(err.Error(), "connection refused"):
		return HelperProblemRefused
	}
	return HelperProblemOther
}

// HelperStatusError is a helper's answer of an error status.
type HelperStatusError struct{ Code int }

func (e *HelperStatusError) Error() string { return fmt.Sprintf("status %d", e.Code) }

// HelperCheckTimeout bounds one ProbeAddress: long enough for a helper
// that checks credentials or warms up on the first request.
const HelperCheckTimeout = 10 * time.Second

// ProbeAddress asks health on base (an address the addon uses; client nil:
// a plain client, its timeout HelperCheckTimeout when it has none). Any
// answer below 400 is up; with anyAnswer, any HTTP answer but 401 and 403
// is (an address of the admin's may put the service under a path of its
// own). Redirects are not followed. An error status is a
// *HelperStatusError; the error never names base.
func ProbeAddress(ctx context.Context, client *http.Client, base, health string, anyAnswer bool) error {
	c := http.Client{Timeout: HelperCheckTimeout}
	if client != nil {
		c = *client
		if c.Timeout == 0 {
			c.Timeout = HelperCheckTimeout
		}
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if health == "" {
		health = "/"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+health, nil)
	if err != nil {
		return errors.New("bad address")
	}
	resp, err := c.Do(req)
	if err != nil {
		return unnamed(err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return &HelperStatusError{Code: resp.StatusCode}
	case resp.StatusCode >= 400 && !anyAnswer:
		return &HelperStatusError{Code: resp.StatusCode}
	}
	return nil
}

// unnamed strips the URL from a client error (it may carry a secret),
// keeping what HelperProblem sorts by.
func unnamed(err error) error {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if inner := ue.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}

// HelperHealth is a helper's rolling health: the outcome of the addon's
// last real request to it, or of its last probe. The zero value is ready
// (nothing known yet); a nil *HelperHealth records nothing.
type HelperHealth struct {
	mu      sync.Mutex
	known   bool
	ok      bool
	problem string
	at      time.Time
	live    bool
	now     func() time.Time
}

func (h *HelperHealth) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// Record keeps the outcome of a real request (live). A request the caller
// gave up on (context.Canceled) says nothing of the helper and is not kept.
func (h *HelperHealth) Record(err error) { h.record(err, true) }

// RecordProbe keeps the outcome of a probe.
func (h *HelperHealth) RecordProbe(err error) { h.record(err, false) }

func (h *HelperHealth) record(err error, live bool) {
	if h == nil || errors.Is(err, context.Canceled) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.known, h.ok, h.problem, h.at, h.live = true, err == nil, HelperProblem(err), h.clock(), live
}

// Last is the last outcome kept: ok, its problem, when, and whether it was
// a real request; known false when none.
func (h *HelperHealth) Last() (ok bool, problem string, at time.Time, live, known bool) {
	if h == nil {
		return false, "", time.Time{}, false, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ok, h.problem, h.at, h.live, h.known
}

// HelperFresh is how old an outcome may be for State to answer with it
// instead of probing.
const HelperFresh = 2 * time.Minute

// State fills st's OK, Problem, CheckedAt and Live: from the last outcome
// when it is younger than HelperFresh, else from probe (nil: none, the
// last outcome as it is), which is recorded. Source HelperNone with no
// probe is Problem HelperProblemUnset.
func (h *HelperHealth) State(ctx context.Context, st HelperState, probe func(ctx context.Context) error) HelperState {
	ok, problem, at, live, known := h.Last()
	if probe != nil && (!known || h.clock().Sub(at) > HelperFresh) {
		pctx, cancel := context.WithTimeout(ctx, HelperCheckTimeout)
		err := probe(pctx)
		cancel()
		if h == nil {
			h = &HelperHealth{}
		}
		h.RecordProbe(err)
		ok, problem, at, live, known = h.Last()
	}
	switch {
	case known:
		st.OK, st.Problem, st.CheckedAt, st.Live = ok, problem, at, live
	case st.Source == HelperNone:
		st.Problem = HelperProblemUnset
	}
	return st
}

// HelperTransport wraps a transport (nil: http.DefaultTransport) so that
// every request to the helper's host (Host, as in a URL: "name:port")
// records its outcome in Health: a transport error, 401/403 (auth) or any
// other answer (up: the helper itself answered, whatever its upstreams
// did).
type HelperTransport struct {
	Base   http.RoundTripper
	Host   string
	Health *HelperHealth
}

func (t *HelperTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if t.Health == nil || !strings.EqualFold(req.URL.Host, t.Host) {
		return resp, err
	}
	switch {
	case err != nil:
		if req.Context().Err() == context.Canceled {
			return resp, err
		}
		t.Health.Record(unnamed(err))
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		t.Health.Record(&HelperStatusError{Code: resp.StatusCode})
	default:
		t.Health.Record(nil)
	}
	return resp, err
}
