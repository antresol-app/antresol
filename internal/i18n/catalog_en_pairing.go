package i18n

// English for sign-in on a TV by QR code (/api/auth/pair/*).
func init() {
	Register(EN, map[string]string{
		"Код не найден или устарел": "The code is unknown or has expired",
		"Слишком много открытых запросов на вход. Попробуйте через минуту": "Too many open sign-in requests. Try again in a minute",
		"Вход отклонён": "Sign-in was declined",
		"Код устарел":   "The code has expired",
		"Детский профиль может подтвердить вход только в свой профиль": "A kid profile can only approve a sign-in to itself",
		"Подтвердить вход в другой профиль может только администратор": "Only an admin can approve a sign-in to another profile",
	})
}
