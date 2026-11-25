// Command fakeaddon is a test double for an addon process, built on
// addonkit. Its behaviour is chosen by FAKE_MODE: ok, never, crash, noise,
// flaky, orphan (flaky that leaves a grandchild behind), stubborn (ignores
// SIGTERM), slowready (healthy at once, ready after 1 s), hooks (answers
// POST /hooks/<name> from the script ANTRESOL_FAKE_HOOKS, misbehaviour
// included) and holder (a well-behaved file holder set up through the core
// API from ANTRESOL_FAKE_HOLDER, see holderScript; it also answers the
// proxy's GET /api/whoami and the admin-only GET /api/admin/ping) and
// player (serves GET /media/x from the file of its variable MEDIA, and registers it
// as an input of the profile USER on its first GET /input).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "grandchild" {
		time.Sleep(5 * time.Minute) // os.Args[2] marks it for pgrep
		return
	}
	env, err := addonkit.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake:", err)
		os.Exit(2)
	}
	mode := os.Getenv("FAKE_MODE")
	if mode == "orphan" {
		// same process group; the marker is the data dir
		gc := exec.Command(os.Args[0], "grandchild", env.DataDir)
		if err := gc.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "fake: grandchild:", err)
			os.Exit(2)
		}
		fmt.Println("fake: grandchild", gc.Process.Pid)
	}
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	if mode == "crash" {
		time.Sleep(100 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "fake: crashing")
		os.Exit(1)
	}
	if mode == "noise" {
		for i := 0; i < 600; i++ {
			fmt.Printf("line %d\n", i)
		}
	}
	ln, err := net.Listen("unix", env.Socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake: listen:", err)
		os.Exit(2)
	}
	mux := http.NewServeMux()
	var ready addonkit.Ready
	switch mode {
	case "never":
		mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	case "slowready":
		time.AfterFunc(time.Second, ready.Set)
		mux.HandleFunc("GET /health", ready.Health)
	case "holder":
		mux.HandleFunc("GET /health", ready.Health)
	default:
		// not JSON: ready at once
		mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	}
	mux.HandleFunc("GET /env", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(os.Environ())
	})
	rec := &recorder{calls: []hookCall{}}
	mux.HandleFunc("GET /calls", rec.serve)
	switch mode {
	case "hooks":
		serveHooks(mux, rec)
	case "holder":
		serveHolder(mux, rec, env, &ready)
	case "player":
		servePlayer(mux, rec, env)
	}
	go http.Serve(ln, mux)
	fmt.Println("fake: up")

	sig := make(chan os.Signal, 1)
	if mode != "stubborn" {
		signal.Notify(sig, syscall.SIGTERM)
	}
	var exit <-chan time.Time
	if mode == "flaky" || mode == "orphan" {
		exit = time.After(time.Second)
	}
	select {
	case <-sig:
		fmt.Println("fake: term")
		ln.Close()
		os.Exit(0)
	case <-exit:
		fmt.Println("fake: flaky exit")
		os.Exit(3)
	}
}

// hookCall is one recorded hook request (or, in player mode, one media read).
type hookCall struct {
	Name   string          `json:"name"`
	Header string          `json:"header"`           // X-Antresol-Hook
	Type   string          `json:"type"`             // Content-Type
	User   string          `json:"user"`             // X-Antresol-User
	Signed bool            `json:"signed,omitempty"` // addonkit.UserFrom took it (the server's signature, with the key in this process's environment)
	Input  string          `json:"input,omitempty"`  // X-Antresol-Input
	Body   json.RawMessage `json:"body"`
}

// recorder keeps every hook request, in order; GET /calls lists them.
type recorder struct {
	mu    sync.Mutex
	calls []hookCall
}

// wrap records each request to /hooks/{name}, then passes it to next with
// its body intact.
func (rc *recorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		if !json.Valid(b) {
			b, _ = json.Marshal(string(b)) // kept as a JSON string
		}
		_, signed := addonkit.UserFrom(r)
		rc.mu.Lock()
		rc.calls = append(rc.calls, hookCall{Name: r.PathValue("name"), Header: r.Header.Get("X-Antresol-Hook"),
			Type: r.Header.Get("Content-Type"), User: r.Header.Get(addonkit.UserHeader), Signed: signed, Body: b})
		rc.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (rc *recorder) serve(w http.ResponseWriter, r *http.Request) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	addonkit.WriteJSON(w, http.StatusOK, rc.calls)
}

// hookAnswer is one scripted answer of the hooks mode.
type hookAnswer struct {
	Status   int             `json:"status"`
	Body     json.RawMessage `json:"body"`
	Raw      string          `json:"raw"` // sent as is when set (not JSON)
	DelayMs  int             `json:"delayMs"`
	Location string          `json:"location"` // Location header (redirects)
}

