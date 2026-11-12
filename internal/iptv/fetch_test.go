package iptv

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func readAll(t *testing.T, f *Fetcher, u string) ([]byte, error) {
	t.Helper()
	rc, err := f.Open(context.Background(), u, "")
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func TestFetchGzipByMagic(t *testing.T) {
	body := []byte("#EXTM3U\n#EXTINF:-1,A\nhttp://stream.example.test/a.ts\n")
	var gotUA string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.UserAgent()
		if strings.HasSuffix(r.URL.Path, "plain.txt") {
			w.Write(body)
			return
		}
		// A gzip file under a name that says nothing about it.
		w.Write(gz(t, body))
	})
	f := NewFetcher()
	for _, p := range []string{"/list.m3u", "/plain.txt"} {
		got, err := readAll(t, f, srv.URL+p)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("%s: %q %v", p, got, err)
		}
	}
	rc, err := f.Open(context.Background(), srv.URL+"/x", "Custom/1")
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()
	if gotUA != "Custom/1" {
		t.Fatalf("ua: %q", gotUA)
	}
}

func TestFetchLimitCompressed(t *testing.T) {
	big := make([]byte, 2<<20)
	rand.Read(big)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chunked" {
			w.(http.Flusher).Flush() // no Content-Length
		}
		w.Write(big)
	})
	f := NewFetcher()
	f.MaxCompressed = 1 << 20
	for _, p := range []string{"/sized", "/chunked"} {
		if _, err := readAll(t, f, srv.URL+p); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("%s: %v", p, err)
		}
	}
}

func TestFetchLimitUncompressed(t *testing.T) {
	bomb := gz(t, make([]byte, 10<<20)) // 10 MB of zeros, ~10 KB on the wire
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write(bomb) })
	f := NewFetcher()
	f.MaxUncompressed = 1 << 20
	if _, err := readAll(t, f, srv.URL+"/guide.xml"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bomb: %v", err)
	}
}

func TestFetchErrorsHideCredentials(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusForbidden) })
	f := NewFetcher()
	u := strings.Replace(srv.URL, "http://", "http://user:secretpw@", 1) + "/get.php?username=user&password=secretpw&type=m3u_plus"
	_, err := f.Open(context.Background(), u, "")
	var he ErrHTTP
	if !errors.As(err, &he) || he.Code != 403 {
		t.Fatalf("want ErrHTTP 403, got %v", err)
	}
	// A connection error (closed port) too.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := strings.Replace(dead.URL, "http://", "http://user:secretpw@", 1) + "/live/user/secretpw/1.ts?token=secretpw"
	dead.Close()
	_, err2 := f.Open(context.Background(), deadURL, "")
	if err2 == nil {
		t.Fatal("dead server: no error")
	}
	for _, e := range []error{err, err2} {
		s := strings.ToLower(e.Error())
		if strings.Contains(s, "password") || strings.Contains(s, "secretpw") {
			t.Fatalf("error carries credentials: %s", e)
		}
	}
}

func TestFetchConcurrencyCap(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("#EXTM3U\n")) })
	f := NewFetcher() // two at once
	a, err := f.Open(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Open(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := f.Open(ctx, srv.URL, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("3rd fetch did not wait: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		c, err := f.Open(context.Background(), srv.URL, "")
		if err == nil {
			c.Close()
		}
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("3rd fetch ran while two are open")
	case <-time.After(50 * time.Millisecond):
	}
	a.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("3rd fetch still waits after a close")
	}
	b.Close()
	b.Close() // twice is harmless
}
