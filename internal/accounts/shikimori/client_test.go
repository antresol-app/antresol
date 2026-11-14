package shikimori

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock drives a limiter without real sleeping.
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }
func (f *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	f.now = f.now.Add(d)
	return nil
}

func ip(i int) *int { return &i }

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL, srv.Client())
	c.limiter = newLimiter(func() time.Time { return time.Now() }, func(context.Context, time.Duration) error { return nil })
	c.Backoff = time.Millisecond
	c.ClientID, c.ClientSecret, c.RedirectURI = "cid", "secret", "https://example.test/cb"
	return c, srv
}

func TestUserByNick(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/tester" || r.URL.Query().Get("is_nickname") != "1" {
			t.Errorf("url = %s", r.URL)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("reads must be anonymous")
		}
		io.WriteString(w, `{"id":42,"nickname":"tester","extra":1}`)
	})
	u, err := c.UserByNick(context.Background(), "tester")
	if err != nil || u.ID != 42 || u.Nickname != "tester" {
		t.Fatalf("u=%+v err=%v", u, err)
	}
}

func TestAnimeRatesParsesEmbeddedAnime(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/42/anime_rates" || r.URL.Query().Get("limit") != "5000" {
			t.Errorf("url = %s", r.URL)
		}
		if h := r.Header.Get("Authorization"); h != "" {
			t.Errorf("anonymous read sent %q", h)
		}
		io.WriteString(w, `[{"id":7,"score":8,"status":"watching","episodes":3,"rewatches":1,
		"updated_at":"2024-05-06T10:11:12.000+03:00",
		"anime":{"id":100,"name":"Sample","russian":"Пример","kind":"tv","status":"ongoing","episodes":12,"episodes_aired":5,"aired_on":"2024-04-01"}}]`)
	})
	rs, err := c.AnimeRates(context.Background(), 42, "")
	if err != nil || len(rs) != 1 {
		t.Fatalf("rs=%v err=%v", rs, err)
	}
	r := rs[0]
	want := time.Date(2024, 5, 6, 7, 11, 12, 0, time.UTC)
	if r.ID != 7 || r.Status != "watching" || r.Score != 8 || r.Episodes != 3 || r.Rewatches != 1 || !r.UpdatedAt.Equal(want) {
		t.Fatalf("rate=%+v", r)
	}
	a := r.Anime
	if a.ID != 100 || a.Russian != "Пример" || a.Kind != "tv" || a.Status != "ongoing" || a.Episodes != 12 || a.EpisodesAired != 5 || a.AiredOn != "2024-04-01" {
		t.Fatalf("anime=%+v", a)
	}
}

func TestAnimeByID(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/animes/100" {
			t.Errorf("path=%s", r.URL.Path)
		}
		io.WriteString(w, `{"id":100,"name":"Sample","kind":"movie","episodes":1}`)
	})
	a, err := c.Anime(context.Background(), 100)
	if err != nil || a.ID != 100 || a.Kind != "movie" {
		t.Fatalf("a=%+v err=%v", a, err)
	}
}

func TestSendsUserAgent(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "Antresol" {
			t.Errorf("UA = %q", ua)
		}
		io.WriteString(w, `{"id":1,"nickname":"x"}`)
	})
	if _, err := c.UserByNick(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
}

func TestLimiterSpacesRequests(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	l := newLimiter(clk.Now, clk.Sleep)
	start := clk.now
	for i := 0; i < 6; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := clk.now.Sub(start); d < time.Second {
		t.Fatalf("6 calls took %v, want >= 1s", d)
	}
	// 90/min: the 91st call within a minute must wait for the window.
	clk2 := &fakeClock{now: time.Unix(5000, 0)}
	l2 := newLimiter(clk2.Now, clk2.Sleep)
	s2 := clk2.now
	for i := 0; i < 91; i++ {
		if err := l2.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := clk2.now.Sub(s2); d < time.Minute {
		t.Fatalf("91 calls took %v, want >= 1m", d)
	}
}

func TestSharedLimiterAcrossClients(t *testing.T) {
	if New("http://a", nil).limiter != New("http://b", nil).limiter {
		t.Fatal("clients must share one limiter")
	}
}

func TestRetryAfterOn429(t *testing.T) {
	var n atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		io.WriteString(w, `{"id":1,"nickname":"x"}`)
	})
	if _, err := c.UserByNick(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Fatalf("calls=%d", n.Load())
	}
}

func TestRateLimitedAfterRetries(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
	})
	_, err := c.UserByNick(context.Background(), "x")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err=%v", err)
	}
}

