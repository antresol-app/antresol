package bookmeta

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

type epubContainer struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfText struct {
	Value  string `xml:",chardata"`
	ID     string `xml:"id,attr"`
	Role   string `xml:"role,attr"`   // opf:role (EPUB 2)
	Scheme string `xml:"scheme,attr"` // opf:scheme (EPUB 2)
}

type opfMeta struct {
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Refines  string `xml:"refines,attr"`
	ID       string `xml:"id,attr"`
	Value    string `xml:",chardata"`
}

type opfPackage struct {
	Metadata struct {
		Titles       []opfText `xml:"title"`
		Creators     []opfText `xml:"creator"`
		Languages    []string  `xml:"language"`
		Descriptions []string  `xml:"description"`
		Identifiers  []opfText `xml:"identifier"`
		Dates        []string  `xml:"date"`
		Publishers   []string  `xml:"publisher"`
		Metas        []opfMeta `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Guide struct {
		Refs []struct {
			Type string `xml:"type,attr"`
			Href string `xml:"href,attr"`
		} `xml:"reference"`
	} `xml:"guide"`
}

// newXMLDecoder is lenient: HTML entities (&nbsp; in FB2 files) and minor
// syntax slips are accepted, the declared charset is honoured.
func newXMLDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charsetReader
	return d
}

// zipFile finds a member by its exact name, then ignoring case.
func zipFile(z *zip.Reader, name string) *zip.File {
	for _, f := range z.File {
		if f.Name == name {
			return f
		}
	}
	for _, f := range z.File {
		if strings.EqualFold(f.Name, name) {
			return f
		}
	}
	return nil
}

func readZipFile(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", f.Name, limit)
	}
	return b, nil
}

func readEPUB(p string) (Meta, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return Meta{}, err
	}
	defer zr.Close()
	return parseEPUB(&zr.Reader)
}

var reISBN = regexp.MustCompile(`^(?:97[89][- ]?)?(?:\d[- ]?){9}[\dXx]$`)

// validISBN checks the ISBN-10 or ISBN-13 check digit (hyphens and spaces ignored).
func validISBN(v string) bool {
	var d []int
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			d = append(d, int(c-'0'))
		case (c == 'X' || c == 'x') && len(d) == 9:
			d = append(d, 10)
		case c == '-' || c == ' ':
		default:
			return false
		}
	}
	sum := 0
	switch len(d) {
	case 10:
		for i, x := range d {
			sum += (10 - i) * x
		}
		return sum%11 == 0
	case 13:
		for i, x := range d {
			if x == 10 {
				return false
			}
			sum += x * (1 + 2*(i%2))
		}
		return sum%10 == 0
	}
	return false
}

func parseEPUB(z *zip.Reader) (Meta, error) {
	cf := zipFile(z, "META-INF/container.xml")
	if cf == nil {
		return Meta{}, errors.New("epub: no META-INF/container.xml")
	}
	raw, err := readZipFile(cf, maxXML)
	if err != nil {
		return Meta{}, err
	}
	var c epubContainer
	if err := newXMLDecoder(strings.NewReader(string(raw))).Decode(&c); err != nil {
		return Meta{}, fmt.Errorf("epub: container.xml: %w", err)
	}
	opfPath := ""
	for _, r := range c.Rootfiles {
		if r.FullPath != "" && (opfPath == "" || r.MediaType == "application/oebps-package+xml") {
			opfPath = r.FullPath
			if r.MediaType == "application/oebps-package+xml" {
				break
			}
		}
	}
	if opfPath == "" {
		return Meta{}, errors.New("epub: container.xml names no rootfile")
	}
	of := zipFile(z, opfPath)
	if of == nil {
		return Meta{}, fmt.Errorf("epub: no %s", opfPath)
	}
	if raw, err = readZipFile(of, maxXML); err != nil {
		return Meta{}, err
	}
	var pkg opfPackage
	if err := newXMLDecoder(strings.NewReader(string(raw))).Decode(&pkg); err != nil {
		return Meta{}, fmt.Errorf("epub: %s: %w", opfPath, err)
	}
	md := pkg.Metadata
	var m Meta

	// EPUB 3 refinements: "#id" → property → value
	refines := map[string]map[string]string{}
	for _, mt := range md.Metas {
		if mt.Refines == "" || mt.Property == "" {
			continue
		}
		id := strings.TrimPrefix(strings.TrimSpace(mt.Refines), "#")
		if refines[id] == nil {
			refines[id] = map[string]string{}
		}
		if _, ok := refines[id][mt.Property]; !ok {
			refines[id][mt.Property] = clean(mt.Value)
		}
	}

	for _, t := range md.Titles {
		if v := clean(t.Value); v != "" {
			typ := refines[t.ID]["title-type"]
			if m.Title == "" || typ == "main" {
				m.Title = v
			}
			if typ == "main" {
				break
			}
		}
	}
	seenAuthor := map[string]bool{}
	for _, cr := range md.Creators {
		role := strings.ToLower(strings.TrimSpace(cr.Role))
		if role == "" {
			role = strings.ToLower(refines[cr.ID]["role"])
		}
		v := clean(cr.Value)
		if v == "" || (role != "" && role != "aut") || seenAuthor[strings.ToLower(v)] {
			continue
		}
		seenAuthor[strings.ToLower(v)] = true
		m.Authors = append(m.Authors, v)
	}
	for _, l := range md.Languages {
		if m.Lang = clean(l); m.Lang != "" {
			break
		}
	}
	for _, d := range md.Descriptions {
		if m.Description = plainText(d); m.Description != "" {
			break
		}
	}
	for _, id := range md.Identifiers {
		v := clean(id.Value)
		low := strings.ToLower(v)
		switch {
		case strings.HasPrefix(low, "urn:isbn:"):
			v = strings.TrimSpace(v[len("urn:isbn:"):])
		case strings.HasPrefix(low, "isbn:"):
			v = strings.TrimSpace(v[len("isbn:"):])
		case strings.EqualFold(id.Scheme, "isbn"), strings.EqualFold(refines[id.ID]["identifier-type"], "15"):
		case reISBN.MatchString(v) && validISBN(v): // bare: only with a valid check digit
		default:
			continue
		}
		if v != "" {
			m.ISBN = v
			break
		}
	}
	for _, d := range md.Dates {
		if m.Year = yearOf(d); m.Year != 0 {
			break
		}
	}
	for _, p := range md.Publishers {
		if m.Publisher = clean(p); m.Publisher != "" {
			break
		}
	}

	coverID := ""
	for _, mt := range md.Metas {
		switch strings.ToLower(mt.Name) {
		case "calibre:series":
			if m.Series == "" {
				m.Series = clean(mt.Content)
			}
		case "calibre:series_index":
			if m.SeriesIndex == 0 {
				m.SeriesIndex = parseIndex(mt.Content)
			}
		case "cover":
			if coverID == "" {
				coverID = strings.TrimSpace(mt.Content)
			}
		}
	}
	if m.Series == "" {
		for _, mt := range md.Metas {
			if mt.Property != "belongs-to-collection" || mt.Refines != "" {
				continue
			}
			if typ := refines[mt.ID]["collection-type"]; typ != "" && typ != "series" {
				continue
			}
			if m.Series = clean(mt.Value); m.Series != "" {
				m.SeriesIndex = parseIndex(refines[mt.ID]["group-position"])
				break
			}
		}
	}

	// cover: <meta name="cover"> → manifest item (by id, or an href some
	// EPUB 2 writers put there), else properties="cover-image", else the
	// guide's cover reference (an image, or the first picture of its page)
	opfDir := path.Dir(opfPath)
	href, mime := "", ""
	if coverID != "" {
		for _, it := range pkg.Manifest.Items {
			if it.ID == coverID {
				href, mime = it.Href, it.MediaType
				break
			}
		}
		if href == "" {
			for _, it := range pkg.Manifest.Items {
				if it.Href == coverID {
					href, mime = it.Href, it.MediaType
					break
				}
			}
		}
	}
	if href == "" {
		for _, it := range pkg.Manifest.Items {
			if hasWord(it.Properties, "cover-image") {
				href, mime = it.Href, it.MediaType
				break
			}
		}
	}
	if href == "" {
		for _, ref := range pkg.Guide.Refs {
			if !strings.EqualFold(ref.Type, "cover") || ref.Href == "" {
				continue
			}
			if p := zipPath(opfDir, ref.Href); imageType("", nil, p) != "" {
				href = ref.Href
			} else if f := zipFile(z, p); f != nil {
				if page, err := readZipFile(f, maxXML); err == nil {
					if mm := reImgSrc.FindSubmatch(page); mm != nil {
						href = path.Join(path.Dir(stripFragment(ref.Href)), string(mm[1]))
					}
				}
			}
			break
		}
	}
	if href != "" {
		p := zipPath(opfDir, href)
		if f := zipFile(z, p); f != nil {
			if data, err := readZipFile(f, maxCover); err == nil && len(data) > 0 {
				if t := imageType(mime, data, p); t != "" {
					m.Cover, m.CoverType = data, t
				}
			}
		}
	}
	return m, nil
}

// reImgSrc finds the first picture of an XHTML page: <img src> or SVG <image xlink:href>.
var reImgSrc = regexp.MustCompile(`(?is)<(?:img\b[^>]*?\bsrc|image\b[^>]*?\bhref)\s*=\s*["']([^"']+)["']`)

func stripFragment(href string) string {
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		return href[:i]
	}
	return href
}

// zipPath resolves a (URL-escaped) href relative to dir inside the archive.
func zipPath(dir, href string) string {
	href = stripFragment(href)
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	return path.Join(dir, href)
}

func hasWord(list, w string) bool {
	for _, f := range strings.Fields(list) {
		if f == w {
			return true
		}
	}
	return false
}
