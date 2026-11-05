package media

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// KeyIndex is where a video's keyframes are, read from the container's own
// index (MP4 sample tables, Matroska Cues) without reading the media data:
// a remux can then list its whole film with exact segment lengths
// (hls_remux.go). Times are presentation seconds, as ffmpeg's -ss counts.
//
// The files are untrusted (downloaded files): every count is checked
// against the bytes it describes, tables and elements are capped, the work
// has a deadline, and a panic becomes "no index", so a play request always
// falls back to the EVENT playlist.
type KeyIndex struct {
	Keyframes []float64 `json:"keyframes"`
	// Duration of the video track (or the file), seconds.
	Duration float64 `json:"duration"`
}

// ErrNoKeyIndex: the file has no usable keyframe index (fragmented MP4,
// Matroska without Cues, another container, a malformed file): a verdict
// on the file, cached. Other errors (a slow disk, I/O) are not.
var ErrNoKeyIndex = errors.New("media: no keyframe index")

// errIndexTooSlow: building the index ran past its budget (a disk spinning
// up): no verdict on the file, try again next time.
var errIndexTooSlow = errors.New("media: reading the keyframe index takes too long")

// keyIndexBudget bounds building an index (reads and work).
var keyIndexBudget = 3 * time.Second

const (
	// maxKeyframeGap: keyframes further apart are too coarse for the player.
	maxKeyframeGap = 30.0
	// maxTable bounds one MP4 sample table or Matroska element read whole
	// (a 3 h film's tables and Cues are a few MB).
	maxTable = 32 << 20
	// maxSamples bounds an MP4 video track (3 h at 120 fps is 1.3 M).
	maxSamples = 5_000_000
	// maxChildren bounds the elements or boxes walked at one level.
	maxChildren = 1 << 20
	// maxKeyframes bounds the keyframes kept (1 per 10 ms over 10 h).
	maxKeyframes = 4_000_000
)

func noIndex(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNoKeyIndex, fmt.Sprintf(format, a...))
}