// serveHooks answers hooks as scripted, however wrong: it tests how the
// core takes an addon's misbehaviour, so it does not use addonkit.HookMux.
func serveHooks(mux *http.ServeMux, rec *recorder) {
	script := map[string]hookAnswer{}
	if err := json.Unmarshal([]byte(os.Getenv("ANTRESOL_FAKE_HOOKS")), &script); err != nil {
		fmt.Fprintln(os.Stderr, "fake: hooks script:", err)
		os.Exit(2)
	}
	mux.Handle("POST /hooks/{name}", rec.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := script[r.PathValue("name")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if a.DelayMs > 0 {
			select {
			case <-time.After(time.Duration(a.DelayMs) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if a.Status == 0 {
			a.Status = http.StatusOK
		}
		if a.Location != "" {
			w.Header().Set("Location", a.Location)
		}
		if a.Raw != "" {
			w.WriteHeader(a.Status)
			io.WriteString(w, a.Raw)
			return
		}
		if len(a.Body) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(a.Status)
		w.Write(a.Body)
	})))
}

// holderScript is ANTRESOL_FAKE_HOLDER: what the holder sets up through
// the core API on start, in this order, before it says it is ready.
type holderScript struct {
	// TMDB: look this title up with ItemByTMDB (skipped when ID is 0).
	TMDB struct {
		Kind string `json:"kind"`
		ID   int64  `json:"id"`
	} `json:"tmdb"`
	// Links: hard links to make (the parent folders of To are created).
	Links []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"links"`
	// Import: Import these paths with ref and owner (skipped when empty).
	Import struct {
		Paths []string `json:"paths"`
		Ref   string   `json:"ref"`
		Owner int64    `json:"owner"`
	} `json:"import"`
	// Holds: PutHolds this set (always sent; null clears).
	Holds []addonkit.Hold `json:"holds"`
	// Follow: files.plan lets every touched key follow the move (else all
	// are stopped). Follow and Refuse can be changed later with POST /script.
	Follow bool `json:"follow"`
	// Refuse: hook name → refusal text (409).
	Refuse map[string]string `json:"refuse"`
}

// holderState is GET /state: what the setup got.
type holderState struct {
	Info      addonkit.Info         `json:"info"`
	Item      addonkit.Item         `json:"item"`
	ItemFound bool                  `json:"itemFound"`
	Import    addonkit.ImportResult `json:"import"`
	Errors    []string              `json:"errors"`
	Done      bool                  `json:"done"`
}

// keysBody is the {"keys": [...]} of a files.* hook.
type keysBody struct {
	Keys []string `json:"keys"`
}

func serveHolder(mux *http.ServeMux, rec *recorder, env addonkit.Env, ready *addonkit.Ready) {
	var script holderScript
	if err := json.Unmarshal([]byte(os.Getenv("ANTRESOL_FAKE_HOLDER")), &script); err != nil {
		fmt.Fprintln(os.Stderr, "fake: holder script:", err)
		os.Exit(2)
	}
	// Follow and Refuse change at run time: POST /script {"follow", "refuse"}
	var answerMu sync.Mutex
	answers := func() (bool, map[string]string) {
		answerMu.Lock()
		defer answerMu.Unlock()
		return script.Follow, script.Refuse
	}
	mux.HandleFunc("POST /script", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Follow bool              `json:"follow"`
			Refuse map[string]string `json:"refuse"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			addonkit.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		answerMu.Lock()
		script.Follow, script.Refuse = b.Follow, b.Refuse
		answerMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	// the proxy's /api/addons/<id>/x/…: who asks, and an admin-only page
	mux.HandleFunc("GET /api/whoami", func(w http.ResponseWriter, r *http.Request) {
		u, _ := addonkit.UserFrom(r)
		addonkit.WriteJSON(w, http.StatusOK, u)
	})
	mux.Handle("GET /api/admin/ping", addonkit.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addonkit.WriteJSON(w, http.StatusOK, map[string]bool{"pong": true})
	})))
	hooks := &addonkit.HookMux{}
	refusing := func(name string, fn addonkit.HookFunc) {
		hooks.Handle(name, func(ctx context.Context, u *addonkit.User, in json.RawMessage) (any, error) {
			if _, refuse := answers(); refuse != nil {
				if msg, ok := refuse[name]; ok {
					return nil, addonkit.Refuse(msg)
				}
			}
			return fn(ctx, u, in)
		})
	}
	refusing("files.plan", func(_ context.Context, _ *addonkit.User, in json.RawMessage) (any, error) {
		var b keysBody
		if err := json.Unmarshal(in, &b); err != nil {
			return nil, err
		}
		keys := append([]string{}, b.Keys...)
		if follow, _ := answers(); follow {
			return map[string][]string{"follow": keys, "stop": {}}, nil
		}
		return map[string][]string{"follow": {}, "stop": keys}, nil
	})
	for _, name := range []string{"files.pause", "files.resume", "files.stop", "files.detach", "files.attach"} {
		refusing(name, func(context.Context, *addonkit.User, json.RawMessage) (any, error) { return nil, nil })
	}
	mux.Handle("POST /hooks/{name}", rec.wrap(hooks))

	var mu sync.Mutex
	st := holderState{Errors: []string{}}
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		addonkit.WriteJSON(w, http.StatusOK, st)
	})
	fail := func(what string, err error) {
		fmt.Fprintf(os.Stderr, "fake: holder: %s: %v\n", what, err)
		mu.Lock()
		st.Errors = append(st.Errors, what+": "+err.Error())
		mu.Unlock()
	}
	core := addonkit.NewClient(env)
	go func() {
		ctx := context.Background()
		defer func() {
			mu.Lock()
			st.Done = true
			mu.Unlock()
			ready.Set() // also after a failure: the test reads /state
		}()
		info, err := core.Info(ctx)
		if err != nil {
			fail("info", err)
			return
		}
		mu.Lock()
		st.Info = info
		mu.Unlock()
		if script.TMDB.ID > 0 {
			it, ok, err := core.ItemByTMDB(ctx, script.TMDB.Kind, script.TMDB.ID)
			if err != nil {
				fail("tmdb", err)
				return
			}
			mu.Lock()
			st.Item, st.ItemFound = it, ok
			mu.Unlock()
		}
		for _, l := range script.Links {
			if err := os.MkdirAll(filepath.Dir(l.To), 0o755); err != nil {
				fail("link", err)
				return
			}
			if err := os.Link(l.From, l.To); err != nil && !os.IsExist(err) {
				fail("link", err)
				return
			}
		}
		if len(script.Import.Paths) > 0 {
			res, err := core.Import(ctx, script.Import.Paths, script.Import.Ref, script.Import.Owner)
			if err != nil {
				fail("import", err)
				return
			}
			mu.Lock()
			st.Import = res
			mu.Unlock()
		}
		if err := core.PutHolds(ctx, script.Holds); err != nil {
			fail("holds", err)
			return
		}
		fmt.Println("fake: holder ready")
	}()
}

