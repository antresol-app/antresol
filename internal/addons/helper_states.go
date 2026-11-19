package addons

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/antresol-app/antresol/addonkit"
)

// helperSources are the HelperState.Source values the core takes.
var helperSources = map[string]bool{addonkit.HelperOverride: true, addonkit.HelperSettings: true,
	addonkit.HelperCompose: true, addonkit.HelperNone: true}

// helperProblems are the HelperState.Problem values the core takes; any
// other reads as "other".
var helperProblems = map[string]bool{addonkit.HelperProblemDNS: true, addonkit.HelperProblemRefused: true,
	addonkit.HelperProblemTimeout: true, addonkit.HelperProblemStatus: true, addonkit.HelperProblemAuth: true,
	addonkit.HelperProblemUnset: true, addonkit.HelperProblemOther: true}

// HelperStates asks every addon declaring helpers.status (Each: in
// parallel, the hook's 12 s, down ones left out) how its helper services
// are, as the addon itself uses them: addon id → its states. A state
// whose name or source is not known is left out; an address is kept only
// for a compose or no source (an address the admin gave is never
// passed on), and only when it is the helper's compose address.
func (h *Hooks) HelperStates(ctx context.Context, declared func(id, name string) (Helper, bool)) map[string][]addonkit.HelperState {
	out := map[string][]addonkit.HelperState{}
	h.Each(ctx, "helpers.status", nil, nil, func(id string, raw json.RawMessage) {
		var st addonkit.HelpersStatus
		if err := json.Unmarshal(raw, &st); err != nil {
			h.log().Warn("addon hook: left out", "addon", id, "hook", "helpers.status", "err", err)
			return
		}
		for _, s := range st.Helpers {
			hp, ok := declared(id, s.Name)
			if !ok || !helperSources[s.Source] {
				continue
			}
			if s.Address != "" && (s.Address != hp.Address() || (s.Source != addonkit.HelperCompose && s.Source != addonkit.HelperNone)) {
				s.Address = ""
			}
			if s.OK {
				s.Problem = ""
			} else if s.Problem = strings.TrimSpace(s.Problem); !helperProblems[s.Problem] {
				s.Problem = addonkit.HelperProblemOther
			}
			if s.CheckedAt.After(time.Now().Add(time.Minute)) {
				s.CheckedAt = time.Time{}
			}
			out[id] = append(out[id], s)
		}
	})
	return out
}
