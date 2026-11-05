package api

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/events"
	"github.com/antresol-app/antresol/internal/files"
	"github.com/antresol-app/antresol/internal/library"
)

// A second delete of the same item, a folder that cannot be written and
// another disk are refused in plain words.
func TestItemDeleteErrWords(t *testing.T) {
	for err, want := range map[error]string{
		files.ErrDeleting:                    "Уже удаляется",
		&os.PathError{Err: os.ErrPermission}: "Нет прав переместить файлы в Корзину",
		files.ErrCrossDevice:                 "на другом диске",
		library.ErrScanBusy:                  "Идёт сканирование медиатеки — попробуйте через минуту",
		files.ErrOtherDelete:                 "Идёт другое удаление",
		files.ErrPlanChanged:                 "Файлы менялись во время удаления",
		files.ErrImporting:                   "Загрузка ещё переносится в медиатеку — попробуйте позже",
	} {
		w := httptest.NewRecorder()
		itemDeleteErr(w, httptest.NewRequest("POST", "/", nil), "delete", err)
		if w.Code != 409 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%v: %d %s", err, w.Code, w.Body)
		}
	}
}

// The delete tells open pages at once (library.updated with the ids).
func TestItemDeletePublishes(t *testing.T) {
	bus := &events.Bus{}
	ch, cancel := bus.Subscribe()
	defer cancel()
	(&itemDeleteAPI{bus: bus}).changed([]int64{4, 5})
	select {
	case ev := <-ch:
		if ev.Type != events.LibraryUpdated {
			t.Fatalf("%+v", ev)
		}
	default:
		t.Fatal("no event")
	}
}

// Files that reached a trashed folder late are named in the answer.
func TestItemDeleteLateNote(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	if n := lateNote(r, 0); n != "" {
		t.Fatalf("%q", n)
	}
	if n := lateNote(r, 2); !strings.Contains(n, "2") || !strings.Contains(n, "вместе с папкой") {
		t.Fatalf("%q", n)
	}
}
