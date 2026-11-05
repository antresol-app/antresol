//go:build !sources

package main

import (
	"testing"

	"github.com/antresol-app/antresol/internal/vofit"
)

// The core build has no stage B: no voice-over is ever fitted (the
// fit worker does not run).
func TestLibraryEditionFitsNothing(t *testing.T) {
	if (&extras{}).voiceOverFit(&vofit.Worker{}) {
		t.Fatal("the core build runs the voice-over fit")
	}
}
