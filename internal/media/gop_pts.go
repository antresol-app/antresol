package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// The presentation time of the first frame of a GOP file ffmpeg wrote (the
// keyframe it starts with): a remux maps its GOP files onto the stream's
// timeline with it (hls_remux.go). ffmpeg's own EXTINFs are not keyframe
// gaps for an open-GOP stream (they follow the reordered B-frames).

var errNoPTS = errors.New("media: no video timestamp in the segment")

// fmp4Video is the video track of an fMP4 init segment.
type fmp4Video struct {
	track uint32
	scale uint32
}

// readFMP4Video reads init.mp4's video track id and timescale.
func readFMP4Video(path string) (fmp4Video, error) {
	f, err := os.Open(path)
	if err != nil {
		return fmp4Video{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fmp4Video{}, err
	}
	top, err := boxes(f, 0, st.Size())
	if err != nil {
		return fmp4Video{}, err
	}
	for _, b := range top {
		if b.typ != "moov" {
			continue
		}
		traks, err := boxes(f, b.start, b.end)
		if err != nil {
			return fmp4Video{}, err
		}
		for _, t := range traks {
			if t.typ != "trak" {
				continue
			}
			hdlr, ok, err := boxPath(f, t, "mdia", "hdlr")
			if err != nil || !ok {
				continue
			}
			h, err := readFull(f, hdlr.start, min(hdlr.end-hdlr.start, 12))
			if err != nil || len(h) < 12 || string(h[8:12]) != "vide" {
				continue
			}
			tkhd, ok, err := child(f, t, "tkhd")
			if err != nil || !ok {
				continue
			}
			th, err := readFull(f, tkhd.start, min(tkhd.end-tkhd.start, 24))
			if err != nil || len(th) < 16 {
				continue
			}
			var v fmp4Video
			if th[0] == 1 && len(th) >= 24 {
				v.track = binary.BigEndian.Uint32(th[20:24])
			} else {
				v.track = binary.BigEndian.Uint32(th[12:16])
			}
			mdhd, ok, err := boxPath(f, t, "mdia", "mdhd")
			if err != nil || !ok {
				continue
			}
			mh, err := readFull(f, mdhd.start, min(mdhd.end-mdhd.start, 24))
			if err != nil {
				continue
			}
			v.scale, _ = fullBoxTimescale(mh)
			if v.scale > 0 {
				return v, nil
			}
		}
	}
	return fmp4Video{}, fmt.Errorf("%w: no video track in %s", errNoPTS, path)
}

// fmp4FirstPTS is the presentation time of the video track's first sample
// in an fMP4 fragment file: tfdt + the first sample's composition offset.
func fmp4FirstPTS(path string, v fmp4Video) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	r := bytesReaderAt(b)
	top, err := boxes(r, 0, int64(len(b)))
	if err != nil {
		return 0, err
	}
	for _, moof := range top {
		if moof.typ != "moof" {
			continue
		}
		trafs, err := boxes(r, moof.start, moof.end)
		if err != nil {
			return 0, err
		}
		for _, traf := range trafs {
			if traf.typ != "traf" {
				continue
			}
			kids, err := boxes(r, traf.start, traf.end)
			if err != nil {
				return 0, err
			}
			var track uint32
			var base uint64
			var cto int64
			haveBase := false
			for _, k := range kids {
				p := b[k.start:k.end]
				switch k.typ {
				case "tfhd":
					if len(p) >= 8 {
						track = binary.BigEndian.Uint32(p[4:8])
					}
				case "tfdt":
					if len(p) >= 8 && p[0] == 1 && len(p) >= 12 {
						base, haveBase = binary.BigEndian.Uint64(p[4:12]), true
					} else if len(p) >= 8 {
						base, haveBase = uint64(binary.BigEndian.Uint32(p[4:8])), true
					}
				case "trun":
					cto = trunFirstCTO(p)
				}
			}
			if track == v.track && haveBase {
				return float64(int64(base)+cto) / float64(v.scale), nil
			}
		}
	}
	return 0, errNoPTS
}

