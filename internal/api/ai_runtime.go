package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/channels"
	"github.com/antresol-app/antresol/internal/db"
)

// aiRuntime is what the server learns of the list's models as it runs, per
// database: the cool-downs and the statuses from real task calls (and
// checks). In memory, the statuses also stored in the settings row — a model
// that broke at 3 a.m. shows red next morning without a single extra request.
//
//   - Cool-down: 401/403/404 (the key or the model is wrong: another try
//     changes nothing) sits the entry out until it is edited (its print
//     changes) or a check finds it well; a timeout, 5xx, 429 or an
//     unreachable endpoint sits it out for aiCoolDown. A 400 or an answer
//     that is no JSON (the request's or the model's fault) does not.
//   - Writes: a status is written when its state changes (rare) and, while
//     it does not, at most once per entry per aiWriteEvery — no write storm
//     while a run categorises a playlist.
type aiRuntime struct {
	mu    sync.Mutex
	cool  map[string]aiCool
	live  map[string]aiLive
	wrote map[string]time.Time
	now   func() time.Time
}

type aiCool struct {
	print string
	until time.Time // zero: until the entry is edited or checked well
}

type aiLive struct {
	print  string
	status AIStatus
}

const (
	aiCoolDown   = 2 * time.Minute
	aiWriteEvery = time.Minute
)

var aiRuntimes sync.Map // *sql.DB → *aiRuntime

// aiRuntimeFor is the runtime of a database (the settings page and the
// tasks share it, so an edit or a good check frees an entry at once).
func aiRuntimeFor(conn *sql.DB) *aiRuntime {
	if v, ok := aiRuntimes.Load(conn); ok {
		return v.(*aiRuntime)
	}
	v, _ := aiRuntimes.LoadOrStore(conn, &aiRuntime{cool: map[string]aiCool{}, live: map[string]aiLive{}, wrote: map[string]time.Time{}, now: time.Now})
	return v.(*aiRuntime)
}

// cooling: the entry (as it is now) sits out.
func (rt *aiRuntime) cooling(id, print string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	c, ok := rt.cool[id]
	if !ok {
		return false
	}
	if c.print != print || (!c.until.IsZero() && !rt.now().Before(c.until)) {
		delete(rt.cool, id)
		return false
	}
	return true
}

// outcome is a status made of one ask.
func outcome(took time.Duration, err error, source string, at time.Time) AIStatus {
	st := AIStatus{State: aiOK, MS: took.Milliseconds(), At: at, Source: source}
	if err != nil {
		st.State, st.MS = aiError, 0
		st.Kind, st.Code = channels.ErrorInfo(err)
		st.Error = clipText(readableError(strings.TrimPrefix(err.Error(), "llm: ")), 200)
	}
	return st
}

// readableError turns a provider's JSON error body into its message:
// `401 Unauthorized {"error":{"message":"Invalid API Key"}}` reads
// «401 Unauthorized · Invalid API Key».
func readableError(s string) string {
	i := strings.Index(s, "{")
	if i < 0 {
		return s
	}
	var body struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if json.Unmarshal([]byte(s[i:]), &body) != nil {
		return s
	}
	msg := body.Message
	switch e := body.Error.(type) {
	case string:
		msg = e
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			msg = m
		}
	}
	if msg == "" {
		return s
	}
	head := strings.TrimRight(strings.TrimSpace(s[:i]), ":·- ")
	if head == "" {
		return msg
	}
	return head + " · " + msg
}

func clipText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// record notes one ask of an entry: the cool-down, the status, and, when it
// is due, the stored row.
func (rt *aiRuntime) record(conn *sql.DB, env *channels.LLM, e AIModel, took time.Duration, err error, source string) {
	print := entryPrint(e, env)
	now := rt.now()
	st := outcome(took, err, source, now)
	rt.mu.Lock()
	prev, had := rt.live[e.ID]
	changed := !had || prev.print != print || prev.status.State != st.State
	rt.live[e.ID] = aiLive{print: print, status: st}
	delete(rt.cool, e.ID)
	if err != nil {
		switch kind, code := channels.ErrorInfo(err); {
		case kind == channels.ErrHTTP && (code == 401 || code == 403 || code == 404), kind == channels.ErrKey:
			rt.cool[e.ID] = aiCool{print: print}
		case kind == channels.ErrTimeout, kind == channels.ErrNetwork, kind == channels.ErrHTTP && (code == 429 || code >= 500):
			rt.cool[e.ID] = aiCool{print: print, until: now.Add(aiCoolDown)}
		}
	}
	due := changed || source == aiByCheck || now.Sub(rt.wrote[e.ID]) >= aiWriteEvery
	if due {
		rt.wrote[e.ID] = now
	}
	rt.mu.Unlock()
	if due && conn != nil {
		rt.persist(conn, env, []aiStatusUpdate{{e.ID, print, st}})
	}
}

