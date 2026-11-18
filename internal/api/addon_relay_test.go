package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/channels"
)

// relayTarget is one running addon answering channels.* on a unix
// socket of this process (the HookTarget of the hooks and the relay).
type relayTarget struct {
	in   addons.Installed
	sock string
}

func (r *relayTarget) Get(_ context.Context, id string) (addons.Installed, error) {
	if id != r.in.ID {
		return addons.Installed{}, addons.ErrNotFound
	}
	return r.in, nil
}

func (r *relayTarget) List(context.Context) ([]addons.Installed, error) {
	return []addons.Installed{r.in}, nil
}

func (r *relayTarget) Serving(id string) (addons.Status, string) {
	if id != r.in.ID {
		return addons.StatusStopped, ""
	}
	return addons.StatusRunning, r.sock
}

func newRelayTarget(t *testing.T) *relayTarget {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	rt := &relayTarget{sock: filepath.Join(dir, "a.sock"), in: addons.Installed{ID: "tv", Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: "tv", Version: "1.0.0", Contract: addons.ContractVersion, Access: "adults",
			Hooks: []string{"channels.ready", "channels.candidates", "channels.resolve", "channels.release"}}}}
	mux := &addonkit.HookMux{}
	mux.Handle("channels.ready", func(context.Context, *addonkit.User, json.RawMessage) (any, error) {
		return addonkit.ChannelReady{Ready: true}, nil
	})
	mux.Handle("channels.candidates", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		var q addonkit.ChannelCandidates
		json.Unmarshal(in, &q)
		var out addonkit.ChannelCandidatesAnswer
		for i := 1; i <= 10 && i <= q.Limit; i++ {
			out.Titles = append(out.Titles, addonkit.ChannelTitle{Key: fmt.Sprintf("m%d", i), Kind: "movie", TMDBID: int64(500 + i),
				Title: fmt.Sprintf("Внешний %d", i), Year: 1999, Rating: "12+", Poster: "/p.jpg", Backdrop: "/b.jpg", RuntimeMs: 95 * 60000})
		}
		return out, nil
	})
	mux.Handle("channels.resolve", func(context.Context, *addonkit.User, json.RawMessage) (any, error) {
		return addonkit.ChannelStream{Path: "/ch/t/index.m3u8"}, nil
	})
	mux.Handle("channels.release", func(context.Context, *addonkit.User, json.RawMessage) (any, error) { return nil, nil })
	ln, err := net.Listen("unix", rt.sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			mux.ServeHTTP(w, r)
			return
		}
		io.WriteString(w, "#EXTM3U\n")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return rt
}

