package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// mbox is an MP4 box.
func mbox(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], typ)
	return append(b, body...)
}

func u32s(v ...uint32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.BigEndian.PutUint32(b[i*4:], x)
	}
	return b
}

// mp4With is a minimal MP4 whose video track's sample tables are the given
// boxes (mvhd and mdhd are replaced when given).
func mp4With(mvhd, mdhd []byte, stbl ...[]byte) []byte {
	if mvhd == nil {
		mvhd = mbox("mvhd", u32s(0, 0, 0, 1000, 10000))
	}
	if mdhd == nil {
		mdhd = mbox("mdhd", u32s(0, 0, 0, 1000, 10000))
	}
	hdlr := mbox("hdlr", u32s(0, 0), []byte("vide"), u32s(0, 0, 0))
	trak := mbox("trak", mbox("mdia", mdhd, hdlr, mbox("minf", mbox("stbl", stbl...))))
	return append(mbox("ftyp", []byte("isom"), u32s(0)), mbox("moov", mvhd, trak)...)
}

// ebml is a Matroska element with a 4-byte size (or 8 for big payloads).
func ebml(id uint32, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	var idb []byte
	switch {
	case id > 0xFFFFFF:
		idb = []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xFFFF:
		idb = []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xFF:
		idb = []byte{byte(id >> 8), byte(id)}
	default:
		idb = []byte{byte(id)}
	}
	size := make([]byte, 8)
	binary.BigEndian.PutUint64(size, uint64(len(body)))
	size[0] = 0x01 // 8-byte size
	return append(append(idb, size...), body...)
}

// mkvWith is a minimal Matroska file: Info, Tracks (video track 1), Cues
// and a SeekHead pointing at them.
func mkvWith(info, cues []byte) []byte {
	tracks := ebml(mkvTracks, ebml(mkvTrackEntry, ebml(mkvTrackNumber, []byte{1}), ebml(mkvTrackType, []byte{1})))
	body := append(append(append([]byte{}, info...), tracks...), cues...)
	head := ebml(0x1A45DFA3, ebml(0x4282, []byte("matroska")))
	return append(head, ebml(mkvSegment, body)...)
}

func goodInfo() []byte {
	d := make([]byte, 8)
	binary.BigEndian.PutUint64(d, math.Float64bits(60_000))
	return ebml(mkvInfo, ebml(mkvTimecodeScale, []byte{0x0F, 0x42, 0x40}), ebml(mkvDuration, d))
}

func malformedInputs(t testing.TB) map[string][]byte {
	stts10 := mbox("stts", u32s(0, 1, 10, 1000))
	stss1 := mbox("stss", u32s(0, 1, 1))
	inputs := map[string][]byte{
		// the review's verified hang: ctts entries of 2^32-1 samples
		"ctts huge counts": mp4With(nil, nil, stts10, stss1, mbox("ctts", u32s(0, 4, 0xFFFFFFFF, 1, 0xFFFFFFFF, 1, 0xFFFFFFFF, 1, 0xFFFFFFFF, 1))),
		// the review's verified panics
		"empty mvhd":        mp4With(mbox("mvhd"), nil, stts10, stss1),
		"empty mdhd":        mp4With(nil, mbox("mdhd"), stts10, stss1),
		"short stss":        mp4With(nil, nil, stts10, mbox("stss", []byte{0, 0})),
		"stss count":        mp4With(nil, nil, stts10, mbox("stss", u32s(0, 0x7FFFFFFF, 1))),
		"stts count":        mp4With(nil, nil, mbox("stts", u32s(0, 0x7FFFFFFF, 1, 1)), stss1),
		"stts samples":      mp4With(nil, nil, mbox("stts", u32s(0, 3, 0xFFFFFFFF, 1, 0xFFFFFFFF, 1, 0xFFFFFFFF, 1)), stss1),
		"stss past the end": mp4With(nil, nil, stts10, mbox("stss", u32s(0, 2, 1, 999))),
		"stss decreasing":   mp4With(nil, nil, stts10, mbox("stss", u32s(0, 2, 5, 3))),
		"box size overflow": append(mbox("ftyp", []byte("isom")), []byte{0, 0, 0, 1, 'm', 'o', 'o', 'v', 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}...),
		"mkv NaN duration":  mkvWith(ebml(mkvInfo, ebml(mkvDuration, u32s(0x7FC00000))), ebml(mkvCues)),
		"mkv element size":  append(ebml(0x1A45DFA3), 0x18, 0x53, 0x80, 0x67, 0x01, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE),
		"truncated":         mp4With(nil, nil, stts10, stss1)[:40],
	}
	// the review's memory blow-up: 16 MiB of empty CuePoints
	empty := bytes.Repeat([]byte{mkvCuePoint, 0x80}, 8<<20)
	inputs["mkv 16 MiB of empty cue points"] = mkvWith(goodInfo(), ebml(mkvCues, empty))
	return inputs
}

