package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
)

// HookTarget is what the hook caller needs of the addon manager
// (*Manager implements it). Serving gives the socket only while the addon
// is running: a starting addon's hooks count as down.
type HookTarget interface {
	Get(ctx context.Context, id string) (Installed, error)
	List(ctx context.Context) ([]Installed, error)
	Serving(id string) (Status, string)
}

// Hooks calls the hooks addons declare: POST /hooks/<name> on the addon's
// socket, JSON both ways. A caller batches its work into one body per
// addon (files.* take {keys: [...]}), so one operation makes one call per
// hook per addon.
//
// A Hooks must not be copied after first use (it holds a sync.Map).
type Hooks struct {
	T   HookTarget
	Log *slog.Logger

	timeouts map[string]time.Duration // test override of HookTimeout
	once404  sync.Map                 // "<id> <name>" → logged

	downMu  sync.Mutex
	down    map[string]time.Time // downKey → end of its down period
	now     func() time.Time     // test clock (nil: time.Now)
	downFor time.Duration        // test override of hookDownFor

	ownersMu sync.Mutex
	lastSvcs map[string][]string // addon id → the services its last import.services answer listed

	leftMu sync.Mutex
	left   map[string]leftoversEntry // language → the last full migrate.leftovers answer
}

// hookDownFor is how long a hook of an addon counts as down after a call
// of it ran out of the hook's own deadline. A process that accepts on its
// socket but never answers would otherwise cost every caller in a serial
// loop (a tracking round, the letters after a scan) the full deadline per
// call. While down, a call of that hook fails at once with ErrHookDown;
// files.* hooks are still tried (a file operation is refused on any
// failure, and an attach after a move must be attempted).
//
// The down period is the hook's alone (downKey): a slow disk.usage under
// load must not silence the addon's tracking.scan or the letters, nor a
// slow import service list its search. Any status change of the addon
// (a restart, a crash, a stop or start) ends all its down periods at once
// (StatusChanged).
const hookDownFor = 60 * time.Second

// downKey is what a timeout of hook name of addon id puts in a down
// period: that hook of that addon alone. Every key of addon id starts with
// id + " " (StatusChanged).
func downKey(id, name string) string {
	return id + " " + name
}

func (h *Hooks) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// isDown reports whether key (downKey's addon and hook) is in a down
// period.
func (h *Hooks) isDown(key string) bool {
	h.downMu.Lock()
	defer h.downMu.Unlock()
	until, ok := h.down[key]
	if !ok {
		return false
	}
	if h.clock().Before(until) {
		return true
	}
	delete(h.down, key)
	return false
}

// markDown starts the down period of hook name of addon id after its
// timeout (downKey; one Warn per period per hook).
func (h *Hooks) markDown(id, name string) {
	key := downKey(id, name)
	d := h.downFor
	if d == 0 {
		d = hookDownFor
	}
	now := h.clock()
	h.downMu.Lock()
	if h.down == nil {
		h.down = map[string]time.Time{}
	}
	until, was := h.down[key]
	was = was && now.Before(until)
	h.down[key] = now.Add(d)
	h.downMu.Unlock()
	if !was {
		h.log().Warn("addon hook: timed out, the hook counts as down", "addon", id, "hook", name, "for", d)
	}
}

// StatusChanged ends every down period of addon id: a restarted (or
// stopped, crashed, started) process is not the one that ran out of time.
// It is a Supervisor.OnStatus listener: it only takes the down lock.
func (h *Hooks) StatusChanged(id string, _ Status) {
	prefix := id + " "
	h.downMu.Lock()
	defer h.downMu.Unlock()
	for k := range h.down {
		if strings.HasPrefix(k, prefix) {
			delete(h.down, k)
		}
	}
}

// errCooling marks a call refused because its hook is in a down period:
// Each leaves it out without a log line (markDown logged the period).
var errCooling = errors.New("in a down period after a timeout")

