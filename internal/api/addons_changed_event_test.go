package api

import (
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/events"
)

// statusFeedAddons is fakeAddons with the Manager's status feed.
type statusFeedAddons struct {
	*fakeAddons
	feed []func(id string, st addons.Status)
}

func (s *statusFeedAddons) OnStatus(f func(id string, st addons.Status)) { s.feed = append(s.feed, f) }

// TestAddonStatusChangePublishesAddonsChanged: an open tab lists only the
// running addons' web parts, so each status change is told to it
// (addons.changed) and the tab loads its addons again: an addon still
// starting when the tab loaded joins its slots without a page reload.
func TestAddonStatusChangePublishesAddonsChanged(t *testing.T) {
	bus := &events.Bus{}
	rt := &statusFeedAddons{fakeAddons: &fakeAddons{installed: map[string]addons.Installed{}, status: map[string]addons.Status{}}}
	newAddonsAPI(Deps{Addons: rt, Events: bus})
	ch, cancel := bus.Subscribe()
	defer cancel()
	if len(rt.feed) == 0 {
		t.Fatal("the API does not follow the status feed")
	}
	for _, f := range rt.feed {
		f("fetcher", addons.StatusRunning)
	}
	select {
	case e := <-ch:
		if e.Type != events.AddonsChanged {
			t.Fatalf("event %q, want %q", e.Type, events.AddonsChanged)
		}
		d, _ := e.Data.(map[string]string)
		if d["id"] != "fetcher" || d["status"] != "running" {
			t.Errorf("data %v", e.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("no addons.changed event")
	}
}
