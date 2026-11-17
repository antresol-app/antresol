package addons

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type tent struct {
	name, link string
	typ        byte
	mode       int64
	body       string
}

func makeTGZ(t *testing.T, ents []tent) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Linkname: e.link}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUnpack_RefusesEscapes(t *testing.T) {
	cases := map[string][]tent{
		"dotdot":   {{name: "ok.txt", typ: tar.TypeReg, mode: 0o644, body: "a"}, {name: "../x", typ: tar.TypeReg, mode: 0o644, body: "x"}},
		"abs":      {{name: "/abs", typ: tar.TypeReg, mode: 0o644, body: "x"}},
		"symlink":  {{name: "l", link: "../../etc", typ: tar.TypeSymlink}},
		"hardlink": {{name: "h", link: "/etc/passwd", typ: tar.TypeLink}},
		"chain": {
			{name: "l1", link: ".", typ: tar.TypeSymlink},
			{name: "l2", link: "l1/..", typ: tar.TypeSymlink},
			{name: "l2/x", typ: tar.TypeReg, mode: 0o644, body: "x"},
		},
		"fifo": {{name: "f", typ: tar.TypeFifo}},
	}
	for name, ents := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			dst := filepath.Join(base, "out")
			if err := Unpack(bytes.NewReader(makeTGZ(t, ents)), dst); err == nil {
				t.Fatal("want error")
			}
			left, _ := os.ReadDir(base)
			if len(left) != 0 {
				t.Fatalf("left behind: %v", left)
			}
		})
	}
}

func TestUnpack_KeepsExecBit(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out")
	err := Unpack(bytes.NewReader(makeTGZ(t, []tent{
		{name: "bin/", typ: tar.TypeDir, mode: 0o755},
		{name: "bin/run", typ: tar.TypeReg, mode: 0o755, body: "#!/bin/sh\n"},
		{name: "data.txt", typ: tar.TypeReg, mode: 0o644, body: "d"},
	})), dst)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dst, "bin", "run"))
	if err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("exec bit lost: %v %v", st, err)
	}
	st, _ = os.Stat(filepath.Join(dst, "data.txt"))
	if st.Mode().Perm()&0o111 != 0 {
		t.Fatal("unexpected exec bit")
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("tmp left")
	}
}

func TestUnpack_CapsEntries(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i <= maxEntries; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	base := t.TempDir()
	err := Unpack(bytes.NewReader(buf.Bytes()), filepath.Join(base, "out"))
	if err != errTooBig {
		t.Fatalf("err = %v, want %v", err, errTooBig)
	}
	if left, _ := os.ReadDir(base); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}
