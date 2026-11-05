package i18n

// English for the third-party client protocols: Subsonic (/rest), OPDS
// (/opds), the shared audio streaming (audioserve) and the Jellyfin API.
func init() {
	Register(EN, map[string]string{
		// shared
		"Что-то пошло не так":                              "Something went wrong",
		"Не найдено":                                       "Not found",
		"Файл не найден":                                   "File not found",
		"Внутренняя ошибка":                                "Internal error",
		"Метод не поддерживается":                          "Method not supported",
		"Неверное имя или пароль":                          "Incorrect name or password",
		"Слишком много попыток, подождите":                 "Too many attempts. Please wait.",
		"Задайте пароль для приложений в профиле Antresol": "Set an app password in your Antresol profile",
		"Для входа не из дома у профиля должен быть пароль. Попросите администратора задать его в Управлении → Профили.": "To sign in away from home, the profile needs a password. Ask the administrator to set one in Manage → Profiles.",
		"Неверный maxBitRate": "Invalid maxBitRate",

		// Subsonic
		"Песня не найдена":               "Song not found",
		"Не указан пароль (p или t и s)": "Password missing (p, or t and s)",
		"Артист не найден":               "Artist not found",
		"Альбом не найден":               "Album not found",
		"Папка не найдена":               "Folder not found",
		"Музыка":                         "Music",
		"Неизвестный тип списка: %s":     "Unknown list type: %s",
		"Неверный timeOffset":            "Invalid timeOffset",
		"Слишком много потоков одновременно, подождите": "Too many streams at once. Please wait.",
		"Обложка не найдена":                            "Cover not found",
		"Плейлист не найден":                            "Playlist not found",
		"Слишком много песен":                           "Too many songs",
		"Неверная позиция в плейлисте":                  "Invalid position in the playlist",
		"Введите название плейлиста":                    "Enter a playlist name",
		"Можно смотреть только свои плейлисты":          "You can only view your own playlists",
		"Не удалось разобрать запрос":                   "Couldn’t parse the request",
		"Не указан параметр %s":                         "Missing parameter %s",
		"Недостаточно прав":                             "Not allowed",
		"Профиль не найден":                             "Profile not found",

		// OPDS
		"Нужны имя профиля и пароль для приложений": "A profile name and app password are required",
		"Не удалось собрать каталог":                "Couldn’t build the catalog",
		"Поиск":     "Search",
		"Поиск: %s": "Search: %s",
		"Поиск книг, аудиокниг и комиксов": "Search books, audiobooks and comics",
		"Новые книги":               "New Books",
		"Недавно добавленные книги": "Recently added books",
		"Авторы":                    "Authors",
		"Книги по авторам":          "Books by author",
		"Серии":                     "Series",
		"Книги по сериям":           "Books by series",
		"Аудиокниги":                "Audiobooks",
		"Аудиокниги одним файлом можно скачать": "Single-file audiobooks can be downloaded",
		"Манга и комиксы":                       "Manga & Comics",
		"Тома по сериям":                        "Volumes by series",
		"Томов: %d":                             "Volumes: %d",
		"Книг: %d":                              "Books: %d",
		"Аудиокнига из %d файлов — слушайте в Antresol": "Audiobook in %d files — listen in Antresol",
		"Аудиокнига": "Audiobook",
		"Том":        "Volume",
		"Книга":      "Book",

		// audioserve
		"Неизвестный формат":                         "Unknown format",
		"Позиция за концом трека":                    "Position is past the end of the track",
		"Файл пропал с диска":                        "The file is missing from the disk",
		"Сервер занят, попробуйте через пару секунд": "The server is busy. Try again in a few seconds.",
		"Не удалось перекодировать аудио":            "Couldn’t transcode the audio",

		// Jellyfin views
		"Фильмы":  "Movies",
		"Сериалы": "TV Shows",
	})
}
