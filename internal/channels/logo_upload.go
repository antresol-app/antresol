package channels

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // uploaded logos
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // uploaded logos
)

// Uploaded logos: at most MaxLogoBytes of PNG, JPEG or WebP, fitted into
// LogoBox×LogoBox keeping their shape and kept as PNG in
// <data>/channel-logos/<id>.png; channels.logo_version names the upload.
const (
	MaxLogoBytes = 2 << 20
	LogoBox      = 512
	// maxLogoPixels refuses images that would take too much memory to
	// decode (a small file can claim a huge picture).
	maxLogoPixels = 50_000_000
)

// ErrLogoFormat: not a PNG, JPEG or WebP image (or a broken one).
var ErrLogoFormat = errors.New("channels: not a PNG, JPEG or WebP image")

// FitLogo decodes an uploaded logo (PNG, JPEG or WebP), fits it into
// LogoBox×LogoBox keeping its shape (a smaller one keeps its size) and
// returns it as PNG.
func FitLogo(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") {
		return nil, ErrLogoFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxLogoPixels {
		return nil, ErrLogoFormat
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrLogoFormat
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > LogoBox || h > LogoBox {
		if w >= h {
			w, h = LogoBox, max(1, h*LogoBox/w)
		} else {
			w, h = max(1, w*LogoBox/h), LogoBox
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LogoFile is where channel id's uploaded logo is kept.
func LogoFile(dataDir string, id int64) string {
	return filepath.Join(dataDir, "channel-logos", strconv.FormatInt(id, 10)+".png")
}

// LogoQuery is the query of a channel's logo URL that names its upload
// ("?v=<version>"; "" without one): a new upload is a new URL.
func LogoQuery(c Channel) string {
	if c.LogoVersion == 0 {
		return ""
	}
	return "?v=" + strconv.FormatInt(c.LogoVersion, 10)
}

// SaveLogo keeps pngData (FitLogo's) as channel c's logo and returns the
// channel with its new version.
func (s *Service) SaveLogo(ctx context.Context, c Channel, pngData []byte) (Channel, error) {
	if s.DataDir == "" {
		return c, errors.New("channels: no data dir for logos")
	}
	p := LogoFile(s.DataDir, c.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return c, err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", p, os.Getpid())
	if err := os.WriteFile(tmp, pngData, 0o644); err != nil {
		return c, err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return c, err
	}
	v := max(s.Store.now().Unix(), c.LogoVersion+1)
	if err := s.Store.SetLogo(ctx, c.ID, v); err != nil {
		return c, err
	}
	c.LogoVersion = v
	return c, nil
}

// RemoveLogo forgets channel id's uploaded logo (none: nothing to do).
func (s *Service) RemoveLogo(ctx context.Context, id int64) error {
	if s.DataDir != "" {
		if err := os.Remove(LogoFile(s.DataDir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := s.Store.SetLogo(ctx, id, 0); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// LogoPNG is channel c's logo: the uploaded one (uploaded true), else the
// generated tile (Logo).
func (s *Service) LogoPNG(c Channel) (data []byte, uploaded bool, err error) {
	if c.LogoVersion != 0 && s.DataDir != "" {
		if b, err := os.ReadFile(LogoFile(s.DataDir, c.ID)); err == nil {
			return b, true, nil
		}
	}
	b, err := Logo(c)
	return b, false, err
}
