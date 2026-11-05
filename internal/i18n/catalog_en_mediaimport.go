package i18n

// «Управление → Переезд», the part both editions have (internal/mediaimport):
// Jellyfin, Kavita and Audiobookshelf. Report lines are saved in Russian and
// translated with Localize when served.
func init() {
	Register(EN, map[string]string{
		"Не найден — смонтируйте %s":                                                    "Not found — mount %s",
		"Не удалось прочитать базу %s":                                                  "Couldn’t read the %s database",
		"%s · место внутри EPUB не переносится (только процент)":                        "%s · the place inside EPUB books isn’t moved (only the percentage)",
		"Перенос из %s прервался: что-то пошло не так (подробности в журнале Antresol)": "Moving from %s stopped: something went wrong (details in the Antresol log)",
		"Не удалось создать временную папку переноса — ничего не перенесено":            "Couldn’t create the temporary migration folder — nothing was moved",
		"Профиль не выбран":                                                             "No profile chosen",
		"В Antresol позиция новее":                                                      "The position in Antresol is newer",
		"Уже в Antresol":                                                                "Already in Antresol",
		"Время в источнике в будущем — перенесено без даты":                             "The time in the source is in the future — moved without a date",
		"Уже в «Хочу посмотреть»":                                                       "Already in “Want to Watch”",
		"Нет данных для переноса":                                                       "Nothing to move",
		"В «Хочу посмотреть»":                                                           "To “Want to Watch”",
		"Позиция внутри книги — по проценту":                                            "Position inside the book — by percentage",
		"Нет позиции для переноса":                                                      "No position to move",
		"Не найдено в Antresol: %s (%s)":                                                "Not found in Antresol: %s (%s)",
		"%s не найден":                                                                  "%s not found",
		"%s: не переносятся %s (%s)":                                                    "%s: %s not moved (%s)",
		"Другие отметки":                                                                "Other Marks",
		"подкасты":                                                                      "podcasts",
		"видео":                                                                         "videos",
		"музыка":                                                                        "music",
		"альбомы":                                                                       "albums",
		"исполнители":                                                                   "artists",
		"клипы":                                                                         "music videos",
		"сезоны":                                                                        "seasons",
		"серии":                                                                         "episodes",
		"сериалы":                                                                       "TV shows",
		"фильмы":                                                                        "movies",
		"люди":                                                                          "people",
		"коллекции":                                                                     "collections",
		"папки":                                                                         "folders",
		"книги":                                                                         "books",
		"аудиокниги":                                                                    "audiobooks",
		"фото":                                                                          "photos",
		"трейлеры":                                                                      "trailers",
		"ТВ-каналы":                                                                     "TV Channels",
		"Jellyfin: ТВ-каналы не переносятся (%s)":                                       "Jellyfin: TV channels aren’t moved (%s)",
		"Не удалось проверить %s — посмотрите там вручную, что не переносится": "Couldn’t check %s — look there yourself for what isn’t moved",
		// the answers of /api/migrate*
		"Сначала проверьте, что будет перенесено":           "First check what will be moved",
		"Перенос уже идёт":                                  "A migration is already running",
		"Выберите, что перенести":                           "Choose what to move",
		"Такого профиля в Antresol нет — обновите страницу": "There’s no such profile in Antresol — reload the page",
		"Отчёт не найден":                                   "Report not found",
	})
	RegisterPlural(EN, "пользователь", "пользователя", "пользователей", "user", "users")
	RegisterPlural(EN, "отметка", "отметки", "отметок", "mark", "marks")
	RegisterPlural(EN, "канал", "канала", "каналов", "channel", "channels")
}