// Malformed and hostile files never hang, panic or take much memory: they
// are "no index" (and /api/play falls back to the EVENT playlist).
func TestKeyIndexMalformedInputs(t *testing.T) {
	for name, data := range malformedInputs(t) {
		t.Run(name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			start := time.Now()
			done := make(chan error, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						done <- errors.New("panic")
					}
				}()
				_, err := keyIndexFrom(&budgetReader{r: bytesReaderAt(data), deadline: time.Now().Add(keyIndexBudget)}, int64(len(data)))
				done <- err
			}()
			select {
			case err := <-done:
				// an index from what is sound (an optional mvhd, ctts runs
				// clamped to the samples) or "no index"; never anything else
				usable := name == "ctts huge counts" || name == "empty mvhd"
				if (err == nil && !usable) || (err != nil && (err.Error() == "panic" || !errors.Is(err, ErrNoKeyIndex))) {
					t.Fatalf("want ErrNoKeyIndex, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("hangs")
			}
			runtime.ReadMemStats(&after)
			if took := time.Since(start); took > 2*time.Second {
				t.Fatalf("took %v", took)
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
				t.Fatalf("allocated %d MiB", alloc>>20)
			}
		})
	}
	// through the file API too: the recover makes any panic "no index"
	p := filepath.Join(t.TempDir(), "x.mp4")
	os.WriteFile(p, malformedInputs(t)["empty mdhd"], 0o644)
	if _, err := ReadKeyIndex(p); !errors.Is(err, ErrNoKeyIndex) {
		t.Fatal(err)
	}
}

// A slow disk or an I/O error is no verdict: not cached, the next play
// tries again.
func TestKeyIndexCacheOnlyVerdicts(t *testing.T) {
	ff := needFFmpeg(t)
	clip := irregularClip(t, ff, "mkv", 12)
	c := KeyIndexCache{Dir: t.TempDir()}
	old := keyIndexBudget
	keyIndexBudget = -time.Second // every read is past the budget
	_, built, err := c.Get(clip)
	keyIndexBudget = old
	if !built || !errors.Is(err, errIndexTooSlow) || errors.Is(err, ErrNoKeyIndex) {
		t.Fatalf("slow: %v %v", built, err)
	}
	if es, _ := os.ReadDir(c.Dir); len(es) != 0 {
		t.Fatalf("a slow read was cached: %d entries", len(es))
	}
	if ki, built, err := c.Get(clip); err != nil || !built || len(ki.Keyframes) == 0 {
		t.Fatalf("retry: %v %v", built, err)
	}
	// an I/O error (a directory is not readable as a file) is not cached either
	dir := filepath.Join(t.TempDir(), "film.mkv")
	os.Mkdir(dir, 0o755)
	if _, _, err := c.Get(dir); err == nil || errors.Is(err, ErrNoKeyIndex) {
		t.Fatalf("dir: %v", err)
	}
}

// FuzzKeyIndex: any bytes parse to an index or ErrNoKeyIndex, without a
// panic (keyIndexFrom has no recover), quickly.
func FuzzKeyIndex(f *testing.F) {
	for _, data := range malformedInputs(f) {
		if len(data) < 1<<20 {
			f.Add(data)
		}
	}
	f.Add(mp4With(nil, nil, mbox("stts", u32s(0, 1, 10, 1000)), mbox("stss", u32s(0, 2, 1, 6)),
		mbox("ctts", u32s(0, 1, 10, 500)), mbox("edts", mbox("elst", u32s(0, 1, 1000, 0, 0x10000)))))
	if ff, err := exec.LookPath("ffmpeg"); err == nil && !testing.Short() {
		for _, ext := range []string{"mp4", "mkv"} {
			out := filepath.Join(f.TempDir(), "seed."+ext)
			if exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=10", "-t", "3",
				"-c:v", "libx264", "-g", "7", out).Run() == nil {
				if b, err := os.ReadFile(out); err == nil {
					f.Add(b)
				}
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		ki, err := keyIndexFrom(&budgetReader{r: bytesReaderAt(data), deadline: time.Now().Add(time.Second)}, int64(len(data)))
		if err != nil && !errors.Is(err, ErrNoKeyIndex) && !errors.Is(err, errIndexTooSlow) {
			t.Fatalf("unexpected error kind: %v", err)
		}
		if err == nil && (len(ki.Keyframes) == 0 || !(ki.Duration > 0)) {
			t.Fatalf("an empty index without an error: %+v", ki)
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Fatalf("took %v", took)
		}
	})
}
