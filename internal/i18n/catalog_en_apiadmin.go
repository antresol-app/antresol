package i18n

// Keys of the admin/auth part of the API (sign-in, profiles, files,
// settings, subtitles), internal/auth and internal/files. The sources build's
// own admin keys: catalog_en_apiadmin_full.go.
func init() {
	Register(EN, map[string]string{
		// common
		"Не удалось разобрать запрос": "Couldn’t parse the request",
		"Что-то пошло не так":         "Something went wrong",
		"Не найдено":                  "Not found",
		"Неверный id":                 "Invalid id",
		"Файл не найден":              "File not found",
		"Нужно войти":                 "Sign in required",
		"Недостаточно прав":           "Not allowed",

		// auth, profiles
		"Первую настройку можно сделать только из домашней сети": "The first setup can only be done from the home network",
		"Профиль не найден":                                           "Profile not found",
		"Такое имя уже занято":                                        "This name is already taken",
		"Это может изменить только администратор":                     "Only an administrator can change this",
		"Нужен хотя бы один администратор":                            "At least one administrator is required",
		"Пароль слишком длинный (не более 72 байт)":                   "The password is too long (72 bytes at most)",
		"Пароль администратора — не короче 6 символов":                "The administrator password must be at least 6 characters",
		"Пароль — не короче 4 символов":                               "The password must be at least 4 characters",
		"Проверьте имя и пароль (у администратора пароль обязателен)": "Check the name and password (an administrator must have a password)",
		"%d сек.": "%d sec",
		"%d мин.": "%d min",
		"Слишком много неудачных попыток входа. Попробуйте снова через %s": "Too many failed sign-in attempts. Try again in %s",
		"Неверный пароль":                      "Wrong password",
		"Сервер уже настроен":                  "The server is already set up",
		"Не удалось выйти, попробуйте ещё раз": "Couldn’t sign out. Try again",
		"Запрос с другого сайта отклонён":      "A request from another site was rejected",
		"Для входа не из дома у профиля должен быть пароль. Попросите администратора задать его в Управлении → Профили.": "To sign in away from home, the profile needs a password. Ask the administrator to set one in Manage → Profiles.",

		// files
		"Нет миниатюры":                                "No thumbnail",
		"Папку нельзя открыть как файл":                "A folder can’t be opened as a file",
		"Файл слишком большой для просмотра":           "The file is too large to preview",
		"Этот файл можно только скачать":               "This file can only be downloaded",
		"Ничего не выбрано":                            "Nothing selected",
		"Не все объекты удалось переместить в корзину": "Not all items could be moved to the Trash",
		"Раздел не найден":                             "Location not found",
		"Недопустимый путь":                            "Invalid path",
		"Это не папка":                                 "This isn’t a folder",
		"Объект с таким именем уже есть":               "An item with this name already exists",
		"Недопустимое имя":                             "Invalid name",
		"Этот раздел нельзя изменить":                  "This location can’t be changed",
		"Нельзя переместить папку в саму себя":         "A folder can’t be moved into itself",
		"Нельзя переместить на другой диск":            "Items can’t be moved to another disk",
		"Кеш просмотра управляется приложением, сюда нельзя перемещать файлы": "The streaming cache is managed by the app; files can’t be moved here",
		"Нет доступа к файлу": "No access to the file",
		// the media disk
		"Не удалось узнать, сколько места на диске": "Couldn’t find out how much disk space there is",
		// deleting library items
		"Это удаляется только вместе с альбомом или сериалом":                                              "This can only be deleted with its album or TV show",
		"Медиадиск недоступен, ничего не удалено. Подключите его и попробуйте ещё раз":                     "The media disk isn’t available, nothing was deleted. Connect it and try again",
		"Файлов нет на диске, ничего не удалено. Проверьте, подключён ли медиадиск":                        "The files aren’t on the disk, nothing was deleted. Check that the media disk is connected",
		"Файлы лежат на другом диске, чем Корзина, и переместить их туда нельзя. Ничего не удалено":        "The files are on another disk than the Trash and can’t be moved there. Nothing was deleted",
		"Идёт сканирование медиатеки — попробуйте через минуту":                                            "The library is being scanned. Try again in a minute",
		"Идёт другое удаление — попробуйте через минуту":                                                   "Another delete is running. Try again in a minute",
		"Файлы менялись во время удаления, ничего не удалено. Попробуйте ещё раз":                          "The files kept changing during the delete, nothing was deleted. Try again",
		"Загрузка ещё переносится в медиатеку — попробуйте позже":                                          "The download is still being moved into the library. Try again later",
		"Новых файлов, появившихся во время удаления, тоже в Корзине: %d. Восстановите их вместе с папкой": "New files that appeared during the delete are in the Trash too: %d. Restore them with the folder",
		"Уже удаляется": "Already being deleted",
		"Нет прав переместить файлы в Корзину, ничего не удалено":                                    "No permission to move the files to the Trash, nothing was deleted",
		"Файлы лежат вне папок медиатеки, ничего не удалено":                                         "The files are outside the library folders, nothing was deleted",
		"Удалось удалить не всё: часть файлов уже в Корзине в «Файлах», остальные остались на месте": "Not everything was deleted: some files are already in the Trash in Files, the rest stayed where they were",
		"Медиатека": "Library",
		"Фильмы":    "Movies",
		"Сериалы":   "TV Shows",
		"Музыка":    "Music",
		"Книги":     "Books",
		"Весь диск": "Entire Disk",

		// scans, sources of a scan
		"Медиатека ещё сканируется — подождите": "The library is still being scanned — please wait",
		"Неизвестный источник":                  "Unknown source",

		// settings
		"Выберите качество вне дома: оригинал, 1080p, 720p или 480p": "Choose the away-from-home quality: original, 1080p, 720p or 480p",
		"Неизвестное качество музыки":                                "Unknown music quality",
		"Неизвестный язык субтитров":                                 "Unknown subtitle language",
		"Выберите хотя бы один язык":                                 "Choose at least one language",

		// subtitles
		"Поиск субтитров не настроен: нужен ключ OpenSubtitles":                "Subtitle search isn’t set up: an OpenSubtitles key is required",
		"Субтитры ищутся только для фильмов и серий":                           "Subtitles are searched only for movies and episodes",
		"Лимит загрузок OpenSubtitles на сегодня исчерпан":                     "Today’s OpenSubtitles download limit is used up",
		"OpenSubtitles не принял ключ или пароль. Проверьте настройки сервера": "OpenSubtitles rejected the key or password. Check the server settings",
		"Не удалось связаться с OpenSubtitles. Попробуйте позже":               "Couldn’t reach OpenSubtitles. Try again later",
		"OpenSubtitles не ответил как ожидалось. Попробуйте позже":             "OpenSubtitles didn’t respond as expected. Try again later",

		// playback
		"Перекодирование недоступно": "Transcoding isn’t available",
	})
}
