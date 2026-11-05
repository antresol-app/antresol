package i18n

// Keys of the shared server parts (settings, chapter titles) made with the
// i18n package itself.
func init() {
	Register(EN, map[string]string{
		"Выберите язык метаданных: русский или английский": "Choose the metadata language: Russian or English",
		"Часть %s · Глава %s": "Part %s · Chapter %s",
		"Часть %s":            "Part %s",
		"Глава %s":            "Chapter %s",
		// the media disk is away (cmd/vault storageOffline)
		"Медиадиск недоступен: нет папки %s": "The media disk is unavailable: no folder %s",
	})
}
