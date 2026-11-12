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
// StripCreds.
type Fetcher struct {
	Client          *http.Client
	MaxCompressed   int64
	MaxUncompressed int64
	// Sem holds a slot per open download (nil: no cap).
	Sem chan struct{}
}

// NewFetcher is a Fetcher with the default limits, a 15 s connect timeout
// and MaxFetches slots.
func NewFetcher() *Fetcher {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = ConnectTimeout
	tr.ResponseHeaderTimeout = time.Minute
	return &Fetcher{
		Client:          &http.Client{Transport: tr},
		MaxCompressed:   DefaultMaxCompressed,
		MaxUncompressed: DefaultMaxUncompressed,
		Sem:             make(chan struct{}, MaxFetches),
	}
}

// Open starts a download; the reader gives the uncompressed body. Close it
// to free the slot. ua "" sends Go's default User-Agent.
func (f *Fetcher) Open(ctx context.Context, rawURL, ua string) (io.ReadCloser, error) {
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
	rc, err := f.open(ctx, rawURL, ua)
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

func (f *Fetcher) open(ctx context.Context, rawURL, ua string) (opened, error) {
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