type aiStatusUpdate struct {
	id, print string
	status    AIStatus
}

// persist writes the statuses into the stored list — only the entries that
// are still what they were asked as (same print), and nothing else of the
// settings row (a PUT in flight keeps its changes).
func (rt *aiRuntime) persist(conn *sql.DB, env *channels.LLM, ups []aiStatusUpdate) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := persistAIStatuses(ctx, conn, env, ups); err != nil {
		slog.Warn("ai status not stored", "err", err)
	}
}

func persistAIStatuses(ctx context.Context, conn *sql.DB, env *channels.LLM, ups []aiStatusUpdate) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, found, err := db.GetSetting(ctx, tx, appSettingsKey)
	if err != nil || !found {
		return err
	}
	var row map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &row) != nil || row["ai"] == nil {
		return nil
	}
	var ai AISettings
	if json.Unmarshal(row["ai"], &ai) != nil {
		return nil
	}
	ai = ai.migrated()
	changed := false
	for i := range ai.List {
		for _, u := range ups {
			if ai.List[i].ID == u.id && entryPrint(ai.List[i], env) == u.print {
				ai.List[i].Status, changed = u.status, true
			}
		}
	}
	if !changed {
		return nil
	}
	if row["ai"], err = json.Marshal(ai); err != nil {
		return err
	}
	out, err := json.Marshal(row)
	if err != nil {
		return err
	}
	if err := db.PutSetting(ctx, tx, appSettingsKey, string(out)); err != nil {
		return err
	}
	return tx.Commit()
}

// status is what the page shows of an entry: the stored status or a newer
// one of this process, then the computed states — off, noKey (no request is
// made for it), unchecked.
func (rt *aiRuntime) status(e AIModel, env *channels.LLM) AIStatus {
	if e.Off {
		return AIStatus{State: aiOff}
	}
	if e.keyMissing(env) {
		return AIStatus{State: aiNoKey}
	}
	st := e.Status
	rt.mu.Lock()
	if l, ok := rt.live[e.ID]; ok && l.print == entryPrint(e, env) && l.status.At.After(st.At) {
		st = l.status
	}
	rt.mu.Unlock()
	if st.State != aiOK && st.State != aiError {
		return AIStatus{State: aiUnchecked}
	}
	return st
}

// view is the settings as answers carry them: the server's own model
// resolved, no key anywhere (KeySet says it is there), the statuses, and the
// first own model in the old fields.
func (rt *aiRuntime) view(a AISettings, env *channels.LLM) AISettings {
	a = a.resolved(env)
	out := AISettings{Off: a.Off, Features: a.Features, List: make([]AIModel, len(a.List))}
	for i, e := range a.List {
		st := rt.status(e, env)
		e.KeySet, e.Key, e.Status = e.key(env) != "", "", st
		if e.Env && env != nil {
			e.ModelDefault = env.Model
		}
		out.List[i] = e
	}
	out.URL, out.Model, out.Models = mirror(a.List)
	return out
}

// eligible: the entry may be asked for a task of tier t (on, serves it, has
// the key its provider wants).
func (e AIModel) eligible(t channels.Tier, env *channels.LLM) bool {
	return !e.Off && e.serves(t) && e.URL != "" && e.Model != "" && !e.keyMissing(env)
}

// llm of an entry: the server's own as the environment has it (its
// threads, its timeout), the others with the timeout of one ask.
func (e AIModel) llm(env *channels.LLM, timeout time.Duration) *channels.LLM {
	if e.Env {
		c := *env
		c.Chain = nil
		c.Model = e.modelOf(env)
		return &c
	}
	return &channels.LLM{URL: e.URL, Model: e.Model, Key: e.key(env), Timeout: timeout}
}

// chain is the model a task of tier t asks: the first eligible entry, with
// the rest behind it (channels.Chain; nil: none is eligible). The head's
// address and model are the first one's whatever is cooling down now, so
// a task's fingerprint does not follow an outage.
func (rt *aiRuntime) chain(conn *sql.DB, a AISettings, env *channels.LLM, t channels.Tier) *channels.LLM {
	a = a.resolved(env)
	var members []channels.ChainMember
	byID := map[string]AIModel{}
	for _, e := range a.List {
		if e.eligible(t, env) {
			members = append(members, channels.ChainMember{ID: e.ID, LLM: e.llm(env, 30*time.Second)})
			byID[e.ID] = e
		}
	}
	if len(members) == 0 {
		return nil
	}
	head := *members[0].LLM
	head.Timeout = channels.ChainBudget
	head.Chain = &channels.Chain{
		Members: members,
		Skip:    func(id string) bool { return rt.cooling(id, entryPrint(byID[id], env)) },
		Report:  func(id string, took time.Duration, err error) { rt.record(conn, env, byID[id], took, err, aiByTask) },
	}
	return &head
}
