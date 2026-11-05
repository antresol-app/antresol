package webui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFSHasIndexWithRoot(t *testing.T) {
	b, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `<div id="root"></div>`) {
		t.Fatalf("index.html lacks root div: %s", b)
	}
}
