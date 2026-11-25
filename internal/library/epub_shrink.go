package library

import (
	"archive/zip"
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
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
	// shrinkMaxPicture: a picture entry declared larger is left as it is
	// (not read): the book's own header names the size, and the zip reader
	// stops a body that outgrows it.
	shrinkMaxPicture = 64 << 20
)

// ShrinkEPUB recompresses the JPEG and PNG pictures of the EPUB at r (size
// bytes) until it is at most target bytes long, reporting whether it got
// there. Names, text and everything else stay as they are, so the manifest
// still holds. Nothing is held in memory but the picture being worked on:
// each try goes to a temp file in dir ("": the system's), and the smallest
// one is returned, rewound, for the caller to close and remove. nil: no try
// got smaller than the book itself (ok: it fits already; not ok: it is not a
// ZIP or no picture would shrink).
func ShrinkEPUB(r io.ReaderAt, size, target int64, dir string) (best *os.File, ok bool, err error) {
	if size <= target {
		return nil, true, nil
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, false, nil
	}
	bestSize := size
	for _, step := range shrinkSteps {
		tmp, err := os.CreateTemp(dir, "shrink-*.epub")
		if err != nil {
			discardBest(best)
			return nil, false, err
		}
		if err := writeShrunk(zr, tmp, step); err != nil {
			discardTemp(tmp)
			break
		}
		st, err := tmp.Stat()
		if err != nil {
			discardTemp(tmp)
			discardBest(best)
			return nil, false, err
		}
		if st.Size() >= bestSize {
			discardTemp(tmp)
			continue
		}
		discardBest(best)
		best, bestSize = tmp, st.Size()
		if bestSize <= target {
			ok = true
			break
		}
	}
	if best == nil {
		return nil, false, nil
	}
	best, err = rewound(best)
	return best, ok, err
}

func discardBest(f *os.File) {
	if f != nil {
		discardTemp(f)
	}
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
	if f.UncompressedSize64 < shrinkMinPicture || f.UncompressedSize64 > shrinkMaxPicture {
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
