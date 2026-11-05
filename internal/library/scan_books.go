package library

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/antresol-app/antresol/internal/library/bookmeta"
	"github.com/antresol-app/antresol/internal/natsort"
)

// bookExts lists the e-book formats the book scanner indexes (DJVU,
// comics and everything else are left alone).
var bookExts = map[string]bool{".epub": true, ".fb2": true, ".fb2.zip": true, ".mobi": true, ".azw3": true, ".pdf": true}

var (
	reBookFB2Zip = regexp.MustCompile(`(?i)\.fb2\.zip$`)
	reSiteBookID = regexp.MustCompile(`\.\d{4,}$`)
)

// bookExt returns ".fb2.zip" for an FB2 archive, else the lower-cased
// extension.
func bookExt(name string) string {
	if reBookFB2Zip.MatchString(name) {
		return ".fb2.zip"
	}
	return strings.ToLower(filepath.Ext(name))
}

// bookStem is the file name without its bookExt.
func bookStem(name string) string { return name[:len(name)-len(bookExt(name))] }

// parseBookFile reads "Author - Title" or "Title" from a book file name;
// a book site's numeric id (".401234") is dropped and "_" means a space.
func parseBookFile(name string) (author, title string) {
	stem := reSiteBookID.ReplaceAllString(bookStem(name), "")
	stem = strings.Join(strings.Fields(strings.ReplaceAll(stem, "_", " ")), " ")
	if a, t, ok := strings.Cut(stem, " - "); ok && strings.TrimSpace(a) != "" && strings.TrimSpace(t) != "" {
		return strings.TrimSpace(a), strings.TrimSpace(t)
	}
	return "", stem
}

// formatRank orders the formats of one book for its metadata: EPUB, then
// FB2 (plain or zipped), then the rest.
func formatRank(path string) int {
	switch bookExt(filepath.Base(path)) {
	case ".epub":
		return 0
	case ".fb2", ".fb2.zip":
		return 1
	}
	return 2
}

type bookFile struct {
	path        string
	size, mtime int64
}

// walkBooks lists the book files under root, skipping hidden entries and
// NAS/macOS junk folders. An error means the walk is incomplete: the
// caller must not prune.
func walkBooks(ctx context.Context, root string) ([]bookFile, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []bookFile
	var firstErr error
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			low := strings.ToLower(name)
			if p != root && (strings.HasPrefix(name, ".") || skipDirs[low] || low == "__macosx") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !bookExts[bookExt(name)] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		out = append(out, bookFile{p, info.Size(), info.ModTime().Unix()})
		return nil
	})
	if err == nil {
		err = firstErr
	}
	return out, err
}

// bookGroup is one book: the formats in one folder with the same parsed title.
type bookGroup struct {
	dir   string
	files []bookFile
}

// scanBooks indexes the ebooks root: one book per folder and parsed title,
// all its formats as files, metadata from the files (EPUB > FB2 > others)
// and the names. A root that cannot be read completely is never pruned.
func (s *Scanner) scanBooks(ctx context.Context, st *ScanStats) error {
	root, err := filepath.Abs(s.Ebooks)
	if err != nil {
		return err
	}
	files, walkErr := walkBooks(ctx, root)
	if walkErr != nil && files == nil {
		return walkErr
	}
	groups := map[string]*bookGroup{}
	var order []string
	perDir := map[string]int{}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.path] = true
		key := bookMatchKey(f.path)
		g := groups[key]
		if g == nil {
			g = &bookGroup{dir: filepath.Dir(f.path)}
			groups[key] = g
			order = append(order, key)
			perDir[g.dir]++
		}
		g.files = append(g.files, f)
	}
	sort.Strings(order)
	stored, err := s.Repo.bookFilesUnder(ctx, root)
	if err != nil {
		return err
	}
	di := dirIndex{}
	arts := map[string]dirArt{}
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		g := groups[key]
		art, ok := arts[g.dir]
		if !ok {
			art = listDirArt(g.dir)
			arts[g.dir] = art
		}
		if err := s.scanBook(ctx, root, g, perDir[g.dir] == 1, di, art, stored, st); err != nil {
			return err
		}
	}
	if walkErr != nil {
		return walkErr // incomplete walk: no pruning
	}
	return s.finish(ctx, root, seen, st)
}

