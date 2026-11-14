package i18n

// Keys of «Настройки сервера → Перекодирование» (internal/api).
func init() {
	Register(EN, map[string]string{
		"Неизвестный режим ускорения":      "Unknown acceleration mode",
		"Нет такого устройства видеокарты": "No such GPU device",
	})
}
