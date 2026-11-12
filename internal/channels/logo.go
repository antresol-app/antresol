package channels

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Logo sizes: 16:9, what IPTV players draw channel logos in.
const (
	LogoWidth  = 480
	LogoHeight = 270
)

// logoColors are the avatar gradients of the web app (tokens --grad-1…7):
// a channel's logo and its tile in the web app share them (LogoIndex).
var logoColors = [][2]color.RGBA{
	{{0x5e, 0x5c, 0xe6, 255}, {0xbf, 0x5a, 0xf2, 255}}, // indigo → purple
	{{0x0a, 0x84, 0xff, 255}, {0x64, 0xd2, 0xff, 255}}, // blue → teal
	{{0xff, 0x37, 0x5f, 255}, {0xff, 0x9f, 0x0a, 255}}, // pink → orange
	{{0x30, 0xd1, 0x58, 255}, {0x00, 0xc7, 0xbe, 255}}, // green → cyan
	{{0xff, 0x9f, 0x0a, 255}, {0xff, 0xd6, 0x0a, 255}}, // orange → yellow
	{{0xbf, 0x5a, 0xf2, 255}, {0xff, 0x37, 0x5f, 255}}, // purple → pink
	{{0x00, 0xc7, 0xbe, 255}, {0x0a, 0x84, 0xff, 255}}, // cyan → blue
}

// LogoIndex is the gradient (0…6) of channel id.
func LogoIndex(id int64) int { return int(id % int64(len(logoColors))) }

var (
	logoFaceOnce sync.Once
	logoFont     *opentype.Font
	logoCache    sync.Map // name|index → []byte
)

// Logo is the PNG logo of a channel: its gradient with its name in white.
func Logo(c Channel) ([]byte, error) {
	key := c.Name + "|" + string(rune('0'+LogoIndex(c.ID)))
	if b, ok := logoCache.Load(key); ok {
		return b.([]byte), nil
	}
	var err error
	logoFaceOnce.Do(func() { logoFont, err = opentype.Parse(gobold.TTF) })
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, LogoWidth, LogoHeight))
	g := logoColors[LogoIndex(c.ID)]
	// a 145° gradient, as the web's
	for y := 0; y < LogoHeight; y++ {
		for x := 0; x < LogoWidth; x++ {
			t := (float64(x)/LogoWidth*0.57 + float64(y)/LogoHeight*0.43)
			img.SetRGBA(x, y, color.RGBA{lerp(g[0].R, g[1].R, t), lerp(g[0].G, g[1].G, t), lerp(g[0].B, g[1].B, t), 255})
		}
	}
	lines, size := layoutName(c.Name)
	face, err := opentype.NewFace(logoFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	m := face.Metrics()
	lineH := (m.Ascent + m.Descent).Ceil()
	top := (LogoHeight-lineH*len(lines))/2 + m.Ascent.Ceil()
	for i, ln := range lines {
		w := font.MeasureString(face, ln).Ceil()
		x := (LogoWidth - w) / 2
		y := top + i*lineH
		// a soft shadow keeps white legible on the light gradients
		for _, d := range []struct {
			dx, dy int
			c      color.Color
		}{{0, 2, color.RGBA{0, 0, 0, 70}}, {0, 0, color.White}} {
			dr := &font.Drawer{Dst: img, Src: image.NewUniform(d.c), Face: face, Dot: fixed.P(x+d.dx, y+d.dy)}
			dr.DrawString(ln)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	logoCache.Store(key, buf.Bytes())
	return buf.Bytes(), nil
}

func lerp(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }

// layoutName splits a name into at most 3 lines that fit the logo and
// picks the font size: one short word large, long names smaller.
func layoutName(name string) ([]string, float64) {
	words := strings.Fields(name)
	if len(words) == 0 {
		return []string{""}, 64
	}
	for _, size := range []float64{72, 60, 50, 42, 36, 30} {
		perLine := int(float64(LogoWidth-60) / (size * 0.62)) // Go Bold is ~0.62 em wide per letter
		var lines []string
		cur := ""
		ok := true
		for _, w := range words {
			if len([]rune(w)) > perLine {
				ok = false
				break
			}
			switch {
			case cur == "":
				cur = w
			case len([]rune(cur))+1+len([]rune(w)) <= perLine:
				cur += " " + w
			default:
				lines = append(lines, cur)
				cur = w
			}
		}
		lines = append(lines, cur)
		if ok && len(lines) <= 3 && float64(len(lines))*size*1.2 <= LogoHeight-40 {
			return lines, size
		}
	}
	// a name too long for any size: cut
	r := []rune(name)
	return []string{string(r[:min(len(r), 22)])}, 30
}
