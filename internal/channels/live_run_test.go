package channels

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What ffmpeg says on stderr is read (a stall's log line) while exec's own
// goroutine still copies it in: that is a data race unless limited locks.
// Run with -race.
func TestFFmpegStderrReadWhileItRuns(t *testing.T) {
	l := &Live{FFmpeg: "/bin/sh"}
	stderr, exited, err := l.startFFmpeg(context.Background(),
		[]string{"-c", "for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do echo word$i >&2; sleep 0.01; done"})
	if err != nil {
		t.Fatal(err)
	}
	for read := false; ; {
		select {
		case err := <-exited:
			if err != nil {
				t.Fatal(err)
			}
			if got := stderr.String(); !strings.HasPrefix(got, "word1\nword2") || !strings.HasSuffix(got, "word20") {
				t.Fatalf("stderr: %q", got)
			}
			if !read {
				t.Fatal("never read while it ran")
			}
			return
		default:
			_ = stderr.String()
			read = true
			time.Sleep(time.Millisecond)
		}
	}
}

func TestLimitedKeepsTheFirstBytes(t *testing.T) {
	w := &limited{n: 8}
	w.Write([]byte("  hello "))
	if n, _ := w.Write([]byte("world")); n != 5 {
		t.Fatalf("a write reports what was given: %d", n)
	}
	if got := w.String(); got != "hello" {
		t.Fatalf("kept %q", got)
	}
}

// A playlist that has not changed since it was read is not read again.
func TestFileStampReadsOnlyChangedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.m3u8")
	var st fileStamp
	if _, ok := st.readChanged(path); ok {
		t.Fatal("a missing file was read")
	}
	if err := os.WriteFile(path, []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, ok := st.readChanged(path); !ok || string(b) != "#EXTM3U\n" {
		t.Fatalf("first read: %q %v", b, ok)
	}
	if _, ok := st.readChanged(path); ok {
		t.Fatal("an unchanged file was read again")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("#EXTINF:6.0,\nseg1.ts\n")
	f.Close()
	if b, ok := st.readChanged(path); !ok || !strings.HasSuffix(string(b), "seg1.ts\n") {
		t.Fatalf("grown file: %q %v", b, ok)
	}
}
