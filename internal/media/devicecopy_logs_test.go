package media

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// A comic zip's access time that cannot be kept (the sidecar cannot be
// made) is logged, not dropped: eviction would see a stale time.
func TestRecordZipAccessLogsErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "comics")
	os.MkdirAll(dir, 0o755)
	zip := filepath.Join(dir, "1-2.zip")
	os.WriteFile(zip, []byte("PK"), 0o644)
	os.Chmod(dir, 0o555) // the sidecar cannot be created
	defer os.Chmod(dir, 0o755)
	buf := captureLog(t)
	recordZipAccess(zip, time.Now())
	if !strings.Contains(buf.String(), "comic zip access time") {
		t.Fatalf("log: %q", buf.String())
	}
}

// A folder of device copies that cannot be listed at start-up is logged.
func TestDeviceCopiesLoadLogsReadDirErrors(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"comics", "subs"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	for _, d := range []string{root, filepath.Join(root, "comics"), filepath.Join(root, "subs")} {
		defer os.Chmod(d, 0o755)
	}
	os.Chmod(filepath.Join(root, "comics"), 0o311) // no read: ReadDir fails
	os.Chmod(filepath.Join(root, "subs"), 0o311)
	buf := captureLog(t)
	c := &DeviceCopies{Dir: root, jobs: map[string]*copyJob{}, files: map[string]*storedFile{}}
	c.load()
	out := buf.String()
	if !strings.Contains(out, filepath.Join(root, "comics")) || !strings.Contains(out, filepath.Join(root, "subs")) {
		t.Fatalf("log: %q", out)
	}
}