var (
	// ErrHookDown: the addon does not declare the hook, is not running or
	// could not be reached.
	ErrHookDown = errors.New("addon hook: down")
	// ErrHookTimeout: the hook did not answer within its deadline.
	ErrHookTimeout = errors.New("addon hook: timeout")
	// ErrHookFailed: the addon answered with an error status (redirects
	// are not followed), a 409 without a text, or a body that is not what
	// the hook expects.
	ErrHookFailed = errors.New("addon hook: failed")
)

// HookRefusal is a 409 {"error"} answer: the addon refuses, and Message
// is what the user is shown.
type HookRefusal struct{ Addon, Message string }

func (r *HookRefusal) Error() string { return r.Message }

// maxHookBody caps a hook's answer.
const maxHookBody = 32 << 20

// fileOpDeadline is the deadline of files.* hooks when the operation's
// context carries none.
const fileOpDeadline = 30 * time.Second

var hookTimeouts = map[string]time.Duration{
	"files.plan":          10 * time.Second,
	"play.progress":       5 * time.Second,
	"library.pending":     2 * time.Second,
	"disk.usage":          2 * time.Second,
	"requests.open":       2 * time.Second,
	"requests.title":      10 * time.Second,
	"requests.search":     10 * time.Second,
	"tracking.scan":       120 * time.Second,
	"tracking.titles":     10 * time.Second,
	"tracking.watched":    3 * time.Second,
	"notify.interest":     5 * time.Second,
	"voiceover.origLang":  10 * time.Second,
	"trailers.fetch":      2 * time.Second,
	"import.services":     2 * time.Second,
	"import.stop":         5 * time.Second,
	"import.resolve":      30 * time.Second,
	"import.search":       30 * time.Second,
	"migrate.leftovers":   10 * time.Second,
	"library.afterScan":   60 * time.Second,
	"home.continue":       500 * time.Millisecond,
	"channels.ready":      2 * time.Second,
	"channels.candidates": 30 * time.Second,
	"channels.resolve":    60 * time.Second,
	"channels.release":    5 * time.Second,
	"channels.fetch":      15 * time.Second,
	"helpers.status":      12 * time.Second,
}

// HookTimeout is the hook's own timeout. files.* other than files.plan
// run under the file operation's deadline; 30 s is theirs when the
// operation has none.
func HookTimeout(name string) time.Duration {
	base, _, _ := strings.Cut(name, ":")
	if d, ok := hookTimeouts[base]; ok {
		return d
	}
	if strings.HasPrefix(base, "files.") {
		return fileOpDeadline
	}
	return 10 * time.Second
}

// hookDeadline: how long a call of name may take under ctx.
func hookDeadline(ctx context.Context, name string, over map[string]time.Duration) time.Duration {
	d, ok := over[name]
	if !ok {
		d = HookTimeout(name)
		if dl, has := ctx.Deadline(); has && strings.HasPrefix(name, "files.") && name != "files.plan" {
			return time.Until(dl)
		}
	}
	if dl, has := ctx.Deadline(); has {
		d = min(d, time.Until(dl))
	}
	return d
}

func (h *Hooks) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// Call runs hook name of addon id with body in (nil: {}) and decodes a 200
// answer into out (nil: discarded). 204 and 404 (not implemented, logged
// once) leave out untouched and return nil.
func (h *Hooks) Call(ctx context.Context, id, name string, user *addonkit.User, in, out any) error {
	body, err := hookBody(in)
	if err != nil {
		return fmt.Errorf("addon %s hook %s: %w: %v", id, name, ErrHookFailed, err)
	}
	cctx, cancel := context.WithTimeout(ctx, hookDeadline(ctx, name, h.timeouts))
	defer cancel()
	raw, err := h.call(ctx, cctx, id, name, user, body)
	if err != nil || raw == nil || out == nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("addon %s hook %s: %w: %v", id, name, ErrHookFailed, err)
	}
	return nil
}

// Each runs hook name on every enabled, running addon that declares it,
// in parallel under one deadline, and hands each 200 answer to each in
// addon-id order. Addons that are not running or whose hook is in a down
// period are left out quietly; those that are slow, fail, refuse or have
// nothing to add (204/404) are left out (logged).
func (h *Hooks) Each(ctx context.Context, name string, user *addonkit.User, in any, each func(id string, raw json.RawMessage)) {
	h.EachFor(ctx, name, user, nil, in, each)
}

