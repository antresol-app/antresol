package api

import (
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
)

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "томов", 1: "том", 2: "тома", 4: "тома", 5: "томов", 11: "томов", 12: "томов", 14: "томов",
		21: "том", 22: "тома", 25: "томов", 101: "том", 111: "томов", 1004: "тома"} {
		if got := i18n.PluralL(i18n.RU, n, "том", "тома", "томов"); got != itoa(int64(n))+" "+want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
	song := func(lang string, n int) string {
		return i18n.PluralL(lang, n, "песня", "песни", "песен")
	}
	if song(i18n.RU, 1) != "1 песня" || song(i18n.RU, 2) != "2 песни" || song(i18n.RU, 5) != "5 песен" {
		t.Error("песни")
	}
	if song(i18n.EN, 1) != "1 song" || song(i18n.EN, 2) != "2 songs" || song(i18n.EN, 0) != "0 songs" {
		t.Error("songs")
	}
	if got := i18n.PluralL(i18n.EN, 1, "том", "тома", "томов"); got != "1 volume" {
		t.Errorf("volume: %q", got)
	}
	if got := i18n.PluralL(i18n.EN, 21, "том", "тома", "томов"); got != "21 volumes" {
		t.Errorf("volumes: %q", got)
	}
}
