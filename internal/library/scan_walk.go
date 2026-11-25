package library

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// walkedFile is a file a scanner's walk found: its path, size and mtime
// (seconds).
type walkedFile struct {
	path        string
	size, mtime int64
}

// unchangedFiles reports whether every file of a group is a visible files
// row of one visible item with the same size and mtime, written by a scan
// that saw the same signature: the item then keeps its rows untouched.
func unchangedFiles(files []walkedFile, sig string, stored map[string]storedFile) bool {
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

// walkTree is the walk every scanner makes under root. Hidden files and
// folders (a leading dot) are skipped, so are the folders skipDir names (by
// their lower-cased name; root itself never is). Of the other files want
// says which the scanner reads (it may also record what it does not keep, a
// sheet beside the tracks); visit gets those with their info. A folder or
// file that cannot be read is skipped and the walk goes on, but its error is
// returned at the end: the walk is incomplete and the caller must not prune.
func walkTree(ctx context.Context, root string, skipDir func(low string) bool,
	want func(path string, d fs.DirEntry) bool, visit func(path string, info fs.FileInfo)) error {
	st, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	var firstErr error
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			note(err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || skipDir(strings.ToLower(name))) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !want(p, d) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			note(err)
			return nil
		}
		visit(p, info)
		return nil
	})
	if err == nil {
		err = firstErr
	}
	return err
}

// dirLister lists each folder once.
type dirLister map[string][]fs.DirEntry

func (l dirLister) read(dir string) ([]fs.DirEntry, error) {
	if es, ok := l[dir]; ok {
		return es, nil
	}
	es, err := os.ReadDir(dir)
	if err == nil {
		l[dir] = es
	}
	return es, err
}

// hashDirs adds the files (name, size, mtime) of each of dirs, in order, to
// h; false when a folder cannot be read (the signature is then none).
func hashDirs(h io.Writer, dirs []string, ls dirLister) bool {
	for _, d := range dirs {
		es, err := ls.read(d)
		if err != nil {
			return false
		}
		fmt.Fprintf(h, "D%s\x00", d)
		for _, e := range es {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return false
			}
			fmt.Fprintf(h, "%s\x00%d\x00%d\x00", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	return true
}

// seenPaths is the paths of the files a walk found: what a scan keeps when it prunes.
func seenPaths(files []walkedFile) map[string]bool {
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.path] = true
	}
	return seen
}

// walkFiles is walkTree collecting the files whose name wantName accepts.
func walkFiles(ctx context.Context, root string, skipDir func(low string) bool, wantName func(name string) bool) ([]walkedFile, error) {
	var out []walkedFile
	err := walkTree(ctx, root, skipDir, func(_ string, d fs.DirEntry) bool { return wantName(d.Name()) },
		func(p string, info fs.FileInfo) { out = append(out, walkedFile{p, info.Size(), info.ModTime().Unix()}) })
	return out, err
}
