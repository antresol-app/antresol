package iptv

import (
	"fmt"
	"testing"
)

func TestGroupCodes(t *testing.T) {
	for g, want := range map[string]string{
		"Кино":           "[movies]",
		"RU | Фильмы HD": "[movies]",
		"Movies":         "[movies]",
		"КИНОХИТ":        "[movies]",
		"Кино и сериалы": "[movies series]",
		"Детские":        "[kids]",
		"Детективы":      "[]",
		"Новостные":      "[news]",
		"Sport HD":       "[sport]",
		"Познавательные": "[education]",
		"Документальные": "[documentary]",
		"18+":            "[adult]",
		"Для взрослых":   "[adult]",
		"Премиум":        "[]",
		"HD":             "[]",
	} {
		if got := fmt.Sprint(GroupCodes(g)); got != want && !(want == "[]" && got == "[]") {
			t.Errorf("GroupCodes(%q) = %s, want %s", g, got, want)
		}
	}
}

func TestCleanGroup(t *testing.T) {
	for g, want := range map[string]string{
		"RU | НОВОСТНЫЕ HD": "Новостные",
		"премиум":           "Премиум",
		"[UA] Спорт":        "Спорт",
		"VIP":               "VIP",
		"HD":                "",
	} {
		if got := CleanGroup(g); got != want {
			t.Errorf("CleanGroup(%q) = %q, want %q", g, got, want)
		}
	}
}
