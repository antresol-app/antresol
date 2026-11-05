package i18n

// Keys of the library scanner, media decisions, languages and the server
// binary (group "library").
func init() {
	Register(EN, map[string]string{
		// cmd/vault
		"Медиатека ещё сканируется — подождите": "The library is still being scanned — please wait",

		// internal/lang: «на …» in a release's reason line
		"на русском":       "in Russian",
		"на английском":    "in English",
		"на украинском":    "in Ukrainian",
		"на немецком":      "in German",
		"на французском":   "in French",
		"на испанском":     "in Spanish",
		"на итальянском":   "in Italian",
		"на португальском": "in Portuguese",
		"на польском":      "in Polish",
		"на японском":      "in Japanese",
		"на китайском":     "in Chinese",
		"на корейском":     "in Korean",
		// internal/lang: language names
		"Русский":       "Russian",
		"Английский":    "English",
		"Украинский":    "Ukrainian",
		"Немецкий":      "German",
		"Французский":   "French",
		"Испанский":     "Spanish",
		"Итальянский":   "Italian",
		"Португальский": "Portuguese",
		"Польский":      "Polish",
		"Японский":      "Japanese",
		"Китайский":     "Chinese",
		"Корейский":     "Korean",

		// internal/library: titles the scanner makes (stored in Russian,
		// localized where served)
		"Разные исполнители":      "Various Artists",
		"Неизвестный исполнитель": "Unknown Artist",
		"Без альбома":             "No Album",
		"Трек %d":                 "Track %d",
		"Спецвыпуски":             "Specials",
		"Сезон %d":                "Season %d",
		"Серия %d":                "Episode %d",
		"Том %s · Глава %s":       "Volume %s · Chapter %s",
		"Том %s":                  "Volume %s",

		// internal/media: playback decision reasons
		"видеокодек %s не поддерживается браузером":                 "video codec %s isn’t supported by the browser",
		"H.264 с 10 битами или без 4:2:0 браузеры не воспроизводят": "browsers don’t play 10-bit or non-4:2:0 H.264",
		"высота %d больше допустимой %d":                            "height %d is above the allowed %d",
		"битрейт выше допустимого":                                  "bitrate is above the allowed limit",
		"аудиокодек %s не поддерживается браузером":                 "audio codec %s isn’t supported by the browser",
		"HEVC без метки hvc1: видео перепаковывается":               "HEVC without the hvc1 tag: the video is repackaged",
		"видеокодек %s нельзя упаковать в HLS без перекодирования":  "video codec %s can’t be packaged into HLS without transcoding",
		"аудио %s нельзя упаковать в HLS без перекодирования":       "audio %s can’t be packaged into HLS without transcoding",
		"контейнер %s не поддерживается браузером":                  "container %s isn’t supported by the browser",
		"выбрана не основная звуковая дорожка":                      "a non-default audio track is selected",
		"субтитры вшиваются в картинку":                             "subtitles are burned into the picture",
		// internal/media: a cleaned voice-over's title for players without badges
		"Очищено":      "Cleaned",
		"%s (очищено)": "%s (cleaned)",
		// internal/media: audio plan reasons
		"Оригинал по запросу клиента":           "Original, as the client asked",
		"Трек из образа: вырезается без потерь": "Track from an image: cut losslessly",
		"Трек из образа: перекодируется":        "Track from an image: transcoded",
		"Ограничение битрейта: %d кбит/с":       "Bitrate limit: %d kbps",
		"Формат по запросу клиента":             "Format the client asked for",
		"Клиент не воспроизводит %s":            "The client doesn’t play %s",
		"Оригинал": "Original",
		// internal/media: device copy errors (CopyStatus.Error)
		"Не удалось подготовить копию":          "Couldn’t prepare the copy",
		"На сервере не хватает места для копии": "There isn’t enough space on the server for the copy",
	})
}