// EachFor is Each over the addons admit lets through (nil: all), as for
// a profile (Manifest.OpenTo); the others are never asked.
func (h *Hooks) EachFor(ctx context.Context, name string, user *addonkit.User, admit func(Installed) bool, in any, each func(id string, raw json.RawMessage)) {
	res, _, _ := h.fanout(ctx, name, user, admit, in)
	for _, r := range res {
		switch {
		case errors.Is(r.err, errCooling):
		case r.err != nil:
			h.log().Warn("addon hook: left out", "addon", r.id, "hook", name, "err", r.err)
		case r.raw != nil:
			each(r.id, r.raw)
		}
	}
}

// hookResult is one addon's answer to a fanned-out hook: its 200 body (nil
// for 204 and 404) or the error.
type hookResult struct {
	id  string
	raw json.RawMessage
	err error
}

// fanout runs hook name on every enabled, running addon that declares it
// (and that admit lets through, when it is not nil), in parallel under one
// deadline, and gives their results in addon-id
// order; declared is how many enabled addons declare the hook, running or
// not. A list or body failure is logged and gives nothing, with ok false
// (nobody was asked, which is not an answer).
func (h *Hooks) fanout(ctx context.Context, name string, user *addonkit.User, admit func(Installed) bool, in any) (res []hookResult, declared int, ok bool) {
	list, err := h.T.List(ctx)
	if err != nil {
		h.log().Warn("addon hook: list", "hook", name, "err", err)
		return nil, 0, false
	}
	var ids []string
	for _, a := range list {
		if !a.Enabled || !a.Manifest.Answers(name) || (admit != nil && !admit(a)) {
			continue
		}
		declared++
		if st, _ := h.T.Serving(a.ID); st != StatusRunning {
			continue
		}
		ids = append(ids, a.ID)
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return nil, declared, true
	}
	body, err := hookBody(in)
	if err != nil {
		h.log().Warn("addon hook: body", "hook", name, "err", err)
		return nil, declared, false
	}
	cctx, cancel := context.WithTimeout(ctx, hookDeadline(ctx, name, h.timeouts))
	defer cancel()
	res = make([]hookResult, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			raw, err := h.call(ctx, cctx, id, name, user, body)
			res[i] = hookResult{id: id, raw: raw, err: err}
		})
	}
	wg.Wait()
	return res, declared, true
}

func hookBody(in any) ([]byte, error) {
	if in == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(in)
}

// HookAnswer is an answer of Ask: the status (200 or 202 with Body, 204
// or 404 without) and the socket of the process that gave it.
type HookAnswer struct {
	Status int
	Body   json.RawMessage
	Socket string
}

// Ask runs hook name of addon id with body in (nil: {}) for the hooks
// whose contract has a "still working" answer (trailers.fetch): 200 or 202
// with a JSON body, or 204 or 404 (not this addon). A refusal is a
// *HookRefusal; down, timed out and failed calls (a 200 or 202 whose body
// is not JSON, an empty one included) are errors as for Call.
func (h *Hooks) Ask(ctx context.Context, id, name string, user *addonkit.User, in any) (HookAnswer, error) {
	body, err := hookBody(in)
	if err != nil {
		return HookAnswer{}, fmt.Errorf("addon %s hook %s: %w: %v", id, name, ErrHookFailed, err)
	}
	cctx, cancel := context.WithTimeout(ctx, hookDeadline(ctx, name, h.timeouts))
	defer cancel()
	a, err := h.do(ctx, cctx, id, name, user, body)
	if err != nil {
		return HookAnswer{}, err
	}
	switch a.Status {
	case http.StatusOK, http.StatusAccepted:
		if !json.Valid(a.Body) {
			return HookAnswer{}, fmt.Errorf("addon %s hook %s: %w: answer is not JSON", id, name, ErrHookFailed)
		}
		return a, nil
	case http.StatusNoContent, http.StatusNotFound:
		a.Body = nil
		return a, nil
	}
	return HookAnswer{}, fmt.Errorf("addon %s hook %s: %w: status %d", id, name, ErrHookFailed, a.Status)
}

