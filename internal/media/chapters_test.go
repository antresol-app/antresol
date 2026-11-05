package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// writeChapteredM4B builds an AAC .m4b of len(titles) seconds with one
// chapter per second (skips without ffmpeg/ffprobe).
func writeChapteredM4B(t *testing.T, path string, titles []string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	meta := ";FFMETADATA1\ntitle=Книга\n"
	for i, ti := range titles {
		meta += "[CHAPTER]\nTIMEBASE=1/1000\nSTART=" + strconv.Itoa(i*1000) + "\nEND=" + strconv.Itoa((i+1)*1000) + "\ntitle=" + ti + "\n"
	}
	mf := filepath.Join(t.TempDir(), "meta.txt")
	if err := os.WriteFile(mf, []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration="+strconv.Itoa(max(1, len(titles))),
		"-i", mf, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1", "-ac", "1", "-c:a", "aac", "-b:a", "32k",
		"-f", "mp4", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

func TestChaptersFFprobe(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	p := filepath.Join(t.TempDir(), "concat:книга.m4b") // a protocol-like name stays a file
	writeChapteredM4B(t, p, []string{"Пролог", "Глава 1"})
	chs, err := Chapters(context.Background(), ffprobe, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 2 {
		t.Fatalf("chapters %+v", chs)
	}
	if chs[0].Title != "Пролог" || chs[0].StartMs != 0 || chs[0].EndMs == nil || *chs[0].EndMs != 1000 {
		t.Fatalf("first %+v", chs[0])
	}
	if chs[1].Title != "Глава 1" || chs[1].StartMs != 1000 || chs[1].EndMs == nil || *chs[1].EndMs != 2000 {
		t.Fatalf("second %+v", chs[1])
	}
	// a file without chapters: none, no error
	plain := filepath.Join(t.TempDir(), "plain.m4b")
	writeChapteredM4B(t, plain, nil)
	if chs, err := Chapters(context.Background(), ffprobe, plain); err != nil || len(chs) != 0 {
		t.Fatalf("plain %+v %v", chs, err)
	}
	if _, err := Chapters(context.Background(), ffprobe, "http://192.168.1.5/x.m4b"); err == nil {
		t.Fatal("remote url accepted")
	}
}
