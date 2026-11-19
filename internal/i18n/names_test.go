package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// The product's names settle once and the texts must follow: «Управление» is
// gone from the menu (it is «Настройки → Сервер» now; only «Управление
// каналами» is a real label), and English says «Shows», not «TV Shows».
// Server messages reach people who cannot see the code (a Subsonic client's
// error, a letter, the Telegram bot), so a stale path there sends them
// nowhere. Every Russian literal of the server is a catalog key
// (TestRussianLiteralsTranslated), so the catalogs cover the whole server.
var (
	retiredRU = regexp.MustCompile(`Управлени[еяию]`)
	retiredEN = regexp.MustCompile(`Manage\s*→|→\s*Manage\b|\bTV [Ss]hows?\b`)
)

func TestNoRetiredNames(t *testing.T) {
	for _, l := range Languages[1:] {
		for _, k := range Keys(l) {
			if retiredRU.MatchString(strings.ReplaceAll(k, "Управление каналами", "")) {
				t.Errorf("%q: «Управление» is not in the menu any more (use «Настройки → Сервер → …»)", k)
			}
			if tr, _ := Translation(l, k); retiredEN.MatchString(tr) {
				t.Errorf("%s translation %q: retired name (use «Settings → Server → …», «Shows»)", l, tr)
			}
		}
	}
}
