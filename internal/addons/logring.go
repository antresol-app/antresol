package addons

import (
	"bytes"
	"sync"
)

// logRingSize is how many output lines are kept per addon.
const logRingSize = 500

// maxLogLine caps one line; longer output is split.
const maxLogLine = 4096

// logRing keeps the newest logRingSize lines.
type logRing struct {
	mu   sync.Mutex
	buf  []string
	next int
	full bool
}

func (r *logRing) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buf == nil {
		r.buf = make([]string, logRingSize)
	}
	r.buf[r.next] = line
	r.next = (r.next + 1) % logRingSize
	if r.next == 0 {
		r.full = true
	}
}

// tail returns the newest n lines, oldest first; n <= 0 means all.
func (r *logRing) tail(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []string
	if r.full {
		all = append(append(all, r.buf[r.next:]...), r.buf[:r.next]...)
	} else {
		all = append(all, r.buf[:r.next]...)
	}
	if n > 0 && n < len(all) {
		all = all[len(all)-n:]
	}
	return all
}

// lineWriter splits a byte stream into lines for emit.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(trimCR(string(w.buf[:i])))
		w.buf = w.buf[i+1:]
	}
	for len(w.buf) >= maxLogLine {
		w.emit(string(w.buf[:maxLogLine]))
		w.buf = w.buf[maxLogLine:]
	}
	return len(p), nil
}

// flush emits an unterminated last line.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(trimCR(string(w.buf)))
		w.buf = nil
	}
}

func trimCR(s string) string {
	if n := len(s); n > 0 && s[n-1] == '\r' {
		return s[:n-1]
	}
	return s
}
