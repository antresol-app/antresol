package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

// apiExtras is a channels.Extras of n titles outside the library.
type apiExtras struct {
	ready bool
	n     int
}

func (x *apiExtras) Ready(context.Context) bool { return x.ready }

func (x *apiExtras) Candidates(_ context.Context, _ channels.Rules, limit int) ([]channels.ExtraTitle, error) {
	var out []channels.ExtraTitle
	for i := 1; i <= x.n && i <= limit; i++ {
		out = append(out, channels.ExtraTitle{Key: fmt.Sprintf("movie:%d", i), Kind: "movie", TMDBID: i, Title: fmt.Sprintf("Внешний %d", i),
			Year: 1999, Overview: "о", Poster: "/p" + itoa(int64(i)) + ".jpg", Backdrop: "/b.jpg", RuntimeMs: 95 * 60000})
	}
	return out, nil
}

func (x *apiExtras) Resolve(context.Context, int64, channels.ExtraTitle) (channels.Stream, error) {
	return channels.Stream{URL: "http://127.0.0.1:1/x"}, nil
}

func extrasAvailable(t *testing.T, e *libEnv, u *http.Cookie) bool {
	t.Helper()
	w := e.do("GET", "/api/channels/options", "", u)
	var o struct {
		ExtrasAvailable *bool `json:"extrasAvailable"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &o) != nil || o.ExtrasAvailable == nil {
		t.Fatalf("options %d %s", w.Code, w.Body)
	}
	return *o.ExtrasAvailable
}

func TestChannelOptionsExtrasAvailable(t *testing.T) {
	e, svc := newChannelsEnv(t)
	if extrasAvailable(t, e, e.admin) {
		t.Fatal("extras offered without an implementation (the core build)")
	}
	x := &apiExtras{}
	svc.SetExtras(x)
	if extrasAvailable(t, e, e.admin) {
		t.Fatal("extras offered while online is off")
	}
	x.ready = true
	if !extrasAvailable(t, e, e.admin) || extrasAvailable(t, e, e.kid("")) {
		t.Fatal("extras: admin/kid")
	}
}

func TestChannelProgrammesOutsideTheLibrary(t *testing.T) {
	e, svc := newChannelsEnv(t)
	for i := 0; i < 6; i++ {
		e.movie(fmt.Sprintf("Фильм %d", i), "12+", fmt.Sprint(i))
	}
	e.withDuration()
	svc.SetExtras(&apiExtras{ready: true, n: 10})
	w := e.do("POST", "/api/channels", `{"name":"Смесь","category":"Кино","rules":{"extras":true}}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	if c.Rules == nil || !c.Rules.Extras {
		t.Fatalf("rule not kept: %s", w.Body)
	}
	w = e.do("GET", "/api/channels/"+itoa(c.ID)+"/guide?hours=48", "", e.admin)
	var g struct{ Programs []programJSON }
	json.Unmarshal(w.Body.Bytes(), &g)
	var ext, lib int
	for _, p := range g.Programs {
		if p.External {
			ext++
			if p.ItemID >= 0 || p.Title == "" || p.Year != 1999 || p.Overview == "" || p.Kind != "movie" ||
				p.Backdrop != "/api/images/art/b.jpg" || len(p.Poster) < len("/api/images/art/p1.jpg") {
				t.Fatalf("external programme %+v", p)
			}
		} else {
			lib++
		}
	}
	if ext == 0 || ext > lib {
		t.Fatalf("%d external, %d library programmes", ext, lib)
	}
	// the guide grid and the IPTV guide take them too
	if w := e.do("GET", "/api/channels/epg?hours=24", "", e.admin); w.Code != 200 {
		t.Fatalf("epg %d", w.Code)
	}
}
