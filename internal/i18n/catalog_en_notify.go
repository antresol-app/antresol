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
		"Изменить их можно в меню профиля: «Уведомления на почту».":                                                  "You can change them in the profile menu: “Email Notifications”.",
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
		// kids' requests
		"Просьбы":              "Requests",
		"Просьбу одобрили: %s": "Request approved: %s",
		"Уже в медиатеке: %s":  "Now in the library: %s",
		// the pages of the letters' links
		"Ссылка недействительна": "Invalid Link",
		"Ссылка устарела или относится к другому адресу. Откройте настройки уведомлений в меню профиля.": "The link has expired or is for another address. Open the notification settings in the profile menu.",
		"Адрес подтверждён": "Address Confirmed",
		"Уведомления, которые вы включили, будут приходить на этот адрес.": "The notifications you turned on will come to this address.",
		"Больше не присылать «%s»?":           "Stop sending “%s”?",
		"Остальные уведомления не изменятся.": "Your other notifications stay as they are.",
		"Отписаться":            "Unsubscribe",
		"Вы отписались от «%s»": "You unsubscribed from “%s”",
		"Остальные уведомления не изменились. Включить снова можно в меню профиля: «Уведомления на почту».": "Your other notifications are unchanged. You can turn this back on in the profile menu: “Email Notifications”.",
	})
	RegisterPlural(EN, "новая серия", "новые серии", "новых серий", "new episode", "new episodes")
	RegisterPlural(EN, "новость", "новости", "новостей", "update", "updates")
}