func TestExchangeAndRefreshRotate(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/oauth/token" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "c0de" || r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "secret" || r.Form.Get("redirect_uri") != "https://example.test/cb" {
				t.Errorf("form=%v", r.Form)
			}
			io.WriteString(w, `{"access_token":"a1","refresh_token":"r1","expires_in":86400,"created_at":1700000000,"token_type":"Bearer","scope":"user_rates"}`)
		case "refresh_token":
			if r.Form.Get("refresh_token") != "r1" {
				t.Errorf("form=%v", r.Form)
			}
			io.WriteString(w, `{"access_token":"a2","refresh_token":"r2","expires_in":86400,"created_at":1700090000,"token_type":"Bearer","scope":"user_rates"}`)
		default:
			t.Errorf("grant=%q", r.Form.Get("grant_type"))
		}
	})
	t1, err := c.Exchange(context.Background(), "c0de")
	if err != nil || t1.Access != "a1" || t1.Refresh != "r1" || !t1.Expires.Equal(time.Unix(1700086400, 0)) {
		t.Fatalf("t1=%+v err=%v", t1, err)
	}
	t2, err := c.Refresh(context.Background(), t1.Refresh)
	if err != nil || t2.Access != "a2" || t2.Refresh != "r2" || t2.Refresh == t1.Refresh {
		t.Fatalf("t2=%+v err=%v", t2, err)
	}
}

func TestAuthorizeURL(t *testing.T) {
	c := New("https://shiki.test/", nil)
	c.ClientID, c.RedirectURI = "cid", "https://example.test/cb"
	u := c.AuthorizeURL("st8")
	for _, want := range []string{"https://shiki.test/oauth/authorize?", "client_id=cid", "response_type=code", "scope=user_rates", "state=st8", "redirect_uri=https%3A%2F%2Fexample.test%2Fcb"} {
		if !strings.Contains(u, want) {
			t.Errorf("%q missing %q", u, want)
		}
	}
}

func TestWhoamiBearer(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/whoami" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		io.WriteString(w, `{"id":42,"nickname":"tester"}`)
	})
	u, err := c.Whoami(context.Background(), "tok")
	if err != nil || u.ID != 42 {
		t.Fatalf("u=%+v err=%v", u, err)
	}
}

func TestWriteRateCalls(t *testing.T) {
	var got []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		switch r.Method {
		case http.MethodPost:
			var m map[string]map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			ur := m["user_rate"]
			if ur["user_id"] != float64(42) || ur["target_id"] != float64(100) || ur["target_type"] != "Anime" ||
				ur["status"] != "watching" || ur["score"] != float64(7) || ur["episodes"] != float64(3) {
				t.Errorf("post body=%s", b)
			}
			io.WriteString(w, `{"id":555,"updated_at":"2026-11-14T10:00:00.123+03:00"}`)
		case http.MethodPatch:
			var m map[string]map[string]any
			json.Unmarshal(b, &m)
			ur := m["user_rate"]
			if ur["status"] != "completed" || ur["score"] != float64(9) || ur["episodes"] != float64(12) {
				t.Errorf("patch body=%s", b)
			}
			if _, has := ur["user_id"]; has {
				t.Errorf("patch must not carry user_id: %s", b)
			}
			io.WriteString(w, `{"id":555,"updated_at":"2026-11-14T11:00:00+03:00"}`)
		case http.MethodGet:
			io.WriteString(w, `{"id":555,"updated_at":"2026-11-14T12:00:00+03:00"}`)
		case http.MethodDelete:
			w.WriteHeader(204)
		}
	})
	ctx := context.Background()
	cr, err := c.CreateRate(ctx, "tok", 42, 100, RateInput{Status: "watching", Score: ip(7), Episodes: ip(3)})
	if err != nil || cr.ID != 555 || cr.UpdatedAt.Unix() != time.Date(2026, 11, 14, 7, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("created=%+v err=%v", cr, err)
	}
	up, err := c.UpdateRate(ctx, "tok", 555, RateInput{Status: "completed", Score: ip(9), Episodes: ip(12)})
	if err != nil || up.ID != 555 || !up.UpdatedAt.Equal(time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("updated=%+v err=%v", up, err)
	}
	rd, err := c.UserRate(ctx, "tok", 555)
	if err != nil || !rd.UpdatedAt.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("read=%+v err=%v", rd, err)
	}
	if err := c.DeleteRate(ctx, "tok", 555); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /api/v2/user_rates", "PATCH /api/v2/user_rates/555", "GET /api/v2/user_rates/555", "DELETE /api/v2/user_rates/555"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("calls=%v", got)
	}
}

