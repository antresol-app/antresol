package iptv

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/netguard"
)

// Fetch limits and timeouts (callers put the overall one on ctx).
const (
	DefaultMaxCompressed   = 200 << 20
	DefaultMaxUncompressed = 1 << 30
	ConnectTimeout         = 15 * time.Second
	M3UTimeout             = 2 * time.Minute
	EPGTimeout             = 10 * time.Minute
	// MaxFetches: at most this many downloads at once.
	MaxFetches = 2
)

// ErrTooLarge: the download went past a size limit.
var ErrTooLarge = errors.New("iptv: download too large")

// ErrHTTP: the server answered with a non-2xx status.
type ErrHTTP struct{ Code int }

func (e ErrHTTP) Error() string { return fmt.Sprintf("iptv: HTTP %d", e.Code) }

// Fetcher downloads playlists and guides: within size limits (counted
// while reading, on the wire and after gunzip), gzip found by its magic
// bytes, at most cap(Sem) at once. Its errors show URLs only through
// StripCreds. Its clients never dial a link-local, reserved or other
// address that is no one's server (netguard), and the LAN's own addresses
// only for a download whose source is spelled as a LAN address (OpenVia).
type Fetcher struct {
	Client *http.Client
	// Local: the client for a LAN source (nil: Client does it all).
	Local           *http.Client
	MaxCompressed   int64
	MaxUncompressed int64
	// Sem holds a slot per open download (nil: no cap).
	Sem chan struct{}
}

// NewFetcher is a Fetcher with the default limits, a 15 s connect timeout
// and MaxFetches slots.
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client:          guardedClient(false),
		Local:           guardedClient(true),
		MaxCompressed:   DefaultMaxCompressed,
		MaxUncompressed: DefaultMaxUncompressed,
		Sem:             make(chan struct{}, MaxFetches),
	}
}

// guardedClient dials public addresses, and the LAN's when lan is set, at
// every connection (redirects included). It uses no proxy: a proxy would be
// the address dialled, not the target.
func guardedClient(lan bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = (&net.Dialer{Timeout: ConnectTimeout, KeepAlive: 30 * time.Second, ControlContext: netguard.Control(lan)}).DialContext
	tr.TLSHandshakeTimeout = ConnectTimeout
	tr.ResponseHeaderTimeout = time.Minute
	return &http.Client{Transport: tr}
}

// Open starts a download; the reader gives the uncompressed body. Close it
// to free the slot. ua "" sends Go's default User-Agent. rawURL is also
// what decides whether the LAN may be reached (OpenVia).
func (f *Fetcher) Open(ctx context.Context, rawURL, ua string) (io.ReadCloser, error) {
	return f.OpenVia(ctx, rawURL, ua, rawURL)
}

// OpenVia is Open for a download whose address is a third party's word (a
// guide a playlist names): the LAN is reachable only when lanOf, the
// address the admin typed for its source, is spelled as a LAN address.
func (f *Fetcher) OpenVia(ctx context.Context, rawURL, ua, lanOf string) (io.ReadCloser, error) {
	safe := StripCreds(rawURL)
	if f.Sem != nil {
		select {
		case f.Sem <- struct{}{}:
		case <-ctx.Done():
			return nil, fmt.Errorf("iptv: fetch %s: %w", safe, ctx.Err())
		}
	}
	release := func() {
		if f.Sem != nil {
			<-f.Sem
		}
	}
	rc, err := f.open(ctx, rawURL, ua, lanOf)
	if err != nil {
		release()
		return nil, fmt.Errorf("iptv: fetch %s: %w", safe, err)
	}
	return &fetchBody{Reader: rc.r, closer: rc.body, release: release}, nil
}

type opened struct {
	r    io.Reader
	body io.Closer
}

func (f *Fetcher) open(ctx context.Context, rawURL, ua, lanOf string) (opened, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return opened{}, errors.New("bad URL")
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	// Asked explicitly, so the transport does not gunzip behind our
	// counters; gzip is found by its magic bytes below.
	req.Header.Set("Accept-Encoding", "gzip")
	c := f.Client
	if u, err := url.Parse(lanOf); err == nil && f.Local != nil && netguard.LANSpelling(u.Hostname()) {
		c = f.Local
	}
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the bare cause, without the URL
		}
		return opened{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return opened{}, ErrHTTP{Code: resp.StatusCode}
	}
	maxC, maxU := f.MaxCompressed, f.MaxUncompressed
	if maxC <= 0 {
		maxC = DefaultMaxCompressed
	}
	if maxU <= 0 {
		maxU = DefaultMaxUncompressed
	}
	if resp.ContentLength > maxC {
		resp.Body.Close()
		return opened{}, fmt.Errorf("%w (%d bytes)", ErrTooLarge, resp.ContentLength)
	}
	var r io.Reader = &capReader{r: resp.Body, left: maxC}
	// Up to two gzip layers (Content-Encoding over a .gz file).
	for range 2 {
		br := bufio.NewReaderSize(r, 64*1024)
		r = br
		magic, _ := br.Peek(2)
		if len(magic) < 2 || magic[0] != 0x1f || magic[1] != 0x8b {
			break
		}
		zr, err := gzip.NewReader(br)
		if err != nil {
			resp.Body.Close()
			return opened{}, fmt.Errorf("gzip: %w", err)
		}
		zr.Multistream(true)
		r = zr
	}
	return opened{r: &capReader{r: r, left: maxU}, body: resp.Body}, nil
}

// capReader fails with ErrTooLarge once more than left bytes come.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n, ErrTooLarge
	}
	return n, err
}

type fetchBody struct {
	io.Reader
	closer  io.Closer
	release func()
	once    sync.Once
}

func (b *fetchBody) Close() error {
	var err error
	b.once.Do(func() {
		err = b.closer.Close()
		b.release()
	})
	return err
}
