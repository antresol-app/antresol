package addonkit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHelperProblem(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{&net.DNSError{Err: "no such host", Name: "x"}, HelperProblemDNS},
		{&net.DNSError{Err: "i/o timeout", IsTimeout: true}, HelperProblemTimeout},
		{&HelperStatusError{Code: 502}, HelperProblemStatus},
		{&HelperStatusError{Code: 401}, HelperProblemAuth},
		{&HelperStatusError{Code: 403}, HelperProblemAuth},
		{context.DeadlineExceeded, HelperProblemTimeout},
		{errors.New("dial tcp: connection refused"), HelperProblemRefused},
		{errors.New("weird"), HelperProblemOther},
	}
	for _, c := range cases {
		if got := HelperProblem(c.err); got != c.want {
			t.Errorf("%v: got %q want %q", c.err, got, c.want)
		}
	}
}

func TestProbeAddress(t *testing.T) {
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()
	ctx := context.Background()
	if err := ProbeAddress(ctx, nil, srv.URL+"/", "/", false); err != nil {
		t.Fatal(err)
	}
	code = 404
	if err := ProbeAddress(ctx, nil, srv.URL, "/", false); HelperProblem(err) != HelperProblemStatus {
		t.Fatalf("404 strict: %v", err)
	}
	if err := ProbeAddress(ctx, nil, srv.URL, "/", true); err != nil {
		t.Fatalf("404 any answer: %v", err)
	}
	code = 401
	if err := ProbeAddress(ctx, nil, srv.URL, "/", true); HelperProblem(err) != HelperProblemAuth {
		t.Fatalf("401: %v", err)
	}
	// the error never names the address (it may carry a secret)
	err := ProbeAddress(ctx, nil, "http://user:s3cr3t@127.0.0.1:1", "/", false)
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "127.0.0.1:1/") {
		t.Fatalf("closed port: %v", err)
	}
	if HelperProblem(err) != HelperProblemRefused {
		t.Fatalf("closed port problem: %q (%v)", HelperProblem(err), err)
	}
}

func TestHelperHealthState(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	h := &HelperHealth{now: func() time.Time { return now }}
	probes := 0
	probe := func(context.Context) error { probes++; return &HelperStatusError{Code: 500} }
	ctx := context.Background()

	// nothing known: probed
	st := h.State(ctx, HelperState{Name: "svc", Source: HelperOverride}, probe)
	if probes != 1 || st.OK || st.Problem != HelperProblemStatus || st.Live || !st.CheckedAt.Equal(now) {
		t.Fatalf("first: %+v probes %d", st, probes)
	}
	// a real request since: its outcome, no probe
	now = now.Add(time.Minute)
	h.Record(nil)
	st = h.State(ctx, HelperState{Name: "svc", Source: HelperOverride}, probe)
	if probes != 1 || !st.OK || st.Problem != "" || !st.Live {
		t.Fatalf("live: %+v probes %d", st, probes)
	}
	// a cancelled request says nothing
	h.Record(context.Canceled)
	if ok, _, _, _, _ := h.Last(); !ok {
		t.Fatal("cancel recorded")
	}
	// stale: probed again
	now = now.Add(HelperFresh + time.Second)
	st = h.State(ctx, HelperState{Name: "svc", Source: HelperSettings}, probe)
	if probes != 2 || st.OK || st.Live {
		t.Fatalf("stale: %+v probes %d", st, probes)
	}
	// no address, no probe: unset
	var none HelperHealth
	if st := none.State(ctx, HelperState{Name: "svc", Source: HelperNone}, nil); st.Problem != HelperProblemUnset || st.OK {
		t.Fatalf("none: %+v", st)
	}
	// nil records nothing and does not panic
	var nh *HelperHealth
	nh.Record(nil)
	if st := nh.State(ctx, HelperState{Source: HelperCompose}, func(context.Context) error { return nil }); !st.OK {
		t.Fatalf("nil health with probe: %+v", st)
	}
}

func TestHelperTransport(t *testing.T) {
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer other.Close()
	h := &HelperHealth{}
	c := &http.Client{Transport: &HelperTransport{Host: strings.TrimPrefix(srv.URL, "http://"), Health: h}}
	get := func(u string) {
		resp, err := c.Get(u)
		if err == nil {
			resp.Body.Close()
		}
	}
	code = 502 // an upstream's failure behind the helper: the helper is up
	get(srv.URL + "/x")
	if ok, _, _, live, known := h.Last(); !ok || !live || !known {
		t.Fatal("502 should be up")
	}
	get(other.URL) // another host: not recorded
	if ok, _, _, _, _ := h.Last(); !ok {
		t.Fatal("other host recorded")
	}
	code = 401
	get(srv.URL)
	if ok, p, _, _, _ := h.Last(); ok || p != HelperProblemAuth {
		t.Fatalf("401: %v %q", ok, p)
	}
}

func TestHelperWatchRecordsHealth(t *testing.T) {
	h := &HelperHealth{}
	w := &HelperWatch{Helper: goodHelper(), Health: h, Probe: func(context.Context) error { return &HelperStatusError{Code: 503} }}
	w.Check(context.Background())
	if ok, p, _, live, known := h.Last(); ok || p != HelperProblemStatus || live || !known {
		t.Fatalf("got %v %q %v %v", ok, p, live, known)
	}
}
