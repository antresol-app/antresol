package i18n

// Keys of kids' requests (internal/api: «Попросить» and the admin's «Запросы»).
func init() {
	Register(EN, map[string]string{
		"Попросить могут только детские профили":                         "Only kid profiles can ask for titles",
		"Запросы здесь не принимаются":                                   "Requests are not accepted here",
		"Возраст не указан, поэтому попросить нельзя":                    "It has no age rating, so you can’t ask for it",
		"Это %s, а в твоём профиле можно до %s":                          "This is rated %s, and your profile allows up to %s",
		"Не указано название":                                            "No title given",
		"Подожди пару секунд и попроси ещё раз":                          "Wait a couple of seconds and ask again",
		"Уже много запросов ждут ответа. Подожди, пока родители ответят": "Lots of requests are already waiting. Wait until your parents answer",
		"Запрос не найден":                                               "Request not found",
		"На этот запрос уже ответили":                                    "This request has already been answered",
		"Неизвестный фильм или сериал":                                   "Unknown movie or TV show",
		"Недоступно для этого профиля":                                   "Not available for this profile",
		"Такого фильма или сериала нет в TMDB":                           "This movie or TV show isn’t on TMDB",
		"TMDB сейчас недоступен. Попробуйте позже":                       "TMDB is unavailable right now. Try again later",
	})
}
