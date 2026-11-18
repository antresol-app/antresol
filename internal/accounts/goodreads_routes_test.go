package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGoodreads plays the Goodreads side of the routes.
type fakeGoodreads struct {
	st      *Store
	checks  map[int64]error // profile id → check result (absent: unknown)
	pulls   []int64
	csvs    []string
	linked  int64
	summary ShelfSummary
	forgot  bool
}

func (f *fakeGoodreads) Check(_ context.Context, id int64) error {
	err, ok := f.checks[id]
	if !ok {
		return ErrRemoteNotFound
	}
	return err
}

func (f *fakeGoodreads) Link(ctx context.Context, uid, id int64, name string) error {
	f.linked = id
	return f.st.SetNickname(ctx, uid, goodreadsProvider, name, id)
}

func (f *fakeGoodreads) Pull(_ context.Context, uid int64) error {
	f.pulls = append(f.pulls, uid)
	return nil
}

func (f *fakeGoodreads) Import(_ context.Context, _ int64, r io.Reader) (ShelfSummary, error) {
	b, _ := io.ReadAll(r)
	if !strings.HasPrefix(string(b), "Title,") {
		return ShelfSummary{}, ErrBadImport
	}
	f.csvs = append(f.csvs, string(b))
	f.summary = ShelfSummary{Total: 5, Read: 3, Reading: 1, ToRead: 1, InLibrary: 2}
	return f.summary, nil
}

func (f *fakeGoodreads) Forget(ctx context.Context, uid int64) error {
	f.forgot, f.summary = true, ShelfSummary{}
	return f.st.Unlink(ctx, uid, goodreadsProvider)
}

func (f *fakeGoodreads) Summary(context.Context, int64) (ShelfSummary, error) { return f.summary, nil }

func (f *fakeGoodreads) ParseProfile(s string) (int64, string, bool) {
	s = strings.TrimSpace(s)
	switch s {
	case "12345", "12345-tester":
		return 12345, "tester", true
	case "666":
		return 666, "hidden", true
	case "777":
		return 777, "down", true
	}
	return 0, "", false
}

func TestGoodreadsRoutes(t *testing.T) {
	e := newAPIEnv(t)
	gr := &fakeGoodreads{st: e.sy.Accounts, checks: map[int64]error{12345: nil, 777: errors.New("timeout")}}
	e.api.Goodreads = gr
	type state struct {
		Linked    bool    `json:"linked"`
		Nickname  string  `json:"nickname"`
		Status    string  `json:"status"`
		LastSync  *string `json:"lastSync"`
		Total     int     `json:"total"`
		Read      int     `json:"read"`
		Reading   int     `json:"reading"`
		ToRead    int     `json:"toRead"`
		InLibrary int     `json:"inLibrary"`
	}
	get := func(w *httptest.ResponseRecorder) *state {
		t.Helper()
		var m struct {
			Goodreads *state `json:"goodreads"`
		}
		must(t, json.Unmarshal(w.Body.Bytes(), &m))
		return m.Goodreads
	}

	if s := get(e.do("GET", "/api/accounts", "")); s == nil || s.Linked || s.Status != StatusOK || s.Total != 0 {
		t.Fatalf("unlinked: %+v", s)
	}
	for body, code := range map[string]int{`{"profile":"nonsense"}`: 400, `{"profile":" "}`: 400, `{}`: 400, `{"profile":"666"}`: 404, `{"profile":"777"}`: 502} {
		if w := e.do("PUT", "/api/accounts/goodreads", body); w.Code != code || !strings.Contains(w.Body.String(), `"error":"L:`) {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if s := get(e.do("GET", "/api/accounts", "")); s.Linked || len(gr.pulls) != 0 {
		t.Fatalf("a failed check linked or pulled: %+v %v", s, gr.pulls)
	}

	w := e.do("PUT", "/api/accounts/goodreads", `{"profile":"12345-tester"}`)
	if w.Code != 200 {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	if s := get(w); !s.Linked || s.Nickname != "tester" {
		t.Fatalf("link body: %+v", s)
	}
	if len(gr.pulls) != 1 || gr.pulls[0] != e.uid || gr.linked != 12345 {
		t.Fatalf("pulls: %v linked %d", gr.pulls, gr.linked)
	}

	// the export needs no link and is read as the text of the file
	if w := e.do("POST", "/api/accounts/goodreads/import", `{"csv":"nothing useful"}`); w.Code != 422 || !strings.Contains(w.Body.String(), `"error":"L:`) {
		t.Fatalf("not an export: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/accounts/goodreads/import", `not json`); w.Code != 400 {
		t.Fatalf("not json: %d", w.Code)
	}
	w = e.do("POST", "/api/accounts/goodreads/import", `{"csv":"Title,Author\nDune,Frank Herbert\n"}`)
	if w.Code != 200 || len(gr.csvs) != 1 || gr.csvs[0] != "Title,Author\nDune,Frank Herbert\n" {
		t.Fatalf("import: %d %s %v", w.Code, w.Body, gr.csvs)
	}
	if s := get(w); s.Total != 5 || s.Read != 3 || s.Reading != 1 || s.ToRead != 1 || s.InLibrary != 2 {
		t.Fatalf("import body: %+v", s)
	}
	// over the limit as text, and over three times it as a request
	for _, n := range []int{maxGoodreadsCSV + 1, 3 * maxGoodreadsCSV} {
		big := `{"csv":"` + strings.Repeat("x", n) + `"}`
		if w := e.do("POST", "/api/accounts/goodreads/import", big); w.Code != 413 {
			t.Fatalf("too big (%d): %d", n, w.Code)
		}
	}

	if w := e.do("DELETE", "/api/accounts/goodreads", ""); w.Code != 204 || !gr.forgot {
		t.Fatalf("unlink: %d", w.Code)
	}
	if s := get(e.do("GET", "/api/accounts", "")); s.Linked || s.Total != 0 {
		t.Fatalf("still linked: %+v", s)
	}

	e.viewer.IsKid = true
	for _, c := range [][3]string{{"PUT", "/api/accounts/goodreads", `{"profile":"12345"}`}, {"DELETE", "/api/accounts/goodreads", ""},
		{"POST", "/api/accounts/goodreads/import", `{"csv":"Title,"}`}} {
		if w := e.do(c[0], c[1], c[2]); w.Code != 403 {
			t.Fatalf("kid %s %s: %d", c[0], c[1], w.Code)
		}
	}
}

func TestGoodreadsAbsentWithoutRemote(t *testing.T) {
	e := newAPIEnv(t)
	if strings.Contains(e.do("GET", "/api/accounts", "").Body.String(), "goodreads") {
		t.Fatal("goodreads must be absent without a remote")
	}
	for _, c := range [][3]string{{"PUT", "/api/accounts/goodreads", `{"profile":"12345"}`}, {"DELETE", "/api/accounts/goodreads", ""},
		{"POST", "/api/accounts/goodreads/import", `{"csv":"Title,"}`}} {
		if w := e.do(c[0], c[1], c[2]); w.Code != 404 {
			t.Fatalf("%s %s: %d", c[0], c[1], w.Code)
		}
	}
}
