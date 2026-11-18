package addonkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func hookReq(name, body, user string) *http.Request {
	r := httptest.NewRequest("POST", "/hooks/"+name, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Antresol-Hook", name)
	if user != "" {
		r.Header.Set("X-Antresol-User", user)
	}
	return r
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("not JSON: %q", rec.Body)
	}
	return e.Error
}

func TestHookMux(t *testing.T) {
	m := &HookMux{}
	var gotUser *User
	var gotIn string
	m.Handle("files.plan", func(_ context.Context, u *User, in json.RawMessage) (any, error) {
		gotUser, gotIn = u, string(in)
		return map[string][]string{"follow": {"k1"}, "stop": {}}, nil
	})
	m.Handle("files.pause", func(context.Context, *User, json.RawMessage) (any, error) { return nil, nil })
	m.Handle("files.detach", func(context.Context, *User, json.RawMessage) (any, error) {
		return nil, &Refusal{Message: "Файл занят"}
	})
	m.Handle("files.stop", func(context.Context, *User, json.RawMessage) (any, error) {
		return nil, errors.New("disk on fire")
	})
	m.Handle("files.attach", func(context.Context, *User, json.RawMessage) (any, error) {
		return nil, Refuse("через функцию")
	})
	m.Handle("files.resume", func(context.Context, *User, json.RawMessage) (any, error) {
		return nil, Refuse("")
	})
	m.Handle("files.plan.empty", func(context.Context, *User, json.RawMessage) (any, error) {
		return nil, &Refusal{}
	})

	// 200 with the answer; the body and the user reach the handler
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.plan", `{"keys":["k1"]}`, `{"id":2,"name":"Мама","isAdmin":true,"lang":"ru"}`))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), `"follow":["k1"]`) {
		t.Fatalf("plan: %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if gotIn != `{"keys":["k1"]}` || gotUser == nil || gotUser.ID != 2 || gotUser.Name != "Мама" || !gotUser.IsAdmin {
		t.Fatalf("in %q user %+v", gotIn, gotUser)
	}
	// no user header: nil user
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.plan", `{}`, ""))
	if rec.Code != 200 || gotUser != nil {
		t.Fatalf("no user: %d %+v", rec.Code, gotUser)
	}
	// an empty body is {}
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.plan", ``, ""))
	if rec.Code != 200 || gotIn != `{}` {
		t.Fatalf("empty body: %d %q", rec.Code, gotIn)
	}
	// nil, nil: 204
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.pause", `{"keys":["k1"]}`, ""))
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("pause: %d %s", rec.Code, rec.Body)
	}
	// a refusal: 409 with its text
	for _, name := range []string{"files.detach", "files.attach"} {
		rec = httptest.NewRecorder()
		m.ServeHTTP(rec, hookReq(name, `{}`, ""))
		if rec.Code != 409 || errorOf(t, rec) == "" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec = httptest.NewRecorder(); true {
		m.ServeHTTP(rec, hookReq("files.detach", `{}`, ""))
		if errorOf(t, rec) != "Файл занят" {
			t.Fatalf("refusal text %s", rec.Body)
		}
	}
	// a refusal without a text: the default text (a 409 needs one)
	for _, name := range []string{"files.resume", "files.plan.empty"} {
		rec = httptest.NewRecorder()
		m.ServeHTTP(rec, hookReq(name, `{}`, ""))
		if rec.Code != 409 || errorOf(t, rec) != DefaultRefusal {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if Refuse("").Error() != DefaultRefusal {
		t.Fatalf("Refuse(\"\") text %q", Refuse("").Error())
	}
	// another error: a failure (500), never a refusal
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.stop", `{}`, ""))
	if rec.Code != 500 {
		t.Fatalf("error: %d", rec.Code)
	}
	// an unknown hook: 404
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("library.pending", `{}`, ""))
	if rec.Code != 404 {
		t.Fatalf("unknown: %d", rec.Code)
	}
	// not a hook path, not POST
	for _, r := range []*http.Request{httptest.NewRequest("POST", "/other", nil), httptest.NewRequest("GET", "/hooks/files.plan", nil)} {
		rec = httptest.NewRecorder()
		m.ServeHTTP(rec, r)
		if rec.Code == 200 || rec.Code == 204 {
			t.Fatalf("%s %s: %d", r.Method, r.URL.Path, rec.Code)
		}
	}
	// a broken user header: 400, the handler is not run
	gotIn = "untouched"
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.plan", `{"x":1}`, `{broken`))
	if rec.Code != 400 || gotIn != "untouched" {
		t.Fatalf("broken user: %d %q", rec.Code, gotIn)
	}
	// a body that is not JSON: 400
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("files.plan", `nope`, ""))
	if rec.Code != 400 {
		t.Fatalf("bad body: %d", rec.Code)
	}
}

