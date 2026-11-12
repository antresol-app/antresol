package channels

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
)

// Display is a channel's name and category in language lang: a generated
// channel nobody renamed is shown in the viewer's language (its stored
// name is Russian); anything typed by hand is shown as typed.
func Display(lang string, c Channel) (name, category string) {
	name, category = c.Name, c.Category
	if category == "" {
		category = DefaultCategory
	}
	if c.AutoKey == "" || c.Edited || lang == i18n.RU {
		return name, i18n.TL(lang, category)
	}
	category = i18n.TL(lang, category)
	switch keyKind(c.AutoKey) {
	case "decade":
		_, v, _ := strings.Cut(c.AutoKey, ":")
		if d, err := strconv.Atoi(v); err == nil {
			return DecadeNameIn(lang, d), category
		}
	case "series":
		if strings.Contains(c.AutoKey, ":") {
			return name, category // a series' own channel: its title
		}
	}
	return i18n.TL(lang, name), category
}

// DecadeNameIn is DecadeName in lang.
func DecadeNameIn(lang string, d int) string {
	if lang == i18n.RU {
		return DecadeName(d)
	}
	if d >= 2000 {
		return fmt.Sprintf(i18n.TL(lang, "Кино %d-х"), d)
	}
	return fmt.Sprintf(i18n.TL(lang, "Кино %02d-х"), d%100)
}
