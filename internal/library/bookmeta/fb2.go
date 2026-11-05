package bookmeta

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
)

// charsetReader decodes the charset an FB2 or OPF prolog declares: the
// usual Cyrillic 8-bit ones by name, others through htmlindex.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "windows-1251", "cp1251", "cp-1251", "win-1251", "x-cp1251":
		return charmap.Windows1251.NewDecoder().Reader(in), nil
	case "koi8-r", "koi8r", "koi8":
		return charmap.KOI8R.NewDecoder().Reader(in), nil
	case "koi8-u":
		return charmap.KOI8U.NewDecoder().Reader(in), nil
	case "windows-1252", "cp1252", "iso-8859-1", "latin1":
		return charmap.Windows1252.NewDecoder().Reader(in), nil
	case "utf8", "utf-8":
		return in, nil
	}
	// the rest by its WHATWG/IANA name (cp866, iso-8859-5, x-mac-cyrillic, ...)
	if enc, err := htmlindex.Get(label); err == nil {
		return enc.NewDecoder().Reader(in), nil
	}
	return nil, fmt.Errorf("unsupported charset %q", label)
}

type fb2Author struct {
	First    string `xml:"first-name"`
	Middle   string `xml:"middle-name"`
	Last     string `xml:"last-name"`
	Nickname string `xml:"nickname"`
}

func (a fb2Author) name() string {
	if n := clean(a.First + " " + a.Middle + " " + a.Last); n != "" {
		return n
	}
	return clean(a.Nickname)
}

type fb2Description struct {
	TitleInfo struct {
		Authors    []fb2Author `xml:"author"`
		BookTitle  string      `xml:"book-title"`
		Annotation struct {
			Inner string `xml:",innerxml"`
		} `xml:"annotation"`
		Date struct {
			Value string `xml:"value,attr"`
			Text  string `xml:",chardata"`
		} `xml:"date"`
		Coverpage struct {
			Images []struct {
				Href string `xml:"href,attr"` // l:href / xlink:href
			} `xml:"image"`
		} `xml:"coverpage"`
		Lang     string `xml:"lang"`
		Sequence []struct {
			Name   string `xml:"name,attr"`
			Number string `xml:"number,attr"`
		} `xml:"sequence"`
	} `xml:"title-info"`
	PublishInfo struct {
		Publisher string `xml:"publisher"`
		Year      string `xml:"year"`
		ISBN      string `xml:"isbn"`
	} `xml:"publish-info"`
}

func readFB2File(p string) (Meta, error) {
	f, err := os.Open(p)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()
	return parseFB2(io.LimitReader(f, maxFB2))
}

func readFB2Zip(p string) (Meta, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return Meta{}, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(pathExt(f.Name), ".fb2") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return Meta{}, err
		}
		defer rc.Close()
		return parseFB2(io.LimitReader(rc, maxFB2))
	}
	return Meta{}, errors.New("fb2.zip: no .fb2 inside")
}

func pathExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 && !strings.ContainsAny(name[i:], `/\`) {
		return name[i:]
	}
	return ""
}

const sniffSize = 64 << 10

var reXMLEncoding = regexp.MustCompile(`^\s*<\?xml[^>]*\bencoding\s*=\s*["']([^"']*)["']`)

// undeclared8bit reports whether a file that declares no 8-bit charset
// (none, or UTF-8) is not valid UTF-8 in its first bytes: such FB2 files
// are Windows-1251 in practice.
func undeclared8bit(head []byte) bool {
	if m := reXMLEncoding.FindSubmatch(head); m != nil {
		if enc := strings.ToLower(string(m[1])); enc != "utf-8" && enc != "utf8" {
			return false
		}
	}
	// a rune cut by the end of the sniffed block is not an error
	n := len(head)
	for k := 1; k < utf8.UTFMax && k <= n; k++ {
		if utf8.RuneStart(head[n-k]) {
			if !utf8.FullRune(head[n-k:]) {
				head = head[:n-k]
			}
			break
		}
	}
	return !utf8.Valid(head)
}

// capReader fails once more than limit bytes were read while limit >= 0,
// so that the part of a file the XML decoder buffers stays bounded.
type capReader struct {
	r        io.Reader
	n, limit int64
}

var errTooLarge = errors.New("larger than the limit")

func (c *capReader) Read(p []byte) (int, error) {
	if c.limit >= 0 {
		if c.n >= c.limit {
			return 0, errTooLarge
		}
		if rest := c.limit - c.n; int64(len(p)) > rest {
			p = p[:rest]
		}
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// skipTo consumes br up to (not including) the next occurrence of tag; it
// reports false at the end of the input.
func skipTo(br *bufio.Reader, tag string) bool {
	for {
		c, err := br.ReadByte()
		if err != nil {
			return false
		}
		if c != tag[0] {
			continue
		}
		br.UnreadByte() // right after ReadByte: cannot fail
		if next, err := br.Peek(len(tag)); err == nil && string(next) == tag {
			return true
		}
		br.ReadByte()
	}
}

// discardText consumes br up to (not including) the next '<'.
func discardText(br *bufio.Reader) {
	for {
		b, err := br.Peek(1)
		if err != nil || b[0] == '<' {
			return
		}
		br.ReadByte()
	}
}

// readBase64 decodes the base64 text at br (up to, not including, the next
// '<'; whitespace ignored) in 64 KB steps. It returns nil (still consuming
// the text) for invalid data or more than limit decoded bytes, so memory
// stays about twice limit at most.
func readBase64(br *bufio.Reader, limit int64) []byte {
	const step = 64 << 10 // a multiple of 4: chunks decode on their own
	enc := make([]byte, 0, step)
	var parts [][]byte
	var total int64
	failed := false
	flush := func() {
		if failed || len(enc) == 0 {
			enc = enc[:0]
			return
		}
		out := make([]byte, base64.StdEncoding.DecodedLen(len(enc)))
		n, err := base64.StdEncoding.Decode(out, enc)
		enc = enc[:0]
		if total += int64(n); err != nil || total > limit {
			failed, parts = true, nil
			return
		}
		parts = append(parts, out[:n])
	}
	for {
		b, err := br.Peek(1)
		if err != nil || b[0] == '<' {
			break
		}
		c, _ := br.ReadByte()
		if failed || c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		if enc = append(enc, c); len(enc) == step {
			flush()
		}
	}
	flush()
	if failed || total == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return bytes.Join(parts, nil)
}

// fb2UTF8 is the text of the FB2 at br in UTF-8: without a BOM, in the
// charset its prolog declares (Windows-1251 when it declares none and is not
// valid UTF-8).
func fb2UTF8(br *bufio.Reader) (io.Reader, error) {
	// a UTF-8 BOM before the prolog is not XML
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		br.Discard(3)
	}
	head, _ := br.Peek(sniffSize)
	if undeclared8bit(head) {
		return charmap.Windows1251.NewDecoder().Reader(br), nil
	}
	if m := reXMLEncoding.FindSubmatch(head); m != nil {
		cr, err := charsetReader(string(m[1]), br)
		if err != nil {
			return nil, fmt.Errorf("fb2: %w", err)
		}
		return cr, nil
	}
	return br, nil
}

// NewFB2Decoder is an XML decoder of the FB2 text in r, whatever its charset
// (the prolog's declaration is then already satisfied).
func NewFB2Decoder(r io.Reader) (*xml.Decoder, error) {
	src, err := fb2UTF8(bufio.NewReaderSize(r, sniffSize))
	if err != nil {
		return nil, err
	}
	d := newXMLDecoder(bufio.NewReader(src))
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	return d, nil
}

// parseFB2 decodes <description> (at most maxXML bytes into the file) and
// then only the cover's <binary>, scanning raw bytes past the body and the
// other pictures: memory stays bounded by maxXML + maxCover whatever the
// size of the file.
func parseFB2(r io.Reader) (Meta, error) {
	br := bufio.NewReaderSize(r, sniffSize)
	// the charset is converted here, so that the decoder reads from raw
	// (a bufio.Reader is an io.ByteReader: the decoder does no read-ahead
	// of its own and the raw scans below start where it stopped)
	src, err := fb2UTF8(br)
	if err != nil {
		return Meta{}, err
	}
	capped := &capReader{r: src, limit: maxXML}
	raw := bufio.NewReader(capped)
	d := newXMLDecoder(raw)
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil } // already UTF-8

	root := false
	var desc *fb2Description
	for desc == nil {
		tok, err := d.Token()
		if err == io.EOF {
			return Meta{}, errors.New("fb2: no <description>")
		}
		if err != nil {
			return Meta{}, fmt.Errorf("fb2: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !root {
			if se.Name.Local != "FictionBook" {
				return Meta{}, fmt.Errorf("fb2: root element <%s>", se.Name.Local)
			}
			root = true
			continue
		}
		if se.Name.Local != "description" {
			if err := d.Skip(); err != nil {
				return Meta{}, fmt.Errorf("fb2: %w", err)
			}
			continue
		}
		desc = &fb2Description{}
		if err := d.DecodeElement(desc, &se); err != nil {
			return Meta{}, fmt.Errorf("fb2: description: %w", err)
		}
	}
	capped.limit = -1
	m := descMeta(desc)
	coverID := ""
	for _, img := range desc.TitleInfo.Coverpage.Images {
		if id := strings.TrimPrefix(strings.TrimSpace(img.Href), "#"); id != "" {
			coverID = id
			break
		}
	}
	if coverID == "" {
		return m, nil
	}
	// a damaged tail only costs the cover: the description is enough
	for skipTo(raw, "<binary") {
		tok, err := d.Token()
		if err != nil {
			return m, nil
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "binary" {
			continue
		}
		id, ctype := "", ""
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "id":
				id = a.Value
			case "content-type":
				ctype = a.Value
			}
		}
		if id != coverID {
			discardText(raw)
			continue
		}
		// nil: empty, broken or larger than maxCover
		if data := readBase64(raw, maxCover); data != nil {
			if t := imageType(ctype, data, id); t != "" {
				m.Cover, m.CoverType = data, t
			}
		}
		return m, nil
	}
	return m, nil
}

func descMeta(desc *fb2Description) Meta {
	ti, pi := desc.TitleInfo, desc.PublishInfo
	m := Meta{
		Title:       clean(ti.BookTitle),
		Lang:        clean(ti.Lang),
		Description: plainText(ti.Annotation.Inner),
		ISBN:        clean(pi.ISBN),
		Publisher:   clean(pi.Publisher),
	}
	seen := map[string]bool{}
	for _, a := range ti.Authors {
		if n := a.name(); n != "" && !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			m.Authors = append(m.Authors, n)
		}
	}
	for _, s := range ti.Sequence {
		if n := clean(s.Name); n != "" {
			m.Series, m.SeriesIndex = n, parseIndex(s.Number)
			break
		}
	}
	if m.Year = yearOf(ti.Date.Value); m.Year == 0 {
		if m.Year = yearOf(ti.Date.Text); m.Year == 0 {
			m.Year = yearOf(pi.Year)
		}
	}
	return m
}
