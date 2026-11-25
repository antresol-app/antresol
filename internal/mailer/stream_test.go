package mailer

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/mail"
	"regexp"
	"runtime"
	"testing"
	"testing/iotest"
)

// patternReader is n bytes that are never held anywhere.
type patternReader struct{ left int64 }

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left <= 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(b)), p.left))
	for i := range b[:n] {
		b[i] = byte(i * 7)
	}
	p.left -= int64(n)
	return n, nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

var (
	testFrom = &mail.Address{Address: "sender@example.com"}
	testTo   = &mail.Address{Address: "reader@example.com"}
)

// The attachment goes from its reader through the encoder to the connection
// in pieces: sending a book does not take memory by its size.
func TestAttachmentIsStreamedNotBuffered(t *testing.T) {
	const size = 128 << 20
	m := msg(nil)
	m.Attachment, m.Size = &patternReader{left: size}, size
	var cw countWriter
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := writeMessage(&cw, testFrom, testTo, m); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 2<<20 {
		t.Errorf("writing a %d MB attachment allocated %d MB", size>>20, got>>20)
	}
	if want := int64(size) / 3 * 4; cw.n < want {
		t.Errorf("%d bytes written, want at least %d", cw.n, want)
	}
}

// What goes out is the same whatever sizes the reader hands over: a file,
// a pipe and a slice all make the one message.
func TestMessageDoesNotDependOnHowTheReaderChunks(t *testing.T) {
	payload := make([]byte, 100_000+1)
	rand.NewChaCha8([32]byte{1}).Read(payload)
	render := func(r io.Reader) string {
		m := msg(nil)
		m.Attachment, m.Size = r, int64(len(payload))
		var b bytes.Buffer
		if err := writeMessage(&b, testFrom, testTo, m); err != nil {
			t.Fatal(err)
		}
		s := b.String()
		// the boundary and the date are the message's own, not the payload's
		bound := regexp.MustCompile(`boundary="([^"]+)"`).FindStringSubmatch(s)[1]
		s = regexp.MustCompile(`Date: [^\r]*\r\n`).ReplaceAllString(s, "")
		return string(bytes.ReplaceAll([]byte(s), []byte(bound), []byte("B")))
	}
	want := render(bytes.NewReader(payload))
	for name, r := range map[string]io.Reader{
		"one byte":  iotest.OneByteReader(bytes.NewReader(payload)),
		"half":      iotest.HalfReader(bytes.NewReader(payload)),
		"data+err":  iotest.DataErrReader(bytes.NewReader(payload)),
		"odd sizes": &oddReader{b: payload},
	} {
		if got := render(r); got != want {
			t.Errorf("%s: the message differs (%d and %d bytes)", name, len(got), len(want))
		}
	}
}

// oddReader hands over 1, 2, 3 … 997 bytes at a time: no piece is a multiple of 3.
type oddReader struct {
	b []byte
	k int
}

func (o *oddReader) Read(p []byte) (int, error) {
	if len(o.b) == 0 {
		return 0, io.EOF
	}
	o.k = o.k%997 + 1
	n := min(o.k, len(p), len(o.b))
	copy(p, o.b[:n])
	o.b = o.b[n:]
	return n, nil
}
