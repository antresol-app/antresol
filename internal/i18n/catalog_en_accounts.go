package i18n

// English for external accounts (internal/accounts: link statuses and
// the accounts API).
func init() {
	Register(EN, map[string]string{
		"Список скрыт: войдите через Shikimori":            "The list is private: sign in with Shikimori",
		"Пользователь Shikimori не найден":                 "Shikimori user not found",
		"Нужно войти в Shikimori заново":                   "Sign in with Shikimori again",
		"Укажите ник на Shikimori":                         "Enter your Shikimori nickname",
		"Shikimori не отвечает, попробуйте позже":          "Shikimori is not answering, try again later",
		"Запись на Shikimori не настроена на этом сервере": "Writing to Shikimori is not set up on this server",
		"Не удалось прочитать данные":                      "Could not read the data",
		"Не удалось сохранить":                             "Could not save",
		"Неверный запрос":                                  "Bad request",
	})
}
