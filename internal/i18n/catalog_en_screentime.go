package i18n

// English for «Экранное время» (internal/screentime and its API).
func init() {
	Register(EN, map[string]string{
		"Время на сегодня закончилось. Завтра можно снова!": "That’s all for today. You can watch again tomorrow!",
		"Сейчас нельзя смотреть: с %s до %s":                "No watching right now: from %s to %s",
		"Сейчас нельзя слушать: с %s до %s":                 "No listening right now: from %s to %s",
		"Экранное время есть только у детских профилей":     "Screen time is only for kid profiles",
		"Неверные ограничения времени":                      "Invalid screen time limits",
		"Неверный бонус":                                    "Invalid bonus",
		"Скачивание выключено родителем":                    "Downloads are turned off by a parent",
		"Скачивание на устройство выключено родителем":      "Downloads to this device are turned off by a parent",
	})
}
