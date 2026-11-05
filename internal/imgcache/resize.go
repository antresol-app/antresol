package imgcache

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// maxPixels bounds the images Resized decodes (about 200 MB as RGBA).
const maxPixels = 50_000_000

// Resized returns a copy of the image at path that fits in maxW×maxH (0 =
// no limit on that side): JPEG q85, CatmullRom, never enlarged. It is
// kept in cacheDir/resized/<sha1(path|mtime|w|h)>.jpg, so a second call
// reuses the file and a changed original gets a new one. SVG and GIF, an
// image already small enough, no limits at all or an undecodable file
// give path itself (the caller serves the original as before).
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
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
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
