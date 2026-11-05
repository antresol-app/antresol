// Package fb2epub converts an FB2 book to EPUB with the standard library: the
// server has no Calibre, and a reader service that takes EPUB only (Send to
// Kindle) does not take the FB2 most Russian libraries hold.
//
// The whole text is held in memory as a tree, so a picture or a link is only
// written when what it points at exists; dangling references are what makes
// Amazon's converter reject a book.
package fb2epub

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
)

// ErrNotFB2: the input is not an FB2 book with a text (or nests absurdly).
var ErrNotFB2 = errors.New("fb2epub: not an FB2 book")

// Options of Convert.
type Options struct {
	// Title names a book whose file names none.
	Title string
}

// maxDepth bounds the nesting a book may have (the renderer recurses).
const maxDepth = 256

// node is an element of the FB2 tree: Name "" is a text.
type node struct {
	name, text string
	attrs      map[string]string
	kids       []*node
}

func (n *node) attr(k string) string { return n.attrs[k] }

func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

// plain is the text under n, whitespace collapsed.
func (n *node) plain() string {
	var b strings.Builder
	var walk func(*node)
	walk = func(x *node) {
		if x.name == "" {
			b.WriteString(x.text)
			return
		}
		for _, k := range x.kids {
			walk(k)
			if k.name == "p" || k.name == "v" {
				b.WriteByte(' ')
			}
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

type picture struct {
	data       []byte
	ext, media string
}

type book struct {
	desc   *node
	bodies []*node
	pics   map[string]*picture // by the FB2 binary id
}

// parse reads the tree of an FB2; pictures are decoded on the way.
func parse(r io.Reader) (*book, error) {
	d, err := bookmeta.NewFB2Decoder(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFB2, err)
	}
	d.Strict = false
	d.Entity = xml.HTMLEntity
	b := &book{pics: map[string]*picture{}}
	root := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotFB2, err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !root {
			if se.Name.Local != "FictionBook" {
				return nil, fmt.Errorf("%w: root <%s>", ErrNotFB2, se.Name.Local)
			}
			root = true
			continue
		}
		switch se.Name.Local {
		case "binary":
			if err := b.readBinary(d, se); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrNotFB2, err)
			}
		case "description", "body":
			n, err := readNode(d, se, 1)
			if err != nil {
				return nil, err
			}
			if se.Name.Local == "body" {
				b.bodies = append(b.bodies, n)
			} else if b.desc == nil {
				b.desc = n
			}
		default:
			if err := d.Skip(); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrNotFB2, err)
			}
		}
	}
	if !root || len(b.bodies) == 0 {
		return nil, fmt.Errorf("%w: no text", ErrNotFB2)
	}
	return b, nil
}

func readNode(d *xml.Decoder, se xml.StartElement, depth int) (*node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nested too deep", ErrNotFB2)
	}
	n := &node{name: se.Name.Local}
	if len(se.Attr) > 0 {
		n.attrs = make(map[string]string, len(se.Attr))
		for _, a := range se.Attr {
			n.attrs[a.Name.Local] = a.Value // l:href and xlink:href alike
		}
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotFB2, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			k, err := readNode(d, t, depth+1)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, k)
		case xml.CharData:
			n.kids = append(n.kids, &node{text: string(t)})
		case xml.EndElement:
			return n, nil
		}
	}
}

func (b *book) readBinary(d *xml.Decoder, se xml.StartElement) error {
	var id string
	for _, a := range se.Attr {
		if a.Name.Local == "id" {
			id = a.Value
		}
	}
	var enc bytes.Buffer
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			for _, c := range t {
				if !unicode.IsSpace(rune(c)) {
					enc.WriteByte(c)
				}
			}
		}
	}
	data := make([]byte, base64.StdEncoding.DecodedLen(enc.Len()))
	n, err := base64.StdEncoding.Decode(data, enc.Bytes())
	if err != nil || n == 0 || id == "" {
		return nil // a broken picture must not lose the book
	}
	data = data[:n]
	p := &picture{data: data}
	// by content: the declared type of an FB2 picture is often wrong
	switch http.DetectContentType(data) {
	case "image/jpeg":
		p.ext, p.media = "jpg", "image/jpeg"
	case "image/png":
		p.ext, p.media = "png", "image/png"
	case "image/gif":
		p.ext, p.media = "gif", "image/gif"
	default:
		return nil // not an EPUB core type
	}
	b.pics[id] = p
	return nil
}

