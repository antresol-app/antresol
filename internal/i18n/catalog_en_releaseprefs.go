package i18n

// Keys of internal/releaseprefs: release preferences (both editions) —
// validation messages, the studio limits and the music format labels.
func init() {
	Register(EN, map[string]string{
		// preferences
		"Неизвестное качество: %dp":                        "Unknown quality: %dp",
		"Неизвестный формат музыки: %s":                    "Unknown music format: %s",
		"Неизвестный битрейт: %s":                          "Unknown bitrate: %s",
		"Слишком много студий (до %d)":                     "Too many studios (up to %d)",
		"Слишком длинное название студии (до %d символов)": "Studio name too long (up to %d characters)",
		"Значения не могут быть отрицательными":            "Values can’t be negative",

		// music format labels («Формат загрузки»)
		"любой":      "any",
		"MP3 от 256": "MP3 256+",
	})
}
