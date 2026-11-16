package i18n

// English for trailers played inside the apps (internal/api/trailers_play.go).
func init() {
	Register(EN, map[string]string{
		"Трейлер не найден": "Trailer not found",
		"Трейлер ещё готовится, попробуйте через пару секунд": "The trailer is still being prepared, try again in a moment",
		"Не удалось загрузить трейлер":                        "Couldn’t load the trailer",
	})
}
