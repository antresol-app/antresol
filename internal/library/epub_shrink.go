package library

import (
	"archive/zip"
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"path"
	"strings"

	"golang.org/x/image/draw"
)

// shrinkStep is one try: pictures at most this long, JPEG at this quality.
type shrinkStep struct{ side, quality int }

// shrinkSteps go from what a Kindle's own screen shows without loss to what
// is still readable; the first that fits is used.
var shrinkSteps = []shrinkStep{{1600, 80}, {1400, 70}, {1200, 60}, {1000, 50}}

const (
	shrinkMinPicture = 64 << 10 // smaller pictures do not decide a book's size
	shrinkMaxPixels  = 100e6    // a header claiming more is not decoded
)

// ShrinkEPUB returns the EPUB at most target bytes long if recompressing its
// JPEG and PNG pictures can get it there, reporting whether it did; otherwise
// the smallest it got. Names, text and everything else stay as they are, so
// the manifest still holds. A book that already fits comes back unchanged.
func ShrinkEPUB(data []byte, target int) ([]byte, bool) {
	if len(data) <= target {
		return data, true
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return data, false
	}
	best := data
	for _, step := range shrinkSteps {
		var out bytes.Buffer
		if err := writeShrunk(zr, &out, step); err != nil {
			return best, false
		}
		if out.Len() < len(best) {
			best = out.Bytes()
		}
		if out.Len() <= target {
			return out.Bytes(), true
		}
	}
	return best, false
}

func writeShrunk(zr *zip.Reader, w io.Writer, step shrinkStep) error {
	zw := zip.NewWriter(w)
	for _, f := range zr.File {
		small, ok := shrinkPicture(f, step)
		if !ok {
			if err := zw.Copy(f); err != nil {
				return err
			}
			continue
		}
		// stored: a JPEG or PNG does not deflate any further
		e, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Store, Modified: f.Modified})
		if err != nil {
			return err
		}
		if _, err := e.Write(small); err != nil {
			return err
		}
	}
	return zw.Close()
}

// shrinkPicture is the entry's picture recompressed, false when it is not a
// JPEG or PNG worth it, cannot be read, or would not get smaller.
func shrinkPicture(f *zip.File, step shrinkStep) ([]byte, bool) {
	ext := strings.ToLower(path.Ext(f.Name))
	isPNG := ext == ".png"
	if !isPNG && ext != ".jpg" && ext != ".jpeg" {
		return nil, false
	}
	if f.UncompressedSize64 < shrinkMinPicture {
		return nil, false
	}
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || float64(cfg.Width)*float64(cfg.Height) > shrinkMaxPixels {
		return nil, false
	}
	var img image.Image
	if isPNG {
		img, err = png.Decode(bytes.NewReader(raw))
	} else {
		img, err = jpeg.Decode(bytes.NewReader(raw))
	}
	if err != nil {
		return nil, false
	}
	img = scaleToSide(img, step.side)
	var out bytes.Buffer
	if isPNG {
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		err = enc.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: step.quality})
	}
	if err != nil || out.Len() >= len(raw) {
		return nil, false
	}
	return out.Bytes(), true
}

// scaleToSide fits img in a side×side square, keeping its shape; a picture
// that fits already is returned as it is.
func scaleToSide(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	nw, nh := side, h*side/w
	if h > w {
		nw, nh = w*side/h, side
	}
	dst := image.NewNRGBA(image.Rect(0, 0, max(nw, 1), max(nh, 1)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}