// A relay URL (its key and tokens) is the server's own ffmpeg's: no
// client-visible answer of the channels or Home ever holds one, while a
// channel plays a title from an addon.
func TestRelay_URLNeverInClientAnswers(t *testing.T) {
	rt := newRelayTarget(t)
	relay := addons.NewRelay(rt.Serving, func() int { return 8420 })
	hooks := &addons.Hooks{T: rt, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	x := addons.NewChannelExtras(hooks, &addons.Supervisor{}, relay)
	var svc *channels.Service
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = channels.New(db, nil)
		d.Channels, d.DB = svc, db
		d.AddonRelay = relay
		d.AddonHooks = hooks
	})
	svc.SetExtras(x)
	for i := 0; i < 6; i++ {
		e.movie(fmt.Sprintf("Фильм %d", i), "12+", fmt.Sprint(i))
	}
	e.withDuration()
	if !extrasAvailable(t, e, e.admin) {
		t.Fatal("extras not available with the addon")
	}
	w := e.do("POST", "/api/channels", `{"name":"Смесь","category":"Кино","rules":{"extras":true}}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	w = e.do("GET", "/api/channels/"+itoa(c.ID)+"/guide?hours=48", "", e.admin)
	var g struct{ Programs []programJSON }
	json.Unmarshal(w.Body.Bytes(), &g)
	var urls []string
	for _, p := range g.Programs {
		if !p.External {
			continue
		}
		pr, err := svc.Program(context.Background(), c.ID, p.Start.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		urls = append(urls, pr.URL)
		if pr.Release != nil {
			defer pr.Release()
		}
		break
	}
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "http://127.0.0.1:8420/api/addon-relay/") {
		t.Fatalf("no relay stream: %v (guide %d programmes)", urls, len(g.Programs))
	}
	parts := strings.Split(strings.TrimPrefix(urls[0], "http://127.0.0.1:8420/api/addon-relay/"), "/")
	key, tok := parts[0], parts[1]
	// the relay is mounted, for loopback alone
	if w := e.do("GET", "/api/addon-relay/"+key+"/"+tok+"/index.m3u8", "", nil); w.Code != 403 {
		t.Fatalf("relay from a non-loopback client: %d", w.Code)
	}
	if !mediaRoute("/api/addon-relay/" + key + "/" + tok + "/index.m3u8") {
		t.Fatal("the relay is not a media route")
	}
	for _, u := range []*http.Cookie{e.admin, e.login(`{"name":"Петя"}`)} {
		for _, path := range []string{"/api/channels", "/api/channels/options", "/api/channels/" + itoa(c.ID) + "/guide?hours=48",
			"/api/channels/epg?hours=24", "/api/home", "/api/home?addons=1"} {
			w := e.do("GET", path, "", u)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			b := w.Body.String()
			if strings.Contains(b, "/api/addon-relay/") || strings.Contains(b, key) || strings.Contains(b, tok) {
				t.Fatalf("%s holds the relay URL: %s", path, b)
			}
		}
	}
}

// D9: a kid never gets a title an addon gave, on a shared channel either:
// the list, the guide and the EPG leave it out, and the live stream is
// refused while one is on air; an adult on the same channel still gets it.
func TestChannelExtras_KidNeverGetsAddonProgrammes(t *testing.T) {
	rt := newRelayTarget(t)
	relay := addons.NewRelay(rt.Serving, func() int { return 8420 })
	hooks := &addons.Hooks{T: rt, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	x := addons.NewChannelExtras(hooks, &addons.Supervisor{}, relay)
	var svc *channels.Service
	var sdb *sql.DB
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		svc = channels.New(db, nil)
		d.Channels, d.DB, d.IPTV = svc, db, true
		d.AddonRelay, d.AddonHooks = relay, hooks
		sdb = db
	})
	svc.SetExtras(x)
	for i := 0; i < 6; i++ {
		e.movie(fmt.Sprintf("Фильм %d", i), "12+", fmt.Sprint(i))
	}
	e.withDuration()
	w := e.do("POST", "/api/channels", `{"name":"Смесь","category":"Кино","rules":{"extras":true,"maxRating":"12+"}}`, e.admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var c channelJSON
	json.Unmarshal(w.Body.Bytes(), &c)
	kid, adult := e.kid("12+"), e.login(`{"name":"Петя"}`)
	type prog struct {
		External bool      `json:"external"`
		Start    time.Time `json:"start"`
		End      time.Time `json:"end"`
	}
	var firstEnd time.Time
	externals := func(u *http.Cookie) (n int, first time.Time) {
		w := e.do("GET", "/api/channels/"+itoa(c.ID)+"/guide?hours=48", "", u)
		var g struct{ Programs []prog }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &g) != nil || len(g.Programs) == 0 {
			t.Fatalf("guide %d %s", w.Code, w.Body)
		}
		for _, p := range g.Programs {
			if p.External {
				if n == 0 {
					first, firstEnd = p.Start, p.End
				}
				n++
			}
		}
		return n, first
	}
	n, first := externals(adult)
	if n == 0 {
		t.Fatal("the adult sees no addon programme")
	}
	if n, _ := externals(kid); n != 0 {
		t.Fatalf("the kid sees %d addon programmes in the guide", n)
	}
	for _, path := range []string{"/api/channels", "/api/channels/epg?hours=48"} {
		w := e.do("GET", path, "", kid)
		if w.Code != 200 || strings.Contains(w.Body.String(), `"external":true`) {
			t.Fatalf("%s for the kid: %d %s", path, w.Code, w.Body)
		}
		if w := e.do("GET", path, "", adult); !strings.Contains(w.Body.String(), `"external":true`) && path != "/api/channels" {
			t.Fatalf("%s for the adult has no addon programme: %s", path, w.Body)
		}
	}
	// the IPTV guides too (the kid's link, if a kid gets one)
	for _, u := range []*http.Cookie{kid, adult} {
		w := e.do("GET", "/api/channels/iptv", "", u)
		if w.Code != 200 {
			continue
		}
		var l iptvLinks
		json.Unmarshal(w.Body.Bytes(), &l)
		g := e.do("GET", strings.TrimPrefix(l.Guide, "http://example.com"), "", nil)
		if has := strings.Contains(g.Body.String(), "Внешний"); g.Code != 200 || has != (u == adult) {
			t.Fatalf("XMLTV (kid %v): %d %s", u == kid, g.Code, g.Body)
		}
	}
	// the kid's stream is refused while it may hold the addon's programme:
	// on air, starting within the encoder's lead (it runs ahead of the
	// clock), and just over (its segments are still kept)
	svc.Live = &channels.Live{} // never reached for the kid
	start, end := first, firstEnd
	shift := func(newStart time.Time) {
		d := newStart.Sub(start).Milliseconds()
		if _, err := sdb.Exec(`UPDATE channel_playout SET start_ms=start_ms+?, end_ms=end_ms+? WHERE channel_id=?`, d, d, c.ID); err != nil {
			t.Fatal(err)
		}
		start, end = start.Add(time.Duration(d)*time.Millisecond), end.Add(time.Duration(d)*time.Millisecond)
	}
	for _, at := range []struct {
		name  string
		start func() time.Time
	}{
		{"starts within the lead", func() time.Time { return time.Now().Add(10 * time.Second) }},
		{"ended 5 s ago", func() time.Time { return time.Now().Add(-5 * time.Second).Add(-end.Sub(start)) }},
		{"on air", func() time.Time { return time.Now().Add(-time.Minute) }},
	} {
		shift(at.start())
		for _, name := range []string{"index.m3u8", "seg1.ts"} {
			w := e.do("GET", "/api/channels/live/"+itoa(c.ID)+"/"+name, "", kid)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "Недоступно в детском профиле") {
				t.Fatalf("%s: the kid's %s: %d %s", at.name, name, w.Code, w.Body)
			}
		}
	}
	behind, ahead, _ := svc.Live.Span()
	if ahead < 20*time.Second || behind < 30*time.Second {
		t.Fatalf("span behind %v ahead %v", behind, ahead)
	}
	// the list gives the kid no programme on air, the adult the addon's
	var l struct {
		Channels []struct {
			Now *prog `json:"now"`
		}
	}
	json.Unmarshal(e.do("GET", "/api/channels", "", kid).Body.Bytes(), &l)
	if len(l.Channels) != 1 || l.Channels[0].Now != nil {
		t.Fatalf("the kid's list: %+v", l)
	}
	json.Unmarshal(e.do("GET", "/api/channels", "", adult).Body.Bytes(), &l)
	if len(l.Channels) != 1 || l.Channels[0].Now == nil || !l.Channels[0].Now.External {
		t.Fatalf("the adult's list: %+v", l)
	}
}
