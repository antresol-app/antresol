package addons

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/channels"
)

func TestChannelFetcher(t *testing.T) {
	tg := &chanTarget{ins: map[string]Installed{}, addons: map[string]*chanAddon{}, status: map[string]Status{}}
	x := &ChannelFetcher{H: &Hooks{T: tg}}
	ctx := context.Background()
	if x.Ready(ctx) {
		t.Fatal("ready with no addon")
	}
	var got addonkit.ChannelFetch
	mux := &addonkit.HookMux{}
	mux.Handle("channels.fetch", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		if err := json.Unmarshal(in, &got); err != nil {
			return nil, err
		}
		return addonkit.ChannelFetchAnswer{States: []addonkit.ChannelFetchState{
			{Key: "movie:5:0:0", State: "downloading", Size: 100, Done: 10, Rate: 7, Keep: true},
			{Key: "not:asked:0:0", State: "done"},
			{Key: "episode:9:1:2", State: "bogus"},
		}}, nil
	})
	ra := newRelayAddon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			mux.ServeHTTP(w, r)
		}
	}))
	tg.ins["fetcher"] = Installed{ID: "fetcher", Version: "1.0.0", Enabled: true,
		Manifest: Manifest{ID: "fetcher", Version: "1.0.0", Contract: ContractVersion, Access: "adults", Hooks: []string{"channels.fetch"}}}
	tg.addons["fetcher"] = &chanAddon{relayAddon: ra}
	tg.status["fetcher"] = StatusRunning
	if !x.Ready(ctx) {
		t.Fatal("not ready with a running addon")
	}
	states, err := x.Fetch(ctx, []channels.FetchWant{{Key: "movie:5:0:0", Kind: "movie", TMDBID: 5}, {Key: "episode:9:1:2", Kind: "episode", TMDBID: 9, Season: 1, Episode: 2}}, []string{"movie:7:0:0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Want) != 2 || got.Want[1].Episode != 2 || len(got.Drop) != 1 {
		t.Fatalf("the addon got %+v", got)
	}
	if len(states) != 1 || states[0].Key != "movie:5:0:0" || states[0].Rate != 7 || !states[0].Keep {
		t.Fatalf("states %+v (unasked keys and unknown states are left out)", states)
	}
	tg.status["fetcher"] = StatusStopped
	if _, err := x.Fetch(ctx, nil, nil); err == nil {
		t.Fatal("a stopped addon answered")
	}
}
