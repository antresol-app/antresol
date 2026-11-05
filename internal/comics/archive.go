// Package comics reads manga and comic volumes: CBZ/ZIP, CBR/RAR, CB7/7z
// archives and folders of images. Peek lists a volume for the scanner
// without writing anything; Open serves its pages to the reader (RAR and 7z
// are unpacked once into a size-limited cache, see cache.go).
//
// Archives are untrusted input: entry names are only used to list and
// order pages (never as paths on disk), every read is capped (a page, the
// ComicInfo.xml, a Peek, the whole unpack) and decoder memory is limited
// (limits7z.go, and the patched sevenzip in third_party/sevenzip). Only
// regular files are read: symlinks, FIFOs and devices are refused.
package comics

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
	"golang.org/x/text/encoding/charmap"

	"github.com/antresol-app/antresol/internal/natsort"
)

// Limits (variables for the tests).
var (
	maxPageBytes    int64 = 64 << 20  // one page, decompressed
	maxExtractBytes int64 = 4 << 30   // all pages of one archive unpacked into the cache (also ≤ cacheLimit)
	maxPeekBytes    int64 = 512 << 20 // bytes one Peek may decompress (incl. the solid prefix it must skip)
	maxPages              = 20000
	maxEntries            = 200000 // entries walked in one archive (pages or not)
)

var (
	errTooBig      = errors.New("comics: entry over the size limit")
	errTooMany     = errors.New("comics: too many entries")
	errUnsupported = errors.New("comics: unsupported format")
	errPeekBudget  = errors.New("comics: reading this entry would decompress too much")
	errNotRegular  = errors.New("comics: not a regular file")
)

// pageTypes are the page formats, by extension.
var pageTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp",
	".avif": "image/avif", ".gif": "image/gif",
}

// Archive is an opened volume. Page may be called concurrently. Every
// Archive must be closed: an open one keeps its zip file handle and pins
// its cache entry (it is never evicted while open).
type Archive struct {
	pages   []string // relative names, natural order
	info    ComicInfo
	hasInfo bool

	zf     *os.File
	zfiles []*zip.File // parallel to pages (zip)
	files  []string    // parallel to pages (cache)
	root   *os.Root    // folder volume: pages are opened inside it only

	closeOnce sync.Once
	release   func()
}

// Pages returns the page names (relative paths inside the volume) in
// reading order.
func (a *Archive) Pages() []string { return append([]string(nil), a.pages...) }

// Info returns the volume's ComicInfo.xml, if it has one.
func (a *Archive) Info() (ComicInfo, bool) { return a.info, a.hasInfo }

