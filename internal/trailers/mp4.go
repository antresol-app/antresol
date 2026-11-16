package trailers

import (
	"encoding/binary"
	"io"
	"os"
)

// isMP4 says whether p starts with an ISO BMFF ftyp box.
func isMP4(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [8]byte
	if _, err := io.ReadFull(f, b[:]); err != nil {
		return false
	}
	return string(b[4:8]) == "ftyp"
}

// Duration is the length of the MP4 at p in ms, from its movie header
// (moov/mvhd, at the start or the end of the file); 0 when unknown.
func Duration(p string) int64 {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	moov, size, ok := findBox(f, 0, st.Size(), "moov")
	if !ok {
		return 0
	}
	mvhd, msize, ok := findBox(f, moov, moov+size, "mvhd")
	if !ok || msize < 32 {
		return 0
	}
	var h [32]byte
	if _, err := f.ReadAt(h[:], mvhd); err != nil {
		return 0
	}
	var scale, dur uint64
	if h[0] == 1 { // version 1: 64-bit times
		var v [12]byte
		if _, err := f.ReadAt(v[:], mvhd+4+16); err != nil {
			return 0
		}
		scale = uint64(binary.BigEndian.Uint32(v[0:4]))
		dur = binary.BigEndian.Uint64(v[4:12])
	} else {
		scale = uint64(binary.BigEndian.Uint32(h[12:16]))
		dur = uint64(binary.BigEndian.Uint32(h[16:20]))
	}
	if scale == 0 {
		return 0
	}
	return int64(dur * 1000 / scale)
}

// findBox finds the box named typ among the boxes in [from, to): the
// offset of its payload and the payload's size.
func findBox(f *os.File, from, to int64, typ string) (int64, int64, bool) {
	var h [16]byte
	for off := from; off+8 <= to; {
		if _, err := f.ReadAt(h[:8], off); err != nil {
			return 0, 0, false
		}
		size := int64(binary.BigEndian.Uint32(h[0:4]))
		head := int64(8)
		switch size {
		case 1:
			if _, err := f.ReadAt(h[8:16], off+8); err != nil {
				return 0, 0, false
			}
			size = int64(binary.BigEndian.Uint64(h[8:16]))
			head = 16
		case 0:
			size = to - off
		}
		if size < head || off+size > to {
			return 0, 0, false
		}
		if string(h[4:8]) == typ {
			return off + head, size - head, true
		}
		off += size
	}
	return 0, 0, false
}
