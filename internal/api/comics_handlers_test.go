package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/comics"
)

func TestComicPagesAPI(t *testing.T) {
	e := newLibEnv(t)
	var opens atomic.Int32
	prev := openComic
	openComic = func(p, cache string) (*comics.Archive, error) { opens.Add(1); return comics.Open(p, cache) }
	t.Cleanup(func() { openComic = prev })
	_, vols := e.comic("Берсерк", "", 1)
	f := itoa(vols[0].file)
	w := e.do("GET", "/api/comics/"+f+"/pages", "", e.admin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"count":3`) || !strings.Contains(w.Body.String(), `"direction":"rtl"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for i := 0; i < 3; i++ {
		w = e.do("GET", "/api/comics/"+f+"/page/0", "", e.admin)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("Cache-Control") != "private, max-age=86400" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Body.Len() == 0 {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
	}
	w = e.do("GET", "/api/comics/"+f+"/page/3", "", e.admin)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Страница не найдена") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/comics/"+f+"/page/-1", "", e.admin); w.Code != 404 {
		t.Fatalf("negative %d", w.Code)
	}
	if n := opens.Load(); n != 1 {
		t.Fatalf("archive opened %d times", n)
	}
}

// A page whose bytes are not an image (HTML named .jpg) is not served.
func TestComicPageSniffed(t *testing.T) {
	e := newLibEnv(t)
	_, vols := e.comic("Берсерк", "", 1)
	fl, _ := e.repo.FileByID(t.Context(), vols[0].file)
	writeCBZ(t, fl.Path, map[string][]byte{"01.jpg": []byte("<html><script>alert(1)</script></html>")})
	w := e.do("GET", "/api/comics/"+itoa(vols[0].file)+"/page/0", "", e.admin)
	if w.Code == 200 || strings.Contains(w.Body.String(), "<script>") {
		t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
	}
}

func TestComicLRUClosesEvicted(t *testing.T) {
	c := newComicArchives(2, "")
	dir := t.TempDir()
	jpg := jpegBytes(t)
	var paths []string
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, n+".cbz")
		writeCBZ(t, p, map[string][]byte{"1.jpg": jpg})
		paths = append(paths, p)
	}
	readable := func(a interface {
		Page(int) (io.ReadCloser, string, error)
	}) bool {
		rc, _, err := a.Page(0)
		if err != nil {
			return false
		}
		defer rc.Close()
		_, err = io.ReadAll(rc)
		return err == nil
	}
	held, release, err := c.get(t.Context(), 1, paths[0]) // in use while evicted
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range paths[1:] {
		a, done, err := c.get(t.Context(), int64(i+2), p)
		if err != nil || len(a.Pages()) != 1 {
			t.Fatal(err)
		}
		done()
	}
	if n := c.len(); n != 2 {
		t.Fatalf("cache holds %d", n)
	}
	if !readable(held) {
		t.Fatal("an evicted archive in use was closed")
	}
	release()
	if readable(held) {
		t.Fatal("an evicted archive was not closed after its last use")
	}
}

// Two requests for one volume share one open; a waiter that goes away
// does not disturb the other, and the entry is closed after its last use.
func TestComicLRUSingleflightCancel(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.cbz")
	writeCBZ(t, p, map[string][]byte{"1.jpg": jpegBytes(t)})
	var opens atomic.Int32
	gate := make(chan struct{})
	prev := openComic
	openComic = func(p, cache string) (*comics.Archive, error) { opens.Add(1); <-gate; return comics.Open(p, cache) }
	t.Cleanup(func() { openComic = prev })
	c := newComicArchives(1, "")

	ctx1, cancel1 := context.WithCancel(t.Context())
	err1 := make(chan error, 1)
	go func() { _, _, err := c.get(ctx1, 1, p); err1 <- err }()
	type res struct {
		a    *comics.Archive
		done func()
		err  error
	}
	r2 := make(chan res, 1)
	go func() { a, d, err := c.get(t.Context(), 1, p); r2 <- res{a, d, err} }()
	for opens.Load() == 0 {
		runtime.Gosched()
	}
	cancel1()
	if err := <-err1; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter: %v", err)
	}
	close(gate)
	got := <-r2
	if got.err != nil || len(got.a.Pages()) != 1 {
		t.Fatalf("%v", got.err)
	}
	if n := opens.Load(); n != 1 {
		t.Fatalf("opened %d times", n)
	}
	// evict it while in use, then release: closed
	q := filepath.Join(dir, "b.cbz")
	writeCBZ(t, q, map[string][]byte{"1.jpg": jpegBytes(t)})
	_, d2, err := c.get(t.Context(), 2, q)
	if err != nil {
		t.Fatal(err)
	}
	d2()
	if rc, _, err := got.a.Page(0); err != nil {
		t.Fatal("evicted archive in use was closed")
	} else {
		rc.Close()
	}
	got.done()
	rc, _, err := got.a.Page(0)
	if err == nil {
		_, err = io.ReadAll(rc)
		rc.Close()
	}
	if err == nil {
		t.Fatal("evicted archive not closed after its last use")
	}
}

// An open nobody waits for any more by the time it gets a slot is skipped.
func TestComicOpenAbandoned(t *testing.T) {
	var opens atomic.Int32
	prev := openComic
	openComic = func(p, cache string) (*comics.Archive, error) { opens.Add(1); return comics.Open(p, cache) }
	t.Cleanup(func() { openComic = prev })
	c := newComicArchives(8, "")
	for i := 0; i < comicOpensAtOnce; i++ { // all slots busy
		c.slots <- struct{}{}
	}
	p := filepath.Join(t.TempDir(), "a.cbz")
	writeCBZ(t, p, map[string][]byte{"1.jpg": jpegBytes(t)})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := c.get(ctx, 1, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < comicOpensAtOnce; i++ {
		<-c.slots
	}
	for c.len() != 0 {
		runtime.Gosched()
	}
	if n := opens.Load(); n != 0 {
		t.Fatalf("abandoned open ran %d times", n)
	}
}

// /pages reports each page's size from its header; a page that is not an
// image gets [0, 0].
func TestComicPageSizes(t *testing.T) {
	e := newLibEnv(t)
	_, vols := e.comic("Берсерк", "", 1)
	fl, _ := e.repo.FileByID(t.Context(), vols[0].file)
	var tall bytes.Buffer
	if err := png.Encode(&tall, image.NewRGBA(image.Rect(0, 0, 8, 40))); err != nil {
		t.Fatal(err)
	}
	writeCBZ(t, fl.Path, map[string][]byte{"01.jpg": jpegBytes(t), "02.png": tall.Bytes(), "03.jpg": []byte("not an image")})
	w := e.do("GET", "/api/comics/"+itoa(vols[0].file)+"/pages", "", e.admin)
	var got struct {
		Count int      `json:"count"`
		Sizes [][2]int `json:"sizes"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	want := [][2]int{{4, 4}, {8, 40}, {0, 0}}
	if got.Count != 3 || len(got.Sizes) != 3 || got.Sizes[0] != want[0] || got.Sizes[1] != want[1] || got.Sizes[2] != want[2] {
		t.Fatalf("sizes %+v", got)
	}
}