// servePlayer is the player mode: GET /media/x serves the file of the addon's
// own variable MEDIA (ANTRESOL_<ID>_MEDIA; with Range), each read recorded
// at /calls; the first GET /input registers it as an input of the profile
// USER through the core API, retrying a 503 (the core
// may not yet see this process as running) every 200 ms for up to 10 s,
// and later calls answer the same id.
func servePlayer(mux *http.ServeMux, rec *recorder, env addonkit.Env) {
	media := env.Var("MEDIA")
	user, err := strconv.ParseInt(env.Var("USER"), 10, 64)
	if media == "" || err != nil {
		fmt.Fprintln(os.Stderr, "fake: player needs its MEDIA and USER variables")
		os.Exit(2)
	}
	mux.HandleFunc("GET /media/x", func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.calls = append(rec.calls, hookCall{Name: "media/x", User: r.Header.Get(addonkit.UserHeader),
			Input: r.Header.Get("X-Antresol-Input"), Body: json.RawMessage("null")})
		rec.mu.Unlock()
		http.ServeFile(w, r, media)
	})
	core := addonkit.NewClient(env)
	var mu sync.Mutex
	id := ""
	mux.HandleFunc("GET /input", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if id != "" {
			addonkit.WriteJSON(w, http.StatusOK, map[string]string{"id": id})
			return
		}
		fi, err := os.Stat(media)
		if err != nil {
			addonkit.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		req := addonkit.InputRequest{UserID: user, Path: "/media/x", Name: "Film/x.mp4", Size: fi.Size()}
		end := time.Now().Add(10 * time.Second)
		for {
			in, err := core.RegisterInput(r.Context(), req)
			if err == nil {
				id = in.ID
				addonkit.WriteJSON(w, http.StatusOK, map[string]string{"id": id})
				return
			}
			var ae *addonkit.APIError
			if !errors.As(err, &ae) || ae.Status != http.StatusServiceUnavailable || time.Now().After(end) {
				addonkit.WriteError(w, http.StatusBadGateway, err.Error())
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
}
