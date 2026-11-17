package addons

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

const (
	// maxUnpacked caps the total size of unpacked regular files.
	maxUnpacked = 512 << 20
	// maxEntries caps the number of archive entries (of any type).
	maxEntries = 100000
)

var (
	errUnsafe   = userError("Небезопасный архив расширения")
	errTooBig   = userError("Архив расширения слишком велик")
	errBadTar   = userError("Не удалось распаковать архив расширения")
	errDstTaken = userError("Каталог расширения уже существует")
)

// Unpack extracts a tar.gz stream into dst. It writes into dst+".tmp"
// through an os.Root (so nothing can land outside it), renames to dst only
// on success and removes the temporary directory on any error. Symlinks,
// hardlinks and special files are refused; dst must not exist.
func Unpack(r io.Reader, dst string) (err error) {
	if _, e := os.Lstat(dst); e == nil {
		return errDstTaken
	}
	tmp := dst + ".tmp"
	if err = os.RemoveAll(tmp); err != nil {
		return errBadTar
	}
	if err = os.MkdirAll(tmp, 0o755); err != nil {
		return errBadTar
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()
	root, err := os.OpenRoot(tmp)
	if err != nil {
		return errBadTar
	}
	err = extract(r, root)
	root.Close()
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, dst); err != nil {
		return errBadTar
	}
	return nil
}

func extract(r io.Reader, root *os.Root) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return errBadTar
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for entries := 0; ; entries++ {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errBadTar
		}
		if entries >= maxEntries {
			return errTooBig
		}
		name := h.Name
		if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return errUnsafe
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == ".." {
				return errUnsafe
			}
		}
		name = path.Clean(name)
		switch h.Typeflag {
		case tar.TypeDir:
			if name == "." {
				continue
			}
			if err := root.MkdirAll(name, 0o755); err != nil {
				return errUnsafe
			}
		case tar.TypeReg:
			if name == "." {
				return errUnsafe
			}
			if dir := path.Dir(name); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return errUnsafe
				}
			}
			total += h.Size
			if h.Size < 0 || total > maxUnpacked {
				return errTooBig
			}
			perm := fs.FileMode(h.Mode).Perm()&0o755 | 0o600
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
			if err != nil {
				return errUnsafe
			}
			n, err := io.Copy(f, tr)
			cerr := f.Close()
			if err != nil || cerr != nil || n != h.Size {
				return errBadTar
			}
			// umask may have stripped bits; restore exactly.
			if err := root.Chmod(name, perm); err != nil {
				return errBadTar
			}
		default: // symlinks, hardlinks, devices, fifos
			return errUnsafe
		}
	}
}
