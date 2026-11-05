package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/go-chi/chi/v5"
)

// textPreviewLimit bounds inline text/subtitle previews (Quick Look).
const textPreviewLimit = 1 << 20

type filesAPI struct{ svc *files.Service }

func mountFiles(r chi.Router, svc *files.Service) {
	h := &filesAPI{svc: svc}
	r.Group(func(r chi.Router) {
		r.Use(noKids)
		r.Get("/api/files/roots", h.roots)
		r.Get("/api/files/list", h.list)
		r.Get("/api/files/stat", h.stat)
		r.Get("/api/files/thumb", h.thumb)
		r.Get("/api/files/content", h.content)
		r.Get("/api/files/usage", h.usage)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Post("/api/files/mkdir", h.mkdir)
			r.Post("/api/files/rename", h.rename)
			r.Post("/api/files/move", h.move)
			r.Post("/api/files/trash", h.trash)
			r.Get("/api/files/trash", h.trashList)
			r.Post("/api/files/trash/restore", h.restore)
			r.Delete("/api/files/trash/{id}", h.trashDelete)
		})
	})
}

// noKids: the file browser is not available to kid profiles. Runs after auth.Require.
func noKids(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userOf(r).IsKid {
			writeErr(w, r, http.StatusForbidden, "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// filesErr maps files errors to responses. Messages never carry server paths.
func filesErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, files.ErrUnknownRoot):
		writeErr(w, r, http.StatusNotFound, "Раздел не найден")
	case errors.Is(err, files.ErrOutside):
		writeErr(w, r, http.StatusBadRequest, "Недопустимый путь")
	case errors.Is(err, files.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeErr(w, r, http.StatusNotFound, "Не найдено")
	case errors.Is(err, files.ErrNotDir):
		writeErr(w, r, http.StatusBadRequest, "Это не папка")
	case errors.Is(err, files.ErrExists):
		writeErr(w, r, http.StatusConflict, "Объект с таким именем уже есть")
	case errors.Is(err, files.ErrBadName):
		writeErr(w, r, http.StatusBadRequest, "Недопустимое имя")
	case errors.Is(err, files.ErrIsRoot):
		writeErr(w, r, http.StatusBadRequest, "Этот раздел нельзя изменить")
	case errors.Is(err, files.ErrIntoSelf):
		writeErr(w, r, http.StatusBadRequest, "Нельзя переместить папку в саму себя")
	case errors.Is(err, files.ErrCrossDevice):
		writeErr(w, r, http.StatusConflict, "Нельзя переместить на другой диск")
	case errors.Is(err, files.ErrReadOnly):
		writeErr(w, r, http.StatusForbidden, "Кеш просмотра управляется приложением, сюда нельзя перемещать файлы")
	case errors.Is(err, files.ErrNoThumb):
		writeErr(w, r, http.StatusNotFound, "Нет миниатюры")
	case errors.Is(err, os.ErrPermission):
		writeErr(w, r, http.StatusForbidden, "Нет доступа к файлу")
	case errors.Is(err, files.ErrTorrent):
		// a download using the data could not be stopped: nothing was moved
		writeErr(w, r, http.StatusConflict, fileOpConflict(r, err))
	default:
		internalErr(w, r, "files "+what, err)
	}
}

// allowRoot checks that the root exists and the profile may open it.
func (h *filesAPI) allowRoot(w http.ResponseWriter, r *http.Request, id string) bool {
	root, err := h.svc.Sandbox.Root(id)
	if err != nil {
		filesErr(w, r, "root", err)
		return false
	}
	if root.AdminOnly && !userOf(r).IsAdmin {
		writeErr(w, r, http.StatusForbidden, "Недостаточно прав")
		return false
	}
	return true
}

// resolve reads ?root=&path= and returns the absolute path.
func (h *filesAPI) resolve(w http.ResponseWriter, r *http.Request) (root, rel, abs string, ok bool) {
	q := r.URL.Query()
	root, rel = q.Get("root"), q.Get("path")
	if !h.allowRoot(w, r, root) {
		return
	}
	abs, err := h.svc.Sandbox.Resolve(root, rel)
	if err != nil {
		filesErr(w, r, "resolve", err)
		return
	}
	return root, rel, abs, true
}

type rootInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon"`
}