// --- metadata ---

type meta struct {
	title, lang, series, seriesIdx string
	authors                        []string
	cover                          string // FB2 binary id
}

var reLang = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

func (b *book) meta(opt Options) meta {
	m := meta{lang: "ru"}
	var ti *node
	if b.desc != nil {
		ti = b.desc.child("title-info")
	}
	if ti != nil {
		if t := ti.child("book-title"); t != nil {
			m.title = t.plain()
		}
		if l := ti.child("lang"); l != nil && reLang.MatchString(l.plain()) {
			m.lang = l.plain()
		}
		for _, k := range ti.kids {
			switch k.name {
			case "author":
				var parts []string
				for _, f := range []string{"first-name", "middle-name", "last-name"} {
					if c := k.child(f); c != nil && c.plain() != "" {
						parts = append(parts, c.plain())
					}
				}
				if len(parts) == 0 {
					if c := k.child("nickname"); c != nil && c.plain() != "" {
						parts = append(parts, c.plain())
					}
				}
				if len(parts) > 0 {
					m.authors = append(m.authors, strings.Join(parts, " "))
				}
			case "sequence":
				if m.series == "" {
					m.series, m.seriesIdx = strings.TrimSpace(k.attr("name")), strings.TrimSpace(k.attr("number"))
				}
			case "coverpage":
				if img := k.child("image"); img != nil {
					id := strings.TrimPrefix(img.attr("href"), "#")
					if _, ok := b.pics[id]; ok {
						m.cover = id
					}
				}
			}
		}
	}
	if m.title == "" {
		m.title = strings.TrimSpace(opt.Title)
	}
	if m.title == "" {
		m.title = "Без названия"
	}
	return m
}

// --- rendering ---

// tocEntry is a titled section; a kid of its parent in the table of contents.
type tocEntry struct {
	label, href string
	kids        []*tocEntry
}

type chapter struct {
	file  string
	nodes []*node
	notes bool
}

type renderer struct {
	picFile map[string]string // binary id -> images/x.ext
	ids     map[string]string // FB2 id -> "file#anchor"
	used    map[string]bool   // anchors handed out
	auto    int
	cur     *strings.Builder
}

var reNotXMLName = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// assign gives the id of n an anchor in file, once per book (a later node
// with the same id loses it); ids are kept when they already are valid XML
// names.
func (r *renderer) assign(n *node, file string) {
	if n.name == "" {
		return
	}
	if id := n.attr("id"); id != "" {
		if _, dup := r.ids[id]; !dup {
			a := reNotXMLName.ReplaceAllString(id, "_")
			if c := a[0]; c == '-' || c == '.' || (c >= '0' && c <= '9') {
				a = "x" + a
			}
			for r.used[a] {
				a += "_"
			}
			r.used[a] = true
			r.ids[id] = file + "#" + a
		} else {
			delete(n.attrs, "id")
		}
	}
	for _, k := range n.kids {
		r.assign(k, file)
	}
}

// anchor of n as rendered, "" when it has none.
func (r *renderer) anchorAttr(n *node) string {
	id := n.attr("id")
	if id == "" {
		return ""
	}
	t := r.ids[id]
	return ` id="` + t[strings.IndexByte(t, '#')+1:] + `"`
}

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	// EscapeText writes &#34; &#39; &#xA; ...: valid, but keep text readable
	out := strings.NewReplacer("&#34;", `"`, "&#39;", "'", "&#xA;", "\n", "&#x9;", "\t", "&#xD;", "").Replace(b.String())
	return out
}

func escAttr(s string) string {
	return strings.ReplaceAll(esc(s), `"`, "&#34;")
}

// inline writes the content of a paragraph-like element.
func (r *renderer) inline(n *node) {
	for _, k := range n.kids {
		switch k.name {
		case "":
			r.cur.WriteString(esc(k.text))
		case "emphasis":
			r.wrap("em", k)
		case "strong":
			r.wrap("strong", k)
		case "strikethrough":
			r.wrap("del", k)
		case "sub":
			r.wrap("sub", k)
		case "sup":
			r.wrap("sup", k)
		case "code":
			r.wrap("code", k)
		case "a":
			r.link(k)
		case "image":
			r.image(k)
		default: // style and anything unknown: only its text
			r.inline(k)
		}
	}
}

func (r *renderer) wrap(tag string, n *node) {
	r.cur.WriteString("<" + tag + ">")
	r.inline(n)
	r.cur.WriteString("</" + tag + ">")
}