// ReadKeyIndex reads the keyframe index of an MP4/MOV or Matroska/WebM file.
func ReadKeyIndex(path string) (ki KeyIndex, err error) {
	defer func() {
		if p := recover(); p != nil {
			ki, err = KeyIndex{}, noIndex("malformed file (%v)", p)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return KeyIndex{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return KeyIndex{}, err
	}
	return keyIndexFrom(&budgetReader{r: f, deadline: time.Now().Add(keyIndexBudget)}, st.Size())
}

// keyIndexFrom parses an index from r (size bytes); no recover, so the
// fuzz test sees any panic.
func keyIndexFrom(r io.ReaderAt, size int64) (KeyIndex, error) {
	head, err := readFull(r, 0, 8)
	if err != nil {
		return KeyIndex{}, err
	}
	var ki KeyIndex
	switch {
	case binary.BigEndian.Uint32(head[:4]) == 0x1A45DFA3:
		ki, err = mkvKeyIndex(r, size)
	case string(head[4:8]) == "ftyp" || string(head[4:8]) == "moov" || string(head[4:8]) == "free" || string(head[4:8]) == "wide":
		ki, err = mp4KeyIndex(r, size)
	default:
		return KeyIndex{}, noIndex("not MP4 or Matroska")
	}
	if err != nil {
		return KeyIndex{}, err
	}
	if len(ki.Keyframes) > maxKeyframes {
		return KeyIndex{}, noIndex("%d keyframes", len(ki.Keyframes))
	}
	sort.Float64s(ki.Keyframes)
	out := ki.Keyframes[:0]
	for _, k := range ki.Keyframes {
		if math.IsNaN(k) || math.IsInf(k, 0) {
			continue
		}
		if len(out) == 0 || k-out[len(out)-1] > 1e-4 {
			out = append(out, k)
		}
	}
	ki.Keyframes = out
	if len(ki.Keyframes) == 0 || !(ki.Duration > 0) || math.IsInf(ki.Duration, 0) {
		return KeyIndex{}, noIndex("no keyframes or duration")
	}
	prev := ki.Keyframes[0]
	for _, k := range append(ki.Keyframes[1:], ki.Duration) {
		if k-prev > maxKeyframeGap {
			return KeyIndex{}, noIndex("keyframes %.1f s apart", k-prev)
		}
		prev = k
	}
	return ki, nil
}

// budgetReader bounds the reads and the work of building one index.
type budgetReader struct {
	r        io.ReaderAt
	deadline time.Time
	n        int
}

func (b *budgetReader) ReadAt(p []byte, off int64) (int, error) {
	if time.Now().After(b.deadline) {
		return 0, errIndexTooSlow
	}
	return b.r.ReadAt(p, off)
}

// tick is called in long loops: every 4096th call checks the deadline.
func (b *budgetReader) tick() error {
	b.n++
	if b.n%4096 == 0 && time.Now().After(b.deadline) {
		return errIndexTooSlow
	}
	return nil
}

// tick checks the budget when r has one (gop_pts reads ffmpeg's own files).
func tick(r io.ReaderAt) error {
	if b, ok := r.(*budgetReader); ok {
		return b.tick()
	}
	return nil
}

// readFull reads n bytes at off. A short read (the file ends) is a verdict;
// other errors (I/O, the budget) are passed on as they are.
func readFull(r io.ReaderAt, off, n int64) ([]byte, error) {
	if n < 0 || n > maxTable || off < 0 {
		return nil, noIndex("element of %d bytes", n)
	}
	b := make([]byte, n)
	if n == 0 {
		return b, nil
	}
	got, err := r.ReadAt(b, off)
	if err != nil && !(errors.Is(err, io.EOF) && got == len(b)) {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, noIndex("truncated at %d", off)
		}
		return nil, err
	}
	return b, nil
}

// MARK: MP4

type box struct {
	typ        string
	start, end int64 // the payload
}

// boxes lists the boxes in [off, end).
func boxes(r io.ReaderAt, off, end int64) ([]box, error) {
	var out []box
	for off+8 <= end {
		if len(out) >= maxChildren {
			return nil, noIndex("too many boxes")
		}
		if err := tick(r); err != nil {
			return nil, err
		}
		h, err := readFull(r, off, 8)
		if err != nil {
			return nil, err
		}
		size, typ, hl := int64(binary.BigEndian.Uint32(h[:4])), string(h[4:8]), int64(8)
		switch size {
		case 1:
			x, err := readFull(r, off+8, 8)
			if err != nil {
				return nil, err
			}
			u := binary.BigEndian.Uint64(x)
			if u > math.MaxInt64 {
				return nil, noIndex("box %q of %d bytes", typ, u)
			}
			size, hl = int64(u), 16
		case 0:
			size = end - off
		}
		if size < hl || size > end-off {
			return nil, noIndex("box %q of %d bytes", typ, size)
		}
		out = append(out, box{typ, off + hl, off + size})
		off += size
	}
	return out, nil
}

func child(r io.ReaderAt, parent box, typ string) (box, bool, error) {
	bs, err := boxes(r, parent.start, parent.end)
	if err != nil {
		return box{}, false, err
	}
	for _, b := range bs {
		if b.typ == typ {
			return b, true, nil
		}
	}
	return box{}, false, nil
}

func boxPath(r io.ReaderAt, parent box, typs ...string) (box, bool, error) {
	b := parent
	for _, t := range typs {
		var ok bool
		var err error
		if b, ok, err = child(r, b, t); !ok || err != nil {
			return box{}, false, err
		}
	}
	return b, true, nil
}

// fullBoxTimescale reads the timescale of an mvhd/mdhd payload.
func fullBoxTimescale(b []byte) (uint32, bool) {
	switch {
	case len(b) >= 24 && b[0] == 1:
		return binary.BigEndian.Uint32(b[20:24]), true
	case len(b) >= 16 && b[0] == 0:
		return binary.BigEndian.Uint32(b[12:16]), true
	}
	return 0, false
}

func readBox(r io.ReaderAt, b box, limit int64) ([]byte, error) {
	return readFull(r, b.start, min(b.end-b.start, limit))
}

func mp4KeyIndex(r io.ReaderAt, size int64) (KeyIndex, error) {
	top, err := boxes(r, 0, size)
	if err != nil {
		return KeyIndex{}, err
	}
	var moov box
	found := false
	for _, b := range top {
		if b.typ == "moov" {
			moov, found = b, true
		}
	}
	if !found {
		return KeyIndex{}, noIndex("no moov")
	}
	if _, ok, err := child(r, moov, "mvex"); err != nil {
		return KeyIndex{}, err
	} else if ok {
		return KeyIndex{}, noIndex("fragmented MP4")
	}
	var movieScale uint32
	if mvhd, ok, err := child(r, moov, "mvhd"); err != nil {
		return KeyIndex{}, err
	} else if ok {
		b, err := readBox(r, mvhd, 32)
		if err != nil {
			return KeyIndex{}, err
		}
		movieScale, _ = fullBoxTimescale(b)
	}
	traks, err := boxes(r, moov.start, moov.end)
	if err != nil {
		return KeyIndex{}, err
	}
	for _, t := range traks {
		if t.typ != "trak" {
			continue
		}
		hdlr, ok, err := boxPath(r, t, "mdia", "hdlr")
		if err != nil {
			return KeyIndex{}, err
		}
		if !ok {
			continue
		}
		h, err := readBox(r, hdlr, 12)
		if err != nil {
			return KeyIndex{}, err
		}
		if len(h) < 12 || string(h[8:12]) != "vide" {
			continue
		}
		return mp4Track(r, t, movieScale)
	}
	return KeyIndex{}, noIndex("no video track")
}

// table reads a sample table box of stbl and checks its entry count
// against its size; nil when the box is absent.
func table(r io.ReaderAt, stbl box, typ string, entrySize int) ([]byte, int, error) {
	bx, ok, err := child(r, stbl, typ)
	if err != nil || !ok {
		return nil, 0, err
	}
	b, err := readFull(r, bx.start, bx.end-bx.start)
	if err != nil {
		return nil, 0, err
	}
	if len(b) < 8 {
		return nil, 0, noIndex("short %s", typ)
	}
	n := uint64(binary.BigEndian.Uint32(b[4:8]))
	if n*uint64(entrySize) > uint64(len(b)-8) {
		return nil, 0, noIndex("%s lists %d entries in %d bytes", typ, n, len(b))
	}
	return b, int(n), nil
}

func mp4Track(r io.ReaderAt, trak box, movieScale uint32) (KeyIndex, error) {
	mdhd, ok, err := boxPath(r, trak, "mdia", "mdhd")
	if err != nil {
		return KeyIndex{}, err
	}
	if !ok {
		return KeyIndex{}, noIndex("no mdhd")
	}
	b, err := readBox(r, mdhd, 32)
	if err != nil {
		return KeyIndex{}, err
	}
	scale, ok := fullBoxTimescale(b)
	if !ok || scale == 0 {
		return KeyIndex{}, noIndex("no timescale")
	}
	stbl, ok, err := boxPath(r, trak, "mdia", "minf", "stbl")
	if err != nil {
		return KeyIndex{}, err
	}
	if !ok {
		return KeyIndex{}, noIndex("no stbl")
	}
	stts, sttsN, err := table(r, stbl, "stts", 8)
	if err != nil {
		return KeyIndex{}, err
	}
	if stts == nil {
		return KeyIndex{}, noIndex("no stts")
	}
	stss, stssN, err := table(r, stbl, "stss", 4)
	if err != nil {
		return KeyIndex{}, err
	}
	if stss == nil {
		return KeyIndex{}, noIndex("no sync sample table") // every frame a keyframe
	}
	ctts, cttsN, err := table(r, stbl, "ctts", 8)
	if err != nil {
		return KeyIndex{}, err
	}
	var samples uint64
	for i := range sttsN {
		samples += uint64(binary.BigEndian.Uint32(stts[8+i*8:]))
		if samples > maxSamples {
			return KeyIndex{}, noIndex("more than %d samples", maxSamples)
		}
	}
	if stssN > maxKeyframes {
		return KeyIndex{}, noIndex("%d sync samples", stssN)
	}
	shift, err := mp4EditShift(r, trak, scale, movieScale)
	if err != nil {
		return KeyIndex{}, err
	}
	// Walk the run-length tables once alongside the (increasing) sync
	// sample numbers: no per-sample arrays.
	ki := KeyIndex{Keyframes: make([]float64, 0, stssN)}
	var si, sLeft int  // stts run and samples left in it
	var sNo uint64 = 1 // the sample at the walk's position (1-based)
	var dts uint64     // its decode time
	var ci, cLeft int  // ctts run, samples left
	var cNo uint64 = 1 // the first sample of the current ctts run
	if sttsN > 0 {
		sLeft = int(binary.BigEndian.Uint32(stts[8:]))
	}
	if cttsN > 0 {
		cLeft = int(binary.BigEndian.Uint32(ctts[8:]))
	}
	var last uint64
	for k := range stssN {
		if err := tick(r); err != nil {
			return KeyIndex{}, err
		}
		want := uint64(binary.BigEndian.Uint32(stss[8+k*4:]))
		if want == 0 || want > samples || want <= last {
			return KeyIndex{}, noIndex("sync sample %d (of %d)", want, samples)
		}
		last = want
		// decode time: advance whole stts runs, then within one
		for sNo+uint64(sLeft) <= want {
			if err := tick(r); err != nil {
				return KeyIndex{}, err
			}
			delta := uint64(binary.BigEndian.Uint32(stts[12+si*8:]))
			dts += uint64(sLeft) * delta
			sNo += uint64(sLeft)
			si++
			if si >= sttsN {
				return KeyIndex{}, noIndex("stts ends before sample %d", want)
			}
			sLeft = int(binary.BigEndian.Uint32(stts[8+si*8:]))
		}
		delta := uint64(binary.BigEndian.Uint32(stts[12+si*8:]))
		step := want - sNo
		dts += step * delta
		sNo = want
		sLeft -= int(step)
		// composition offset of that sample
		var cto int64
		for cttsN > 0 && ci < cttsN && cNo+uint64(cLeft) <= want {
			if err := tick(r); err != nil {
				return KeyIndex{}, err
			}
			cNo += uint64(cLeft)
			ci++
			if ci < cttsN {
				cLeft = int(binary.BigEndian.Uint32(ctts[8+ci*8:]))
			}
		}
		if cttsN > 0 && ci < cttsN {
			raw := binary.BigEndian.Uint32(ctts[12+ci*8:])
			cto = int64(raw)
			if ctts[0] == 1 {
				cto = int64(int32(raw))
			}
		}
		ki.Keyframes = append(ki.Keyframes, math.Max(0, (float64(dts)+float64(cto))/float64(scale)+shift))
	}
	// the track's end: the decode time after the last sample
	for ; si < sttsN; si++ {
		if err := tick(r); err != nil {
			return KeyIndex{}, err
		}
		dts += uint64(sLeft) * uint64(binary.BigEndian.Uint32(stts[12+si*8:]))
		if si+1 < sttsN {
			sLeft = int(binary.BigEndian.Uint32(stts[8+(si+1)*8:]))
		}
	}
	ki.Duration = float64(dts)/float64(scale) + shift
	return ki, nil
}

// mp4EditShift is the edit list's effect on presentation times: an empty
// edit delays, the first media edit's media_time skips.
func mp4EditShift(r io.ReaderAt, trak box, scale, movieScale uint32) (float64, error) {
	elst, ok, err := boxPath(r, trak, "edts", "elst")
	if err != nil || !ok {
		return 0, err
	}
	e, err := readFull(r, elst.start, elst.end-elst.start)
	if err != nil {
		return 0, err
	}
	if len(e) < 8 {
		return 0, nil
	}
	version, cnt := e[0], int(binary.BigEndian.Uint32(e[4:8]))
	var shift float64
	pos := 8
	for i := 0; i < cnt && i < 64; i++ {
		var dur uint64
		var mt int64
		if version == 1 {
			if pos+20 > len(e) {
				break
			}
			dur, mt = binary.BigEndian.Uint64(e[pos:]), int64(binary.BigEndian.Uint64(e[pos+8:]))
			pos += 20
		} else {
			if pos+12 > len(e) {
				break
			}
			dur, mt = uint64(binary.BigEndian.Uint32(e[pos:])), int64(int32(binary.BigEndian.Uint32(e[pos+4:])))
			pos += 12
		}
		if mt == -1 { // empty edit
			if movieScale > 0 {
				shift += float64(dur) / float64(movieScale)
			}
			continue
		}
		shift -= float64(mt) / float64(scale)
		break
	}
	return shift, nil
}

// MARK: Matroska

const (
	mkvSegment       = 0x18538067
	mkvSeekHead      = 0x114D9B74
	mkvSeek          = 0x4DBB
	mkvSeekID        = 0x53AB
	mkvSeekPosition  = 0x53AC
	mkvInfo          = 0x1549A966
	mkvTimecodeScale = 0x2AD7B1
	mkvDuration      = 0x4489
	mkvTracks        = 0x1654AE6B
	mkvTrackEntry    = 0xAE
	mkvTrackNumber   = 0xD7
	mkvTrackType     = 0x83
	mkvCues          = 0x1C53BB6B
	mkvCuePoint      = 0xBB
	mkvCueTime       = 0xB3
	mkvCuePositions  = 0xB7
	mkvCueTrack      = 0xF7
	mkvCluster       = 0x1F43B675
)

// ebmlVint reads an element ID (marker kept) or size (marker dropped).
func ebmlVint(b []byte, keepMarker bool) (uint64, int, bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	n := 1
	for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 || len(b) < n {
		return 0, 0, false
	}
	v := uint64(b[0])
	if !keepMarker {
		v &= uint64(0xFF >> n)
	}
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, n, true
}

type element struct {
	id         uint64
	start, end int64 // the payload; end -1 = unknown size
}

func readElement(r io.ReaderAt, off, limit int64) (element, error) {
	if limit-off < 2 {
		return element{}, noIndex("no element at %d", off)
	}
	h, err := readFull(r, off, min(12, limit-off))
	if err != nil {
		return element{}, err
	}
	id, idn, ok := ebmlVint(h, true)
	if !ok {
		return element{}, noIndex("bad element id")
	}
	size, sn, ok := ebmlVint(h[idn:], false)
	if !ok {
		return element{}, noIndex("bad element size")
	}
	start := off + int64(idn+sn)
	if size == (uint64(1)<<(7*sn))-1 {
		return element{id, start, -1}, nil
	}
	if size > uint64(limit-start) {
		return element{}, noIndex("element %X of %d bytes", id, size)
	}
	return element{id, start, start + int64(size)}, nil
}

// eachChild visits the elements of an in-memory payload in place (no
// slices); fn's error stops the walk.
func eachChild(r io.ReaderAt, b []byte, fn func(id uint64, payload []byte) error) error {
	count := 0
	for off := 0; off < len(b); {
		count++
		if count > maxChildren {
			return noIndex("too many elements")
		}
		if err := tick(r); err != nil {
			return err
		}
		id, idn, ok := ebmlVint(b[off:], true)
		if !ok {
			return nil
		}
		size, sn, ok := ebmlVint(b[off+idn:], false)
		if !ok {
			return nil
		}
		start := off + idn + sn
		if size > uint64(len(b)-start) {
			return nil
		}
		end := start + int(size)
		if err := fn(id, b[start:end]); err != nil {
			return err
		}
		off = end
	}
	return nil
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for _, x := range b[:min(len(b), 8)] {
		v = v<<8 | uint64(x)
	}
	return v
}

func ebmlFloat(b []byte) float64 {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b))
	}
	return 0
}

