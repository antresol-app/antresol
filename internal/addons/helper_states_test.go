package addons

import (
	"context"
	"testing"
	"time"
)

// helpers.status: each addon's states of the helpers it declares; an
// address it was given by the admin is never passed on, unknown names,
// sources and problems are dropped or sorted as "other".
func TestHelperStates(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"helpers.status": 2 * time.Second}
	hookAddon(t, tg, "web", `{"helpers.status": {"body": {"helpers": [
		{"name": "svc", "source": "settings", "address": "http://u:pw@svc-own:1", "ok": false, "problem": "auth", "live": true},
		{"name": "nope", "source": "compose", "ok": true},
		{"name": "svc2", "source": "galaxy", "ok": true},
		{"name": "svc3", "source": "compose", "address": "http://svc3:80", "ok": false, "problem": "weird"}
	]}}}`, []string{"helpers.status"}, true)
	hookAddon(t, tg, "down", `{"helpers.status": {"body": {"helpers": [{"name": "svc", "source": "compose", "ok": true}]}}}`, []string{"helpers.status"}, false)
	decl := map[string]Helper{
		"web svc":  {Name: "svc", Image: "img:1", Port: 1, Health: "/"},
		"web svc2": {Name: "svc2", Image: "img:1", Port: 1, Health: "/"},
		"web svc3": {Name: "svc3", Image: "img:1", Port: 80, Health: "/"},
		"down svc": {Name: "svc", Image: "img:1", Port: 1, Health: "/"},
	}
	got := h.HelperStates(context.Background(), func(id, name string) (Helper, bool) {
		x, ok := decl[id+" "+name]
		return x, ok
	})
	if _, ok := got["down"]; ok {
		t.Fatalf("down addon answered: %+v", got)
	}
	web := got["web"]
	if len(web) != 2 {
		t.Fatalf("web: %+v", web)
	}
	if s := web[0]; s.Name != "svc" || s.Source != "settings" || s.Address != "" || s.OK || s.Problem != "auth" || !s.Live {
		t.Errorf("svc: %+v", s)
	}
	if s := web[1]; s.Name != "svc3" || s.Address != "http://svc3:80" || s.Problem != "other" {
		t.Errorf("svc3: %+v", s)
	}
}
