package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var addonOnly = []string{"bin/addon"}

func archive(t *testing.T, path string, hdrs ...*tar.Header) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range hdrs {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(make([]byte, h.Size))
		}
	}
	tw.Close()
	gz.Close()
}

func reg(name string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: 4, Format: tar.FormatUSTAR}
}

func setup(t *testing.T) (src, dir string) {
	dir = t.TempDir()
	src = filepath.Join(dir, "src.json")
	os.WriteFile(src, []byte(`{"id":"demo","version":"1.2.3","contract":1,"platforms":{}}`), 0o644)
	return src, dir
}

func TestWritesPlatformsAndWebWithTheirSums(t *testing.T) {
	src, dir := setup(t)
	archive(t, filepath.Join(dir, "demo-linux-amd64.tar.gz"), &tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755}, reg("bin/addon"))
	archive(t, filepath.Join(dir, "demo-web.tar.gz"), &tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}, reg("./index.js"))
	if err := run(src, dir, "v1.2.3", addonOnly); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "antresol-addon.json"))
	var m struct {
		Platforms map[string]asset `json:"platforms"`
		Web       asset            `json:"web"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "demo-linux-amd64.tar.gz"))
	sum := sha256.Sum256(raw)
	if p := m.Platforms["linux/amd64"]; p.Asset != "demo-linux-amd64.tar.gz" || p.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("platform: %+v", m.Platforms)
	}
	if m.Web.Asset != "demo-web.tar.gz" || len(m.Web.SHA256) != 64 {
		t.Fatalf("web: %+v", m.Web)
	}
}

func TestRefusesWhatTheCoreRefuses(t *testing.T) {
	for name, hdrs := range map[string][]*tar.Header{
		"link":   {reg("bin/addon"), {Name: "bin/x", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		"pax":    {{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "x"}, Format: tar.FormatPAX}, reg("bin/addon")},
		"apple":  {reg("bin/addon"), reg("bin/._addon")},
		"no bin": {reg("addon")},
	} {
		t.Run(name, func(t *testing.T) {
			src, dir := setup(t)
			archive(t, filepath.Join(dir, "demo-linux-amd64.tar.gz"), hdrs...)
			if err := run(src, dir, "", addonOnly); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestTagMustMatchVersion(t *testing.T) {
	src, dir := setup(t)
	archive(t, filepath.Join(dir, "demo-linux-amd64.tar.gz"), reg("bin/addon"))
	if err := run(src, dir, "v1.2.4", addonOnly); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err %v", err)
	}
}

// -require names the files a platform archive must hold besides bin/addon
// (the trailers addon's pinned yt-dlp).
func TestRequiredFilesMustBeInEveryPlatformArchive(t *testing.T) {
	need := []string{"bin/addon", "bin/tool"}
	src, dir := setup(t)
	archive(t, filepath.Join(dir, "demo-linux-amd64.tar.gz"), reg("bin/addon"))
	if err := run(src, dir, "", need); err == nil || !strings.Contains(err.Error(), "no bin/tool") {
		t.Fatalf("without the tool: %v", err)
	}
	archive(t, filepath.Join(dir, "demo-linux-amd64.tar.gz"), reg("bin/addon"), reg("bin/tool"))
	if err := run(src, dir, "", need); err != nil {
		t.Fatal(err)
	}
}

// minCore, the oldest server a release runs on, is carried into the release
// manifest as written; a malformed one stops the release.
func TestMinCoreIsKeptAndChecked(t *testing.T) {
	write := func(src, extra string) {
		os.WriteFile(src, []byte(`{"id":"demo","version":"1.2.3","contract":1,`+extra+`"platforms":{}}`), 0o644)
	}
	src, dir := setup(t)
	archive(t, filepath.Join(dir, "demo-linux-amd64.tar.gz"), reg("bin/addon"))

	write(src, `"minCore":"0.8.28",`)
	if err := run(src, dir, "v1.2.3", addonOnly); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "antresol-addon.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["minCore"] != "0.8.28" {
		t.Fatalf("minCore in the release manifest: %v (%v)", m["minCore"], err)
	}

	write(src, "") // none: none is written
	if err := run(src, dir, "v1.2.3", addonOnly); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "antresol-addon.json"))
	if strings.Contains(string(b), "minCore") {
		t.Fatalf("minCore invented: %s", b)
	}

	for _, bad := range []string{`"0.8"`, `"v0.8.28"`, `"0.8.28-rc.1"`, `""`, `8`, `null`, `"latest"`} {
		write(src, `"minCore":`+bad+`,`)
		if err := run(src, dir, "v1.2.3", addonOnly); err == nil || !strings.Contains(err.Error(), "minCore") {
			t.Errorf("minCore %s: %v", bad, err)
		}
	}
}
