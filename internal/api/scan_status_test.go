package api

import (
	"net/http/httptest"
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// A failed scan's error reaches an English viewer in English.
func TestScanErrorLocalized(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/library/scan", nil)
	r = r.WithContext(i18n.WithLanguage(r.Context(), i18n.EN))
	if st := localizedScan(r, library.JobStatus{Error: "Что-то пошло не так"}); st.Error != "Something went wrong" {
		t.Fatalf("%q", st.Error)
	}
}
