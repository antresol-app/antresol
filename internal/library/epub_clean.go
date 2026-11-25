package library

import (
	"archive/zip"
	"errors"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// mailRefusedExts are the file types Gmail refuses, also inside an archive,
// which an EPUB is (support.google.com/mail/answer/6590; Google updates it).
// A book needs none of them, and some sites stamp EPUBs with such files: one
// named after the site's domain only looks like a Windows program.
var mailRefusedExts = func() map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Fields(`.ade .adp .apk .appx .appxbundle .bat .cab .chm .cmd .com .cpl
		.diagcab .diagcfg .diagpkg .dll .dmg .ex .ex_ .exe .hta .img .ins .iso .isp .jar .jnlp .js .jse
		.lib .lnk .mde .mjs .msc .msi .msix .msixbundle .msp .mst .nsh .pif .ps1 .scr .sct .shb .sys
		.vb .vbe .vbs .vhd .vxd .wsc .wsf .wsh .xll`) {
		m[x] = true
	}
	return m
}()

func mailRefusedName(name string) bool {
	return mailRefusedExts[strings.ToLower(path.Ext(name))]
}

var (
	reManifestItem = regexp.MustCompile(`(?is)<item\b[^>]*>`)
	reItemHref     = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']*)["']`)
)

// dropManifestItems removes the items of an OPF that name a refused file: a
// manifest entry without its file is an error in Amazon's converter.
func dropManifestItems(opf string) string {
	return reManifestItem.ReplaceAllStringFunc(opf, func(item string) string {
		if m := reItemHref.FindStringSubmatch(item); m != nil && mailRefusedName(m[1]) {
			return ""
		}
		return item
	})
}

func hasRefusedEntry(zr *zip.Reader) bool {
	for _, f := range zr.File {
		if mailRefusedName(f.Name) {
			return true
		}
	}
	return false
}

const maxOPF = 16 << 20

func copyOPF(zw *zip.Writer, f *zip.File) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	opf, err := io.ReadAll(io.LimitReader(rc, maxOPF+1))
	if err != nil {
		return err
	}
	if len(opf) > maxOPF {
		return errors.New("OPF too large")
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: f.Modified})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, dropManifestItems(string(opf)))
	return err
}

// writeCleanEPUB writes zr without the refused entries, mimetype first (the
// place EPUB requires it, which such sites' tools often lose); other entries
// are copied as they are.
func writeCleanEPUB(zr *zip.Reader, w io.Writer) error {
	zw := zip.NewWriter(w)
	for _, first := range []bool{true, false} {
		for _, f := range zr.File {
			if (f.Name == "mimetype") != first || mailRefusedName(f.Name) {
				continue
			}
			var err error
			if strings.EqualFold(path.Ext(f.Name), ".opf") {
				err = copyOPF(zw, f)
			} else {
				err = zw.Copy(f)
			}
			if err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

// CleanEPUB writes the EPUB at r (size bytes) without the files mail servers
// refuse into a new temp file in dir ("": the system's), rewound, which the
// caller closes and removes. A book with none, or one it cannot rewrite,
// gives nil: it stays as it is. The book is never held in memory.
func CleanEPUB(r io.ReaderAt, size int64, dir string) (*os.File, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil || !hasRefusedEntry(zr) {
		return nil, nil
	}
	tmp, err := os.CreateTemp(dir, "clean-*.epub")
	if err != nil {
		return nil, err
	}
	if err := writeCleanEPUB(zr, tmp); err != nil {
		log.Printf("library: clean EPUB: %v", err)
		discardTemp(tmp)
		return nil, nil
	}
	return rewound(tmp)
}

// discardTemp closes and removes a temp file.
func discardTemp(f *os.File) {
	f.Close()
	os.Remove(f.Name())
}

// rewound is f at its start (f is discarded when it cannot be).
func rewound(f *os.File) (*os.File, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		discardTemp(f)
		return nil, err
	}
	return f, nil
}

// CleanEPUBFile rewrites the EPUB at p without the files mail servers refuse,
// and reports whether it did. The clean copy replaces p by rename: a reader of
// the old file is undisturbed, and a hard link of it (the copy another tool keeps
// from) keeps the original bytes. A file that is not a readable ZIP is left
// alone and is no error: the scanner keeps broken books visible.
func CleanEPUBFile(p string) (bool, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return false, nil
	}
	defer zr.Close()
	if !hasRefusedEntry(&zr.Reader) {
		return false, nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	// hidden: the scanner does not take it for a book
	tmp, err := os.CreateTemp(filepath.Dir(p), ".clean-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	err = writeCleanEPUB(&zr.Reader, tmp)
	if err == nil {
		err = tmp.Chmod(st.Mode().Perm())
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
