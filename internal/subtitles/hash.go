// Package subtitles finds subtitles that a video lacks: OpenSubtitles
// search and download (Auto, the API), sidecar files next to videos and in
// DataDir, and the language rules shared with playback and organize.
package subtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrTooSmall: OpenSubtitles hashes need at least two 64 KiB blocks.
var ErrTooSmall = errors.New("subtitles: file too small to hash")

const hashChunk = 65536

// Hash is the OpenSubtitles movie hash: the file size plus the sum of the
// little-endian 64-bit words of the first and the last 64 KiB (mod 2^64),
// as 16 hex digits.
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	if size < 2*hashChunk {
		return "", ErrTooSmall
	}
	sum := uint64(size)
	buf := make([]byte, hashChunk)
	for _, off := range []int64{0, size - hashChunk} {
		if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		for i := 0; i < hashChunk; i += 8 {
			sum += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", sum), nil
}
