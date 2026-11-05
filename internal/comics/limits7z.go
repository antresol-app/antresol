package comics

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stangelandcl/ppmd"
	"github.com/ulikunitz/xz/lzma"
)

// maxDictBytes caps the decoder memory an archive may ask for (RAR and 7z
// dictionaries, PPMd model): a crafted header could otherwise make a
// decoder allocate gigabytes before a single byte is read. 7-Zip and RAR
// use 16–64 MB by default.
var maxDictBytes int64 = 256 << 20

var errDictTooBig = errors.New("comics: archive needs too much decoder memory")

// The 7z decoders that size their memory from the archive's own data are
// replaced by copies of sevenzip's that check the size first (sevenzip
// v1.6.5 internal/{lzma,lzma2,ppmd,zstd}; it has no option for this).
// Brotli (16 MB window), LZ4, bzip2 and deflate are small by design.
func init() {
	sevenzip.RegisterDecompressor([]byte{0x04, 0xf7, 0x11, 0x01}, sevenzip.Decompressor(newZstdReader))
	sevenzip.RegisterDecompressor([]byte{0x03, 0x01, 0x01}, sevenzip.Decompressor(newLZMAReader))
	sevenzip.RegisterDecompressor([]byte{0x21}, sevenzip.Decompressor(newLZMA2Reader))
	sevenzip.RegisterDecompressor([]byte{0x03, 0x04, 0x01}, sevenzip.Decompressor(newPPMdReader))
}

type decoderCloser struct {
	io.Reader
	c io.Closer
}

func (d *decoderCloser) Close() error { return d.c.Close() }

func newLZMAReader(p []byte, size uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 || len(p) != 5 {
		return nil, errors.New("comics: lzma: bad coder")
	}
	if int64(binary.LittleEndian.Uint32(p[1:])) > maxDictBytes {
		return nil, errDictTooBig
	}
	h := bytes.NewBuffer(append([]byte(nil), p...))
	_ = binary.Write(h, binary.LittleEndian, size)
	lr, err := lzma.ReaderConfig{DictCap: int(maxDictBytes)}.NewReader(io.MultiReader(h, readers[0]))
	if err != nil {
		return nil, fmt.Errorf("comics: lzma: %w", err)
	}
	return &decoderCloser{lr, readers[0]}, nil
}

func newLZMA2Reader(p []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 || len(p) != 1 || p[0] > 40 {
		return nil, errors.New("comics: lzma2: bad coder")
	}
	dictCap := int64(2|(p[0]&1)) << (p[0]/2 + 11) // Lzma2Dec.c
	if dictCap > maxDictBytes {
		return nil, errDictTooBig
	}
	cfg := lzma.Reader2Config{DictCap: int(dictCap)}
	if err := cfg.Verify(); err != nil {
		return nil, fmt.Errorf("comics: lzma2: %w", err)
	}
	lr, err := cfg.NewReader2(readers[0])
	if err != nil {
		return nil, fmt.Errorf("comics: lzma2: %w", err)
	}
	return &decoderCloser{lr, readers[0]}, nil
}

func newPPMdReader(p []byte, size uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 || len(p) != 5 {
		return nil, errors.New("comics: ppmd: bad coder")
	}
	mem := int64(binary.LittleEndian.Uint32(p[1:]))
	if mem > maxDictBytes || size > uint64(maxExtractBytes) {
		return nil, errDictTooBig
	}
	pr, err := ppmd.NewH7zReader(readers[0], int(p[0]), int(mem), int(size))
	if err != nil {
		return nil, fmt.Errorf("comics: ppmd: %w", err)
	}
	return &decoderCloser{&pr, readers[0]}, nil
}

// zstdCloser closes the decoder and the packed stream.
type zstdCloser struct {
	*zstd.Decoder
	c io.Closer
}

func (z *zstdCloser) Close() error {
	z.Decoder.Close()
	return z.c.Close()
}

// newZstdReader is sevenzip's zstd coder with the window capped at
// maxDictBytes (klauspost's default allows 512 MB), low-memory mode and one
// decoding goroutine.
func newZstdReader(_ []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 {
		return nil, errors.New("comics: zstd: bad coder")
	}
	d, err := zstd.NewReader(readers[0], zstd.WithDecoderMaxWindow(uint64(maxDictBytes)),
		zstd.WithDecoderLowmem(true), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("comics: zstd: %w", err)
	}
	return &zstdCloser{d, readers[0]}, nil
}
