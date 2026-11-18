package mediacheck

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Bad is a path the server could not write to.
type Bad struct {
	Path string // a folder that exists: the one to make writable
	Why  string // Dir.Why
}

// neverChown: the folders no advice hands over, whatever failed below them
// (the root of the file system).
func neverChown(p string) bool { return p == "/" || p == "." || p == "" }

// FixInput is what FixFor needs to word the fix.
type FixInput struct {
	Bad []Bad
	// Mount are folders whose place is not there (NearestWithin found
	// nothing): no ownership fixes that, the volume does.
	Mount []string
	// Service is the compose service the server runs as ("antresol"); Docker
	// says the server runs in a container; ConfigDir is its settings folder
	// ("" when not known).
	Service   string
	ConfigDir string
	Docker    bool
	Self      Identity
	// Owner finds the owner of a path (nil: OwnerOf).
	Owner func(path string) (uid, gid int, ok bool)
}

// Fix is what to tell the person whose folders the server may not write:
// who the server runs as, who owns the folder, and commands to copy. The
// strings are commands and compose lines, in no language.
type Fix struct {
	// Paths are the folders to fix (container paths).
	Paths  []string `json:"paths"`
	Docker bool     `json:"docker"`
	// Mount are folders whose place is missing: the mount (the compose
	// volume) is to be fixed, with no chown to copy.
	Mount []string `json:"mount,omitempty"`
	// UID and GID are the server's; Owner is the first folder's owner as
	// "uid:gid" ("" when not known).
	UID   int    `json:"uid"`
	GID   int    `json:"gid"`
	Owner string `json:"owner,omitempty"`
	// Chown gives the folders to the server's user. "": a read-only mount,
	// which no ownership changes.
	Chown string `json:"chown,omitempty"`
	// ComposeUser is the line that runs the server as the folders' owner
	// instead, ConfigChown the one command that hands the server's own
	// settings folder to that user first. Both empty when that is no advice
	// (not in a container, the owner is root or already the server's user).
	ComposeUser string `json:"composeUser,omitempty"`
	ConfigChown string `json:"configChown,omitempty"`
}

// FixFor words the fix for the folders that failed.
func FixFor(in FixInput) Fix {
	owner := in.Owner
	if owner == nil {
		owner = OwnerOf
	}
	f := Fix{Paths: []string{}, Docker: in.Docker, UID: in.Self.UID, GID: in.Self.GID} // (Paths is [] in JSON, never null)
	svc := in.Service
	if svc == "" {
		svc = "antresol"
	}
	// the outer folder first: one inside it is fixed with it
	bad := append([]Bad(nil), in.Bad...)
	sort.SliceStable(bad, func(i, j int) bool { return len(bad[i].Path) < len(bad[j].Path) })
	var denied []string
	for _, m := range in.Mount {
		if m = filepath.Clean(m); !neverChown(m) && !underAny(m, f.Mount) {
			f.Mount = append(f.Mount, m)
		}
	}
	for _, b := range bad {
		p := filepath.Clean(b.Path)
		if neverChown(p) {
			continue // never advised, whatever failed below it
		}
		if underAny(p, f.Paths) {
			continue
		}
		f.Paths = append(f.Paths, p)
		if b.Why == WhyDenied {
			denied = append(denied, p)
		}
	}
	if len(denied) == 0 {
		return f
	}
	if uid, gid, ok := owner(denied[0]); ok {
		f.Owner = fmt.Sprintf("%d:%d", uid, gid)
		if in.Docker && uid != 0 && (uid != in.Self.UID || gid != in.Self.GID) {
			f.ComposeUser = fmt.Sprintf("user: %q", f.Owner)
			if in.ConfigDir != "" {
				f.ConfigChown = chownCmd(svc, true, f.Owner, []string{in.ConfigDir})
			}
		}
	}
	f.Chown = chownCmd(svc, in.Docker, fmt.Sprintf("%d:%d", in.Self.UID, in.Self.GID), denied)
	return f
}

// chownCmd: the command that gives paths to owner ("uid:gid"). In a
// container it runs once as root in a throwaway container of the same
// service, which has the same mounts, so no host path has to be known.
func chownCmd(service string, docker bool, owner string, paths []string) string {
	args := make([]string, len(paths))
	for i, p := range paths {
		args[i] = shellQuote(p)
	}
	if docker {
		return fmt.Sprintf("docker compose run --rm --no-deps --user root --entrypoint chown %s -R %s %s", service, owner, strings.Join(args, " "))
	}
	return fmt.Sprintf("sudo chown -R %s %s", owner, strings.Join(args, " "))
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// underAny: p is one of dirs or inside one.
func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}