// fillBookMeta copies into dst the fields that are still empty there.
func fillBookMeta(dst *bookmeta.Meta, src bookmeta.Meta) {
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if len(dst.Authors) == 0 {
		dst.Authors = src.Authors
	}
	if dst.Series == "" {
		dst.Series, dst.SeriesIndex = src.Series, src.SeriesIndex
	}
	if dst.Lang == "" {
		dst.Lang = src.Lang
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.ISBN == "" {
		dst.ISBN = src.ISBN
	}
	if dst.Publisher == "" {
		dst.Publisher = src.Publisher
	}
	if dst.Year == 0 {
		dst.Year = src.Year
	}
	if len(dst.Cover) == 0 {
		dst.Cover, dst.CoverType = src.Cover, src.CoverType
	}
}

// bookReads counts bookmeta reads (tests: an unchanged book is not re-read).
var bookReads atomic.Int64

// dirArt is what a book folder holds besides books for the covers: its
// pictures (natural order) and a signature of their names, sizes, mtimes.
type dirArt struct {
	sig    string
	images []string
}

func listDirArt(dir string) dirArt {
	es, err := os.ReadDir(dir)
	if err != nil {
		return dirArt{}
	}
	var names []string
	h := sha1.New()
	for _, e := range es {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		for _, ie := range imgExts {
			if ext != ie {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return dirArt{}
			}
			names = append(names, e.Name())
			fmt.Fprintf(h, "%s\x00%d\x00%d\x00", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	natsort.Sort(names)
	out := dirArt{sig: hex.EncodeToString(h.Sum(nil))}
	for _, n := range names {
		out.images = append(out.images, filepath.Join(dir, n))
	}
	return out
}

// bookSig is a signature of everything a book's scan reads: its files
// (name, size, mtime), the folder's pictures and whether the folder holds
// only this book. "" when the folder's pictures could not be listed.
func bookSig(files []bookFile, art dirArt, onlyBook bool) string {
	if art.sig == "" {
		return ""
	}
	h := sha1.New()
	fmt.Fprintf(h, "only=%v\x00art=%s\x00", onlyBook, art.sig)
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", f.path, f.size, f.mtime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// unchangedBook reports whether every file of the group is a visible files
// row of one visible book with the same size and mtime, written by a scan
// that saw the same signature: the book then keeps its rows untouched.
func unchangedBook(files []bookFile, sig string, stored map[string]storedBookFile) bool {
	if sig == "" {
		return false
	}
	var item int64
	for _, f := range files {
		sf, ok := stored[f.path]
		if !ok || sf.size != f.size || sf.mtime != f.mtime || sf.sig != sig || (item != 0 && sf.itemID != item) {
			return false
		}
		item = sf.itemID
	}
	return item != 0
}

func (s *Scanner) scanBook(ctx context.Context, root string, g *bookGroup, onlyBook bool, di dirIndex, art dirArt,
	stored map[string]storedBookFile, st *ScanStats) error {
	sort.SliceStable(g.files, func(i, j int) bool {
		ri, rj := formatRank(g.files[i].path), formatRank(g.files[j].path)
		if ri != rj {
			return ri < rj
		}
		return natsort.Less(filepath.Base(g.files[i].path), filepath.Base(g.files[j].path))
	})
	// a book new to the library loses what mail servers refuse (epub_clean.go);
	// one already here is not rewritten behind its reader's back
	for i, f := range g.files {
		if have, ok := stored[f.path]; ok && have.size == f.size && have.mtime == f.mtime {
			continue
		}
		if bookExt(f.path) != ".epub" {
			continue
		}
		changed, err := CleanEPUBFile(f.path)
		if err != nil { // a read-only folder: the book still goes in as it is
			log.Printf("library: clean %s: %v", f.path, err)
			continue
		}
		if changed {
			log.Printf("library: removed files mail servers refuse from %s", f.path)
			if info, err := os.Stat(f.path); err == nil {
				g.files[i].size, g.files[i].mtime = info.Size(), info.ModTime().Unix()
			}
		}
	}
	// an unchanged book (files, pictures, folder) is not read again
	sig := bookSig(g.files, art, onlyBook)
	if unchangedBook(g.files, sig, stored) {
		st.Books++
		st.Files += len(g.files)
		return nil
	}
	var m bookmeta.Meta
	nameAuthor, nameTitle := "", ""
	inputs := make([]FileInput, len(g.files))
	stems := make([]string, len(g.files))
	for i, f := range g.files {
		inputs[i] = FileInput{Path: f.path, Size: f.size, Mtime: f.mtime}
		base := filepath.Base(f.path)
		stems[i] = bookStem(base)
		a, t := parseBookFile(base)
		if nameAuthor == "" {
			nameAuthor = a
		}
		if nameTitle == "" {
			nameTitle = t
		}
		bookReads.Add(1)
		fm, err := bookmeta.Read(f.path)
		if err != nil {
			log.Printf("library: book meta %s: %v", f.path, err)
			continue
		}
		fillBookMeta(&m, fm)
	}
	title := strings.TrimSpace(m.Title)
	if title == "" {
		title = nameTitle
	}
	authors := m.Authors
	if len(authors) == 0 && nameAuthor != "" {
		authors = []string{nameAuthor}
	}
	if len(authors) == 0 && g.dir != root {
		authors = []string{filepath.Base(g.dir)}
	}

	cover := ""
	if len(m.Cover) > 0 {
		name, err := s.saveArt(m.Cover, m.CoverType)
		if err != nil {
			log.Printf("library: book cover %s: %v", g.files[0].path, err)
		}
		cover = name
	}
	if cover == "" {
		cover = di.art(g.dir, append(stems, "cover")...)
	}
	if cover == "" && onlyBook && len(art.images) == 1 {
		cover = fileURL(art.images[0])
	}

	if _, err := s.Repo.UpsertBook(ctx, BookInput{
		Files: inputs, Title: title, Authors: authors, Series: m.Series, SeriesIndex: m.SeriesIndex,
		Lang: m.Lang, Description: m.Description, ISBN: m.ISBN, Publisher: m.Publisher, Year: m.Year, Cover: cover,
		sig: sig,
	}); err != nil {
		return fmt.Errorf("book %s: %w", g.files[0].path, err)
	}
	st.Books++
	st.Files += len(inputs)
	return nil
}

// onlyImage returns "file:<path>" of the single picture in dir, "" if there
// is none or more than one.
func onlyImage(dir string) string {
	if imgs := listDirArt(dir).images; len(imgs) == 1 {
		return fileURL(imgs[0])
	}
	return ""
}

var artExts = map[string]bool{"jpg": true, "png": true, "webp": true, "gif": true}

// saveArt writes artwork read from a file into ArtDir as <sha1>.<ext>
// (once, atomically) and returns that name; "" without an ArtDir or for an
// unknown type.
func (s *Scanner) saveArt(data []byte, ext string) (string, error) {
	if s.ArtDir == "" || len(data) == 0 || !artExts[ext] {
		return "", nil
	}
	name := coverName(data, ext)
	dst := filepath.Join(s.ArtDir, name)
	if _, err := os.Stat(dst); err == nil {
		return name, nil
	}
	if err := os.MkdirAll(s.ArtDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(s.ArtDir, ".cover-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return name, nil
}
