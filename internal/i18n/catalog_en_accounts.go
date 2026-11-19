package i18n

// English for external accounts (internal/accounts: link statuses and
// the accounts API).
func init() {
	Register(EN, map[string]string{
		"Список скрыт: войдите через Shikimori":                                            "The list is private: sign in with Shikimori",
		"Пользователь Shikimori не найден":                                                 "Shikimori user not found",
		"Нужно войти в Shikimori заново":                                                   "Sign in with Shikimori again",
		"Укажите ник на Shikimori":                                                         "Enter your Shikimori nickname",
		"Shikimori не отвечает, попробуйте позже":                                          "Shikimori is not answering, try again later",
		"Запись на Shikimori не настроена на этом сервере":                                 "Writing to Shikimori is not set up on this server",
		"Укажите логин на MyShows":                                                         "Enter your MyShows login",
		"Пользователь MyShows не найден":                                                   "MyShows user not found",
		"Профиль MyShows скрыт":                                                            "The MyShows profile is private",
		"MyShows не отвечает, попробуйте позже":                                            "MyShows is not answering, try again later",
		"Укажите адрес профиля на Goodreads":                                               "Enter your Goodreads profile address",
		"Не удалось найти номер профиля в адресе: нужен вид goodreads.com/user/show/12345": "Could not find the profile number in the address: it should look like goodreads.com/user/show/12345",
		"Профиль Goodreads не найден или скрыт":                                            "The Goodreads profile was not found or is private",
		"Goodreads не отвечает, попробуйте позже":                                          "Goodreads is not answering, try again later",
		"Goodreads изменил список книг профиля: пока подойдёт только импорт файла CSV":     "Goodreads changed how it lists a profile's books: only importing the CSV file works for now",
		"Файл больше 24 МБ":                                                                "The file is larger than 24 MB",
		"Это не экспорт библиотеки Goodreads: нужен файл из «Мои книги → Импорт и экспорт → Экспортировать библиотеку»": "This is not a Goodreads library export: use the file from My Books → Import and export → Export Library",
		"Сначала свяжите аккаунт":                             "Link the account first",
		"Только что синхронизировано, повторите через минуту": "Synced just now, try again in a minute",
		"Не удалось прочитать данные":                         "Could not read the data",
		"TMDB недоступен, попробуем позже":                    "TMDB is unavailable, will try again later",
		"Не удалось сохранить":                                "Could not save",
		"Неверный запрос":                                     "Bad request",
	})
}
