package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The metadata pass-through: an addon asks a provider through the
// server's client (its key, User-Agent, limits) for a path below the
// client's BaseURL. These helpers keep such a path from ever leaving it.

// MaxRaw is the largest answer body a pass-through returns.
const MaxRaw = 8 << 20

// ErrTooLarge is an answer body over MaxRaw.
var ErrTooLarge = errors.New("metadata: answer too large")

// errUnsafePath is a pass-through path SafeRawPath refuses.
var errUnsafePath = errors.New("metadata: unsafe path")

// maxDecodes: how many times SafeRawPath percent-decodes a path looking for
// a dot segment («%252e%252e» is «..» twice decoded); a path still changing
// after that is refused.
const maxDecodes = 4

// SafeRawPath says whether p, a percent-encoded path, may be appended to a
// client's BaseURL: it starts with exactly one '/', and neither it nor any
// of its decodings holds a backslash, an empty segment («//», which a
// leading «//host» would make a host) or a «.»/«..» segment. A trailing
// '/' is allowed.
func SafeRawPath(p string) bool {
	s := p
	for i := 0; ; i++ {
		if !strings.HasPrefix(s, "/") || strings.Contains(s, `\`) || strings.Contains(s, "//") {
			return false
		}
		for _, seg := range strings.Split(s[1:], "/") {
			if seg == "." || seg == ".." {
				return false
			}
		}
		next, err := url.PathUnescape(s)
		if err != nil {
			return false
		}
		if next == s {
			return true
		}
		if i == maxDecodes {
			return false
		}
		s = next
	}
}

// RawURL is base + p (SafeRawPath) + "?" + q, checked to stay on base's
// scheme, host and path.
func RawURL(base, p string, q url.Values) (string, error) {
	if !SafeRawPath(p) {
		return "", errUnsafePath
	}
	b, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || b.Host == "" || (b.Scheme != "https" && b.Scheme != "http") {
		return "", errors.New("metadata: bad base URL")
	}
	raw := b.String() + p
	if len(q) > 0 {
		raw += "?" + q.Encode()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != b.Scheme || u.Host != b.Host || u.User != nil ||
		!strings.HasPrefix(u.EscapedPath(), b.EscapedPath()+"/") {
		return "", errUnsafePath
	}
	return raw, nil
}

// RawWithin is the pass-through of a client whose base URL is base: it
// checks p (RawURL), takes a slot of sem (the addons' own budget) and GETs
// the URL with Raw (the shared Limiter, UserAgent, retries). Redirects to
// another scheme or host are not followed: the 3xx is the answer.
func (g Getter) RawWithin(ctx context.Context, sem chan struct{}, base, p string, q url.Values) (status int, contentType string, body []byte, err error) {
	u, err := RawURL(base, p, q)
	if err != nil {
		return 0, "", nil, err
	}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return 0, "", nil, ctx.Err()
	}
	b, _ := url.Parse(u) // RawURL parsed it
	g.HTTP = StayOn(g.HTTP, b)
	return g.Raw(ctx, u, p)
}

// StayOn is hc (nil: a client with a 20 s timeout) not following a redirect
// off base's scheme and host: such a redirect's 3xx is returned instead,
// so a pass-through never sends the server's credentials elsewhere.
func StayOn(hc *http.Client, base *url.URL) *http.Client {
	c := http.Client{Timeout: 20 * time.Second}
	if hc != nil {
		c = *hc
	}
	next := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != base.Scheme || req.URL.Host != base.Host {
			return http.ErrUseLastResponse
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return errors.New("metadata: too many redirects")
		}
		return nil
	}
	return &c
}
