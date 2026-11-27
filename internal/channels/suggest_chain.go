package channels

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// Chain is «ИИ-помощник»'s list at run time: the models one task may ask,
// in the admin's order. The first that answers wins; a failure goes on to
// the next one, but at most MaxAttempts models are asked per call and the
// whole call takes at most Budget, so a long categorising run never
// multiplies its cost by the length of the list. An LLM with a Chain asks
// through it (chatFormat); its own URL, Model and Key are the first
// member's, which is what a task's fingerprint and the logs name.
type Chain struct {
	Members []ChainMember
	// Skip says a member sits out now (its cool-down); asked at every call.
	Skip func(id string) bool
	// Report is told each attempt's outcome (nil error: it answered).
	Report func(id string, took time.Duration, err error)
	// MaxAttempts per call (0: ChainAttempts); Budget in all (0: ChainBudget).
	MaxAttempts int
	Budget      time.Duration
}

// ChainMember is one entry of the list; its LLM has no Chain of its own.
type ChainMember struct {
	ID  string
	LLM *LLM
}

const (
	// ChainAttempts: models asked per call, at most.
	ChainAttempts = 3
	// ChainBudget: all the attempts of one call together.
	ChainBudget = 45 * time.Second
)

var (
	errBadAnswer   = errors.New("llm: the answer is not JSON")
	errEmptyAnswer = errors.New("llm: empty answer")
	errNoKey       = errors.New("llm: an API key is required")
	// ErrChainEmpty: every member is sitting out (cooling down).
	ErrChainEmpty = errors.New("llm: no model is available now")
)

// run asks op of the members in order (see Chain). A caller that went away
// is not a model's failure: it is returned unreported.
func (c *Chain) run(parent context.Context, op func(context.Context, *LLM) (string, error)) (string, error) {
	attempts, budget := c.MaxAttempts, c.Budget
	if attempts <= 0 {
		attempts = ChainAttempts
	}
	if budget <= 0 {
		budget = ChainBudget
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	var last error
	asked := 0
	for _, m := range c.Members {
		if asked >= attempts || ctx.Err() != nil {
			break
		}
		if c.Skip != nil && c.Skip(m.ID) {
			continue
		}
		asked++
		actx, acancel := context.WithTimeout(ctx, m.LLM.timeout())
		start := time.Now()
		out, err := op(actx, m.LLM)
		acancel()
		took := time.Since(start)
		if err == nil {
			c.report(m.ID, took, nil)
			return out, nil
		}
		if parent.Err() != nil {
			return "", parent.Err()
		}
		// the call's budget ran out under this member: not its own timeout
		if ctx.Err() != nil && took < m.LLM.timeout() {
			last = err
			break
		}
		c.report(m.ID, took, err)
		last = err
	}
	if last == nil {
		last = ErrChainEmpty
	}
	return "", last
}

func (c *Chain) report(id string, took time.Duration, err error) {
	if c.Report != nil {
		c.Report(id, took, err)
	}
}

// Patient is l given at least d for an answer, for a task whose answers are
// long (a book's chapters): the call as a whole and each model of its list
// get as long. l and its list stay as they are.
func (l *LLM) Patient(d time.Duration) *LLM {
	long := *l
	long.Timeout = max(l.timeout(), d)
	if l.Chain != nil {
		c := *l.Chain
		c.Budget = max(c.Budget, d)
		c.Members = make([]ChainMember, len(l.Chain.Members))
		for i, m := range l.Chain.Members {
			c.Members[i] = ChainMember{ID: m.ID, LLM: m.LLM.Patient(d)}
		}
		long.Chain = &c
	}
	return &long
}

// answerJSON: an answer that is meant to be a JSON object but is not is a
// failure another model may fix.
func answerJSON(out string) error {
	if !json.Valid([]byte(jsonPart(out))) {
		return errBadAnswer
	}
	return nil
}

// Failure kinds of ErrorInfo.
const (
	ErrTimeout = "timeout" // no answer in time
	ErrNetwork = "network" // the endpoint could not be reached
	ErrHTTP    = "http"    // the endpoint refused: the code says why
	ErrAnswer  = "answer"  // an empty answer or one that is not JSON
	ErrKey     = "key"     // the provider needs a key and there is none
	ErrOther   = "other"
)

// ErrorInfo sorts an LLM failure: its kind and, for ErrHTTP, the status code.
func ErrorInfo(err error) (kind string, code int) {
	var st httpStatusErr
	var ne net.Error
	switch {
	case err == nil:
		return "", 0
	case errors.As(err, &st):
		return ErrHTTP, st.code
	case errors.Is(err, errBadAnswer), errors.Is(err, errEmptyAnswer):
		return ErrAnswer, 0
	case errors.Is(err, errNoKey):
		return ErrKey, 0
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return ErrTimeout, 0
	case errors.As(err, &ne):
		return ErrNetwork, 0
	}
	return ErrOther, 0
}
