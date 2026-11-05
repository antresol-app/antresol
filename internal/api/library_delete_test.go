package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/library"
)

func TestItemDeleteAPI(t *testing.T) {
	e := newFilesEnv(t)
	ctx := context.Background()
	mv, sr := filepath.Join(e.root, "media/movies"), filepath.Join(e.root, "media/series")
	e.svc.Items.Roots = []string{mv, sr}
	for p, n := range map[string]int{"Сёгун/Season 01/Сёгун.S01E01.mkv": 5, "Сёгун/Season 01/Сёгун.S01E02.mkv": 5, "Сёгун/Season 02/Сёгун.S02E01.mkv": 7} {
		full := filepath.Join(sr, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(strings.Repeat("x", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan := &library.Scanner{Repo: e.repo, Movies: mv, Series: sr}
	if _, err := scan.ScanVideo(ctx); err != nil {
		t.Fatal(err)
	}
	ep, _, err := e.repo.ItemByPath(ctx, filepath.Join(sr, "Сёгун/Season 01/Сёгун.S01E01.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	season, _ := e.repo.Item(ctx, ep.ParentID)
	series := season.ParentID
	path := "/api/items/" + strconv.FormatInt(series, 10) + "/delete"

	// admins only
	if w := e.do("GET", path, "", e.kid("")); w.Code != 403 {
		t.Fatalf("kid: %d", w.Code)
	}
	user := e.login(`{"name":"Мама"}`)
	if w := e.do("POST", path, "", user); w.Code != 403 {
		t.Fatalf("user: %d", w.Code)
	}

	w := e.do("GET", path, "", e.admin)
	var plan DeletePlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || w.Code != 200 {
		t.Fatalf("plan: %d %s", w.Code, w.Body)
	}
	if plan.Kind != "series" || plan.Size != 17 || plan.Files != 3 || len(plan.Seasons) != 2 ||
		plan.Seasons[0].Number != 1 || plan.Seasons[0].Episodes != 2 || plan.Seasons[0].Size != 10 || plan.Seasons[1].Episodes != 1 {
		t.Fatalf("plan %+v", plan)
	}

	// the media disk away: a plain refusal
	e.svc.Items.Offline = func() string { return "missing folder" }
	w = e.do("POST", "/api/items/"+strconv.FormatInt(season.ID, 10)+"/delete", "", e.admin)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Медиадиск недоступен") {
		t.Fatalf("offline: %d %s", w.Code, w.Body)
	}
	e.svc.Items.Offline = nil

	w = e.do("POST", "/api/items/"+strconv.FormatInt(season.ID, 10)+"/delete", "", e.admin)
	var res DeleteResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != 200 {
		t.Fatalf("delete season: %d %s", w.Code, w.Body)
	}
	if res.ParentID != series || !res.ParentShown || res.Size != 10 || res.Trashed != 1 || len(res.Items) != 3 {
		t.Fatalf("season result %+v", res)
	}
	w = e.do("POST", path, "", e.admin)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != 200 || res.ParentShown || res.Size != 7 {
		t.Fatalf("delete series: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", path, "", e.admin); w.Code != 404 {
		t.Fatalf("deleted series: %d", w.Code)
	}
	if w := e.do("GET", "/api/items/"+strconv.FormatInt(series, 10), "", e.admin); w.Code != 404 {
		t.Fatalf("deleted series detail: %d", w.Code)
	}
}