func (h *filesAPI) roots(w http.ResponseWriter, r *http.Request) {
	admin := userOf(r).IsAdmin
	out := []rootInfo{}
	for _, rt := range h.svc.Sandbox.Roots() {
		if rt.AdminOnly && !admin {
			continue
		}
		if st, err := os.Stat(rt.Dir); err != nil || !st.IsDir() {
			continue // not set up on this server (e.g. no music yet)
		}
		out = append(out, rootInfo{rt.ID, i18n.Localize(r.Context(), rt.Title), rt.Icon})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *filesAPI) list(w http.ResponseWriter, r *http.Request) {
	root, rel, _, ok := h.resolve(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	l, err := h.svc.Browser.List(r.Context(), root, rel, files.ListOptions{Sort: q.Get("sort"), Order: q.Get("order"), Q: q.Get("q")})
	if err != nil {
		filesErr(w, r, "list", err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

type probeVideo struct {
	Codec  string `json:"codec"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	HDR    bool   `json:"hdr"`
}

type probeAudio struct {
	Codec    string `json:"codec"`
	Lang     string `json:"lang"`
	Title    string `json:"title"`
	Channels int    `json:"channels"`
}

type probeSub struct {
	Codec  string `json:"codec"`
	Lang   string `json:"lang"`
	Title  string `json:"title"`
	Forced bool   `json:"forced"`
}

type probeInfo struct {
	Container  string       `json:"container"`
	Video      *probeVideo  `json:"video,omitempty"`
	Audio      []probeAudio `json:"audio"`
	Subs       []probeSub   `json:"subs"`
	Duration   float64      `json:"duration"` // seconds
	DurationMs int64        `json:"durationMs"`
}

type statInfo struct {
	files.Entry
	DirSize        *int64     `json:"dirSize,omitempty"`
	DirSizePending bool       `json:"dirSizePending,omitempty"`
	Probe          *probeInfo `json:"probe,omitempty"`
}

func toProbeInfo(p media.ProbeResult) *probeInfo {
	pi := &probeInfo{Container: p.Container, Audio: []probeAudio{}, Subs: []probeSub{},
		Duration: float64(p.DurationMs) / 1000, DurationMs: p.DurationMs}
	if p.Video != nil {
		pi.Video = &probeVideo{p.Video.Codec, p.Video.Width, p.Video.Height, p.Video.HDR}
	}
	for _, a := range p.Audio {
		pi.Audio = append(pi.Audio, probeAudio{a.Codec, a.Lang, a.Title, a.Channels})
	}
	for _, s := range p.Subs {
		pi.Subs = append(pi.Subs, probeSub{s.Codec, s.Lang, s.Title, s.Forced})
	}
	return pi
}

// probe returns the stream info of a media file: the library's cached
// probe when known, else ffprobe (cached back into the library).
func (h *filesAPI) probe(ctx context.Context, abs string) (media.ProbeResult, bool) {
	var p media.ProbeResult
	var fileID int64
	if h.svc.Library != nil {
		if f, err := h.svc.Library.FileByPath(ctx, abs); err == nil {
			if f.ProbeJSON != "" && json.Unmarshal([]byte(f.ProbeJSON), &p) == nil {
				return p, true
			}
			fileID = f.ID
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// ffprobe shares the thumbnailer's slots: stat on many videos must not
	// spawn unbounded processes reading the media disk.
	release, err := h.svc.Thumbs.Acquire(ctx)
	if err != nil {
		return p, false
	}
	defer release()
	if h.svc.Probe != nil {
		p, err = h.svc.Probe(ctx, abs)
	} else {
		ff := h.svc.FFprobe
		if ff == "" {
			ff = "ffprobe"
		}
		p, err = media.Probe(ctx, ff, abs)
	}
	if err != nil {
		return p, false
	}
	if fileID != 0 {
		if b, err := json.Marshal(p); err == nil {
			if err := h.svc.Library.SetProbe(ctx, fileID, string(b), p.DurationMs); err != nil {
				log.Printf("files: cache probe of %s: %v", abs, err) // the result is still served
			}
		}
	}
	return p, true
}

func (h *filesAPI) stat(w http.ResponseWriter, r *http.Request) {
	root, rel, _, ok := h.resolve(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Browser.Stat(r.Context(), root, rel)
	if err != nil {
		filesErr(w, r, "stat", err)
		return
	}
	out := statInfo{Entry: e}
	switch {
	case e.IsDir:
		if n, ok := h.svc.Usage.DirSize(e.Abs()); ok {
			out.DirSize = &n
		} else {
			out.DirSizePending = true
		}
	case e.Kind == "video" || e.Kind == "audio":
		if p, ok := h.probe(r.Context(), e.Abs()); ok {
			out.Probe = toProbeInfo(p)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *filesAPI) thumb(w http.ResponseWriter, r *http.Request) {
	_, _, abs, ok := h.resolve(w, r)
	if !ok {
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		filesErr(w, r, "thumb", err)
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	if width <= 0 {
		width = 320
	}
	p, err := h.svc.Thumbs.Thumb(r.Context(), abs, files.KindOf(st.Name(), st.IsDir()), width)
	if err != nil {
		if r.Context().Err() == nil && !errors.Is(err, files.ErrNoThumb) {
			filesErr(w, r, "thumb", err)
			return
		}
		writeErr(w, r, http.StatusNotFound, "Нет миниатюры")
		return
	}
	fh, err := os.Open(p)
	if err != nil {
		filesErr(w, r, "thumb open", err)
		return
	}
	defer fh.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=604800") // the URL carries the file's mtime
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, fh)
}

var inlineTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mkv": "video/x-matroska", ".webm": "video/webm",
	".mov": "video/quicktime", ".ts": "video/mp2t", ".m2ts": "video/mp2t", ".avi": "video/x-msvideo",
	".mp3": "audio/mpeg", ".flac": "audio/flac", ".m4a": "audio/mp4", ".m4b": "audio/mp4", ".aac": "audio/aac",
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg", ".wav": "audio/wav", ".mka": "audio/x-matroska",
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
	".bmp": "image/bmp", ".avif": "image/avif", ".heic": "image/heic", ".heif": "image/heif", ".tif": "image/tiff", ".tiff": "image/tiff",
	".pdf": "application/pdf",
}

// content serves a file with Range support: inline for Quick Look (media,
// images, text, subtitles, PDF), or as an attachment with ?download=1.
func (h *filesAPI) content(w http.ResponseWriter, r *http.Request) {
	_, _, abs, ok := h.resolve(w, r)
	if !ok {
		return
	}
	fh, err := os.Open(abs)
	if err != nil {
		filesErr(w, r, "content", err)
		return
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		filesErr(w, r, "content", err)
		return
	}
	if st.IsDir() {
		writeErr(w, r, http.StatusBadRequest, "Папку нельзя открыть как файл")
		return
	}
	name := st.Name()
	hd := w.Header()
	hd.Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("download") == "1" {
		hd.Set("Content-Type", "application/octet-stream")
		hd.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		http.ServeContent(w, r, "", st.ModTime(), fh)
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	kind := files.KindOf(name, false)
	ct, known := inlineTypes[ext]
	switch {
	case kind == "text" || kind == "subtitle":
		if st.Size() > textPreviewLimit {
			writeErr(w, r, http.StatusRequestEntityTooLarge, "Файл слишком большой для просмотра")
			return
		}
		ct = "text/plain; charset=utf-8" // never rendered as HTML/XML
	case known && (kind == "video" || kind == "audio" || kind == "image" || ext == ".pdf"):
	default:
		writeErr(w, r, http.StatusUnsupportedMediaType, "Этот файл можно только скачать")
		return
	}
	hd.Set("Content-Type", ct)
	if ext != ".pdf" { // the browser PDF viewer needs scripts of its own
		hd.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	}
	hd.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	http.ServeContent(w, r, "", st.ModTime(), fh)
}

func (h *filesAPI) usage(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.Usage.Report(r.Context())
	if err != nil {
		filesErr(w, r, "usage", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// entryAt replies with the entry at root/rel after a change.
func (h *filesAPI) entryAt(w http.ResponseWriter, r *http.Request, root, rel string) {
	e, err := h.svc.Browser.Stat(r.Context(), root, rel)
	if err != nil {
		filesErr(w, r, "stat after change", err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func joinRel(dir, name string) string {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func parentOf(rel string) string {
	rel = strings.Trim(rel, "/")
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

func (h *filesAPI) mkdir(w http.ResponseWriter, r *http.Request) {
	var in struct{ Root, Path, Name string }
	if !decode(w, r, &in) || !h.allowRoot(w, r, in.Root) {
		return
	}
	if err := h.svc.Ops.Mkdir(r.Context(), in.Root, in.Path, in.Name); err != nil {
		filesErr(w, r, "mkdir", err)
		return
	}
	h.entryAt(w, r, in.Root, joinRel(in.Path, in.Name))
}

// needsConfirm answers a rename/move of managed download data that was not confirmed,
// like trash: 200 {needsConfirm: true, warnings}; the client repeats the
// request with confirm: true.
func needsConfirm(w http.ResponseWriter, err error) bool {
	var ce *files.ConfirmError
	if !errors.As(err, &ce) {
		return false
	}
	writeJSON(w, http.StatusOK, map[string]any{"needsConfirm": true, "warnings": ce.Warnings})
	return true
}

func (h *filesAPI) rename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root, Path string
		NewName    string `json:"newName"`
		Confirm    bool
	}
	if !decode(w, r, &in) || !h.allowRoot(w, r, in.Root) {
		return
	}
	if err := h.svc.Ops.Rename(r.Context(), in.Root, in.Path, in.NewName, in.Confirm); err != nil {
		if !needsConfirm(w, err) {
			filesErr(w, r, "rename", err)
		}
		return
	}
	h.entryAt(w, r, in.Root, joinRel(parentOf(in.Path), in.NewName))
}

func (h *filesAPI) move(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root, Path string
		ToRoot     string `json:"toRoot"`
		ToPath     string `json:"toPath"`
		Confirm    bool
	}
	if !decode(w, r, &in) || !h.allowRoot(w, r, in.Root) || !h.allowRoot(w, r, in.ToRoot) {
		return
	}
	if err := h.svc.Ops.Move(r.Context(), in.Root, in.Path, in.ToRoot, in.ToPath, in.Confirm); err != nil {
		if !needsConfirm(w, err) {
			filesErr(w, r, "move", err)
		}
		return
	}
	name := path.Base(path.Clean("/" + in.Path))
	h.entryAt(w, r, in.ToRoot, joinRel(in.ToPath, name))
}

func (h *filesAPI) trash(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Root    string
		Paths   []string
		Confirm bool
	}
	if !decode(w, r, &in) || !h.allowRoot(w, r, in.Root) {
		return
	}
	if len(in.Paths) == 0 || len(in.Paths) > 1000 {
		writeErr(w, r, http.StatusBadRequest, "Ничего не выбрано")
		return
	}
	res, err := h.svc.Ops.Trash(r.Context(), in.Root, in.Paths, in.Confirm, userOf(r).ID)
	if err != nil && len(res.Trashed) > 0 {
		// Some items already moved: report them, so the client refreshes.
		log.Printf("files trash: partial (%d moved): %v", len(res.Trashed), err)
		writeJSON(w, http.StatusOK, map[string]any{
			"trashed": res.Trashed, "warnings": res.Warnings, "needsConfirm": false,
			"error": i18n.T(r.Context(), "Не все объекты удалось переместить в корзину"),
		})
		return
	}
	if err != nil {
		filesErr(w, r, "trash", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"trashed": res.Trashed, "warnings": res.Warnings,
		"needsConfirm": len(res.Warnings) > 0 && !in.Confirm,
	})
}

func (h *filesAPI) trashList(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Ops.ListTrash(r.Context())
	if err != nil {
		filesErr(w, r, "trash list", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *filesAPI) restore(w http.ResponseWriter, r *http.Request) {
	var in struct{ ID int64 }
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.Ops.Restore(r.Context(), in.ID); err != nil {
		filesErr(w, r, "restore", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *filesAPI) trashDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.Ops.Delete(r.Context(), id); err != nil {
		filesErr(w, r, "trash delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