func (r *renderer) link(n *node) {
	href := n.attr("href")
	switch {
	case strings.HasPrefix(href, "#"):
		if t, ok := r.ids[href[1:]]; ok {
			if n.attr("type") == "note" {
				r.cur.WriteString(`<sup><a epub:type="noteref" href="` + escAttr(t) + `">`)
				r.inline(n)
				r.cur.WriteString(`</a></sup>`)
				return
			}
			r.cur.WriteString(`<a href="` + escAttr(t) + `">`)
			r.inline(n)
			r.cur.WriteString(`</a>`)
			return
		}
	case strings.HasPrefix(href, "http://"), strings.HasPrefix(href, "https://"), strings.HasPrefix(href, "mailto:"):
		r.cur.WriteString(`<a href="` + escAttr(href) + `">`)
		r.inline(n)
		r.cur.WriteString(`</a>`)
		return
	}
	r.inline(n)
}

func (r *renderer) image(n *node) {
	if f, ok := r.picFile[strings.TrimPrefix(n.attr("href"), "#")]; ok {
		r.cur.WriteString(`<img src="` + f + `" alt="` + escAttr(n.attr("alt")) + `"/>`)
	}
}

func (r *renderer) para(tag, class string, n *node) {
	r.cur.WriteString("<" + tag)
	if class != "" {
		r.cur.WriteString(` class="` + class + `"`)
	}
	r.cur.WriteString(r.anchorAttr(n) + ">")
	r.inline(n)
	r.cur.WriteString("</" + tag + ">\n")
}

func (r *renderer) box(tag, class string, n *node, depth int) {
	r.cur.WriteString("<" + tag + ` class="` + class + `"` + r.anchorAttr(n) + ">\n")
	r.blocks(n, depth)
	r.cur.WriteString("</" + tag + ">\n")
}

func headingLevel(depth int) string { return strconv.Itoa(min(max(depth, 1), 6)) }

// title writes a <title>: each paragraph one line of a heading.
func (r *renderer) title(n *node, depth int) {
	tag := "h" + headingLevel(depth)
	r.cur.WriteString("<" + tag + r.anchorAttr(n) + ">")
	first := true
	for _, k := range n.kids {
		if k.name == "" {
			if s := strings.TrimSpace(k.text); s != "" {
				if !first {
					r.cur.WriteString("<br/>")
				}
				first = false
				r.cur.WriteString(esc(s))
			}
			continue
		}
		if k.name == "empty-line" {
			continue
		}
		if !first {
			r.cur.WriteString("<br/>")
		}
		first = false
		r.inline(k)
	}
	if first {
		r.cur.WriteString("&#160;") // an empty heading is invalid only in spirit, not worth losing its anchor
	}
	r.cur.WriteString("</" + tag + ">\n")
}

// table writes a <table>, its cells' content as inline text.
func (r *renderer) table(n *node) {
	r.cur.WriteString(`<table` + r.anchorAttr(n) + ">\n")
	for _, tr := range n.kids {
		if tr.name != "tr" {
			continue
		}
		r.cur.WriteString("<tr>")
		for _, c := range tr.kids {
			if c.name != "td" && c.name != "th" {
				continue
			}
			r.cur.WriteString("<" + c.name)
			for _, a := range []string{"colspan", "rowspan"} {
				if v := c.attr(a); v != "" {
					if i, err := strconv.Atoi(v); err == nil && i > 0 {
						r.cur.WriteString(" " + a + `="` + strconv.Itoa(i) + `"`)
					}
				}
			}
			r.cur.WriteString(r.anchorAttr(c) + ">")
			r.inline(c)
			r.cur.WriteString("</" + c.name + ">")
		}
		r.cur.WriteString("</tr>\n")
	}
	r.cur.WriteString("</table>\n")
}

