package i18n

// Keys of the folder checks of the first-run wizard and «Настройки сервера»
// (internal/api/storage_settings.go).
func init() {
	Register(EN, map[string]string{
		"Папка должна быть внутри медиа-папки":                       "The folder must be inside the media folder",
		"Выберите папку внутри медиа-папки, а не её саму":            "Pick a folder inside the media folder, not the media folder itself",
		"Две библиотеки не могут лежать в одной папке":               "Two libraries cannot share one folder",
		"Первая настройка уже завершена":                             "The first setup is already finished",
		"Папка одной библиотеки не может лежать внутри папки другой": "One library's folder cannot be inside another library's folder",
		"Диск не подключён. Пока он не вернётся, папки не создаются": "The disk is not connected. No folders are made until it is back",
	})
}
