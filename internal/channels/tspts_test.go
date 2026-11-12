package channels

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// tsPacket is one TS packet starting a PES with PTS pts (after an
// adaptation field when adapt is set).
func tsPacket(pts int64, adapt bool) []byte {
	p := make([]byte, 188)
	p[0], p[1], p[2] = 0x47, 0x41, 0x00
	i := 4
	if adapt {
		p[3] = 0x30
		p[4] = 7
		i = 4 + 1 + 7
	} else {
		p[3] = 0x10
	}
	h := p[i:]
	copy(h, []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 5})
	h[9] = byte(0x21 | (pts>>30&7)<<1)
	h[10] = byte(pts >> 22)
	h[11] = byte((pts>>15&0x7f)<<1 | 1)
	h[12] = byte(pts >> 7)
	h[13] = byte((pts&0x7f)<<1 | 1)
	return p
}

func TestParsePTS(t *testing.T) {
	pat := make([]byte, 188)
	copy(pat, []byte{0x47, 0x40, 0x00, 0x10, 0x00, 0x00, 0xB0, 0x0D})
	for _, pts := range []int64{0, 126000, ptsWrap - 1, 1<<32 + 12345} {
		for _, adapt := range []bool{false, true} {
			b := append(append([]byte{}, pat...), tsPacket(pts, adapt)...)
			got, err := parsePTS(b)
			if err != nil || got != pts {
				t.Fatalf("pts %d adapt %v: %d %v", pts, adapt, got, err)
			}
		}
	}
	if _, err := parsePTS(pat); err != errNoPTS {
		t.Fatal(err)
	}
	if d := ptsSince(ptsWrap-90000, 90000); d != 180000 {
		t.Fatalf("rollover %d", d)
	}
}

func TestFirstPTSOfAnFFmpegSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil || testing.Short() {
		t.Skip("no ffmpeg")
	}
	p := filepath.Join(t.TempDir(), "s.ts")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-output_ts_offset", "100", "-f", "mpegts", p).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	pts, err := firstPTS(p)
	if err != nil {
		t.Fatal(err)
	}
	// 100 s plus the muxer's 1.4 s delay
	if pts != 101.4*90000 {
		t.Fatalf("pts %d", pts)
	}
}