// blocks writes the kids of a flow element; depth is the heading level of a
// title among them.
func (r *renderer) blocks(n *node, depth int) {
	for _, k := range n.kids {
		switch k.name {
		case "":
			if s := strings.TrimSpace(k.text); s != "" { // stray text in a section
				r.cur.WriteString("<p>" + esc(s) + "</p>\n")
			}
		case "title":
			r.title(k, depth)
		case "p":
			r.para("p", "", k)
		case "subtitle":
			r.para("p", "subtitle", k)
		case "v":
			r.para("p", "verse", k)
		case "text-author":
			r.para("p", "author", k)
		case "date":
			r.para("p", "date", k)
		case "empty-line":
			r.cur.WriteString(`<p class="empty">&#160;</p>` + "\n")
		case "image":
			_, exists := r.picFile[strings.TrimPrefix(k.attr("href"), "#")]
			if !exists && k.attr("id") == "" {
				continue // no such picture: no empty box
			}
			r.cur.WriteString(`<div class="image"` + r.anchorAttr(k) + ">")
			r.image(k)
			r.cur.WriteString("</div>\n")
		case "epigraph":
			r.box("blockquote", "epigraph", k, depth+1)
		case "cite":
			r.box("blockquote", "cite", k, depth+1)
		case "annotation":
			r.box("div", "annotation", k, depth+1)
		case "poem":
			r.box("div", "poem", k, depth+1)
		case "stanza":
			r.box("div", "stanza", k, depth+1)
		case "section":
			r.box("div", "section", k, depth+1)
		case "table":
			r.table(k)
		default:
			r.blocks(k, depth)
		}
	}
}

const css = `body { margin: 0; }
h1, h2, h3, h4, h5, h6 { text-align: center; }
p { margin: 0; text-indent: 1.2em; }
p.empty { text-indent: 0; }
p.subtitle { font-weight: bold; text-align: center; text-indent: 0; margin: 0.5em 0; }
p.author { font-style: italic; text-align: right; text-indent: 0; }
p.date { text-align: right; text-indent: 0; }
p.verse { text-indent: 0; }
blockquote { margin: 0.5em 1.5em; }
blockquote.epigraph { font-style: italic; margin-left: 30%; margin-right: 0; }
div.poem { margin: 0.7em 1.5em; }
div.stanza { margin: 0.5em 0; }
div.image { text-align: center; margin: 0.5em 0; }
img { max-width: 100%; }
table { border-collapse: collapse; margin: 0.5em auto; }
td, th { border: 1px solid; padding: 0.2em 0.4em; }
`

func page(title, lang, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="` + lang + `" lang="` + lang + `">
<head><meta charset="UTF-8"/><title>` + esc(title) + `</title><link rel="stylesheet" type="text/css" href="style.css"/></head>
<body>
` + body + `</body>
</html>
`
}

// isNotes: a second-or-later body named as notes or comments.
func isNotes(b *node) bool { return b.attr("name") != "" }

// chapters splits the bodies into files: a file for what precedes the first
// section of a body, a file per top-level section.
func chapters(bodies []*node) []*chapter {
	var out []*chapter
	add := func(nodes []*node, notes bool) {
		out = append(out, &chapter{file: fmt.Sprintf("text%04d.xhtml", len(out)+1), nodes: nodes, notes: notes})
	}
	for i, b := range bodies {
		notes := i > 0 && isNotes(b)
		if notes { // notes stay together: one file per set of notes
			add(b.kids, true)
			continue
		}
		var front []*node
		flush := func() {
			keep := false
			for _, n := range front {
				if n.name != "" || strings.TrimSpace(n.text) != "" {
					keep = true
				}
			}
			if keep {
				add(front, false)
			}
			front = nil
		}
		for _, k := range b.kids {
			if k.name == "section" {
				flush()
				add([]*node{k}, false)
			} else {
				front = append(front, k)
			}
		}
		flush()
	}
	return out
}

// toc collects the titled sections under n (a section or a body).
func (r *renderer) toc(n *node, file string) []*tocEntry {
	var out []*tocEntry
	for _, k := range n.kids {
		if k.name != "section" {
			continue
		}
		kids := r.toc(k, file)
		t := k.child("title")
		if t == nil || t.plain() == "" {
			out = append(out, kids...) // lift the titled ones up
			continue
		}
		if k.attr("id") == "" {
			r.auto++
			if k.attrs == nil {
				k.attrs = map[string]string{}
			}
			k.attrs["id"] = "toc-" + strconv.Itoa(r.auto)
			r.assign(&node{name: "section", attrs: map[string]string{"id": k.attrs["id"]}}, file)
		}
		out = append(out, &tocEntry{label: t.plain(), href: r.ids[k.attr("id")], kids: kids})
	}
	return out
}

// tocOf is the table of contents of the chapters. It is never empty: EPUB
// requires an entry, the book itself when nothing has a title.
func (r *renderer) tocOf(chs []*chapter, m meta) []*tocEntry {
	var out []*tocEntry
	for _, c := range chs {
		wrapper := &node{name: "body", kids: c.nodes}
		if c.notes {
			// a set of notes is one entry: its title, not every note
			label := ""
			if t := wrapper.child("title"); t != nil {
				label = t.plain()
			}
			if label == "" {
				label = "Примечания"
			}
			out = append(out, &tocEntry{label: label, href: c.file})
			continue
		}
		out = append(out, r.toc(wrapper, c.file)...)
	}
	if len(out) == 0 {
		out = append(out, &tocEntry{label: m.title, href: chs[0].file})
	}
	return out
}

func writeNav(b *strings.Builder, es []*tocEntry) {
	b.WriteString("<ol>\n")
	for _, e := range es {
		b.WriteString(`<li><a href="` + escAttr(e.href) + `">` + esc(e.label) + `</a>`)
		if len(e.kids) > 0 {
			writeNav(b, e.kids)
		}
		b.WriteString("</li>\n")
	}
	b.WriteString("</ol>\n")
}

func writeNCX(b *strings.Builder, es []*tocEntry, seq *int) {
	for _, e := range es {
		*seq++
		fmt.Fprintf(b, `<navPoint id="nav%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="%s"/>`+"\n", *seq, *seq, esc(e.label), escAttr(e.href))
		writeNCX(b, e.kids, seq)
		b.WriteString("</navPoint>\n")
	}
}