// Answering are the addons, in id order, that are on, running and
// declare hook name.
func (h *Hooks) Answering(ctx context.Context, name string) ([]string, error) {
	list, err := h.T.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, a := range list {
		if !a.Enabled || !a.Manifest.Answers(name) {
			continue
		}
		if st, _ := h.T.Serving(a.ID); st != StatusRunning {
			continue
		}
		ids = append(ids, a.ID)
	}
	slices.Sort(ids)
	return ids, nil
}

// call does one request under ctx (parent with the hook's deadline
// added); the result is the 200 body, nil for 204 and for 404 (a failure
// for files.* hooks). A call that runs out of the hook's own deadline
// (not the parent's) starts that hook's down period (downKey).
func (h *Hooks) call(parent, ctx context.Context, id, name string, user *addonkit.User, body []byte) (json.RawMessage, error) {
	fail := func(kind error, detail any) error {
		return fmt.Errorf("addon %s hook %s: %w: %v", id, name, kind, detail)
	}
	a, err := h.do(parent, ctx, id, name, user, body)
	if err != nil {
		return nil, err
	}
	st, b := a.Status, a.Body
	switch st {
	case http.StatusOK:
		if !json.Valid(b) {
			return nil, fail(ErrHookFailed, "answer is not JSON")
		}
		return b, nil
	case http.StatusNoContent:
		return nil, nil
	case http.StatusNotFound:
		if strings.HasPrefix(name, "files.") {
			// a file operation never takes "not implemented" for a yes
			return nil, fail(ErrHookFailed, "404 from a declared files hook")
		}
		if _, seen := h.once404.LoadOrStore(id+" "+name, true); !seen {
			h.log().Info("addon hook: not implemented", "addon", id, "hook", name)
		}
		return nil, nil
	}
	return nil, fail(ErrHookFailed, "status "+strconv.Itoa(st))
}

// do sends one request under ctx (see call) and reads the answer: its
// status, body and the socket asked. Down addons, transport errors, timeouts, bodies over
// maxHookBody and 409 answers (a *HookRefusal, or a failure without a
// text) are errors; every other status is the caller's to judge.
func (h *Hooks) do(parent, ctx context.Context, id, name string, user *addonkit.User, body []byte) (HookAnswer, error) {
	fail := func(kind error, detail any) error {
		return fmt.Errorf("addon %s hook %s: %w: %v", id, name, kind, detail)
	}
	in, err := h.T.Get(ctx, id)
	if err != nil {
		return HookAnswer{}, fail(ErrHookDown, err)
	}
	if !in.Manifest.Answers(name) {
		return HookAnswer{}, fail(ErrHookDown, "not declared")
	}
	st, sock := h.T.Serving(id)
	if st != StatusRunning || sock == "" {
		return HookAnswer{}, fail(ErrHookDown, st)
	}
	if !strings.HasPrefix(name, "files.") && h.isDown(downKey(id, name)) {
		return HookAnswer{}, fmt.Errorf("addon %s hook %s: %w: %w", id, name, ErrHookDown, errCooling)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://addon/hooks/"+name, bytes.NewReader(body))
	if err != nil {
		return HookAnswer{}, fail(ErrHookFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Antresol-Hook", name)
	if user != nil {
		u, _ := json.Marshal(user)
		req.Header.Set("X-Antresol-User", ASCIIJSON(u))
	}
	timedOut := func(err error) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if parent.Err() == nil {
				h.markDown(id, name)
			}
			return fail(ErrHookTimeout, err)
		}
		return fail(ErrHookDown, err)
	}
	client := unixClient(sock, 0)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return HookAnswer{}, timedOut(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxHookBody+1))
	if err != nil {
		return HookAnswer{}, timedOut(err)
	}
	if len(b) > maxHookBody {
		return HookAnswer{}, fail(ErrHookFailed, "answer over 32 MiB")
	}
	if resp.StatusCode == http.StatusConflict {
		var r struct {
			Error string `json:"error"`
		}
		msg := ""
		if json.Unmarshal(b, &r) == nil {
			msg = refusalText(r.Error)
		}
		if msg == "" {
			return HookAnswer{}, fail(ErrHookFailed, "409 without an error text")
		}
		return HookAnswer{}, &HookRefusal{Addon: id, Message: msg}
	}
	return HookAnswer{Status: resp.StatusCode, Body: b, Socket: sock}, nil
}

