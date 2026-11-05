// Package metadata holds metadata helpers shared by providers: the image cache.
package metadata

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BadImageError means the server answered but gave no usable image (HTTP
// status != 200 or empty body), as opposed to a transport failure.
type BadImageError struct {
	Msg    string
	Status int // the HTTP status (0: a 200 with no usable body)
}

func (e *BadImageError) Error() string { return e.Msg }

// LocalError wraps a local filesystem failure (cannot create dir/temp file or
// rename), which says nothing about network reachability.
type LocalError struct{ Err error }

func (e *LocalError) Error() string { return "local image store: " + e.Err.Error() }
func (e *LocalError) Unwrap() error { return e.Err }

// Images is an on-disk cache of remote images keyed by sha1(url).
type Images struct {
	Dir  string       // e.g. DataDir/images
	HTTP *http.Client // optional
	// UserAgent, when set, is sent with every download (MusicBrainz, Cover
	// Art Archive and Open Library ask clients to identify themselves).
	UserAgent string
}

// Fetch downloads url into the cache (unless already there) and returns the
// local file name (<sha1>.<ext>), served by GET /api/images/{name}.
func (im *Images) Fetch(ctx context.Context, rawURL string) (string, error) {
	return im.FetchMax(ctx, rawURL, 0)
}

// FetchMax is Fetch refusing a body over max bytes (a BadImageError; 0:
// no limit).
func (im *Images) FetchMax(ctx context.Context, rawURL string, max int64) (string, error) {
	name, ok := im.Cached(rawURL)
	if ok {
		return name, nil
	}
	dst := filepath.Join(im.Dir, name)
	if err := os.MkdirAll(im.Dir, 0o755); err != nil {
		return "", &LocalError{err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	if im.UserAgent != "" {
		req.Header.Set("User-Agent", im.UserAgent)
	}
	c := im.HTTP
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &BadImageError{fmt.Sprintf("image %s: HTTP %d", rawURL, resp.StatusCode), resp.StatusCode}
	}
	tmp, err := os.CreateTemp(im.Dir, ".dl-*")
	if err != nil {
		return "", &LocalError{err}
	}
	defer os.Remove(tmp.Name())
	var body io.Reader = resp.Body
	if max > 0 {
		body = io.LimitReader(resp.Body, max+1)
	}
	n, err := io.Copy(tmp, body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", &BadImageError{fmt.Sprintf("image %s: empty body", rawURL), 0}
	}
	if max > 0 && n > max {
		return "", &BadImageError{fmt.Sprintf("image %s: over %d bytes", rawURL, max), 0}
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", &LocalError{err}
	}
	return name, nil
}

// Name is the cache file name of rawURL (<sha1>.<ext>), downloaded or not.
func Name(rawURL string) string {
	sum := sha1.Sum([]byte(rawURL))
	return hex.EncodeToString(sum[:]) + ext(rawURL)
}

// Cached returns the cache file name of rawURL and whether that file is
// already there (no network).
func (im *Images) Cached(rawURL string) (string, bool) {
	name := Name(rawURL)
	st, err := os.Stat(filepath.Join(im.Dir, name))
	return name, err == nil && st.Size() > 0
}

func ext(rawURL string) string {
	p := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		p = u.Path
	}
	switch e := strings.ToLower(path.Ext(p)); e {
	case ".png", ".webp", ".gif", ".svg":
		return e
	case ".jpeg":
		return ".jpg"
	}
	return ".jpg"
}
