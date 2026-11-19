package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// compose.yml is settings with short comments about each; how to run it
// (`docker compose up -d` and the rest) is docs/install.md's job. The
// tester asked for it twice, and a later merge added it back once.
func TestComposeFileHasNoRunInstructions(t *testing.T) {
	b, err := os.ReadFile("../../compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	run := regexp.MustCompile("(?i)docker[ -]compose|\\bup -d\\b|\\bdocker (run|pull)\\b")
	for i, line := range strings.Split(string(b), "\n") {
		if run.MatchString(line) {
			t.Errorf("compose.yml:%d: %q is a run instruction: put it in docs/install.md", i+1, strings.TrimSpace(line))
		}
	}
}