func mkvKeyIndex(r io.ReaderAt, size int64) (KeyIndex, error) {
	head, err := readElement(r, 0, size)
	if err != nil {
		return KeyIndex{}, err
	}
	if head.end < 0 {
		return KeyIndex{}, noIndex("bad EBML header")
	}
	seg, err := readElement(r, head.end, size)
	if err != nil {
		return KeyIndex{}, err
	}
	if seg.id != mkvSegment {
		return KeyIndex{}, noIndex("no segment")
	}
	segEnd := seg.end
	if segEnd < 0 || segEnd > size {
		segEnd = size
	}
	// the top-level elements, found through the SeekHead(s) and by walking
	// the elements before the first cluster
	pos := map[uint64]int64{}
	var seekHeads []int64
	for off, n := seg.start, 0; off < segEnd && n < 64; n++ {
		e, err := readElement(r, off, segEnd)
		if err != nil {
			if errors.Is(err, ErrNoKeyIndex) {
				break // the walk only helps the SeekHead
			}
			return KeyIndex{}, err
		}
		if e.end < 0 || e.id == mkvCluster {
			break
		}
		if _, ok := pos[e.id]; !ok {
			pos[e.id] = off
		}
		if e.id == mkvSeekHead {
			seekHeads = append(seekHeads, off)
		}
		off = e.end
	}
	for i := 0; i < len(seekHeads) && i < 4; i++ {
		e, err := readElement(r, seekHeads[i], segEnd)
		if err != nil {
			if errors.Is(err, ErrNoKeyIndex) {
				continue
			}
			return KeyIndex{}, err
		}
		if e.end < 0 {
			continue
		}
		b, err := readFull(r, e.start, e.end-e.start)
		if err != nil {
			return KeyIndex{}, err
		}
		err = eachChild(r, b, func(id uint64, s []byte) error {
			if id != mkvSeek {
				return nil
			}
			var sid uint64
			var at int64 = -1
			err := eachChild(r, s, func(fid uint64, v []byte) error {
				switch fid {
				case mkvSeekID:
					sid = ebmlUint(v)
				case mkvSeekPosition:
					if p := ebmlUint(v); p < uint64(segEnd) {
						at = seg.start + int64(p)
					}
				}
				return nil
			})
			if err != nil || at < 0 || at >= segEnd {
				return err
			}
			if _, ok := pos[sid]; !ok {
				pos[sid] = at
				if sid == mkvSeekHead {
					seekHeads = append(seekHeads, at)
				}
			}
			return nil
		})
		if err != nil {
			return KeyIndex{}, err
		}
	}
	load := func(id uint64) ([]byte, error) {
		at, ok := pos[id]
		if !ok {
			return nil, noIndex("no element %X", id)
		}
		e, err := readElement(r, at, segEnd)
		if err != nil {
			return nil, err
		}
		if e.id != id || e.end < 0 {
			return nil, noIndex("element %X unreadable", id)
		}
		return readFull(r, e.start, e.end-e.start)
	}
	info, err := load(mkvInfo)
	if err != nil {
		return KeyIndex{}, err
	}
	scale, dur := uint64(1_000_000), 0.0
	err = eachChild(r, info, func(id uint64, v []byte) error {
		switch id {
		case mkvTimecodeScale:
			scale = ebmlUint(v)
		case mkvDuration:
			dur = ebmlFloat(v)
		}
		return nil
	})
	if err != nil {
		return KeyIndex{}, err
	}
	if scale == 0 || math.IsNaN(dur) || math.IsInf(dur, 0) {
		return KeyIndex{}, noIndex("bad timecode scale or duration")
	}
	tracks, err := load(mkvTracks)
	if err != nil {
		return KeyIndex{}, err
	}
	video := uint64(0)
	err = eachChild(r, tracks, func(id uint64, t []byte) error {
		if id != mkvTrackEntry || video != 0 {
			return nil
		}
		var num, typ uint64
		if err := eachChild(r, t, func(fid uint64, v []byte) error {
			switch fid {
			case mkvTrackNumber:
				num = ebmlUint(v)
			case mkvTrackType:
				typ = ebmlUint(v)
			}
			return nil
		}); err != nil {
			return err
		}
		if typ == 1 {
			video = num
		}
		return nil
	})
	if err != nil {
		return KeyIndex{}, err
	}
	if video == 0 {
		return KeyIndex{}, noIndex("no video track")
	}
	cues, err := load(mkvCues)
	if err != nil {
		return KeyIndex{}, err
	}
	sec := float64(scale) / 1e9
	ki := KeyIndex{Duration: dur * sec}
	err = eachChild(r, cues, func(id uint64, cp []byte) error {
		if id != mkvCuePoint {
			return nil
		}
		var at uint64
		has := false
		err := eachChild(r, cp, func(fid uint64, v []byte) error {
			switch fid {
			case mkvCueTime:
				at = ebmlUint(v)
			case mkvCuePositions:
				return eachChild(r, v, func(gid uint64, g []byte) error {
					if gid == mkvCueTrack && ebmlUint(g) == video {
						has = true
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		if has {
			if len(ki.Keyframes) >= maxKeyframes {
				return noIndex("too many cue points")
			}
			ki.Keyframes = append(ki.Keyframes, float64(at)*sec)
		}
		return nil
	})
	if err != nil {
		return KeyIndex{}, err
	}
	return ki, nil
}

// MARK: cache

// KeyIndexCache keeps built indexes (and verdicts that a file has none) on
// disk, keyed by the file's path, size and modification time. A slow or
// failing read is no verdict: it is not cached, and the next play tries
// again.
type KeyIndexCache struct {
	Dir string
}

type cachedIndex struct {
	Index KeyIndex `json:"index"`
	Err   string   `json:"err,omitempty"`
}

// building: one build per file at a time (concurrent plays wait for it).
var building sync.Map // cache path → chan struct{}

// Get returns the file's index, building and caching it on first use;
// built = it was not in the cache (the caller logs a failure once).
func (c KeyIndexCache) Get(file string) (ki KeyIndex, built bool, err error) {
	p, err := c.pathFor(file)
	if err != nil {
		return KeyIndex{}, false, err
	}
	for {
		if ki, err, ok := c.cached(p); ok {
			return ki, false, err
		}
		wait := make(chan struct{})
		if other, busy := building.LoadOrStore(p, wait); busy {
			<-other.(chan struct{}) // another play builds it: use its result
			continue
		}
		defer func() {
			building.Delete(p)
			close(wait)
		}()
		break
	}
	ki, err = ReadKeyIndex(file)
	if err != nil && !errors.Is(err, ErrNoKeyIndex) {
		return KeyIndex{}, true, err // a slow disk or an I/O error: no verdict
	}
	ci := cachedIndex{Index: ki}
	if err != nil {
		ci = cachedIndex{Err: err.Error()}
	}
	if werr := c.write(p, ci); werr != nil {
		log.Printf("media: keyframe index cache: %v", werr)
	}
	return ki, true, err
}

// pathFor is the cache file of a file's current version.
func (c KeyIndexCache) pathFor(file string) (string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("media: %s is not a regular file", file)
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d", file, st.Size(), st.ModTime().UnixNano()))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:16])+".json"), nil
}

func (c KeyIndexCache) cached(p string) (KeyIndex, error, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return KeyIndex{}, nil, false
	}
	var ci cachedIndex
	if json.Unmarshal(b, &ci) != nil {
		return KeyIndex{}, nil, false
	}
	if ci.Err != "" {
		return KeyIndex{}, fmt.Errorf("%w: %s", ErrNoKeyIndex, ci.Err), true
	}
	if len(ci.Index.Keyframes) == 0 {
		return KeyIndex{}, nil, false // truncated or edited by hand: build it again
	}
	return ci.Index, nil, true
}

func (c KeyIndexCache) write(p string, ci cachedIndex) error {
	b, err := json.Marshal(ci)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, ".index-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
