package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/go-chi/chi/v5"
)

// Kids' requests through addons: an addon opens them (requests.open),
// names the albums and books asked for (requests.title) and finds them
// for a kid's search (requests.search); after an admin's download it
// approves the requests of that title (POST /requests/approve). The hooks
// are called without a profile: they answer for the household.

// RequestCandidate is a title a kid may ask for (GET /api/requests/search).
type RequestCandidate = addonkit.RequestCandidate

// requestsOpenTTL: how long the addons' requests.open answer is kept (a
// status change of any addon drops it at once).
const requestsOpenTTL = 60 * time.Second

// maxAddonCandidates: the search results taken from one addon, at most.
const maxAddonCandidates = 40

const (
	msgRequestsOff      = "Просьбы недоступны"
	msgApproveNotAdmin  = "Одобрить может только администратор"
	msgRequestAddonDown = "Расширение «%s» не ответило"
)

// AddonRequests asks the addons about kids' requests. Open is
// Deps.RequestsOpen; StatusChanged, the supervisor's OnStatus callback,
// drops its cached answer. The zero value and nil hooks: closed, nothing
// named, nothing found.
type AddonRequests struct {
	hooks *addons.Hooks
	now   func() time.Time // test hook

	mu    sync.Mutex
	gen   uint64 // bumped by StatusChanged: an answer asked before is not kept
	known bool
	open  bool
	at    time.Time
}

// NewAddonRequests asks the addons through h (nil: none).
func NewAddonRequests(h *addons.Hooks) *AddonRequests {
	return &AddonRequests{hooks: h, now: time.Now}
}

func (a *AddonRequests) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *AddonRequests) log() *slog.Logger {
	if a.hooks != nil && a.hooks.Log != nil {
		return a.hooks.Log
	}
	return slog.Default()
}

// Open says whether kids' requests are open: any running addon declaring
// requests.open answers {open: true}. The answer is kept requestsOpenTTL.
func (a *AddonRequests) Open(ctx context.Context) bool {
	if a == nil || a.hooks == nil {
		return false
	}
	a.mu.Lock()
	if a.known && a.clock().Sub(a.at) < requestsOpenTTL {
		open := a.open
		a.mu.Unlock()
		return open
	}
	gen := a.gen
	a.mu.Unlock()
	open := false
	a.hooks.Each(ctx, "requests.open", nil, nil, func(id string, raw json.RawMessage) {
		var ans addonkit.RequestsOpenAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			a.log().Warn("addon hook: bad answer", "addon", id, "hook", "requests.open", "err", err)
			return
		}
		open = open || ans.Open
	})
	a.mu.Lock()
	if a.gen == gen && ctx.Err() == nil {
		a.known, a.open, a.at = true, open, a.clock()
	}
	a.mu.Unlock()
	return open
}

// StatusChanged drops the cached Open answer (Supervisor.OnStatus: an
// addon was installed, started, stopped, removed or crashed).
func (a *AddonRequests) StatusChanged(string, addons.Status) {
	a.mu.Lock()
	a.gen++
	a.known = false
	a.mu.Unlock()
}

// errRequestAddonDown: an addon that names requests did not answer.
type errRequestAddonDown struct{ name string }

func (e errRequestAddonDown) Error() string {
	return "api: addon " + e.name + " did not answer requests.title"
}

// Title names an album, book, audiobook or comic of a catalog: the first
// addon in id order that knows extID answers (ok). None knows it: ok
// false, nil error. An addon's refusal is an *addons.HookRefusal; an
// addon that is down (starting, crashed or unreachable; none answered) an
// errRequestAddonDown. A stopped or incompatible addon is not asked.
func (a *AddonRequests) Title(ctx context.Context, kind, extID string) (addonkit.RequestTitleAnswer, bool, error) {
	if a == nil || a.hooks == nil {
		return addonkit.RequestTitleAnswer{}, false, nil
	}
	list, err := a.hooks.T.List(ctx)
	if err != nil {
		return addonkit.RequestTitleAnswer{}, false, err
	}
	list = slices.DeleteFunc(list, func(in addons.Installed) bool { return !in.Enabled || !in.Manifest.Answers("requests.title") })
	slices.SortFunc(list, func(x, y addons.Installed) int { return strings.Compare(x.ID, y.ID) })
	var down *addons.Installed
	for i, in := range list {
		if st, _ := a.hooks.T.Serving(in.ID); st == addons.StatusStopped || st == addons.StatusIncompatible {
			continue // not coming back by itself: absent, not down
		}
		var raw json.RawMessage
		err := a.hooks.Call(ctx, in.ID, "requests.title", nil, addonkit.RequestTitleQuery{Kind: kind, ExtID: extID}, &raw)
		var refusal *addons.HookRefusal
		if errors.As(err, &refusal) {
			return addonkit.RequestTitleAnswer{}, false, refusal
		}
		var ans addonkit.RequestTitleAnswer
		if err == nil && raw != nil {
			if err = json.Unmarshal(raw, &ans); err == nil && strings.TrimSpace(ans.Title) == "" {
				err = errors.New("no title")
			}
		}
		if err != nil {
			a.log().Warn("addon hook: requests.title", "addon", in.ID, "err", err)
			if down == nil {
				down = &list[i]
			}
			continue
		}
		if raw == nil {
			continue // 204/404: that catalog does not know it
		}
		return ans, true, nil
	}
	if down != nil {
		name := localText(i18n.FromContext(ctx), down.Manifest.Name)
		if name == "" {
			name = down.ID
		}
		return addonkit.RequestTitleAnswer{}, false, errRequestAddonDown{name: name}
	}
	return addonkit.RequestTitleAnswer{}, false, nil
}

