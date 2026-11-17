package addons

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/trailers"
)

// TrailersHook is the hook an addon answers to play trailers
// (docs/addons/contract.md, «Trailers»).
const TrailersHook = "trailers.fetch"

const (
	// trailerPollMin and trailerPollMax bound an addon's retryAfterMs;
	// trailerPollDefault is taken when it gives none.
	trailerPollMin     = 250 * time.Millisecond
	trailerPollMax     = 2 * time.Second
	trailerPollDefault = time.Second
	// trailerPrefetchEvery: a key is prefetched at most this often.
	trailerPrefetchEvery = 24 * time.Hour
	// trailerPrefetchKeep bounds the prefetch memo (older ones go first).
	trailerPrefetchKeep = 4096
	// trailerFilesKeep bounds the files kept from 200 answers.
	trailerFilesKeep = 4096
	// trailerReadHeaders: how long a read waits for the addon's headers.
	trailerReadHeaders = 15 * time.Second
)

// trailerRelayHeaders are the addon's answer headers a read passes on
// (Content-Type is always video/mp4).
var trailerRelayHeaders = []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"}

// Trailers is a trailers.Provider over the addons that declare
// trailers.fetch: it asks them in id order, keeps where the last 200 of
// each key said the file is, and relays reads of it from the addon's
// socket. It keeps no trailer itself.
type Trailers struct {
	H *Hooks
	// Log (nil: slog.Default).
	Log *slog.Logger

	now    func() time.Time // test clock (nil: time.Now)
	sleep  func(ctx context.Context, d time.Duration) error
	client *http.Client
	once   sync.Once

	mu         sync.Mutex
	files      map[string]trailerAt // key → the last 200
	prefetched map[string]time.Time // key → its last prefetch
	bg         sync.WaitGroup
}

// trailerAt is where a 200 of trailers.fetch said a key's file is.
type trailerAt struct {
	addon, socket string
	file          addonkit.TrailerFile
}

var _ trailers.Provider = (*Trailers)(nil)

func (t *Trailers) log() *slog.Logger {
	if t.Log != nil {
		return t.Log
	}
	return slog.Default()
}

