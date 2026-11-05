package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/antresol-app/antresol/internal/library"
)

// maxBookUpload bounds one uploaded book (a variable for tests).
var maxBookUpload int64 = 512 << 20

// uploadIndexWait is how long POST /api/books/upload waits for the book to
// reach the library: longer, a running full scan holds it up, and the page
// learns from library.updated.
var uploadIndexWait = 20 * time.Second

const uploadIndexTimeout = 30 * time.Minute

// safeBookName is the name an upload is stored under: the last path element
// of what the client said, in NFC (macOS sends NFD), without control or
// path characters and leading dots (hidden files are not scanned).
func safeBookName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = name[strings.LastIndexByte(name, '/')+1:]
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`:*?"<>|`, r) {
			return -1
		}
		return r
	}, norm.NFC.String(name))
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	// file systems count bytes; the end of the name holds the format
	if ext := library.BookExt(name); len(name) > 200 && library.IsBookFile(name) {
		stem := strings.ToValidUTF8(name[:200-len(ext)], "")
		name = strings.TrimSpace(stem) + ext
	}
	return name
}

// bookContentOK: the file starts as its extension says. A library must not
// collect what merely has a book's name.
func bookContentOK(ext string, head []byte) bool {
	switch ext {
	case ".epub", ".fb2.zip":
		return bytes.HasPrefix(head, []byte("PK\x03\x04"))
	case ".fb2":
		return bytes.Contains(head, []byte("FictionBook")) ||
			bytes.HasPrefix(head, []byte{0xFF, 0xFE}) || bytes.HasPrefix(head, []byte{0xFE, 0xFF}) // UTF-16
	case ".mobi", ".azw3":
		return len(head) >= 68 && (string(head[60:68]) == "BOOKMOBI" || string(head[60:68]) == "TEXtREAd")
	case ".pdf":
		return bytes.Contains(head[:min(len(head), 1024)], []byte("%PDF-"))
	}
	return false
}

func fileSHA256(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// placeBook moves the finished temp file to name in root: the same content
// under that name is the same book (reported as dup, the temp file removed);
// other content gets «name (2)». No existing file is ever replaced.
func placeBook(tmp, root, name string, size int64, sum []byte) (dest string, dup bool, err error) {
	ext := library.BookExt(name)
	stem := name[:len(name)-len(ext)]
	for i := 1; i < 1000; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		dest = filepath.Join(root, cand)
		st, err := os.Stat(dest)
		if err == nil {
			if st.Size() == size {
				if have, err := fileSHA256(dest); err == nil && bytes.Equal(have, sum) {
					os.Remove(tmp)
					return dest, true, nil
				}
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		// O_EXCL claims the name, so two uploads cannot overwrite each other
		// (a rename alone would); the rename then replaces only this claim
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", false, err
		}
		f.Close()
		if err := os.Rename(tmp, dest); err != nil {
			os.Remove(dest)
			return "", false, err
		}
		return dest, false, nil
	}
	return "", false, errors.New("no free name")
}

// uploadBook is POST /api/books/upload (multipart, one «file»): «Добавить
// книги». The book lands in the ebooks folder, is indexed at once and is
// the uploader's («Моя медиатека»); a full scan follows, for covers and the
// pages open elsewhere. Answers never carry server paths.
func (h *lib) uploadBook(w http.ResponseWriter, r *http.Request) {
	if h.Jobs == nil || h.Jobs.Scanner == nil || h.Jobs.Scanner.Ebooks == "" {
		writeErr(w, r, http.StatusServiceUnavailable, "Загрузка книг недоступна на этом сервере")
		return
	}
	tooBig := func() { writeErr(w, r, http.StatusRequestEntityTooLarge, "Книга больше 512 МБ") }
	unreadable := func() {
		writeErr(w, r, http.StatusBadRequest, "Не удалось прочитать загружаемый файл")
	}
	notBook := func() {
		writeErr(w, r, http.StatusUnsupportedMediaType, "Это не книга: подойдут EPUB, FB2, MOBI, AZW3 и PDF")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBookUpload+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		unreadable()
		return
	}
	var part io.Reader
	var clientName string
	for part == nil {
		p, err := mr.NextPart()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				tooBig()
			} else {
				unreadable()
			}
			return
		}
		if p.FormName() == "file" {
			part, clientName = p, p.FileName()
		}
	}
	name := safeBookName(clientName)
	ext := library.BookExt(name)
	if !library.IsBookFile(name) || len(name) == len(ext) {
		notBook()
		return
	}
	br := bufio.NewReaderSize(part, 64<<10)
	head, err := br.Peek(64 << 10)
	if err != nil && !errors.Is(err, io.EOF) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			tooBig()
		} else {
			unreadable()
		}
		return
	}
	if !bookContentOK(ext, head) {
		notBook()
		return
	}
	root, err := filepath.Abs(h.Jobs.Scanner.Ebooks)
	if err == nil {
		err = os.MkdirAll(root, 0o755)
	}
	if err != nil {
		saveFailed(w, r, err)
		return
	}
	// hidden: a scan running meanwhile does not take a half-written book
	tmp, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		saveFailed(w, r, err)
		return
	}
	defer os.Remove(tmp.Name()) // a no-op once placed
	size, err := io.Copy(tmp, io.LimitReader(br, maxBookUpload+1))
	if err == nil && size > maxBookUpload {
		tmp.Close()
		tooBig()
		return
	}
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			tooBig()
		} else if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
			saveFailed(w, r, err)
		} else {
			unreadable() // the client went away, or its body broke
		}
		return
	}
	// cleaned before it is compared: the same book sent twice is one file
	if ext == ".epub" {
		if _, err := library.CleanEPUBFile(tmp.Name()); err != nil {
			log.Printf("upload: clean: %v", err)
		}
	}
	st, err := os.Stat(tmp.Name())
	var digest []byte
	if err == nil {
		digest, err = fileSHA256(tmp.Name())
	}
	if err != nil {
		saveFailed(w, r, err)
		return
	}
	dest, dup, err := placeBook(tmp.Name(), root, name, st.Size(), digest)
	if err != nil {
		saveFailed(w, r, err)
		return
	}
	userID := userOf(r).ID
	_, err = h.uploads.do(r.Context(), dest+"\x00"+fmt.Sprint(userID), uploadIndexWait, uploadIndexTimeout, func(ctx context.Context) (struct{}, error) {
		if err := h.Jobs.Scanner.ScanBooks(ctx); err != nil {
			log.Printf("upload: scan books: %v", err)
		}
		err := h.Library.ClaimPaths(ctx, []string{dest}, userID)
		if err != nil {
			log.Printf("upload: claim: %v", err)
		}
		h.Jobs.Trigger() // covers and the like; the pages hear library.updated
		return struct{}{}, err
	})
	writeJSON(w, http.StatusCreated, map[string]any{"name": filepath.Base(dest), "duplicate": dup, "indexed": err == nil})
}

func saveFailed(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("upload: %v", err)
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		writeErr(w, r, http.StatusInsufficientStorage, "На сервере не хватает места")
		return
	}
	writeErr(w, r, http.StatusInternalServerError, "Не удалось сохранить книгу")
}
