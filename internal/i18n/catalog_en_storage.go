package i18n

// Keys of the folder checks of the first-run wizard and «Настройки сервера»
// (internal/api/storage_settings.go).
func init() {
	Register(EN, map[string]string{
		"Папка должна быть внутри медиа-папки":                                       "The folder must be inside the media folder",
		"Выберите папку внутри медиа-папки, а не её саму":                            "Pick a folder inside the media folder, not the media folder itself",
		"Две библиотеки не могут лежать в одной папке":                               "Two libraries cannot share one folder",
		"Первая настройка уже завершена":                                             "The first setup is already finished",
		"Папка одной библиотеки не может лежать внутри папки другой":                 "One library's folder cannot be inside another library's folder",
		"Диск не подключён. Пока он не вернётся, папки не создаются":                 "The disk is not connected. No folders are made until it is back",
		"Папка должна быть в медиа-папке или в другой папке, подключённой к серверу": "The folder must be in the media folder or in another folder mounted into the server",
		"Из медиатеки уйдут файлы убранных папок. Подтвердите, что их нужно убрать":  "The files of the folders taken out will leave the library. Confirm that they should go",
		"Медиатека сканируется. Попробуйте через минуту":                             "The library is being scanned. Try again in a minute",
	})
}