func (t *Trailers) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *Trailers) wait(ctx context.Context, d time.Duration) error {
	if t.sleep != nil {
		return t.sleep(ctx, d)
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-tm.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// http is the client reading the addons' sockets: the one in the
// request's context (trailerSockKey), a new connection every time, no
// gzip asked, no redirect followed.
func (t *Trailers) http() *http.Client {
	t.once.Do(func() {
		t.client = &http.Client{
			Transport: &http.Transport{
				DisableKeepAlives:     true,
				DisableCompression:    true,
				ResponseHeaderTimeout: trailerReadHeaders,
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					sock, _ := ctx.Value(trailerSockKey{}).(string)
					if sock == "" {
						return nil, errors.New("addons: trailer read without a socket")
					}
					return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
				},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	})
	return t.client
}

type trailerSockKey struct{}

// Available: an addon that declares trailers.fetch is on and running.
func (t *Trailers) Available(ctx context.Context) bool {
	ids, err := t.H.Answering(ctx, TrailersHook)
	return err == nil && len(ids) > 0
}

// errTrailerNotHere: the addon answered 404 or 204.
var errTrailerNotHere = errors.New("addons: trailer: not this addon")

// ask calls trailers.fetch of addon id once: the file of a valid 200 with
// the socket that answered it, the wait of a 202, errTrailerNotHere, a
// *trailers.Refusal or a failure.
func (t *Trailers) ask(ctx context.Context, id string, req addonkit.TrailerFetch) (trailerAt, time.Duration, error) {
	f, retry, sock, err := t.askOnce(ctx, id, req)
	return trailerAt{addon: id, socket: sock, file: f}, retry, err
}

func (t *Trailers) askOnce(ctx context.Context, id string, req addonkit.TrailerFetch) (addonkit.TrailerFile, time.Duration, string, error) {
	a, err := t.H.Ask(ctx, id, TrailersHook, nil, req)
	var ref *HookRefusal
	switch {
	case errors.As(err, &ref):
		return addonkit.TrailerFile{}, 0, "", &trailers.Refusal{Message: ref.Message}
	case err != nil:
		return addonkit.TrailerFile{}, 0, "", err
	}
	raw := a.Body
	switch a.Status {
	case http.StatusOK:
		var f addonkit.TrailerFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return f, 0, "", errTrailerAnswer(id, err.Error())
		}
		if err := checkTrailerFile(f); err != "" {
			return f, 0, "", errTrailerAnswer(id, err)
		}
		return f, 0, a.Socket, nil
	case http.StatusAccepted:
		var p addonkit.TrailerPending
		if err := json.Unmarshal(raw, &p); err != nil {
			return addonkit.TrailerFile{}, 0, "", errTrailerAnswer(id, err.Error())
		}
		return addonkit.TrailerFile{}, retryAfter(p.RetryAfterMs), "", nil
	}
	return addonkit.TrailerFile{}, 0, "", errTrailerNotHere
}

// retryAfter is a 202's wait: ms within [trailerPollMin, trailerPollMax]
// (clamped before it becomes a Duration, so a huge value cannot
// overflow), trailerPollDefault when absent or not positive.
func retryAfter(ms int64) time.Duration {
	if ms <= 0 {
		return trailerPollDefault
	}
	ms = min(ms, trailerPollMax.Milliseconds())
	return max(time.Duration(ms)*time.Millisecond, trailerPollMin)
}

func errTrailerAnswer(id, detail string) error {
	return errors.New("addon " + id + " hook " + TrailersHook + ": " + ErrHookFailed.Error() + ": " + detail)
}

// checkTrailerFile: the rules of a 200 (contract «Trailers»); "" is fine.
func checkTrailerFile(f addonkit.TrailerFile) string {
	switch {
	case !strings.HasPrefix(f.Path, "/media/") || !validInputPath(f.Path):
		return "path " + strconv.Quote(f.Path) + " is not a route under /media/"
	case f.Size <= 0:
		return "size is not positive"
	case f.DurationMs < 0:
		return "durationMs is negative"
	case f.ContentType != "video/mp4":
		return "contentType " + strconv.Quote(f.ContentType) + " is not video/mp4"
	}
	return ""
}

// Play asks the addons in id order until one has key ready (200), and
// while one is still fetching (202) asks it again until ctx ends.
func (t *Trailers) Play(ctx context.Context, key, url string) (int64, error) {
	f, err := t.fetch(ctx, key, url, true)
	if err != nil {
		return 0, err
	}
	return f.DurationMs, nil
}

// fetch walks the addons for key; poll: wait on a 202 (else it is the
// answer, ErrPending).
func (t *Trailers) fetch(ctx context.Context, key, url string, poll bool) (addonkit.TrailerFile, error) {
	ids, err := t.H.Answering(ctx, TrailersHook)
	if err != nil {
		t.log().Warn("addon trailers: list", "err", err)
		return addonkit.TrailerFile{}, trailers.ErrFetch
	}
	req := addonkit.TrailerFetch{Key: key, URL: url}
	failed := false
	for _, id := range ids {
		for {
			at, retry, err := t.ask(ctx, id, req)
			f := at.file
			var ref *trailers.Refusal
			switch {
			case err == nil && retry == 0:
				t.keep(key, at)
				return f, nil
			case err == nil: // 202
				if !poll {
					return f, trailers.ErrPending
				}
				if werr := t.wait(ctx, retry); werr != nil {
					return f, pending(ctx, werr)
				}
				continue
			case errors.As(err, &ref):
				return f, err
			case errors.Is(err, errTrailerNotHere):
			case ctx.Err() != nil:
				return f, pending(ctx, ctx.Err())
			default:
				failed = true
				if !errors.Is(err, errCooling) {
					t.log().Warn("addon trailers: left out", "addon", id, "err", err)
				}
			}
			break
		}
	}
	if failed {
		return addonkit.TrailerFile{}, trailers.ErrFetch
	}
	return addonkit.TrailerFile{}, trailers.ErrNotFound
}

// pending: the wait ran out (ErrPending), or the caller went away.
func pending(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return trailers.ErrPending
	}
	return err
}

// keep records where key's file is: the socket of the process that
// answered (a restart forgets it).
func (t *Trailers) keep(key string, at trailerAt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.files == nil {
		t.files = map[string]trailerAt{}
	}
	if _, ok := t.files[key]; !ok && len(t.files) >= trailerFilesKeep {
		for k := range t.files { // any one: a later read asks again
			delete(t.files, k)
			break
		}
	}
	t.files[key] = at
}

// forget drops key's file when it is still at (a newer 200 stays).
func (t *Trailers) forget(key string, at trailerAt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cur, ok := t.files[key]; ok && cur.addon == at.addon && cur.socket == at.socket && cur.file.Path == at.file.Path {
		delete(t.files, key)
	}
}

// kept is key's file, while the process that named it still runs.
func (t *Trailers) kept(key string) (trailerAt, bool) {
	t.mu.Lock()
	at, ok := t.files[key]
	t.mu.Unlock()
	if !ok {
		return at, false
	}
	if st, sock := t.H.T.Serving(at.addon); st != StatusRunning || sock == "" || sock != at.socket {
		t.forget(key, at)
		return at, false
	}
	return at, true
}

// Prefetch sends one trailers.fetch for key in the background (no wait
// on a 202), at most once per key per day.
func (t *Trailers) Prefetch(key, url string) {
	now := t.clock()
	t.mu.Lock()
	if t.prefetched == nil {
		t.prefetched = map[string]time.Time{}
	}
	if at, ok := t.prefetched[key]; ok && now.Sub(at) < trailerPrefetchEvery {
		t.mu.Unlock()
		return
	}
	if len(t.prefetched) >= trailerPrefetchKeep {
		for k, at := range t.prefetched {
			if now.Sub(at) >= trailerPrefetchEvery {
				delete(t.prefetched, k)
			}
		}
		if len(t.prefetched) >= trailerPrefetchKeep {
			t.mu.Unlock()
			return
		}
	}
	t.prefetched[key] = now
	t.mu.Unlock()
	t.bg.Add(1)
	go func() {
		defer t.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), HookTimeout(TrailersHook))
		defer cancel()
		if _, err := t.fetch(ctx, key, url, false); errors.Is(err, trailers.ErrFetch) {
			// an addon was down or failing: a later list may try again
			t.mu.Lock()
			if t.prefetched[key].Equal(now) {
				delete(t.prefetched, key)
			}
			t.mu.Unlock()
		}
	}()
}

