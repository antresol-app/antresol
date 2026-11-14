package i18n

// Keys of ongoing tracking (internal/api: marks, the feed, the calendar,
// the «Вышло новое» shelf).
func init() {
	Register(EN, map[string]string{
		"Вышло новое": "New for you",
		"Неизвестное состояние отметки": "Unknown list state",
		"Оценка — от 1 до 10":           "The score is from 1 to 10",
		"Неверное число дней":           "Invalid number of days",
		"Неверный диапазон дат":         "Invalid date range",
	})
}
