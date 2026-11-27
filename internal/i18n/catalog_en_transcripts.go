package i18n

// Keys of «Распознавание речи» (internal/api/stt_settings.go) and the
// audiobooks' transcripts (group "transcripts").
func init() {
	Register(EN, map[string]string{
		"Укажите адрес сервера распознавания речи":        "Enter the address of the speech recognition server",
		"Адрес распознавания речи — http:// или https://": "The speech recognition address is http:// or https://",
		"Укажите модель распознавания речи":               "Enter the speech recognition model",
		"Неизвестный провайдер распознавания речи":        "Unknown speech recognition provider",
		"Неверный ключ распознавания речи":                "Invalid speech recognition key",
		"Распознавание речи выключено":                    "Speech recognition is off",
		"Выберите провайдера распознавания речи":          "Choose a speech recognition provider",
		"Для этого провайдера нужен ключ API":             "This provider needs an API key",
		"Расшифровки нет":                                 "There is no transcript",
	})
}
