package i18n

import (
	"strconv"
	"testing"
)

func TestRuPluralForms(t *testing.T) {
	for n, want := range map[int]string{0: "томов", 1: "том", 2: "тома", 4: "тома", 5: "томов", 11: "томов", 12: "томов", 14: "томов",
		21: "том", 22: "тома", 25: "томов", 101: "том", 111: "томов", 1004: "тома", -1: "том", -5: "томов"} {
		if got := ruPlural(n, "том", "тома", "томов"); got != strconv.Itoa(n)+" "+want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int]string{1: "1 раздача", 2: "2 раздачи", 5: "5 раздач", 11: "11 раздач", 21: "21 раздача"} {
		if got := ruPlural(n, "раздача", "раздачи", "раздач"); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
}
