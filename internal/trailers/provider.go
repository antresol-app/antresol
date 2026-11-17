// Package trailers plays a title's trailers inside Antresol. The server
// lists a title's trailers from TMDB (GET /api/items/{id}/trailers) and
// remembers their keys (Listed); a Provider gets a listed trailer ready as
// an MP4 and serves it, and the apps play that file in their own player.
// Only keys the server listed itself are played: the server is never an
// open video download proxy. Without a Provider the apps play trailers
// their own way.
//
// The public build's Provider is an addon answering the trailers.fetch
// hook (internal/addons.Trailers): the core keeps no trailer of its own.
package trailers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
)

// keyRe: a trailer's id as TMDB lists it.
var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidKey says whether key looks like a trailer's id.
func ValidKey(key string) bool { return keyRe.MatchString(key) }

var (
	// ErrPending: still being fetched (503 with Retry-After; the fetch
	// goes on, the apps ask again).
	ErrPending = errors.New("trailers: still fetching")
	// ErrNotFound: no provider can play the key (404).
	ErrNotFound = errors.New("trailers: no trailer for the key")
	// ErrFetch: the trailer could not be fetched (502).
	ErrFetch = errors.New("trailers: fetch failed")
)

// Refusal is a provider's "no" with a text for the user (502 with it).
type Refusal struct{ Message string }

func (r *Refusal) Error() string { return r.Message }

// Provider gets listed trailers ready and serves them. The caller has
// checked the key (listed, visible to the profile) before any call.
type Provider interface {
	// Available says whether trailers can be played now: the list then
	// gives each its play URL.
	Available(ctx context.Context) bool
	// Play has key's trailer (fetched from url) ready, waiting while ctx
	// lasts: its duration in ms (0: unknown). ErrPending when ctx's
	// deadline came first, ErrNotFound, ErrFetch, a *Refusal, or ctx's
	// error when the caller went away.
	Play(ctx context.Context, key, url string) (int64, error)
	// Prefetch starts getting key's trailer ready in the background.
	Prefetch(key, url string)
	// Serve answers r (GET or HEAD, with Range) with key's trailer file
	// (FileHeaders). ErrNotFound, with nothing written, when there is none
	// now; another error, with nothing written, when it could not be read.
	Serve(w http.ResponseWriter, r *http.Request, key, url string) error
}

// FileHeaders are the headers of a served trailer file.
func FileHeaders(h http.Header) {
	h.Set("Content-Type", "video/mp4")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=3600")
}