// Wait waits for the prefetches started so far (tests).
func (t *Trailers) Wait() { t.bg.Wait() }

// Serve reads key's file from the addon that named it: GET or HEAD with
// Range and If-Range only. With no file kept (or its process gone) it
// asks trailers.fetch once first.
func (t *Trailers) Serve(w http.ResponseWriter, r *http.Request, key, url string) error {
	at, ok := t.kept(key)
	if !ok {
		ctx, cancel := context.WithTimeout(r.Context(), HookTimeout(TrailersHook))
		_, err := t.fetch(ctx, key, url, false)
		cancel()
		if err != nil {
			return trailers.ErrNotFound
		}
		if at, ok = t.kept(key); !ok {
			return trailers.ErrNotFound
		}
	}
	rng, ifRange := r.Header.Get("Range"), r.Header.Get("If-Range")
	if strings.Contains(rng, ",") {
		if ifRange != "" {
			// the range was only wanted if unchanged: the whole file will do
			rng, ifRange = "", ""
		} else {
			// a multipart answer would pass as video/mp4
			h := w.Header()
			h.Set("Cache-Control", "no-store")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Content-Range", "bytes */"+strconv.FormatInt(at.file.Size, 10))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return nil
		}
	}
	method := http.MethodGet
	if r.Method == http.MethodHead {
		method = http.MethodHead
	}
	out, err := http.NewRequestWithContext(context.WithValue(r.Context(), trailerSockKey{}, at.socket), method, "http://addon"+at.file.Path, nil)
	if err != nil {
		return err
	}
	if rng != "" {
		out.Header.Set("Range", rng)
	}
	if ifRange != "" {
		out.Header.Set("If-Range", ifRange)
	}
	resp, err := t.http().Do(out)
	if err != nil {
		if r.Context().Err() != nil {
			return nil // the player went away
		}
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
	case http.StatusNotFound:
		t.forget(key, at)
		return trailers.ErrNotFound
	default:
		return errors.New("addon " + at.addon + ": trailer read: status " + strconv.Itoa(resp.StatusCode))
	}
	h := w.Header()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// no body follows: only the size of the file, never a length
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		if v := resp.Header.Get("Content-Range"); v != "" {
			h.Set("Content-Range", v)
		}
		w.WriteHeader(resp.StatusCode)
		return nil
	}
	trailers.FileHeaders(h)
	for _, k := range trailerRelayHeaders {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	// a HEAD keeps Content-Length: it is the length a GET would send
	w.WriteHeader(resp.StatusCode)
	if method == http.MethodHead {
		return nil
	}
	io.Copy(w, resp.Body) // ends when the player goes or the addon's socket does
	return nil
}