// Search is a kid's search of kind (not video) through the addons
// answering requests.search, merged in addon-id order: candidates of
// another kind are dropped, texts are cut. Down, slow or garbled addons
// are left out.
func (a *AddonRequests) Search(ctx context.Context, kind, q string) []RequestCandidate {
	out := []RequestCandidate{}
	if a == nil || a.hooks == nil {
		return out
	}
	a.hooks.Each(ctx, "requests.search", nil, addonkit.RequestSearchQuery{Kind: kind, Q: q}, func(id string, raw json.RawMessage) {
		var ans addonkit.RequestSearchAnswer
		if err := json.Unmarshal(raw, &ans); err != nil {
			a.log().Warn("addon hook: bad answer", "addon", id, "hook", "requests.search", "err", err)
			return
		}
		n := 0
		for _, c := range ans.Candidates {
			if c.Kind != kind || n >= maxAddonCandidates {
				continue
			}
			c.TMDBID, c.ExtID, c.Title, c.Subtitle = 0, clip(c.ExtID, 200), clip(c.Title, 300), clip(c.Subtitle, 300)
			if c.Title == "" {
				continue
			}
			if c.Year < 0 || c.Year >= 10000 {
				c.Year = 0
			}
			var aliases []string
			for _, x := range c.Aliases {
				if x = clip(x, 300); x != "" && len(aliases) < 10 {
					aliases = append(aliases, x)
				}
			}
			c.Aliases = aliases
			out = append(out, c)
			n++
		}
	})
	return out
}

// mountAddonCoreRequests: the core API's kids' requests (permission
// requests).
func mountAddonCoreRequests(r chi.Router, a *addonsAPI) {
	r.Post("/requests/approve", a.coreApproveRequests)
}

// coreApproveRequests: POST addonkit.ApproveRequest → {approved: n}, the
// requests of that title approved for admin adminId (400 when that
// profile is not an admin); the kids are told.
func (a *addonsAPI) coreApproveRequests(w http.ResponseWriter, r *http.Request) {
	in, ok := a.callingAddon(w, r, "requests")
	if !ok {
		return
	}
	if a.requests == nil || a.users == nil {
		writeErr(w, r, http.StatusServiceUnavailable, msgRequestsOff)
		return
	}
	var body addonkit.ApproveRequest
	if !decodeMax(w, r, &body, 64<<10) {
		return
	}
	video := body.Kind == library.KindMovie || body.Kind == library.KindSeries
	switch {
	case !requestKinds[body.Kind]:
		writeErr(w, r, http.StatusBadRequest, "Неизвестный раздел")
		return
	case video && body.TMDBID <= 0, !video && strings.TrimSpace(body.ExtID) == "" && strings.TrimSpace(body.Title) == "":
		writeErr(w, r, http.StatusBadRequest, "Не указано название")
		return
	}
	admin, err := a.users.Get(r.Context(), body.AdminID)
	if err != nil || !admin.IsAdmin || admin.IsKid {
		writeErr(w, r, http.StatusBadRequest, msgApproveNotAdmin)
		return
	}
	approved, err := a.requests.approveFor(r.Context(), admin.ID, body)
	if err != nil {
		log.Printf("api: addon %s approve requests: %v", in.ID, err)
		writeErr(w, r, http.StatusInternalServerError, msgAddonFailed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"approved": len(approved)})
}
