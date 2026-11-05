package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNotFound is an HTTP 404 from a metadata service.
var ErrNotFound = errors.New("metadata: not found")

// HTTPError is an answer other than 200 and 404 (after any retries).
type HTTPError struct {
	Service, Path string
	Status        int
}

func (e *HTTPError) Error() string { return fmt.Sprintf("%s %s: HTTP %d", e.Service, e.Path, e.Status) }

// DecodeError is a 200 answer whose body is not the expected JSON.
type DecodeError struct {
	Service, Path string
	Err           error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("%s %s: %v", e.Service, e.Path, e.Err) }
func (e *DecodeError) Unwrap() error { return e.Err }

// Getter fetches JSON for one service: every attempt waits on Limiter and
// sends UserAgent; statuses Retry accepts are repeated after RetryDelay, at
// most MaxRetries times. Transport failures are returned wrapped (not
// HTTPError/DecodeError): only they mean "no connection".
type Getter struct {
	Service    string // error prefix: "musicbrainz"
	HTTP       *http.Client
	Limiter    *Limiter
	UserAgent  string
	Retry      func(status int) bool
	RetryDelay time.Duration
	MaxRetries int
}

// JSON GETs rawURL and decodes the body into out; path labels errors.
func (g Getter) JSON(ctx context.Context, rawURL, path string, out any) error {
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	for attempt := 0; ; attempt++ {
		if err := g.Limiter.Wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if g.UserAgent != "" {
			req.Header.Set("User-Agent", g.UserAgent)
		}
		resp, err := hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%s %s: %w", g.Service, path, err)
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
		case g.Retry != nil && g.Retry(resp.StatusCode) && attempt < g.MaxRetries:
			t := time.NewTimer(g.RetryDelay)
			select {
			case <-t.C:
				continue
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			}
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		default:
			return &HTTPError{g.Service, path, resp.StatusCode}
		}
		if rerr != nil { // the connection broke mid-body
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%s %s: %w", g.Service, path, rerr)
		}
		if err := json.Unmarshal(body, out); err != nil {
			return &DecodeError{g.Service, path, err}
		}
		return nil
	}
}

// Class says what a client error means for the item being enriched.
type Class int

const (
	Offline  Class = iota // no connection: stop the run, the item stays pending
	Miss                  // a definite answer: 404 or another 4xx (but 429)
	ItemFail              // the server answered badly (5xx, 429, garbage): retry the item later
)

// Classify sorts a client error (not nil, not a context error).
func Classify(err error) Class {
	var he *HTTPError
	var de *DecodeError
	switch {
	case errors.Is(err, ErrNotFound):
		return Miss
	case errors.As(err, &he):
		if he.Status >= 400 && he.Status < 500 && he.Status != http.StatusTooManyRequests {
			return Miss
		}
		return ItemFail
	case errors.As(err, &de):
		return ItemFail
	}
	return Offline
}
