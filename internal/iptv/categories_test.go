package iptv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

// fakeModel answers the categories question: news for a name with
// «Новости», sport for «Спорт», an unknown code dropped; it counts calls.
func fakeModel(t *testing.T, calls *atomic.Int32) *channels.LLM {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct{ Messages []struct{ Content string } }
		_ = json.NewDecoder(r.Body).Decode(&body)
		ans := map[string]any{}
		for _, line := range strings.Split(body.Messages[len(body.Messages)-1].Content, "\n") {
			num, rest, ok := strings.Cut(line, ". ")
			if !ok {
				continue
			}
			switch {
			case strings.Contains(rest, "Новости"):
				ans[num] = map[string]any{"c": []string{"news", "bogus"}, "l": "ru"}
			case strings.Contains(rest, "Sport"):
				ans[num] = map[string]any{"c": []string{"sport"}, "l": "en"}
			default:
				ans[num] = map[string]any{"c": []string{}, "l": ""}
			}
		}
		b, _ := json.Marshal(ans)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(b)}}}})
	}))
	t.Cleanup(srv.Close)
	return &channels.LLM{URL: srv.URL + "/v1", Model: "light"}
}

func TestCategorizeCachesByBaseName(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	var calls atomic.Int32
	l := fakeModel(t, &calls)
	var tiers []channels.Tier
	e.svc.Model = func(_ context.Context, tier channels.Tier) *channels.LLM { tiers = append(tiers, tier); return l }
	e.p.put("/a.m3u", "#EXTM3U\n#EXTINF:-1 group-title=\"RU | Инфо\",Новости 24 HD\nhttp://a.example.test/1.m3u8\n"+
		"#EXTINF:-1 group-title=\"Sport\",Sport One\nhttp://a.example.test/2.m3u8\n#EXTINF:-1,Другое\nhttp://a.example.test/3.m3u8\n")
	src := e.source("A", "/a.m3u", nil)
	e.refresh(src.ID)
	e.svc.Categorize(ctx)
	if calls.Load() != 1 || !slices.Equal(tiers, []channels.Tier{channels.TierLight}) {
		t.Fatalf("calls %d tiers %v", calls.Load(), tiers)
	}
	got := map[string]ExtChannel{}
	for _, x := range e.visible(channels.Viewer{ID: e.user("v")}, 0) {
		got[x.Channel.Name] = x
	}
	if x := got["Новости 24 HD"]; !slices.Equal(x.AI.Cats, []string{"news"}) || x.AI.Lang != "ru" || !slices.Equal(x.Groups, []string{"RU | Инфо"}) {
		t.Errorf("news: %+v", x)
	}
	if x := got["Sport One"]; !slices.Equal(x.AI.Cats, []string{"sport"}) || x.AI.Lang != "en" {
		t.Errorf("sport: %+v", x)
	}
	// known names are not asked again, the cache survives a restart
	e.svc.Categorize(ctx)
	svc2 := &Service{Store: e.svc.Store, Seal: e.svc.Seal, Fetch: e.svc.Fetch, Now: e.svc.Now, Model: e.svc.Model}
	svc2.Categorize(ctx)
	if calls.Load() != 1 {
		t.Errorf("asked again: %d", calls.Load())
	}
	vis, _ := svc2.Visible(ctx, channels.Viewer{ID: e.user("w")}, 0)
	for _, x := range vis {
		if x.Channel.Name == "Новости 24 HD" && !slices.Equal(x.AI.Cats, []string{"news"}) {
			t.Errorf("after restart: %+v", x.AI)
		}
	}
}