func TestHookMux_InServeMux(t *testing.T) {
	m := &HookMux{}
	m.Handle("files.pause", func(context.Context, *User, json.RawMessage) (any, error) { return nil, nil })
	mux := http.NewServeMux()
	mux.Handle("/hooks/", m)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, hookReq("files.pause", `{}`, ""))
	if rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestUserFrom(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/x", nil)
	if u, ok := UserFrom(r); ok || u != nil {
		t.Fatal("no header")
	}
	r.Header.Set("X-Antresol-User", `{"id":1,"name":"A","isKid":true}`)
	if u, ok := UserFrom(r); !ok || u.ID != 1 || !u.IsKid {
		t.Fatalf("%+v %v", u, ok)
	}
	// as the core sends it: every non-ASCII character escaped
	r.Header.Set("X-Antresol-User", `{"id":2,"name":"\u041c\u0430\u043c\u0430","isAdmin":true,"lang":"ru"}`)
	if u, ok := UserFrom(r); !ok || u.Name != "Мама" || !u.IsAdmin {
		t.Fatalf("escaped: %+v %v", u, ok)
	}
	r.Header.Set("X-Antresol-User", `nope`)
	if _, ok := UserFrom(r); ok {
		t.Fatal("broken header")
	}
}

// The rating limit: read from the header, absent or null = none, and
// left out of the JSON when there is none (round trip).
// The remote quality cap: read from the header, absent = none, and left
// out of the JSON when there is none (round trip).
func TestUserFrom_MaxHeight(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/x", nil)
	for h, want := range map[string]int{
		`{"id":3,"name":"G","maxHeight":720}`:  720,
		`{"id":3,"name":"G","maxHeight":null}`: 0,
		`{"id":3,"name":"G"}`:                  0,
	} {
		r.Header.Set("X-Antresol-User", h)
		if u, ok := UserFrom(r); !ok || u.MaxHeight != want {
			t.Errorf("%s: %+v %v", h, u, ok)
		}
	}
	for _, c := range []struct {
		u    User
		want string
	}{{User{ID: 3, MaxHeight: 480}, `"maxHeight":480`}, {User{ID: 4}, ""}} {
		u, want := c.u, c.want
		b, err := json.Marshal(u)
		if err != nil {
			t.Fatal(err)
		}
		var back User
		if err := json.Unmarshal(b, &back); err != nil || back.MaxHeight != u.MaxHeight {
			t.Errorf("round trip %s: %+v %v", b, back, err)
		}
		if want == "" && strings.Contains(string(b), "maxHeight") || want != "" && !strings.Contains(string(b), want) {
			t.Errorf("json %s", b)
		}
	}
}

func TestUserFrom_MaxRating(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/x", nil)
	for h, want := range map[string]string{
		`{"id":3,"name":"G","maxRating":"12+"}`: "12+",
		`{"id":3,"name":"G","maxRating":null}`:  "",
		`{"id":3,"name":"G"}`:                   "",
	} {
		r.Header.Set("X-Antresol-User", h)
		if u, ok := UserFrom(r); !ok || u.MaxRating != want {
			t.Errorf("%s: %+v %v", h, u, ok)
		}
	}
	for _, c := range []struct {
		u    User
		want string
	}{{User{ID: 3, MaxRating: "16+"}, `"maxRating":"16+"`}, {User{ID: 4}, ""}} {
		u, want := c.u, c.want
		b, err := json.Marshal(u)
		if err != nil {
			t.Fatal(err)
		}
		var back User
		if err := json.Unmarshal(b, &back); err != nil || back.MaxRating != u.MaxRating {
			t.Errorf("round trip %s: %+v %v", b, back, err)
		}
		if want == "" && strings.Contains(string(b), "maxRating") || want != "" && !strings.Contains(string(b), want) {
			t.Errorf("json %s", b)
		}
	}
}

func TestRequireAdmin(t *testing.T) {
	h := RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r)
		w.Write([]byte(u.Name))
	}))
	for _, c := range []struct {
		user string
		code int
	}{
		{`{"id":1,"name":"admin","isAdmin":true}`, 200},
		{`{"id":2,"name":"adult"}`, 403},
		{`{"id":3,"name":"kid","isKid":true,"isAdmin":true}`, 403},
		{``, 403},
		{`broken`, 403},
	} {
		r := httptest.NewRequest("GET", "/api/admin/x", nil)
		if c.user != "" {
			r.Header.Set("X-Antresol-User", c.user)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != c.code {
			t.Fatalf("%q: %d", c.user, rec.Code)
		}
		if c.code == 403 && errorOf(t, rec) != "Недостаточно прав" {
			t.Fatalf("%q: %s", c.user, rec.Body)
		}
		if c.code == 200 && rec.Body.String() != "admin" {
			t.Fatalf("%q: %s", c.user, rec.Body)
		}
	}
}

func TestReady(t *testing.T) {
	var r Ready
	health := func() map[string]bool {
		rec := httptest.NewRecorder()
		r.Health(rec, httptest.NewRequest("GET", "/health", nil))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
		}
		var m map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := health(); !m["ok"] || m["ready"] {
		t.Fatalf("before Set: %v", m)
	}
	r.Set()
	if m := health(); !m["ok"] || !m["ready"] {
		t.Fatalf("after Set: %v", m)
	}
}