// maxRefusal caps the refusal text shown to the user, in runes.
const maxRefusal = 500

// refusalText drops control characters from an addon's refusal text and
// cuts it to maxRefusal runes.
func refusalText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxRefusal {
		s = string(r[:maxRefusal])
	}
	return s
}

// ASCIIJSON escapes every non-ASCII character of JSON text as \uXXXX, so
// that it travels in a header (X-Antresol-User) unchanged.
func ASCIIJSON(b []byte) string {
	var sb strings.Builder
	for _, c := range string(b) {
		if c < 0x80 {
			sb.WriteRune(c)
			continue
		}
		for _, u := range utf16.Encode([]rune{c}) {
			fmt.Fprintf(&sb, `\u%04x`, u)
		}
	}
	return sb.String()
}

// leftoversFor is how long a full migrate.leftovers answer is kept (F11).
const leftoversFor = 60 * time.Second

type leftoversEntry struct {
	lines []runkit.Line
	until time.Time
}

// MigrateLeftovers is «Переезд»'s ExtraLeftovers over migrate.leftovers
// (10 s): the lines of every addon declaring it, in addon-id order, for the
// household in the screen's language; a status other than failed is
// warning. An answer every declaring addon gave is kept 60 s per
// language (an addon that stops meanwhile keeps its lines until then); one
// while an addon is down (stopped, failing, slow, in a down period or
// garbled) or the addons cannot be listed is not kept, so the next look
// asks again.
func (h *Hooks) MigrateLeftovers() func(ctx context.Context) []runkit.Line {
	return func(ctx context.Context) []runkit.Line {
		lang := i18n.FromContext(ctx)
		now := h.clock()
		h.leftMu.Lock()
		e, ok := h.left[lang]
		h.leftMu.Unlock()
		if ok && now.Before(e.until) {
			return slices.Clone(e.lines)
		}
		lines, full := h.leftovers(ctx, lang)
		if full && ctx.Err() == nil {
			// no singleflight: two looks at once both ask, and the last
			// answer written wins (harmless at a screen's rate)
			h.leftMu.Lock()
			if h.left == nil {
				h.left = map[string]leftoversEntry{}
			}
			h.left[lang] = leftoversEntry{lines: slices.Clone(lines), until: now.Add(leftoversFor)}
			h.leftMu.Unlock()
		}
		return lines
	}
}

// leftovers asks every addon declaring migrate.leftovers; full is false
// when one of them gave no answer, or the addons could not be listed.
func (h *Hooks) leftovers(ctx context.Context, lang string) (lines []runkit.Line, full bool) {
	res, declared, ok := h.fanout(ctx, "migrate.leftovers", nil, nil, addonkit.LeftoversRequest{Lang: lang})
	full = ok && len(res) == declared
	for _, r := range res {
		if r.err != nil {
			full = false
			if !errors.Is(r.err, errCooling) {
				h.log().Warn("addon hook: left out", "addon", r.id, "hook", "migrate.leftovers", "err", r.err)
			}
			continue
		}
		if r.raw == nil {
			continue
		}
		var a addonkit.LeftoversAnswer
		if err := json.Unmarshal(r.raw, &a); err != nil {
			full = false
			h.log().Warn("addon hook: left out", "addon", r.id, "hook", "migrate.leftovers", "err", err)
			continue
		}
		for _, l := range a.Lines {
			st := runkit.StatusWarning
			if l.Status == runkit.StatusFailed {
				st = runkit.StatusFailed
			}
			lines = append(lines, runkit.Line{Source: l.Source, Kind: "leftover", Name: l.Name, Status: st, Reason: l.Reason})
		}
	}
	return lines, full
}
