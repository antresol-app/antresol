package channels

import (
	"context"
	"testing"
	"time"
)

// A task with long answers gets that long from the models of the list too,
// not the list's usual budget; the list itself stays as it was.
func TestPatientModel(t *testing.T) {
	slow, _ := chainServer(t, 200, `{"a":1}`, 150*time.Millisecond)
	slow.Timeout = 30 * time.Millisecond
	c := &Chain{Members: []ChainMember{{ID: "a", LLM: slow}}, Budget: 40 * time.Millisecond}
	head := &LLM{Chain: c, Timeout: 40 * time.Millisecond}
	if _, err := head.AskJSON(context.Background(), "s", "u"); err == nil {
		t.Fatal("the slow model answered within the short budget")
	}
	if _, err := head.Patient(2*time.Second).AskSchema(context.Background(), "s", "u", map[string]any{"type": "object"}); err != nil {
		t.Fatalf("the patient model: %v", err)
	}
	if c.Budget != 40*time.Millisecond || slow.Timeout != 30*time.Millisecond || head.Timeout != 40*time.Millisecond {
		t.Fatalf("the list changed: budget %v, member %v, head %v", c.Budget, slow.Timeout, head.Timeout)
	}
	own := &LLM{Timeout: time.Hour}
	if got := own.Patient(time.Minute).Timeout; got != time.Hour {
		t.Fatalf("a longer timeout was shortened to %v", got)
	}
}
