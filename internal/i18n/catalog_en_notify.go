package i18n

// Keys of «Уведомления на почту» (internal/notify and its API): the letters,
// their link pages and the settings' answers.
func init() {
	Register(EN, map[string]string{
		// letters
		"Новое в %s":   "New in %s",
		"Профиль «%s»": "Profile “%s”",
		"Появилось то, что вы хотели посмотреть":                                                                     "What you wanted to watch is here",
		"Больше не присылать «%s»":                                                                                   "Stop sending “%s”",
		"Вы получили это письмо, потому что включили уведомления в профиле «%s».":                                    "You got this email because notifications are on in the profile “%s”.",
		"Изменить их можно в разделе «Настройки → Уведомления».":                                                     "You can change them in Settings → Notifications.",
		"%s: проверка адреса для уведомлений":                                                                        "%s: confirm your notification address",
		"Это проверочное письмо %s. Чтобы получать уведомления на этот адрес, введите код в настройках уведомлений:": "This is a test email from %s. To get notifications at this address, enter the code in the notification settings:",
		"Или подтвердите адрес по ссылке:":                                                                           "Or confirm the address with this link:",
		"Подтвердить адрес": "Confirm Address",
		"Если вы не включали уведомления, просто не обращайте внимания на это письмо.": "If you didn’t turn on notifications, just ignore this email.",
		"Все уведомления": "All Notifications",
		"Сегодня проверочных писем уже достаточно — попробуйте завтра": "That’s enough test emails for today — try again tomorrow",
		"Уведомления взрослого профиля настраивает только он сам":      "Only the adult profile itself sets up its notifications",
		// the settings' answers
		"Укажите адрес почты, например name@example.com":                   "Enter an email address, like name@example.com",
		"Сначала укажите и сохраните адрес почты":                          "Enter and save an email address first",
		"Почта сервера не настроена: её задаёт администратор в настройках": "The server’s mail isn’t set up: an administrator sets it in Settings",
		"Письмо только что отправлено — подождите минуту":                  "An email was just sent — wait a minute",
		"Почтовый сервер не принял этот адрес":                             "The mail server refused this address",
		"Не удалось отправить письмо, попробуйте ещё раз":                  "Couldn’t send the email, try again",
		"Неверный или устаревший код":                                      "Wrong or expired code",
		// Telegram: linking and the bot's settings
		"Telegram подключают только взрослые профили":                                          "Only adult profiles connect Telegram",
		"Telegram не настроен: бота подключает администратор в настройках сервера":             "Telegram isn’t set up: an administrator adds the bot in the server settings",
		"Это не токен бота: он выглядит как 123456:ABC… — скопируйте его целиком у @BotFather": "That isn’t a bot token: it looks like 123456:ABC… — copy it whole from @BotFather",
		"Telegram не принял токен — проверьте, что он скопирован целиком и бот не удалён":      "Telegram didn’t accept the token — check that it was copied whole and the bot wasn’t deleted",
		"Нет связи с Telegram, попробуйте ещё раз":                                             "Can’t reach Telegram, try again",
		"Сначала вставьте токен бота":                                                          "Paste the bot token first",
		// the Telegram bot's answers
		"Это бот уведомлений %s. Чтобы получать их здесь, откройте «Настройки → Уведомления» и нажмите «Подключить Telegram».": "This is the %s notifications bot. To get them here, open Settings → Notifications and press “Connect Telegram”.",
		"Ссылка устарела. Откройте «Настройки → Уведомления» и нажмите «Подключить Telegram» ещё раз.":                         "The link has expired. Open Settings → Notifications and press “Connect Telegram” again.",
		"Не получилось подключить Telegram, попробуйте ещё раз.":                                                               "Couldn’t connect Telegram, try again.",
		"Готово! Уведомления %s профиля «%s» будут приходить сюда.":                                                            "Done! %s notifications for the profile “%s” will arrive here.",
		// kids' requests
		"Запросы":             "Requests",
		"Запрос одобрен: %s":  "Request approved: %s",
		"Уже в медиатеке: %s": "Now in the library: %s",
		// the pages of the letters' links
		"Ссылка недействительна": "Invalid Link",
		"Ссылка устарела или относится к другому адресу. Откройте «Настройки → Уведомления».": "The link has expired or is for another address. Open Settings → Notifications.",
		"Адрес подтверждён": "Address Confirmed",
		"Уведомления, которые вы включили, будут приходить на этот адрес.": "The notifications you turned on will come to this address.",
		"Больше не присылать «%s»?":           "Stop sending “%s”?",
		"Остальные уведомления не изменятся.": "Your other notifications stay as they are.",
		"Отписаться":            "Unsubscribe",
		"Вы отписались от «%s»": "You unsubscribed from “%s”",
		"Остальные уведомления не изменились. Включить снова можно в разделе «Настройки → Уведомления».": "Your other notifications are unchanged. You can turn this back on in Settings → Notifications.",
	})
	RegisterPlural(EN, "новая серия", "новые серии", "новых серий", "new episode", "new episodes")
	RegisterPlural(EN, "новость", "новости", "новостей", "update", "updates")
}