// Page opens page i (0-based) and returns its content type. Reading more
// than the page limit fails; a page file that is not a regular file (a
// symlink swapped in) is refused.
func (a *Archive) Page(i int) (io.ReadCloser, string, error) {
	if i < 0 || i >= len(a.pages) {
		return nil, "", fmt.Errorf("comics: page %d of %d", i, len(a.pages))
	}
	ct := pageTypes[strings.ToLower(path.Ext(a.pages[i]))]
	if a.zf != nil {
		f := a.zfiles[i]
		if f.UncompressedSize64 > uint64(maxPageBytes) {
			return nil, "", errTooBig
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		return capReader(rc, maxPageBytes), ct, nil
	}
	if a.root != nil {
		f, err := openRegularIn(a.root, a.pages[i])
		if err != nil {
			return nil, "", err
		}
		return capReader(f, maxPageBytes), ct, nil
	}
	f, err := openRegular(a.files[i])
	if err != nil {
		return nil, "", err
	}
	return capReader(f, maxPageBytes), ct, nil
}

// Close releases the archive (a zip file handle, a cache entry).
func (a *Archive) Close() error {
	var err error
	a.closeOnce.Do(func() {
		if a.zf != nil {
			err = a.zf.Close()
		}
		if a.root != nil {
			if rerr := a.root.Close(); err == nil {
				err = rerr
			}
		}
		if a.release != nil {
			a.release()
		}
	})
	return err
}

// regularFS opens regular files of one folder only (rardecode opens the
// volumes of a multi-part RAR through it).
type regularFS string

func (d regularFS) Open(name string) (fs.File, error) {
	if name != filepath.Base(name) {
		return nil, fs.ErrNotExist
	}
	return openRegular(filepath.Join(string(d), name))
}

// statVolume returns what a volume path is: a real folder or a regular
// file (not followed if it is a symlink).
func statVolume(p string) (os.FileInfo, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() && !st.Mode().IsRegular() {
		return nil, errNotRegular
	}
	return st, nil
}

type format int

const (
	formatZip format = iota + 1
	formatRar
	format7z
)

var formatByExt = map[string]format{
	".cbz": formatZip, ".zip": formatZip, ".cbr": formatRar, ".rar": formatRar, ".cb7": format7z, ".7z": format7z,
}

// IsArchive reports whether the file name is a comic archive by extension.
func IsArchive(name string) bool { return formatByExt[strings.ToLower(filepath.Ext(name))] != 0 }

// IsPage reports whether a file name is a page image (jpg jpeg png webp avif gif).
func IsPage(name string) bool { return pageTypes[strings.ToLower(filepath.Ext(name))] != "" }

// formatOf picks the reader by the file's signature (a .cbr is often a
// renamed zip and the other way round), else by its extension.
func formatOf(p string) (format, error) {
	byExt := formatByExt[strings.ToLower(filepath.Ext(p))]
	if byExt == 0 {
		return 0, errUnsupported
	}
	f, err := openRegular(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	head := make([]byte, 8)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		return formatZip, nil
	case bytes.HasPrefix(head, []byte("Rar!\x1a\x07")):
		return formatRar, nil
	case bytes.HasPrefix(head, []byte("7z\xbc\xaf\x27\x1c")):
		return format7z, nil
	}
	return byExt, nil
}

// decodeName returns an entry name as UTF-8: a name that is not valid
// UTF-8 (a zip without the UTF-8 flag, from Windows) is Windows-1251.
func decodeName(raw string) string {
	if utf8.ValidString(raw) {
		return raw
	}
	if s, err := charmap.Windows1251.NewDecoder().String(raw); err == nil {
		return s
	}
	return strings.ToValidUTF8(raw, "\uFFFD")
}

// cleanName turns an entry name into a clean relative slash path, or ""
// for a directory or a name that climbs out of the archive.
func cleanName(name string) string {
	n := strings.ReplaceAll(name, `\`, "/")
	if n == "" || strings.HasSuffix(n, "/") {
		return ""
	}
	if len(n) >= 2 && n[1] == ':' { // C:/…
		return ""
	}
	n = path.Clean(strings.TrimLeft(n, "/"))
	if n == "." || n == ".." || strings.HasPrefix(n, "../") {
		return ""
	}
	return n
}

// skipped reports names that are never pages: anything under a hidden
// folder or __MACOSX, hidden files, Thumbs.db.
func skipped(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.EqualFold(part, "__MACOSX") {
			return true
		}
	}
	return strings.EqualFold(path.Base(name), "Thumbs.db")
}

func isPage(name string) bool { return name != "" && !skipped(name) && IsPage(name) }

func isInfo(name string) bool {
	return name != "" && !skipped(name) && strings.EqualFold(path.Base(name), "ComicInfo.xml")
}

func sortNames(names []string) {
	sort.SliceStable(names, func(i, j int) bool { return natsort.Less(names[i], names[j]) })
}

// entry is one file of an archive. open is valid only while the archive
// is open (during a walk callback, or until a listing is closed).
type entry struct {
	name string // cleaned (cleanName) and decoded; "" = not usable
	size int64  // declared size, -1 unknown (never trusted alone)
	cost int64  // bytes decoded to read it (a solid archive decodes what precedes it)
	open func() (io.ReadCloser, error)
}

func clampSize(v uint64) int64 { return int64(min(v, 1<<62)) }

// listArchive lists the file entries of an archive in archive order
// without decoding any data; closeFn closes it.
func listArchive(p string, f format) (entries []entry, closeFn func() error, err error) {
	switch f {
	case formatZip:
		fh, zr, err := openZipReader(p)
		if err != nil {
			return nil, nil, err
		}
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() {
				continue
			}
			size := clampSize(zf.UncompressedSize64)
			entries = append(entries, entry{name: cleanName(decodeName(zf.Name)), size: size, cost: size, open: zf.Open})
		}
		return entries, fh.Close, nil
	case format7z:
		fh, err := openRegular(p)
		if err != nil {
			return nil, nil, err
		}
		st, err := fh.Stat()
		if err != nil {
			fh.Close()
			return nil, nil, err
		}
		r, err := sevenzip.NewReader(fh, st.Size())
		if err != nil {
			fh.Close()
			return nil, nil, err
		}
		if len(r.File) > maxEntries {
			fh.Close()
			return nil, nil, errTooMany
		}
		stream := map[int]int64{} // bytes of each solid stream before an entry
		for _, sf := range r.File {
			if sf.FileInfo().IsDir() {
				continue
			}
			size := clampSize(sf.UncompressedSize)
			stream[sf.Stream] += size
			entries = append(entries, entry{name: cleanName(decodeName(sf.Name)), size: size, cost: stream[sf.Stream], open: sf.Open})
		}
		return entries, fh.Close, nil
	case formatRar:
		files, err := rardecode.List(filepath.Base(p), rarOptions(p)...)
		if err != nil {
			return nil, nil, err
		}
		if len(files) > maxEntries {
			return nil, nil, errTooMany
		}
		var before int64
		for i, rf := range files {
			size := rf.UnPackedSize
			if rf.UnKnownSize || size < 0 {
				size = -1
			}
			decoded := size
			if size < 0 {
				decoded = maxPeekBytes + 1
			}
			before = min(before+decoded, 1<<62)
			if rf.IsDir || rf.LinkType != 0 || rf.Encrypted {
				continue
			}
			e := entry{name: cleanName(decodeName(rf.Name)), size: size, cost: decoded}
			if rf.Solid {
				idx, want := i, rf.Name
				e.cost = before
				e.open = func() (io.ReadCloser, error) { return rarStreamTo(p, idx, want) }
			} else {
				e.open = rf.Open
			}
			entries = append(entries, e)
		}
		return entries, func() error { return nil }, nil
	}
	return nil, nil, errUnsupported
}

func rarOptions(p string) []rardecode.Option {
	return []rardecode.Option{rardecode.FileSystem(regularFS(filepath.Dir(p))), rardecode.MaxDictionarySize(maxDictBytes)}
}

// rarStreamTo decodes a solid RAR from the start up to entry idx and
// returns a reader of that entry.
func rarStreamTo(p string, idx int, name string) (io.ReadCloser, error) {
	rc, err := rardecode.OpenReader(filepath.Base(p), rarOptions(p)...)
	if err != nil {
		return nil, err
	}
	for i := 0; ; i++ {
		h, err := rc.Next()
		if err != nil {
			rc.Close()
			return nil, err
		}
		if i == idx {
			if h.Name != name {
				rc.Close()
				return nil, fmt.Errorf("comics: rar entry %d is %q, listed as %q", idx, h.Name, name)
			}
			return struct {
				io.Reader
				io.Closer
			}{rc, rc}, nil
		}
	}
}

// walkArchive calls fn for every file entry of an archive, in archive
// order (for unpacking: a solid archive is decoded once, front to back).
func walkArchive(p string, f format, fn func(e entry) error) error {
	if f == formatRar {
		return walkRar(p, fn)
	}
	entries, closeFn, err := listArchive(p, f)
	if err != nil {
		return err
	}
	defer closeFn()
	for _, e := range entries {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func walkRar(p string, fn func(e entry) error) error {
	rc, err := rardecode.OpenReader(filepath.Base(p), rarOptions(p)...)
	if err != nil {
		return err
	}
	defer rc.Close()
	for n := 0; ; n++ {
		if n >= maxEntries {
			return errTooMany
		}
		h, err := rc.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.IsDir || h.LinkType != 0 || h.Encrypted {
			continue
		}
		size := h.UnPackedSize
		if h.UnKnownSize {
			size = -1
		}
		if err := fn(entry{name: cleanName(decodeName(h.Name)), size: size, cost: size,
			open: func() (io.ReadCloser, error) { return io.NopCloser(rc), nil }}); err != nil {
			return err
		}
	}
}

// readEntry reads an entry whole, at most limit bytes (errTooBig past it).
func readEntry(e entry, limit int64) ([]byte, error) {
	if e.size > limit {
		return nil, errTooBig
	}
	rc, err := e.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return readCapped(rc, limit)
}

func readCapped(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errTooBig
	}
	return b, nil
}

// cappedReader fails a read that goes past its limit.
type cappedReader struct {
	r    io.ReadCloser
	left int64
}

func capReader(r io.ReadCloser, limit int64) io.ReadCloser { return &cappedReader{r: r, left: limit} }

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, errTooBig
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return n + int(c.left), errTooBig
	}
	return n, err
}

func (c *cappedReader) Close() error { return c.r.Close() }

// Peek lists a volume without writing anything to disk: its pages in
// order, its ComicInfo.xml and the bytes of the first page (nil when that
// page is over the page limit). It reads at most two entries (the first
// page, then ComicInfo.xml) and decodes at most maxPeekBytes for them, a
// solid archive's prefix included; past that budget the page list is still
// returned, with the error.
func Peek(p string) (pages []string, info ComicInfo, hasInfo bool, cover []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("comics: %s: panic: %v", p, r)
		}
	}()
	st, err := statVolume(p)
	if err != nil {
		return nil, info, false, nil, err
	}
	if st.IsDir() {
		a, err := openDir(p, st)
		if err != nil {
			return nil, info, false, nil, err
		}
		defer a.Close()
		if len(a.pages) > 0 {
			if f, _, err := a.Page(0); err == nil {
				cover, _ = readCapped(f, maxPageBytes)
				f.Close()
			}
		}
		return a.pages, a.info, a.hasInfo, cover, nil
	}
	f, err := formatOf(p)
	if err != nil {
		return nil, info, false, nil, err
	}
	entries, closeFn, err := listArchive(p, f)
	if err != nil {
		return nil, info, false, nil, err
	}
	defer closeFn()
	var first, infoEntry *entry
	for i := range entries {
		e := &entries[i]
		switch {
		case isPage(e.name):
			if len(pages) >= maxPages {
				return nil, info, false, nil, errTooMany
			}
			pages = append(pages, e.name)
			if first == nil || natsort.Less(e.name, first.name) {
				first = e
			}
		case isInfo(e.name):
			if infoEntry == nil || strings.Count(e.name, "/") < strings.Count(infoEntry.name, "/") {
				infoEntry = e
			}
		}
	}
	sortNames(pages)
	budget := maxPeekBytes
	read := func(e *entry, limit int64) ([]byte, error) {
		if e.size > limit {
			return nil, errTooBig
		}
		if e.cost > budget {
			return nil, errPeekBudget
		}
		budget -= max(e.cost, 0)
		return readEntry(*e, limit)
	}
	if first != nil {
		b, rerr := read(first, maxPageBytes)
		switch {
		case rerr == nil:
			cover = b
		case !errors.Is(rerr, errTooBig):
			err = rerr
		}
	}
	if infoEntry != nil && err == nil {
		if b, rerr := read(infoEntry, maxInfoBytes); rerr == nil {
			if ci, perr := parseComicInfo(b); perr == nil {
				info, hasInfo = ci, true
			}
		} else if errors.Is(rerr, errPeekBudget) {
			err = rerr
		}
	}
	return pages, info, hasInfo, cover, err
}

// Open opens a volume for reading pages: a folder of images, a zip in
// place, or a RAR/7z archive unpacked once into cacheDir (see cache.go).
// The caller must Close it.
func Open(p, cacheDir string) (*Archive, error) {
	st, err := statVolume(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return openDir(p, st)
	}
	f, err := formatOf(p)
	if err != nil {
		return nil, err
	}
	if f == formatZip {
		return openZip(p)
	}
	return openCached(p, st, cacheDir, f)
}

// openDir reads a folder volume: the regular image files right in it.
// The folder is held as an os.Root and every page is opened through it,
// so swapping the folder (or a parent) for a symlink after Open cannot
// redirect page reads elsewhere.
// st is the folder's Lstat: a folder swapped for a symlink between that
// and OpenRoot is refused.
func openDir(dir string, st os.FileInfo) (*Archive, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if rst, err := root.Stat("."); err != nil || !os.SameFile(st, rst) {
		root.Close()
		return nil, errNotRegular
	}
	d, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	es, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	a := &Archive{root: root}
	for _, e := range es {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		switch {
		case isPage(name):
			a.pages = append(a.pages, name)
		case isInfo(name) && !a.hasInfo:
			if f, err := openRegularIn(root, name); err == nil {
				b, err := readCapped(f, maxInfoBytes)
				f.Close()
				if err == nil {
					if ci, err := parseComicInfo(b); err == nil {
						a.info, a.hasInfo = ci, true
					}
				}
			}
		}
	}
	if len(a.pages) > maxPages {
		root.Close()
		return nil, errTooMany
	}
	sortNames(a.pages)
	return a, nil
}

// openZipReader opens a regular file as a zip.
func openZipReader(p string) (*os.File, *zip.Reader, error) {
	fh, err := openRegular(p)
	if err != nil {
		return nil, nil, err
	}
	st, err := fh.Stat()
	if err == nil {
		var zr *zip.Reader
		if zr, err = zip.NewReader(fh, st.Size()); err == nil {
			if len(zr.File) > maxEntries {
				err = errTooMany
			} else {
				return fh, zr, nil
			}
		}
	}
	fh.Close()
	return nil, nil, err
}

// openZip opens a zip in place: pages are read straight from it.
func openZip(p string) (*Archive, error) {
	fh, zr, err := openZipReader(p)
	if err != nil {
		return nil, err
	}
	type page struct {
		name string
		f    *zip.File
	}
	var pages []page
	a := &Archive{zf: fh}
	infoDepth := -1
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := cleanName(decodeName(f.Name))
		switch {
		case isPage(name):
			pages = append(pages, page{name, f})
		case isInfo(name) && (infoDepth < 0 || strings.Count(name, "/") < infoDepth):
			b, err := readEntry(entry{name: name, size: clampSize(f.UncompressedSize64), open: f.Open}, maxInfoBytes)
			if err != nil {
				continue
			}
			if ci, err := parseComicInfo(b); err == nil {
				a.info, a.hasInfo, infoDepth = ci, true, strings.Count(name, "/")
			}
		}
	}
	if len(pages) > maxPages {
		fh.Close()
		return nil, errTooMany
	}
	sort.SliceStable(pages, func(i, j int) bool { return natsort.Less(pages[i].name, pages[j].name) })
	for _, pg := range pages {
		a.pages = append(a.pages, pg.name)
		a.zfiles = append(a.zfiles, pg.f)
	}
	return a, nil
}
