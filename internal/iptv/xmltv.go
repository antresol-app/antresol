package iptv

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// XMLChannel is a <channel> of a guide.
type XMLChannel struct {
	ID string
	// Names: its display-names, in order.
	Names []string
	Icon  string
}

// XMLProgramme is a <programme> of a guide. Stop is zero when the guide
// gives none.
type XMLProgramme struct {
	Channel                               string
	Start, Stop                           time.Time
	Title, SubTitle, Desc, Category, Icon string
}

// ErrNotXMLTV: the document's root is not <tv>.
var ErrNotXMLTV = errors.New("iptv: not an XMLTV guide")

// ParseXMLTV streams a guide, one element at a time (the document is never
// built): every channel goes to onChannel; a programme goes to
// onProgramme when keep(channel) (nil: all) and it overlaps [from, to) —
// one that does not is skipped before its children are decoded. Of
// several titles, sub-titles, descriptions and categories the first wins,
// a lang="ru" one over one that is not. A cut or broken document returns
// an error after delivering what came before it.
func ParseXMLTV(r io.Reader, keep func(channelID string) bool, from, to time.Time,
	onChannel func(XMLChannel), onProgramme func(XMLProgramme)) error {
	d := xml.NewDecoder(r)
	d.Strict = false // HTML entities (&nbsp;) in descriptions are common
	d.CharsetReader = charsetReader
	p := xmltvParser{d: d}
	root := false
	for {
		tok, err := d.RawToken()
		if err == io.EOF {
			if !root {
				return ErrNotXMLTV
			}
			if p.depth > 0 {
				return fmt.Errorf("iptv: guide: %w", io.ErrUnexpectedEOF)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("iptv: guide: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if !root {
				if t.Name.Local != "tv" {
					return ErrNotXMLTV
				}
				root = true
				p.depth = 1
				continue
			}
			var err error
			switch t.Name.Local {
			case "channel":
				var c XMLChannel
				c, err = p.channel(t)
				if err == nil && onChannel != nil {
					onChannel(c)
				}
			case "programme":
				var pr XMLProgramme
				var ok bool
				pr, ok, err = p.programme(t, keep, from, to)
				if err == nil && ok && onProgramme != nil {
					onProgramme(pr)
				}
			default:
				err = p.skip()
			}
			if err != nil {
				return fmt.Errorf("iptv: guide: %w", err)
			}
		case xml.EndElement:
			if root && t.Name.Local == "tv" {
				p.depth = 0
			}
		}
	}
}

type xmltvParser struct {
	d     *xml.Decoder
	depth int
	text  strings.Builder
}

func attr(t xml.StartElement, name string) (string, bool) {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

// skip reads past the end of the element just started.
func (p *xmltvParser) skip() error {
	n := 1
	for n > 0 {
		tok, err := p.d.RawToken()
		if err != nil {
			if err == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		switch tok.(type) {
		case xml.StartElement:
			n++
		case xml.EndElement:
			n--
		}
	}
	return nil
}

// textOf reads the text of the element just started (nested elements'
// text included) up to its end.
func (p *xmltvParser) textOf() (string, error) {
	p.text.Reset()
	n := 1
	for n > 0 {
		tok, err := p.d.RawToken()
		if err != nil {
			if err == io.EOF {
				return "", io.ErrUnexpectedEOF
			}
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n++
		case xml.EndElement:
			n--
		case xml.CharData:
			p.text.Write(t)
		}
	}
	return strings.TrimSpace(p.text.String()), nil
}

func (p *xmltvParser) channel(start xml.StartElement) (XMLChannel, error) {
	var c XMLChannel
	c.ID, _ = attr(start, "id")
	for {
		tok, err := p.d.RawToken()
		if err != nil {
			if err == io.EOF {
				return c, io.ErrUnexpectedEOF
			}
			return c, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "display-name":
				s, err := p.textOf()
				if err != nil {
					return c, err
				}
				if s != "" {
					c.Names = append(c.Names, s)
				}
			case "icon":
				if c.Icon == "" {
					c.Icon, _ = attr(t, "src")
				}
				if err := p.skip(); err != nil {
					return c, err
				}
			default:
				if err := p.skip(); err != nil {
					return c, err
				}
			}
		case xml.EndElement:
			return c, nil
		}
	}
}

// pick keeps the first value, or a lang="ru" one over a value that is not.
type pick struct {
	v  string
	ru bool
}

func (k *pick) offer(v, lang string) {
	if v == "" {
		return
	}
	ru := strings.EqualFold(lang, "ru") || strings.HasPrefix(strings.ToLower(lang), "ru-")
	if k.v == "" || (ru && !k.ru) {
		k.v, k.ru = v, ru
	}
}

func (p *xmltvParser) programme(start xml.StartElement, keep func(string) bool, from, to time.Time) (XMLProgramme, bool, error) {
	var pr XMLProgramme
	pr.Channel, _ = attr(start, "channel")
	if keep != nil && !keep(pr.Channel) {
		return pr, false, p.skip()
	}
	s, _ := attr(start, "start")
	var err error
	if pr.Start, err = ParseXMLTVTime(s); err != nil {
		return pr, false, p.skip()
	}
	if s, ok := attr(start, "stop"); ok {
		pr.Stop, _ = ParseXMLTVTime(s)
	}
	// Overlaps [from, to); without a stop, starts in it.
	inside := pr.Start.Before(to) && pr.Stop.After(from)
	if pr.Stop.IsZero() {
		inside = pr.Start.Before(to) && !pr.Start.Before(from)
	}
	if !inside {
		return pr, false, p.skip()
	}
	var title, sub, desc, cat pick
	for {
		tok, err := p.d.RawToken()
		if err != nil {
			if err == io.EOF {
				return pr, false, io.ErrUnexpectedEOF
			}
			return pr, false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			lang, _ := attr(t, "lang")
			var dst *pick
			switch t.Name.Local {
			case "title":
				dst = &title
			case "sub-title":
				dst = &sub
			case "desc":
				dst = &desc
			case "category":
				dst = &cat
			case "icon":
				if pr.Icon == "" {
					pr.Icon, _ = attr(t, "src")
				}
			}
			if dst == nil {
				if err := p.skip(); err != nil {
					return pr, false, err
				}
				continue
			}
			v, err := p.textOf()
			if err != nil {
				return pr, false, err
			}
			dst.offer(v, lang)
		case xml.EndElement:
			pr.Title, pr.SubTitle, pr.Desc, pr.Category = title.v, sub.v, desc.v, cat.v
			return pr, true, nil
		}
	}
}

// ParseXMLTVTime reads an XMLTV time: "YYYYMMDDhhmm[ss][ +hhmm|Z]" (no
// offset: UTC).
func ParseXMLTVTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	digits, rest := s[:i], strings.TrimSpace(s[i:])
	switch len(digits) {
	case 12:
		digits += "00"
	case 14:
	default:
		return time.Time{}, fmt.Errorf("iptv: bad XMLTV time %q", s)
	}
	n := func(a, b int) int {
		v := 0
		for _, c := range digits[a:b] {
			v = v*10 + int(c-'0')
		}
		return v
	}
	loc := time.UTC
	switch {
	case rest == "" || rest == "Z" || rest == "z":
	case len(rest) == 5 && (rest[0] == '+' || rest[0] == '-'):
		off := 0
		for _, c := range rest[1:] {
			if c < '0' || c > '9' {
				return time.Time{}, fmt.Errorf("iptv: bad XMLTV time %q", s)
			}
			off = off*10 + int(c-'0')
		}
		secs := (off/100)*3600 + (off%100)*60
		if rest[0] == '-' {
			secs = -secs
		}
		loc = time.FixedZone("", secs)
	default:
		return time.Time{}, fmt.Errorf("iptv: bad XMLTV time %q", s)
	}
	return time.Date(n(0, 4), time.Month(n(4, 6)), n(6, 8), n(8, 10), n(10, 12), n(12, 14), 0, loc).UTC(), nil
}

func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "windows-1251", "cp1251":
		return charmap.Windows1251.NewDecoder().Reader(in), nil
	case "koi8-r":
		return charmap.KOI8R.NewDecoder().Reader(in), nil
	case "iso-8859-1", "latin1":
		return charmap.ISO8859_1.NewDecoder().Reader(in), nil
	}
	return nil, fmt.Errorf("iptv: guide charset %q", label)
}

// parseFileForBench parses a guide file (.xml or gzip) as a refresh would,
// keeping the programmes of the first keepN channels in a window around
// now; for the local load test.
func parseFileForBench(path string, keepN int) (channels, programmes, kept int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64*1024)
	var r io.Reader = br
	if m, _ := br.Peek(2); len(m) == 2 && m[0] == 0x1f && m[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return 0, 0, 0, err
		}
		r = zr
	}
	want := map[string]bool{}
	now := time.Now()
	err = ParseXMLTV(r, func(id string) bool { programmes++; return want[id] },
		now.Add(-24*time.Hour), now.Add(7*24*time.Hour),
		func(c XMLChannel) {
			channels++
			if len(want) < keepN {
				want[c.ID] = true
			}
		},
		func(XMLProgramme) { kept++ })
	return channels, programmes, kept, err
}
