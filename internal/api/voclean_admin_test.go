package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/antresol-app/antresol/internal/voclean"
)

type fakeVoclean struct {
	st       voclean.Status
	kicks    int
	rechecks int
}

func (f *fakeVoclean) Status(context.Context) (voclean.Status, error) { return f.st, nil }
func (f *fakeVoclean) Recheck(context.Context) (int, error) {
	f.rechecks++
	f.st.Queued += f.st.NotEligible
	f.st.NotEligible = 0
	return 0, nil
}
func (f *fakeVoclean) Kick() { f.kicks++ }

func TestVocleanAdmin(t *testing.T) {
	f := &fakeVoclean{st: voclean.Status{Done: 7, Queued: 3, Failed: 1, NotEligible: 2}}
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db; d.Voclean = f })
	ctx := context.Background()
	read := func(body string) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	w := e.do("GET", "/api/admin/voclean", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := w.Body.String(); got != `{"done":7,"queued":3,"failed":1,"notEligible":2,"backfill":false}`+"\n" {
		t.Fatalf("%q", got)
	}
	if LoadVocleanBackfill(ctx, e.db) {
		t.Fatal("on before anything was saved")
	}
	w = e.do("POST", "/api/admin/voclean/backfill", `{"on":true,"recheck":true}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	out := read(w.Body.String())
	if out["backfill"] != true || out["queued"] != float64(5) || out["notEligible"] != float64(0) || f.rechecks != 1 || f.kicks != 1 {
		t.Fatalf("%v rechecks %d kicks %d", out, f.rechecks, f.kicks)
	}
	if !LoadVocleanBackfill(ctx, e.db) {
		t.Fatal("not stored")
	}
	w = e.do("POST", "/api/admin/voclean/backfill", `{"on":false}`, e.admin)
	if w.Code != 200 || read(w.Body.String())["backfill"] != false || f.rechecks != 1 || LoadVocleanBackfill(ctx, e.db) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// admins only
	kid := e.kid("")
	if w := e.do("GET", "/api/admin/voclean", "", kid); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
	if w := e.do("POST", "/api/admin/voclean/backfill", `{"on":true}`, kid); w.Code != 403 {
		t.Fatalf("kid post: %d", w.Code)
	}
	if LoadVocleanBackfill(ctx, e.db) {
		t.Fatal("a kid turned it on")
	}
}