func TestErrorsMapped(t *testing.T) {
	for code, want := range map[int]error{403: ErrForbidden, 404: ErrNotFound, 401: ErrUnauthorized} {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
		if _, err := c.UserByNick(context.Background(), "x"); !errors.Is(err, want) {
			t.Errorf("read %d: err=%v want %v", code, err, want)
		}
		if err := c.DeleteRate(context.Background(), "tok", 1); !errors.Is(err, want) {
			t.Errorf("write %d: err=%v want %v", code, err, want)
		}
	}
}

func TestErrorBodyTruncatedNoSecrets(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, strings.Repeat("x", 5000))
	})
	_, err := c.Refresh(context.Background(), "SECRET-REFRESH")
	if err == nil || len(err.Error()) > 400 || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err=%v", err)
	}
}

func TestPartialUpdateOmitsUnsetFields(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"user_rate":{"status":"dropped"}}` {
			t.Errorf("body=%s", b)
		}
	})
	if _, err := c.UpdateRate(context.Background(), "tok", 1, RateInput{Status: "dropped"}); err != nil {
		t.Fatal(err)
	}
	// An explicit zero score is still sent.
	c2, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"user_rate":{"score":0}}` {
			t.Errorf("body=%s", b)
		}
	})
	if _, err := c2.UpdateRate(context.Background(), "tok", 1, RateInput{Score: ip(0)}); err != nil {
		t.Fatal(err)
	}
}

func TestWritesRefuseRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	})
	ctx := context.Background()
	check := func(name string, err error) {
		if err == nil || !strings.Contains(err.Error(), "VAULT_SHIKIMORI_URL") {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	_, err := c.CreateRate(ctx, "tok", 1, 2, RateInput{Status: "planned"})
	check("create", err)
	_, err = c.UpdateRate(ctx, "tok", 1, RateInput{Status: "planned"})
	check("update", err)
	check("delete", c.DeleteRate(ctx, "tok", 1))
	if hits.Load() != 0 {
		t.Fatalf("redirect target was hit %d times", hits.Load())
	}
}

func TestGetRedirectFollowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":1,"nickname":"x"}`)
	}))
	defer target.Close()
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusMovedPermanently)
	})
	if _, err := c.UserByNick(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
}

func TestLimiterWaiterHonoursOwnContext(t *testing.T) {
	now := time.Unix(1000, 0)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	l := newLimiter(func() time.Time { return now }, func(ctx context.Context, d time.Duration) error {
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	for i := 0; i < perSecond; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	go l.wait(context.Background()) // A sleeps
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.wait(ctx) }() // B queues behind A
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter blocked behind a sleeping one")
	}
	close(release)
}

func TestLimiterConcurrentNeverExceedsFivePerSecond(t *testing.T) {
	var cm sync.Mutex
	now := time.Unix(1000, 0)
	l := newLimiter(func() time.Time { cm.Lock(); defer cm.Unlock(); return now },
		func(_ context.Context, d time.Duration) error { cm.Lock(); now = now.Add(d); cm.Unlock(); return nil })
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.wait(context.Background()) }()
	}
	wg.Wait()
	st := l.stamp
	if len(st) != 30 {
		t.Fatalf("stamps=%d", len(st))
	}
	for i := 0; i+perSecond < len(st); i++ {
		if st[i+perSecond].Sub(st[i]) < time.Second {
			t.Fatalf("stamps %d..%d within %v", i, i+perSecond, st[i+perSecond].Sub(st[i]))
		}
	}
}

func TestTokenEdgeCases(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"","refresh_token":"r","expires_in":1}`)
	})
	if _, err := c.Refresh(context.Background(), "r"); err == nil {
		t.Error("empty access_token accepted")
	}
	c2, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid_grant"}`)
	})
	if _, err := c2.Refresh(context.Background(), "r"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("invalid_grant err=%v", err)
	}
}

// A private list reads with its owner's bearer token.
func TestAnimeRatesWithToken(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		io.WriteString(w, `[{"id":7,"status":"planned","anime":{"id":100}}]`)
	})
	if _, err := c.AnimeRates(context.Background(), 42, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous err = %v", err)
	}
	rs, err := c.AnimeRates(context.Background(), 42, "tok")
	if err != nil || len(rs) != 1 || rs[0].Anime.ID != 100 {
		t.Fatalf("rs=%+v err=%v", rs, err)
	}
}
