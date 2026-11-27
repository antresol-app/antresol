package api

// Audiobooks' transcripts and automatic chapters (internal/transcript), as
// the book page and GET /api/books/{id}/transcript tell them.

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/transcript"
)

// The book page's «transcript»: "" is none and none coming (the feature is
// off), else one of these.
const (
	transcriptPending = "pending"
	transcriptReady   = "ready"
	transcriptFailed  = "failed"
)

// storedTranscript is a book's stored transcript when it was made from the
// files the book has now.
func (h *lib) storedTranscript(ctx context.Context, itemID int64, files []library.File) (transcript.Stored, bool, error) {
	if h.DB == nil || len(files) == 0 {
		return transcript.Stored{}, false, nil
	}
	s, ok, err := transcript.Get(ctx, h.DB, itemID)
	if err != nil || !ok {
		return s, false, err
	}
	sig := make([]transcript.File, len(files))
	for i, f := range files {
		sig[i] = transcript.File{ID: f.ID, Size: f.Size, Mtime: f.Mtime}
	}
	return s, s.Signature == transcript.Signature(sig), nil
}

// transcriptState is the book page's «transcript»; current: s is of the
// book's files.
func (h *lib) transcriptState(ctx context.Context, s transcript.Stored, current bool) string {
	if current && s.State == transcript.StateReady {
		return transcriptReady
	}
	if h.DB == nil {
		return ""
	}
	if _, on := SpeechSource(h.DB, h.SettingsDefaults)(ctx); !on {
		return ""
	}
	if current && s.State == transcript.StateFailed {
		return transcriptFailed
	}
	return transcriptPending
}

// autoChapters are the chapters made from the transcript, on the book's
// timeline (offsets: where each file starts, total: its length); none when
// the book's own titles (own) say something, or none were made.
func (h *lib) autoChapters(ctx context.Context, it library.Item, files []library.File, own []library.Chapter, offsets map[int64]int64, total int64) ([]BookChapter, error) {
	titles := make([]string, len(own))
	for i, c := range own {
		titles[i] = c.Title
	}
	stems := make([]string, len(files))
	for i, f := range files {
		name := filepath.Base(f.Path)
		stems[i] = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if library.RealChapterTitles(it.Title, titles, stems) {
		return nil, nil
	}
	made, err := transcript.AutoChapters(ctx, h.DB, it.ID)
	if err != nil {
		return nil, err
	}
	var out []BookChapter
	for _, c := range made {
		if off, ok := offsets[c.FileID]; ok {
			out = append(out, BookChapter{Title: c.Title, FileID: c.FileID, StartMs: off + c.StartMs})
		}
	}
	slices.SortStableFunc(out, func(a, b BookChapter) int { return int(a.StartMs - b.StartMs) })
	for i := range out {
		out[i].Idx, out[i].EndMs = i, total
		if i+1 < len(out) {
			out[i].EndMs = out[i+1].StartMs
		}
	}
	return out, nil
}

// BookTranscript is GET /api/books/{id}/transcript: the phrases in order,
// in milliseconds from the start of the book.
type BookTranscript struct {
	// Language is the ISO 639-1 code of the speech ("" unknown).
	Language string               `json:"language"`
	Segments []transcript.Segment `json:"segments"`
}

// bookTranscript answers an audiobook's finished transcript, for whoever
// may open the book's page; 404 when there is none.
func (h *lib) bookTranscript(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindAudiobook)
	if !ok {
		return
	}
	ctx := r.Context()
	files, err := h.Library.FilesOf(ctx, it.ID)
	if err != nil {
		internalErr(w, r, "transcript files", err)
		return
	}
	files = library.OrderFiles(files, bookMetaOf(it).FilesOrder)
	s, current, err := h.storedTranscript(ctx, it.ID, files)
	if err != nil {
		internalErr(w, r, "transcript", err)
		return
	}
	if !current || s.State != transcript.StateReady {
		writeErr(w, r, http.StatusNotFound, "Расшифровки нет")
		return
	}
	segs, err := transcript.Segments(ctx, h.DB, it.ID)
	if err != nil {
		internalErr(w, r, "transcript segments", err)
		return
	}
	offsets := map[int64]int64{}
	var at int64
	for _, f := range files {
		offsets[f.ID] = at
		at += f.Length()
	}
	writeJSON(w, http.StatusOK, BookTranscript{Language: s.Language, Segments: transcript.Absolute(segs, offsets)})
}