// trunFirstCTO is the first sample's composition time offset in a trun.
func trunFirstCTO(p []byte) int64 {
	if len(p) < 8 {
		return 0
	}
	version := p[0]
	flags := uint32(p[1])<<16 | uint32(p[2])<<8 | uint32(p[3])
	if flags&0x800 == 0 {
		return 0
	}
	off := 8 // version/flags, sample_count
	if flags&0x1 != 0 {
		off += 4 // data_offset
	}
	if flags&0x4 != 0 {
		off += 4 // first_sample_flags
	}
	for _, f := range []uint32{0x100, 0x200, 0x400} {
		if flags&f != 0 {
			off += 4
		}
	}
	if off+4 > len(p) {
		return 0
	}
	raw := binary.BigEndian.Uint32(p[off:])
	if version == 1 {
		return int64(int32(raw))
	}
	return int64(raw)
}

type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// tsFirstVideoPTS is the PTS of the first video PES in an MPEG-TS file.
func tsFirstVideoPTS(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 188*2000) // a keyframe's PES starts early in the file
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return 0, err
	}
	buf = buf[:n-n%188]
	pmtPID, videoPID := -1, -1
	for off := 0; off+188 <= len(buf); off += 188 {
		pkt := buf[off : off+188]
		if pkt[0] != 0x47 {
			return 0, fmt.Errorf("%w: lost TS sync", errNoPTS)
		}
		pid := int(pkt[1]&0x1F)<<8 | int(pkt[2])
		start := pkt[1]&0x40 != 0
		payload := 4
		if pkt[3]&0x20 != 0 { // adaptation field
			payload += 1 + int(pkt[4])
		}
		if pkt[3]&0x10 == 0 || payload >= 188 {
			continue
		}
		p := pkt[payload:]
		switch {
		case pid == 0 && start && pmtPID < 0:
			if len(p) == 0 || 1+int(p[0]) > len(p) {
				continue
			}
			p = p[1+int(p[0]):] // pointer field
			if len(p) >= 12 {
				// the first program's PMT PID
				for i := 8; i+4 <= 3+int(binary.BigEndian.Uint16(p[1:3])&0x0FFF)-4 && i+4 <= len(p); i += 4 {
					if binary.BigEndian.Uint16(p[i:i+2]) != 0 {
						pmtPID = int(binary.BigEndian.Uint16(p[i+2:i+4]) & 0x1FFF)
						break
					}
				}
			}
		case pid == pmtPID && start && videoPID < 0:
			if len(p) == 0 || 1+int(p[0]) > len(p) {
				continue
			}
			p = p[1+int(p[0]):]
			if len(p) < 12 {
				continue
			}
			sectionEnd := 3 + int(binary.BigEndian.Uint16(p[1:3])&0x0FFF) - 4
			infoLen := int(binary.BigEndian.Uint16(p[10:12]) & 0x0FFF)
			for i := 12 + infoLen; i+5 <= sectionEnd && i+5 <= len(p); {
				st := p[i]
				epid := int(binary.BigEndian.Uint16(p[i+1:i+3]) & 0x1FFF)
				esLen := int(binary.BigEndian.Uint16(p[i+3:i+5]) & 0x0FFF)
				switch st {
				case 0x01, 0x02, 0x1B, 0x24, 0x10, 0x42, 0xEA:
					if videoPID < 0 {
						videoPID = epid
					}
				}
				i += 5 + esLen
			}
		case pid == videoPID && start:
			if len(p) < 14 || p[0] != 0 || p[1] != 0 || p[2] != 1 || p[7]&0x80 == 0 {
				continue
			}
			pts := uint64(p[9]&0x0E)<<29 | uint64(p[10])<<22 | uint64(p[11]&0xFE)<<14 | uint64(p[12])<<7 | uint64(p[13])>>1
			return float64(pts) / 90000, nil
		}
	}
	return 0, errNoPTS
}
