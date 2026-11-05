package i18n

// Playlist import from files, both editions (internal/playlistimport):
// upload errors, review reasons and statuses (stored in Russian, localized
// when served).
func init() {
	Register(EN, map[string]string{
		"Не удалось прочитать файл":                                        "Couldn’t read the file",
		"Не удалось прочитать CSV: нет колонок «Исполнитель» и «Название»": "Couldn’t read the CSV: no “Artist” and “Title” columns",
		"Файл слишком большой":                                             "The file is too large",
		"Неизвестный сервис":                                               "Unknown service",
		"Импорт уже идёт":                                                  "An import is already in progress",
		"Импорт уже начат":                                                 "The import has already started",
		"Нет точного совпадения":                                           "No exact match",
		"Несколько исполнителей с таким именем":                            "Several artists have this name",
		"Не найдено в MusicBrainz":                                         "Not found in MusicBrainz",
		"Не найдено":                                                       "Not found",
		"Нет в медиатеке":                                                  "Not in the library",
		"Каталог недоступен — проверьте интернет":                          "The catalog is unavailable — check your internet connection",
	})
}
