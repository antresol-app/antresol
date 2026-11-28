package main

import (
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

// The audiobooks' chapters and the books' other names ask their own feature
// and tier, and a feature switched off (no model) gives none — not a typed
// nil in the interface.
func TestFeatureModelsAskTheirFeature(t *testing.T) {
	var asked []channels.Feature
	var tiers []channels.Tier
	on := true
	model := func(_ context.Context, f channels.Feature, tier channels.Tier) *channels.LLM {
		asked, tiers = append(asked, f), append(tiers, tier)
		if !on {
			return nil
		}
		return &channels.LLM{URL: "http://x/v1", Model: "m"}
	}
	ctx := context.Background()
	if chaptersModel(model)(ctx) == nil || bookNamesModel(model)(ctx) == nil {
		t.Fatal("no model with the features on")
	}
	if len(asked) != 2 || asked[0] != channels.FeatureChapters || asked[1] != channels.FeatureBookNames || tiers[0] != channels.TierMedium || tiers[1] != channels.TierLight {
		t.Fatalf("asked %v %v", asked, tiers)
	}
	on = false
	if a := chaptersModel(model)(ctx); a != nil {
		t.Errorf("chapters off: %#v", a)
	}
	if a := bookNamesModel(model)(ctx); a != nil {
		t.Errorf("book names off: %#v", a)
	}
}
