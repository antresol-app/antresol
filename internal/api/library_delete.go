package api

import (
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/go-chi/chi/v5"
)

// Deleting library items (admin): the files go to the trash of «Файлы»,
// the library forgets the item at once (files.ItemDelete).
//
//	GET  /api/items/{id}/delete  what would go: {id, kind, title, size, files, seasons, notes}
//	POST /api/items/{id}/delete  delete it: {items, size, trashed, parentId, parentShown}
//
// A series lists its seasons; deleting one season is a POST of the season's id.
type itemDeleteAPI struct {
	svc *files.Service
	bus *events.Bus // nil: no event
}

func mountItemDelete(r chi.Router, svc *files.Service, bus *events.Bus) {
	if svc.Items == nil {
		return
	}
	h := &itemDeleteAPI{svc: svc, bus: bus}
	r.Group(func(r chi.Router) {
		r.Use(noKids, auth.RequireAdmin)
		r.Get("/api/items/{id}/delete", h.plan)
		r.Post("/api/items/{id}/delete", h.delete)
	})
}

// DeleteSeason is a season a series delete may be narrowed to.
type DeleteSeason struct {
	ID       int64  `json:"id"`
	Number   int    `json:"number"`
	Title    string `json:"title"`
	Episodes int    `json:"episodes"`
	Size     int64  `json:"size"`
	// Notes: what deleting just this season does besides (see DeletePlan).
	Notes []string `json:"notes"`
}

// DeletePlan is what deleting an item would take away.
type DeletePlan struct {
	ID      int64          `json:"id"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title"`
	Size    int64          `json:"size"`
	Files   int            `json:"files"`
	Seasons []DeleteSeason `json:"seasons"` // a series: its seasons, in order
	// Notes: what else happens (another build adds its own).
	Notes []string `json:"notes"`
}

// DeleteResult is what a delete did.
type DeleteResult struct {
	Items   []int64 `json:"items"`
	Size    int64   `json:"size"`
	Trashed int     `json:"trashed"`
	// ParentID: the item's parent (a season's series, 0 for none);
	// ParentShown: it is still in the library.
	ParentID    int64 `json:"parentId"`
	ParentShown bool  `json:"parentShown"`
	// Note: what else to know (files that reached a trashed folder while
	// it moved); "" for none.
	Note string `json:"note,omitempty"`
}

// lateNote tells of n files that reached a trashed folder late.
func lateNote(r *http.Request, n int) string {
	if n == 0 {
		return ""
	}
	return i18n.T(r.Context(), "Новых файлов, появившихся во время удаления, тоже в Корзине: %d. Восстановите их вместе с папкой", n)
}

func itemDeleteErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	switch {
	case errors.Is(err, library.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "Не найдено")
	case errors.Is(err, library.ErrNotDeletable):
		writeErr(w, r, http.StatusBadRequest, "Это удаляется только вместе с альбомом или сериалом")
	case errors.Is(err, files.ErrMediaOffline):
		writeErr(w, r, http.StatusConflict, "Медиадиск недоступен, ничего не удалено. Подключите его и попробуйте ещё раз")
	case errors.Is(err, files.ErrNothingOnDisk):
		writeErr(w, r, http.StatusConflict, "Файлов нет на диске, ничего не удалено. Проверьте, подключён ли медиадиск")
	case errors.Is(err, files.ErrOutside):
		writeErr(w, r, http.StatusConflict, "Файлы лежат вне папок медиатеки, ничего не удалено")
	case errors.Is(err, files.ErrDeleting):
		writeErr(w, r, http.StatusConflict, "Уже удаляется")
	case errors.Is(err, library.ErrScanBusy):
		writeErr(w, r, http.StatusConflict, "Идёт сканирование медиатеки — попробуйте через минуту")
	case errors.Is(err, files.ErrOtherDelete):
		writeErr(w, r, http.StatusConflict, "Идёт другое удаление — попробуйте через минуту")
	case errors.Is(err, files.ErrPlanChanged):
		writeErr(w, r, http.StatusConflict, "Файлы менялись во время удаления, ничего не удалено. Попробуйте ещё раз")
	case errors.Is(err, files.ErrImporting):
		writeErr(w, r, http.StatusConflict, "Загрузка ещё переносится в медиатеку — попробуйте позже")
	case errors.Is(err, os.ErrPermission):
		writeErr(w, r, http.StatusConflict, "Нет прав переместить файлы в Корзину, ничего не удалено")
	case errors.Is(err, files.ErrCrossDevice):
		writeErr(w, r, http.StatusConflict, "Файлы лежат на другом диске, чем Корзина, и переместить их туда нельзя. Ничего не удалено")
	default:
		filesErr(w, r, what, err)
	}
}

func (h *itemDeleteAPI) plan(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	set, err := h.svc.Items.Plan(ctx, id)
	if err != nil {
		itemDeleteErr(w, r, "delete plan", err)
		return
	}
	out := DeletePlan{ID: id, Kind: set.Item.Kind, Title: set.Item.Title, Size: set.Size(), Files: len(set.Files),
		Seasons: []DeleteSeason{}, Notes: h.svc.Items.Notes(ctx, set)}
	if set.Item.Kind == library.KindSeries {
		seasons, err := h.svc.Library.Children(ctx, id)
		if err != nil {
			internalErr(w, r, "delete plan seasons", err)
			return
		}
		for _, s := range seasons {
			if s.Kind != library.KindSeason {
				continue
			}
			ss, err := h.svc.Items.Plan(ctx, s.ID)
			if errors.Is(err, library.ErrNotFound) {
				continue
			}
			if err != nil {
				internalErr(w, r, "delete plan season", err)
				return
			}
			out.Seasons = append(out.Seasons, DeleteSeason{ID: s.ID, Number: s.SeasonNo, Title: s.Title,
				Episodes: len(ss.Items) - 1, Size: ss.Size(), Notes: h.svc.Items.Notes(ctx, ss)})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *itemDeleteAPI) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	it, err := h.svc.Library.Item(ctx, id)
	if err != nil {
		itemDeleteErr(w, r, "delete", err)
		return
	}
	res, err := h.svc.Items.Delete(ctx, id, userOf(r).ID)
	if err != nil {
		if len(res.Trashed) > 0 {
			// some files already went: they are in the trash, the rest stayed
			log.Printf("api: delete item %d: partial (%d moved): %v", id, len(res.Trashed), err)
			h.changed(res.Items)
			writeErr(w, r, http.StatusConflict, "Удалось удалить не всё: часть файлов уже в Корзине в «Файлах», остальные остались на месте")
			return
		}
		itemDeleteErr(w, r, "delete", err)
		return
	}
	h.changed(res.Items)
	out := DeleteResult{Items: res.Items, Size: res.Size, Trashed: len(res.Trashed), ParentID: it.ParentID, Note: lateNote(r, res.Late)}
	if it.ParentID != 0 {
		_, err := h.svc.Library.Item(ctx, it.ParentID)
		out.ParentShown = err == nil
	}
	writeJSON(w, http.StatusOK, out)
}

// changed tells the open pages that the library lost items, so lists drop
// their cards without waiting for the next scan.
func (h *itemDeleteAPI) changed(ids []int64) {
	if h.bus != nil {
		h.bus.Publish(events.Event{Type: events.LibraryUpdated, Data: map[string]any{"deleted": ids}})
	}
}