func TestHookMuxAnswer(t *testing.T) {
	m := &HookMux{}
	var next *Answer
	m.Handle("trailers.fetch", func(context.Context, *User, json.RawMessage) (any, error) { return next, nil })
	ask := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, hookReq("trailers.fetch", `{}`, ""))
		return rec
	}
	next = &Answer{Status: 202, Body: TrailerPending{RetryAfterMs: 500}}
	if rec := ask(); rec.Code != 202 || strings.TrimSpace(rec.Body.String()) != `{"retryAfterMs":500}` ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("202: %d %q", rec.Code, rec.Body)
	}
	next = &Answer{Status: 200, Body: TrailerFile{Path: "/media/k.mp4", Size: 1, ContentType: "video/mp4"}}
	if rec := ask(); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"path":"/media/k.mp4"`) {
		t.Fatalf("200: %d %q", rec.Code, rec.Body)
	}
	next = &Answer{Status: 404, Body: "ignored"}
	if rec := ask(); rec.Code != 404 || errorOf(t, rec) == "" {
		t.Fatalf("404: %d %q", rec.Code, rec.Body)
	}
	next = &Answer{Status: 204}
	if rec := ask(); rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("204: %d %q", rec.Code, rec.Body)
	}
	next = nil // a typed nil *Answer: nothing to add
	if rec := ask(); rec.Code != 204 {
		t.Fatalf("nil answer: %d", rec.Code)
	}
	next = &Answer{Status: 302}
	if rec := ask(); rec.Code != 500 {
		t.Fatalf("302: %d", rec.Code)
	}
	// 200 or 202 without a body: a programming error, never a 204
	for _, st := range []int{200, 202} {
		next = &Answer{Status: st}
		if rec := ask(); rec.Code != 500 || errorOf(t, rec) == "" {
			t.Fatalf("%d without a body: %d %q", st, rec.Code, rec.Body)
		}
	}
}

// library.afterScan: no profile, body {}, and nothing to answer (204).
func TestHookMux_AfterScan(t *testing.T) {
	m := &HookMux{}
	var calls int
	var gotUser *User
	var gotIn string
	m.Handle("library.afterScan", func(_ context.Context, u *User, in json.RawMessage) (any, error) {
		calls++
		gotUser, gotIn = u, string(in)
		return nil, nil
	})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("library.afterScan", `{}`, ""))
	if rec.Code != 204 || calls != 1 || gotUser != nil || gotIn != `{}` {
		t.Fatalf("%d, %d calls, user %v, body %q", rec.Code, calls, gotUser, gotIn)
	}
}

// home.continue: the profile in the header, body {}, the cards back as 200.
func TestHookMux_HomeContinue(t *testing.T) {
	m := &HookMux{}
	var gotUser *User
	var gotIn string
	m.Handle("home.continue", func(_ context.Context, u *User, in json.RawMessage) (any, error) {
		gotUser, gotIn = u, string(in)
		return ContinueAnswer{Cards: []ContinueCard{{Key: "k", Kind: "movie", TMDBID: 603, Title: "M"}}}, nil
	})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("home.continue", `{}`, `{"id":2,"uid":"u2","name":"N","isAdmin":false,"isKid":false,"lang":"ru","maxRating":"12+"}`))
	var a ContinueAnswer
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &a) != nil || len(a.Cards) != 1 || a.Cards[0].TMDBID != 603 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if gotUser == nil || gotUser.ID != 2 || gotUser.MaxRating != "12+" || gotIn != `{}` {
		t.Fatalf("user %+v, body %q", gotUser, gotIn)
	}
}

// channels.*: hooks run for no profile (nil user), the bodies as JSON.
func TestHookMux_Channels(t *testing.T) {
	m := &HookMux{}
	var gotUser *User = &User{}
	var got ChannelCandidates
	m.Handle("channels.candidates", func(_ context.Context, u *User, in json.RawMessage) (any, error) {
		gotUser = u
		if err := json.Unmarshal(in, &got); err != nil {
			return nil, err
		}
		return ChannelCandidatesAnswer{Titles: []ChannelTitle{{Key: "k", Kind: "movie", TMDBID: 603, Title: "M", RuntimeMs: 1}}}, nil
	})
	m.Handle("channels.release", func(context.Context, *User, json.RawMessage) (any, error) { return nil, nil })
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("channels.candidates", `{"rules":{"genres":["Драма"],"maxRating":"12+"},"limit":5}`, ""))
	var a ChannelCandidatesAnswer
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &a) != nil || len(a.Titles) != 1 || a.Titles[0].Key != "k" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if gotUser != nil || got.Limit != 5 || got.Rules.MaxRating != "12+" || len(got.Rules.Genres) != 1 {
		t.Fatalf("user %+v, body %+v", gotUser, got)
	}
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, hookReq("channels.release", `{"token":"t","path":"/x"}`, ""))
	if rec.Code != 204 {
		t.Fatalf("release %d", rec.Code)
	}
}
