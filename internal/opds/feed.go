// Package opds serves the library as an OPDS 1.2 catalog for reading apps
// (KOReader, Moon+ Reader, Marvin, PocketBook): navigation and acquisition
// Atom feeds, OpenSearch, file downloads and covers.
package opds

import (
	"context"
	"encoding/xml"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
)

// Feed types and link relations of OPDS 1.2.
const (
	TypeNavigation  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	TypeAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	typeOpenSearch  = "application/opensearchdescription+xml"

	relAcquisition = "http://opds-spec.org/acquisition"
	relImage       = "http://opds-spec.org/image"
	relThumbnail   = "http://opds-spec.org/image/thumbnail"

	// pageSize entries per feed page (then rel="next")
	pageSize = 50
)

type link struct {
	Rel    string `xml:"rel,attr,omitempty"`
	Href   string `xml:"href,attr"`
	Type   string `xml:"type,attr,omitempty"`
	Title  string `xml:"title,attr,omitempty"`
	Length int64  `xml:"length,attr,omitempty"`
}

type person struct {
	Name string `xml:"name"`
}

type text struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type entry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Authors []person `xml:"author"`
	Lang    string   `xml:"dc:language,omitempty"`
	Issued  string   `xml:"dc:issued,omitempty"`
	Content *text    `xml:"content,omitempty"`
	Links   []link   `xml:"link"`
}

type feed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	DC      string   `xml:"xmlns:dc,attr"`
	OPDS    string   `xml:"xmlns:opds,attr"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Author  person   `xml:"author"`
	Links   []link   `xml:"link"`
	Entries []entry  `xml:"entry"`
}

func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339)
}

// newFeed starts a feed at self with the catalog's start and search links.
func newFeed(ctx context.Context, id, title, self, kind string) *feed {
	return &feed{
		DC: "http://purl.org/dc/terms/", OPDS: "http://opds-spec.org/2010/catalog",
		ID: "urn:vault:opds:" + id, Title: title, Updated: stamp(time.Time{}), Author: person{Name: "Vault"},
		Links: []link{
			{Rel: "self", Href: self, Type: kind},
			{Rel: "start", Href: "/opds", Type: TypeNavigation, Title: "Vault"},
			{Rel: "search", Href: "/opds/opensearch.xml", Type: typeOpenSearch, Title: i18n.T(ctx, "Поиск")},
			// OPDS 1.2 §3.3: for readers that skip OpenSearch
			{Rel: "search", Href: "/opds/search?q={searchTerms}", Type: TypeAcquisition, Title: i18n.T(ctx, "Поиск")},
		},
	}
}

// pages adds rel="previous"/"next" for page of a list of total entries
// (total < 0: unknown, more is whether a next page exists).
func (f *feed) pages(base string, page int, more bool, kind string) {
	at := func(p int) string {
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		if p == 0 {
			return base
		}
		return base + sep + "page=" + itoa(p)
	}
	for i := range f.Links {
		if f.Links[i].Rel == "self" {
			f.Links[i].Href = at(page) // self is the page shown
		}
	}
	if page > 0 {
		f.Links = append(f.Links, link{Rel: "previous", Href: at(page - 1), Type: kind})
	}
	if more {
		f.Links = append(f.Links, link{Rel: "next", Href: at(page + 1), Type: kind})
	}
}

// navEntry is an entry that opens another feed.
func navEntry(id, title, href, kind, summary string) entry {
	e := entry{Title: title, ID: "urn:vault:opds:" + id, Updated: stamp(time.Time{}),
		Links: []link{{Rel: "subsection", Href: href, Type: kind}}}
	if summary != "" {
		e.Content = &text{Type: "text", Body: summary}
	}
	return e
}

func writeFeed(w http.ResponseWriter, r *http.Request, kind string, f *feed) {
	out, err := xml.Marshal(f)
	if err != nil {
		http.Error(w, i18n.T(r.Context(), "Не удалось собрать каталог"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Write([]byte(xml.Header))
	w.Write(out)
}

// fileType is the acquisition type of a library file.
func fileType(path string) string {
	lp := strings.ToLower(path)
	if strings.HasSuffix(lp, ".fb2.zip") {
		return "application/fb2+zip"
	}
	switch filepath.Ext(lp) {
	case ".epub":
		return "application/epub+zip"
	case ".fb2":
		return "application/x-fictionbook+xml"
	case ".pdf":
		return "application/pdf"
	case ".mobi":
		return "application/x-mobipocket-ebook"
	case ".azw3":
		return "application/vnd.amazon.ebook"
	case ".cbz", ".zip":
		return "application/vnd.comicbook+zip"
	case ".cbr", ".rar":
		return "application/vnd.comicbook-rar"
	case ".cb7", ".7z":
		return "application/x-cb7"
	case ".mp3":
		return "audio/mpeg"
	case ".m4b", ".m4a", ".aac", ".mp4":
		return "audio/mp4"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	}
	return ""
}

// bookRank orders a book's formats: EPUB, FB2, MOBI, AZW3, PDF.
func bookRank(path string) int {
	switch fileType(path) {
	case "application/epub+zip":
		return 0
	case "application/x-fictionbook+xml", "application/fb2+zip":
		return 1
	case "application/x-mobipocket-ebook":
		return 2
	case "application/vnd.amazon.ebook":
		return 3
	case "application/pdf":
		return 4
	}
	return 5
}

// imageType guesses a cover's type from its name (most covers are JPEG).
func imageType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "image/jpeg"
}
