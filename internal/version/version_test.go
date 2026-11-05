package version

import (
	"os"
	"strings"
	"testing"
)

// Repo is the module path of go.mod, and UserAgent names it.
func TestRepoIsTheModule(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	mod := strings.TrimPrefix(strings.SplitN(string(b), "\n", 2)[0], "module ")
	if Repo != mod || !strings.HasPrefix(Repo, "github.com/") || !strings.HasSuffix(Repo, "/antresol") {
		t.Fatalf("Repo %q, module %q", Repo, mod)
	}
	if UserAgent != "Antresol/0.1 ( https://"+mod+" )" {
		t.Fatalf("UserAgent %q", UserAgent)
	}
}

// VAULT_SOURCE_URL overrides the link set at build time; set but empty, it
// clears it (a server whose source is not the public tree).
func TestSourceURLOverride(t *testing.T) {
	old := SourceURL
	t.Cleanup(func() { SourceURL = old })
	SourceURL = "https://example.org/antresol/tree/v1.0.0"
	if got := SourceLink(); got != SourceURL {
		t.Fatalf("built-in: %q", got)
	}
	t.Setenv("VAULT_SOURCE_URL", "https://example.org/fork/tree/abc")
	if got := SourceLink(); got != "https://example.org/fork/tree/abc" {
		t.Fatalf("override: %q", got)
	}
	t.Setenv("VAULT_SOURCE_URL", "")
	if got := SourceLink(); got != "" {
		t.Fatalf("cleared: %q", got)
	}
}

// Only an absolute http(s) link is offered: anything else (a typo, a
// javascript: URL) is no link at all.
func TestSourceURLOnlyHTTP(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "github.com/x/antresol", "ftp://example.org/src", "https://", " https://example.org"} {
		t.Setenv("VAULT_SOURCE_URL", u)
		if got := SourceLink(); got != "" {
			t.Errorf("%q offered as %q", u, got)
		}
	}
	t.Setenv("VAULT_SOURCE_URL", "http://192.168.1.2:8080/src.tar.gz")
	if SourceLink() == "" {
		t.Error("an http link is refused")
	}
}

// Commit is the ldflags value, else the revision the go tool stamped.
func TestCommit(t *testing.T) {
	old := Commit
	t.Cleanup(func() { Commit = old })
	Commit = "0123abcd"
	if BuildCommit() != "0123abcd" {
		t.Fatal(BuildCommit())
	}
}
