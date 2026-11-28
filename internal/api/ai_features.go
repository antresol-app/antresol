package api

import "github.com/antresol-app/antresol/internal/channels"

// The features of the «ИИ-помощник» page (AISettings.Features): each is on
// unless the admin turned it off, and a feature that is off is never given a
// model. A language model's tasks ask through llmSource, the transcripts'
// speech recognition through SpeechSource.

// on: feature f is not turned off (a missing one is on).
func (a AISettings) featureOn(f channels.Feature) bool {
	on, set := a.Features[string(f)]
	return !set || on
}

// sameFeatures: both switch every feature the same way.
func (a AISettings) sameFeatures(b AISettings) bool {
	for _, f := range channels.Features {
		if a.featureOn(f) != b.featureOn(f) {
			return false
		}
	}
	return true
}

// cleanFeatures keeps the known features that are off (on is the default;
// an id nobody knows is dropped); nil: all on.
func cleanFeatures(in map[string]bool) map[string]bool {
	var out map[string]bool
	for _, f := range channels.Features {
		if on, set := in[string(f)]; set && !on {
			if out == nil {
				out = map[string]bool{}
			}
			out[string(f)] = false
		}
	}
	return out
}
