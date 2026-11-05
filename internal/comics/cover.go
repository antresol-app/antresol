package comics

import (
	"bytes"
	"image"
	_ "image/gif" // page formats Cover decodes
	"image/jpeg"
	_ "image/png"
	"net/http"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Cover limits (variables for the tests).
var (
	maxCoverAsIs         = 2 << 20 // a first page up to this size is the cover as it is
	maxCoverPixels int64 = 40 << 20
)

const coverWidth = 600

// Cover turns the first page into a volume cover: the page itself when it
// is a JPEG, PNG or WebP of at most 2 MB, otherwise a JPEG (quality 85)
// 600 px wide (never enlarged). It returns the bytes and their extension
// (jpg png webp), or nil for AVIF and anything that cannot be decoded or
// has too many pixels.
func Cover(page []byte) ([]byte, string) {
	if len(page) == 0 || isAVIF(page) {
		return nil, ""
	}
	ext := ""
	switch http.DetectContentType(page) {
	case "image/jpeg":
		ext = "jpg"
	case "image/png":
		ext = "png"
	case "image/webp":
		ext = "webp"
	case "image/gif":
	default:
		return nil, ""
	}
	if ext != "" && len(page) <= maxCoverAsIs {
		return page, ext
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(page))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxCoverPixels {
		return nil, ""
	}
	src, _, err := image.Decode(bytes.NewReader(page))
	if err != nil {
		return nil, ""
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > coverWidth {
		w, h = coverWidth, max(1, h*coverWidth/w)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src) // transparency → white
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, ""
	}
	return out.Bytes(), "jpg"
}

// isAVIF reports an ISO-BMFF file with an avif/avis brand.
func isAVIF(b []byte) bool {
	if len(b) < 12 || string(b[4:8]) != "ftyp" {
		return false
	}
	brand := string(b[8:12])
	return brand == "avif" || brand == "avis"
}
