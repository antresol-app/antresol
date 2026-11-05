// Package version holds the build version, set via -ldflags, and how the
// server names itself to other services.
package version

import (
	"net/url"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
)

// Version is overridden with -ldflags "-X github.com/antresol-app/antresol/internal/version.Version=...".
var Version = "dev"

// Commit is the source revision of the build, set with -ldflags
// "-X github.com/antresol-app/antresol/internal/version.Commit=..." (Docker
// builds have no .git); BuildCommit falls back to what the go tool stamped.
var Commit = ""

// SourceURL is where the source code of this very build is published (AGPL
// §13), set with -ldflags "-X github.com/antresol-app/antresol/internal/version.SourceURL=...".
// Official builds set it to their public tag; any other build leaves it
// empty, and then the server offers no link (a link to the public tree would
// be untrue for a build with changes of its own). VAULT_SOURCE_URL overrides it.
var SourceURL = ""

// SourceLink is the source offer the server shows: VAULT_SOURCE_URL when set
// (set but empty: none), else SourceURL; only an absolute http(s) link,
// anything else is no link.
func SourceLink() string {
	s := SourceURL
	if v, ok := os.LookupEnv("VAULT_SOURCE_URL"); ok {
		s = v
	}
	u, err := url.Parse(s)
	if s == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.TrimSpace(s) != s {
		return ""
	}
	return s
}

// BuildCommit is Commit, else the VCS revision the go tool stamped into the
// binary (a build from a git checkout), else "".
func BuildCommit() string {
	if Commit != "" {
		return Commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}

type marker struct{}

// Repo is the public repository, read from the module path
// (github.com/<org>/antresol), so it follows the module.
var Repo = strings.TrimSuffix(reflect.TypeFor[marker]().PkgPath(), "/internal/version")

// UserAgent identifies the server to MusicBrainz, the Cover Art Archive,
// Open Library, Wikidata and the other metadata services (MusicBrainz
// requires "<app>/<version> ( <contact> )"): the public repository is the
// contact.
var UserAgent = "Antresol/0.1 ( https://" + Repo + " )"
