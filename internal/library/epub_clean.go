package library

import (
	"archive/zip"
	"bytes"
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

// CleanEPUB returns the EPUB without the files mail servers refuse. A book
// with none, or one it cannot rewrite, comes back as it is.
func CleanEPUB(data []byte) []byte {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || !hasRefusedEntry(zr) {
		return data
	}
	var out bytes.Buffer
	if err := writeCleanEPUB(zr, &out); err != nil {
		log.Printf("library: clean EPUB: %v", err)
		return data
	}
	return out.Bytes()
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
