package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/tracking"
)

// TestFeed_OnlyKindsTheProfileMayOpen: the feed («Вышло новое»), the
// shelf and the calendar show a source kind only to a profile that may
// open it (sourceKindsFor: the library's, and the scan kinds of the addons
// the profile has access to). An episode whose only sources are of a kind
// the profile may not open is not news for it, and in its calendar it is
// not available yet.
func TestFeed_OnlyKindsTheProfileMayOpen(t *testing.T) {
	e := newTrackEnv(t)
	// "adm" is open to admins only and scans the kind gamma
	e.addons.installed["adm"] = scanAddon("adm", "admins", true, "gamma")
	adult := e.addUser(`{"name":"Гость"}`, e.admin)
	ctx := context.Background()
	now := time.Now()
	for _, uid := range []int64{1, adult.ID} {
		// an episode of gamma only, and one of alpha (adults) and gamma
		only := e.track(uid, "series", 80, "Только админам")
		both := e.track(uid, "series", 81, "Для всех")
		for _, x := range []struct {
			ti   tracking.Title
			kind string
		}{{only, "gamma"}, {both, "gamma"}, {both, "alpha"}} {
			if _, err := e.svc.Store.UpsertSources(ctx, x.ti.ID, x.kind, []tracking.Source{{Season: 1, Episode: 1, Ref: x.kind}}, now); err != nil {
				t.Fatal(err)
			}
		}
		e.refresh(only)
		e.refresh(both)
	}
	kinds := func(c *http.Cookie) map[string][]string {
		out := map[string][]string{}
		for _, it := range e.feed(c).Items {
			if it.Episode == 1 {
				out[it.Title.Name] = it.Kinds
			}
		}
		return out
	}
	if got := kinds(e.admin); !slices.Equal(got["Только админам"], []string{"gamma"}) || !slices.Equal(got["Для всех"], []string{"alpha", "gamma"}) {
		t.Fatalf("admin %v", got)
	}
	ac := session(e.do("POST", "/api/login", `{"userId":`+itoa(adult.ID)+`}`, nil))
	got := kinds(ac)
	if _, ok := got["Только админам"]; ok || !slices.Equal(got["Для всех"], []string{"alpha"}) {
		t.Fatalf("adult %v", got)
	}
	// the calendar: the gamma-only episode is listed, not available, no kinds
	today := time.Now().Format(time.DateOnly)
	w := e.do("GET", "/api/tracking/calendar?from="+today+"&to="+today, "", ac)
	var cal struct {
		Days []struct {
			Items []struct {
				Title struct {
					Name string `json:"name"`
				} `json:"title"`
				Episode   int        `json:"episode"`
				FirstSeen *time.Time `json:"firstSeen"`
				Kinds     []string   `json:"kinds"`
			} `json:"items"`
		} `json:"days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cal); err != nil || w.Code != 200 || len(cal.Days) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	seen := 0
	for _, it := range cal.Days[0].Items {
		if it.Episode != 1 {
			continue
		}
		switch it.Title.Name {
		case "Только админам":
			seen++
			if it.FirstSeen != nil || len(it.Kinds) != 0 {
				t.Fatalf("adult calendar, admins' kind: %+v", it)
			}
		case "Для всех":
			seen++
			if it.FirstSeen == nil || !slices.Equal(it.Kinds, []string{"alpha"}) {
				t.Fatalf("adult calendar: %+v", it)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("calendar %s", w.Body)
	}
}
