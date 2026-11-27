package api

import (
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

// Only what is asked of the models counts as a change of them: what they
// last answered does not.
func TestAIAsksTheSame(t *testing.T) {
	a := AISettings{List: []AIModel{
		{ID: "1", URL: "http://a/v1", Model: "m", Key: "k", Tiers: []channels.Tier{channels.TierMedium}},
		{ID: "2", URL: "http://b/v1", Model: "n"},
	}}
	b := AISettings{List: append([]AIModel(nil), a.List...)}
	b.List[0].Status, b.List[0].KeySet = AIStatus{State: "ok"}, true
	if !a.asksTheSame(b) {
		t.Fatal("a status is not a change of the models")
	}
	if a.List[0].Status.State != "" || b.List[0].Status.State != "ok" {
		t.Fatal("the comparison changed the settings")
	}
	for name, change := range map[string]func(s *AISettings){
		"order": func(s *AISettings) { s.List[0], s.List[1] = s.List[1], s.List[0] },
		"key":   func(s *AISettings) { s.List[0].Key = "other" },
		"off":   func(s *AISettings) { s.List[1].Off = true },
		"all":   func(s *AISettings) { s.Off = true },
		"tasks": func(s *AISettings) { s.List[0].Tiers = []channels.Tier{channels.TierLight} },
		"model": func(s *AISettings) { s.List[1].Model = "o" },
		"gone":  func(s *AISettings) { s.List = s.List[:1] },
	} {
		c := AISettings{List: append([]AIModel(nil), a.List...)}
		change(&c)
		if a.asksTheSame(c) {
			t.Errorf("%s: not seen as a change", name)
		}
	}
}
