package addons

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The helpers the external tests (package addons_test) share with this
// package's own.

// FakeAddonBin is the built fake addon (testdata/fakeaddon).
func FakeAddonBin(t *testing.T) string { return fakeAddon(t) }

// WriteLocalAddon puts a local release of m into dev/<m.ID> (a
// VAULT_ADDONS_DEV folder): its platform archive's bin/addon runs the fake
// addon in mode. m's Platforms and Contract are filled in.
func WriteLocalAddon(t *testing.T, dev string, m Manifest, mode string) {
	t.Helper()
	dir := filepath.Join(dev, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nFAKE_MODE=%s exec %s\n", mode, fakeAddon(t))
	b := makeTGZ(t, []tent{
		{name: "bin/", typ: tar.TypeDir, mode: 0o755},
		{name: "bin/addon", typ: tar.TypeReg, mode: 0o755, body: script},
	})
	if err := os.WriteFile(filepath.Join(dir, "addon.tar.gz"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	m.Contract = ContractVersion
	m.Platforms = map[string]Platform{runtime.GOOS + "/" + runtime.GOARCH: {Asset: "addon.tar.gz", SHA256: hex.EncodeToString(sum[:])}}
	mb, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), mb, 0o644); err != nil {
		t.Fatal(err)
	}
}

// UpdateAvailable returns the version of the source's latest release when
// it is newer than the installed one.
func (m *Manager) UpdateAvailable(ctx context.Context, id string) (string, bool) {
	c := m.CheckUpdate(ctx, id, false)
	return c.Version, c.Version != ""
}
