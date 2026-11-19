package mediacheck

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The folder browser of «Медиатека» (the first-run wizard, «Настройки
// сервера») shows only what the server was given to keep media in: the
// media folder and the other folders mounted into the container (docker's
// volumes and binds). Never the container's own system, nor the server's
// settings and database.

// systemDirs are never a place, nor inside one: the container's own system.
var systemDirs = []string{"/proc", "/sys", "/dev", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64",
	"/libx32", "/boot", "/run", "/var", "/tmp", "/root"}

// virtualFS are file systems no media lives on (the kernel's, the
// container's own layers).
var virtualFS = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true, "devpts": true, "mqueue": true, "cgroup": true,
	"cgroup2": true, "overlay": true, "securityfs": true, "debugfs": true, "tracefs": true, "fusectl": true,
	"configfs": true, "pstore": true, "bpf": true, "autofs": true, "hugetlbfs": true, "nsfs": true,
	"binfmt_misc": true, "ramfs": true, "rpc_pipefs": true, "efivarfs": true, "selinuxfs": true,
}

// MountPoints are the mount points /proc/self/mountinfo lists that may hold
// media: not the root, not on a virtual file system, not a system folder.
func MountPoints(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		// id parent major:minor root mountpoint options [optional…] - fstype source super
		left, right, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		f := strings.Fields(left)
		r := strings.Fields(right)
		if len(f) < 5 || len(r) < 1 || virtualFS[r[0]] {
			continue
		}
		mp := filepath.Clean(unescapeMount(f[4]))
		if !filepath.IsAbs(mp) || mp == "/" || isSystem(mp) {
			continue
		}
		out = append(out, mp)
	}
	return out
}

// unescapeMount undoes mountinfo's octal escapes (\040 is a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// isSystem: p is a system folder or lies in one.
func isSystem(p string) bool {
	for _, d := range systemDirs {
		if Inside(d, p) {
			return true
		}
	}
	return false
}

// Places are where the folder browser starts and what it never leaves: the
// media folder first, then the mounts (MountPoints: no system folder) that
// are folders, are
// not inside the media folder or another place, and neither are nor hold
// one of private (the server's settings and database, the import folder).
// The media folder is a place even when the container has no mountinfo (a
// server run outside docker).
func Places(mediaRoot string, mounts []string, private []string) []string {
	var cands []string
	if mediaRoot != "" {
		cands = append(cands, filepath.Clean(mediaRoot))
	}
	ms := append([]string(nil), mounts...)
	sort.Strings(ms)
	cands = append(cands, ms...)
	var out []string
next:
	for i, c := range cands {
		if c == "" || c == "/" {
			continue
		}
		for _, p := range private {
			if p != "" && (Inside(c, filepath.Clean(p)) || Inside(filepath.Clean(p), c)) {
				continue next
			}
		}
		for _, o := range out {
			if Inside(o, c) {
				continue next
			}
		}
		if i > 0 {
			if st, err := os.Stat(c); err != nil || !st.IsDir() {
				continue // a file bound in (hosts, a socket)
			}
		}
		out = append(out, c)
	}
	return out
}

// SelfMounts reads this process' mount points (none when there is no
// /proc: a server run outside Linux).
func SelfMounts() []string {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	return MountPoints(f)
}

// ErrOutside: a path the browser may not show or the library may not use:
// outside every place (as written, or once its links are followed).
var ErrOutside = errors.New("outside the places")

// Confine finds the place path lies in. The path must be absolute and
// clean of "..", lie in a place as written, and with every link followed
// (Resolve: a part not made yet counts by the deepest folder that is) lie
// in a place too, so a link never leads the browser, or a library folder,
// out of what the server was given.
func Confine(path string, places []string) (place string, err error) {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", ErrOutside
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", ErrOutside
		}
	}
	clean := filepath.Clean(path)
	for _, p := range places {
		if Inside(p, clean) {
			place = p
			break
		}
	}
	if place == "" {
		return "", ErrOutside
	}
	real, err := Resolve(clean)
	if errors.Is(err, fs.ErrPermission) {
		return place, nil // cannot be told; nothing can be read or made there either
	}
	if err != nil {
		return "", ErrOutside
	}
	for _, p := range places {
		rp, err := Resolve(p) // a place not there yet: by its deepest folder that is
		if err != nil {
			rp = p
		}
		if Inside(rp, real) {
			return place, nil
		}
	}
	return "", ErrOutside
}

// Sub is a folder directly in the folder the browser shows.
type Sub struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Items int    `json:"items"`
	More  bool   `json:"more,omitempty"`
}

// BrowseCap bounds how many folders Browse lists.
var BrowseCap = 500

// Browse lists the visible folders directly in dir (already Confine'd),
// sorted by name, with how many entries each holds; a link to a folder is
// one only while it stays in a place. truncated: there were more than
// BrowseCap.
func Browse(dir string, places []string) (subs []Sub, truncated bool, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	subs = []Sub{}
	for _, e := range ents {
		if hidden(e.Name()) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		if !isDir && e.Type()&fs.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil && st.IsDir() {
				_, cerr := Confine(full, places)
				isDir = cerr == nil
			}
		}
		if !isDir {
			continue
		}
		if len(subs) >= BrowseCap {
			truncated = true
			break
		}
		n, more, _ := countEntries(full, 999)
		subs = append(subs, Sub{Name: e.Name(), Path: full, Items: n, More: more})
	}
	sort.Slice(subs, func(i, j int) bool {
		a, b := strings.ToLower(subs[i].Name), strings.ToLower(subs[j].Name)
		if a != b {
			return a < b
		}
		return subs[i].Name < subs[j].Name
	})
	return subs, truncated, nil
}
