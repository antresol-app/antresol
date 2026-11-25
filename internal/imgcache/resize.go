package imgcache

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/sync/singleflight"
)

// maxPixels bounds the images Resized decodes (about 200 MB as RGBA).
const maxPixels = 50_000_000

// slots bounds the decodes at once, for every caller: a cold page asks for
// thirty images together, and a 2 MB JPEG takes ~100 ms of a core to shrink.
var slots = make(chan struct{}, max(2, runtime.NumCPU()/2))

// flight: one shrink per copy, however many ask together.
var flight singleflight.Group

// Resized returns a copy of the image at path that fits in maxW×maxH (0 =
// no limit on that side): JPEG q85, CatmullRom, never enlarged. It is
// kept in cacheDir/resized/<sha1(path|mtime|w|h)>.jpg, so a second call
// reuses the file and a changed original gets a new one. SVG and GIF, an
// image already small enough, no limits at all or an undecodable file
// give path itself (the caller serves the original as before). The decodes
// run a few at a time and once per copy; the cache is pruned (Prune).
func Resized(cacheDir, path string, maxW, maxH int) (string, error) {
	maxW, maxH = max(maxW, 0), max(maxH, 0)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg", ".gif":
		return path, nil
	}
	if maxW == 0 && maxH == 0 {
		return path, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d", path, st.ModTime().UnixNano(), maxW, maxH)))
	dir := filepath.Join(cacheDir, "resized")
	out := filepath.Join(dir, hex.EncodeToString(sum[:])+".jpg")
	if cst, err := os.Stat(out); err == nil {
		touch(out, cst)
		pruneDue(cacheDir)
		return out, nil
	}
	v, err, _ := flight.Do(out, func() (any, error) { return makeCopy(path, dir, out, maxW, maxH) })
	if err != nil {
		return "", err
	}
	pruneDue(cacheDir)
	return v.(string), nil
}

// makeCopy is what one flight does for a copy: nothing when it exists by
// now (a caller that missed it just before the flight that made it ended
// comes here), else a decode slot and the render.
func makeCopy(path, dir, out string, maxW, maxH int) (string, error) {
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	slots <- struct{}{}
	defer func() { <-slots }()
	return render(path, dir, out, maxW, maxH)
}

// render writes the copy of path into dir as out (or says to serve path).
func render(path, dir, out string, maxW, maxH int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width*cfg.Height > maxPixels {
		return path, nil // not an image we decode (or a decompression bomb)
	}
	w, h := fit(cfg.Width, cfg.Height, maxW, maxH)
	if w >= cfg.Width && h >= cfg.Height {
		return path, nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return path, nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".resize-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, dst, &jpeg.Options{Quality: 85}); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return "", err
	}
	return out, nil
}

// Widths are the widths a client-sized copy may have: the web app's srcset
// steps (a card's poster at 1×, 2× and 3×, a backdrop across a phone or a
// window). A client may ask for any size, so Thumb turns its ask into one
// of these widths before Resized: an image has at most len(Widths) copies,
// and no URL can fill the disk with new ones.
var Widths = []int{160, 240, 320, 480, 640, 960, 1280, 1920}

// Snap rounds n up to the next of Widths: 0 (no limit) for n <= 0 and for
// n above the last step (the original is served, no copy is made).
func Snap(n int) int {
	if n <= 0 {
		return 0
	}
	for _, w := range Widths {
		if n <= w {
			return w
		}
	}
	return 0
}

// Thumb is Resized for a client that names a box of its own (Jellyfin's
// maxWidth/maxHeight, Subsonic's size): the box is turned into the width
// the image would have in it, rounded up to a step of Widths (Snap), and
// that width alone is asked for, so the aspect is kept and the image has
// one copy per step whatever boxes are asked. A copy is a little larger
// than the box when the box was not a step. An image that already fits, no
// box, or a width above the last step give the original, as Resized does.
func Thumb(cacheDir, path string, maxW, maxH int) (string, error) {
	maxW, maxH = max(maxW, 0), max(maxH, 0)
	if maxW == 0 && maxH == 0 {
		return path, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	iw, ih, ok := sizeOf(path, st)
	if !ok || iw*ih > maxPixels {
		return path, nil // Resized would not decode it either
	}
	w, _ := fit(iw, ih, maxW, maxH)
	if w >= iw {
		return path, nil
	}
	return Resized(cacheDir, path, Snap(w), 0)
}

// dims remembers the pixel size of the images Thumb looked at (by path and
// mtime), so a request for a copy that exists does not open the original
// (it may be on the media disk). Past dimsMax entries it starts over.
var dims struct {
	sync.Mutex
	m map[string][2]int
}

const dimsMax = 4096

// sizeOf is the pixel size of the image at path; ok is false for a file
// that is not an image Resized decodes.
func sizeOf(path string, st os.FileInfo) (w, h int, ok bool) {
	key := path + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10)
	dims.Lock()
	d, hit := dims.m[key]
	dims.Unlock()
	if !hit {
		var cfg image.Config
		if f, err := os.Open(path); err == nil {
			cfg, _, _ = image.DecodeConfig(f) // a failure leaves 0×0, remembered too
			f.Close()
		}
		d = [2]int{cfg.Width, cfg.Height}
		dims.Lock()
		if dims.m == nil || len(dims.m) >= dimsMax {
			dims.m = map[string][2]int{}
		}
		dims.m[key] = d
		dims.Unlock()
	}
	return d[0], d[1], d[0] > 0 && d[1] > 0
}

// fit scales w×h down into maxW×maxH (0 = unlimited), keeping the aspect.
func fit(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return w, h
	}
	scale := 1.0
	if maxW > 0 && w > maxW {
		scale = float64(maxW) / float64(w)
	}
	if maxH > 0 && float64(h)*scale > float64(maxH) {
		scale = float64(maxH) / float64(h)
	}
	if scale >= 1 {
		return w, h
	}
	return max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
}
