package channels

import (
	"errors"
	"io"
	"os"
)

// ptsWrap is where MPEG-TS timestamps (33 bits, 90 kHz) roll over.
const ptsWrap = 1 << 33

var errNoPTS = errors.New("channels: no PTS in the segment")

// firstPTS is the PTS of the first PES packet with one in an MPEG-TS
// segment (the first frame of a segment that starts with a key frame).
func firstPTS(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	b := make([]byte, 188*512)
	n, err := io.ReadFull(f, b)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, err
	}
	return parsePTS(b[:n])
}

// parsePTS finds the first PES header with a PTS in TS packets.
func parsePTS(b []byte) (int64, error) {
	for off := 0; off+188 <= len(b); off += 188 {
		p := b[off : off+188]
		if p[0] != 0x47 {
			return 0, errors.New("channels: not an MPEG-TS segment")
		}
		if p[1]&0x40 == 0 { // payload_unit_start_indicator
			continue
		}
		i := 4
		switch p[3] >> 4 & 3 { // adaptation_field_control
		case 1:
		case 3:
			i += 1 + int(p[4])
		default:
			continue
		}
		if i+14 > 188 {
			continue
		}
		h := p[i:]
		// PES start code, a stream id of a PES with the optional header
		if h[0] != 0 || h[1] != 0 || h[2] != 1 || h[3] < 0xBD {
			continue
		}
		if h[7]&0x80 == 0 { // PTS_DTS_flags: no PTS
			continue
		}
		t := h[9:14]
		pts := int64(t[0]>>1&7)<<30 | int64(t[1])<<22 | int64(t[2]>>1)<<15 | int64(t[3])<<7 | int64(t[4]>>1)
		return pts, nil
	}
	return 0, errNoPTS
}

// ptsSince is the 90 kHz ticks from a to b, across a rollover.
func ptsSince(a, b int64) int64 {
	d := (b - a) % ptsWrap
	if d < 0 {
		d += ptsWrap
	}
	return d
}