func tocDepth(es []*tocEntry) int {
	d := 0
	for _, e := range es {
		d = max(d, 1+tocDepth(e.kids))
	}
	return d
}

// Convert writes the EPUB of the FB2 text in r to w. A broken or absent
// picture or link is dropped; ErrNotFB2 means there is no book to write.
func Convert(r io.Reader, w io.Writer, opt Options) error {
	b, err := parse(r)
	if err != nil {
		return err
	}
	m := b.meta(opt)
	chs := chapters(b.bodies)
	if len(chs) == 0 {
		return fmt.Errorf("%w: empty text", ErrNotFB2)
	}

	rd := &renderer{picFile: map[string]string{}, ids: map[string]string{}, used: map[string]bool{}}
	// sorted: the names must not depend on map order, so two runs give one
	// archive
	picIDs := make([]string, 0, len(b.pics))
	for id := range b.pics {
		picIDs = append(picIDs, id)
	}
	sort.Strings(picIDs)
	for i, id := range picIDs {
		rd.picFile[id] = fmt.Sprintf("images/img%04d.%s", i+1, b.pics[id].ext)
	}
	for _, c := range chs {
		for _, n := range c.nodes {
			rd.assign(n, c.file)
		}
	}
	toc := rd.tocOf(chs, m)

	zw := zip.NewWriter(w)
	if err := writeStored(zw, "mimetype", "application/epub+zip"); err != nil {
		return err
	}
	if err := writeFile(zw, "META-INF/container.xml", containerXML); err != nil {
		return err
	}
	if err := writeFile(zw, "OEBPS/style.css", css); err != nil {
		return err
	}
	if m.cover != "" {
		body := `<div class="image"><img src="` + rd.picFile[m.cover] + `" alt="` + escAttr(m.title) + `"/></div>` + "\n"
		if err := writeFile(zw, "OEBPS/cover.xhtml", page(m.title, m.lang, body)); err != nil {
			return err
		}
	}
	for _, c := range chs {
		var sb strings.Builder
		rd.cur = &sb
		rd.blocks(&node{name: "body", kids: c.nodes}, 1)
		if err := writeFile(zw, "OEBPS/"+c.file, page(m.title, m.lang, sb.String())); err != nil {
			return err
		}
	}
	for _, id := range picIDs {
		if err := writeFile(zw, "OEBPS/"+rd.picFile[id], string(b.pics[id].data)); err != nil {
			return err
		}
	}
	var nav strings.Builder
	nav.WriteString(`<nav epub:type="toc" id="toc"><h1>` + esc(m.title) + "</h1>\n")
	writeNav(&nav, toc)
	nav.WriteString("</nav>\n")
	if err := writeFile(zw, "OEBPS/nav.xhtml", page(m.title, m.lang, nav.String())); err != nil {
		return err
	}
	uid := bookID(m, b)
	var points strings.Builder
	seq := 0
	writeNCX(&points, toc, &seq)
	ncx := `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head><meta name="dtb:uid" content="` + uid + `"/><meta name="dtb:depth" content="` + strconv.Itoa(tocDepth(toc)) + `"/><meta name="dtb:totalPageCount" content="0"/><meta name="dtb:maxPageNumber" content="0"/></head>
<docTitle><text>` + esc(m.title) + `</text></docTitle>
<navMap>
` + points.String() + `</navMap>
</ncx>
`
	if err := writeFile(zw, "OEBPS/toc.ncx", ncx); err != nil {
		return err
	}
	if err := writeFile(zw, "OEBPS/content.opf", opf(m, uid, chs, picIDs, b, rd)); err != nil {
		return err
	}
	return zw.Close()
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
`

func opf(m meta, uid string, chs []*chapter, picIDs []string, b *book, rd *renderer) string {
	var s strings.Builder
	s.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="book-id">` + uid + `</dc:identifier>
<dc:title>` + esc(m.title) + `</dc:title>
<dc:language>` + m.lang + `</dc:language>
`)
	for _, a := range m.authors {
		s.WriteString(`<dc:creator>` + esc(a) + `</dc:creator>` + "\n")
	}
	s.WriteString(`<meta property="dcterms:modified">` + time.Now().UTC().Format("2006-01-02T15:04:05Z") + `</meta>` + "\n")
	if m.series != "" {
		s.WriteString(`<meta property="belongs-to-collection" id="series">` + esc(m.series) + `</meta>` + "\n")
		s.WriteString(`<meta refines="#series" property="collection-type">series</meta>` + "\n")
		if m.seriesIdx != "" {
			s.WriteString(`<meta refines="#series" property="group-position">` + esc(m.seriesIdx) + `</meta>` + "\n")
		}
		s.WriteString(`<meta name="calibre:series" content="` + escAttr(m.series) + `"/>` + "\n")
		if m.seriesIdx != "" {
			s.WriteString(`<meta name="calibre:series_index" content="` + escAttr(m.seriesIdx) + `"/>` + "\n")
		}
	}
	if m.cover != "" {
		s.WriteString(`<meta name="cover" content="cover-image"/>` + "\n")
	}
	s.WriteString("</metadata>\n<manifest>\n")
	s.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>` + "\n")
	s.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` + "\n")
	s.WriteString(`<item id="css" href="style.css" media-type="text/css"/>` + "\n")
	if m.cover != "" {
		s.WriteString(`<item id="cover-page" href="cover.xhtml" media-type="application/xhtml+xml"/>` + "\n")
	}
	for i, c := range chs {
		fmt.Fprintf(&s, `<item id="ch%d" href="%s" media-type="application/xhtml+xml"/>`+"\n", i+1, c.file)
	}
	for i, id := range picIDs {
		p := b.pics[id]
		item, props := "img"+strconv.Itoa(i+1), ""
		if id == m.cover {
			item, props = "cover-image", ` properties="cover-image"`
		}
		fmt.Fprintf(&s, `<item id="%s" href="%s" media-type="%s"%s/>`+"\n", item, rd.picFile[id], p.media, props)
	}
	s.WriteString("</manifest>\n" + `<spine toc="ncx">` + "\n")
	if m.cover != "" {
		s.WriteString(`<itemref idref="cover-page"/>` + "\n")
	}
	for i := range chs {
		fmt.Fprintf(&s, `<itemref idref="ch%d"/>`+"\n", i+1)
	}
	s.WriteString("</spine>\n</package>\n")
	return s.String()
}

// bookID is a stable name of the book: sending it again replaces nothing but
// keeps one identity.
func bookID(m meta, b *book) string {
	h := sha1.New()
	io.WriteString(h, m.title)
	for _, a := range m.authors {
		io.WriteString(h, "\x00"+a)
	}
	for _, body := range b.bodies {
		io.WriteString(h, "\x00"+body.plain())
	}
	s := h.Sum(nil)
	s[6] = s[6]&0x0f | 0x50
	s[8] = s[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", s[0:4], s[4:6], s[6:8], s[8:10], s[10:16])
}

// writeStored writes the mimetype entry as EPUB readers expect it: stored,
// its sizes in the local header (no data descriptor).
func writeStored(zw *zip.Writer, name, content string) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store, Modified: time.Now().UTC(),
		CRC32: crc32.ChecksumIEEE([]byte(content)), CompressedSize64: uint64(len(content)), UncompressedSize64: uint64(len(content))}
	f, err := zw.CreateRaw(h)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, content)
	return err
}

func writeFile(zw *zip.Writer, name, content string) error {
	f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now().UTC()})
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, content)
	return err
}
