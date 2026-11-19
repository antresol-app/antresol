package i18n

// English for the personal API keys (/api/me/api-keys, the guard) and the
// MCP server (/mcp).
func init() {
	Register(EN, map[string]string{
		"Ключ API не найден или отозван":                               "The API key is unknown or has been revoked",
		"Слишком много запросов с этим ключом, попробуйте чуть позже":  "Too many requests with this key, try again a little later",
		"Ключом API этого сделать нельзя":                              "This cannot be done with an API key",
		"Этот ключ API только для чтения":                              "This API key is read-only",
		"Нужен ключ API: создайте его в «Настройки → Ключи API и ИИ»":  "An API key is needed: create one in Settings → API Keys & AI",
		"У детского профиля нет ключей API":                            "A kid profile has no API keys",
		"Назовите ключ и выберите, что ему можно":                      "Name the key and choose what it may do",
		"Ключ с администрированием может создать только администратор": "Only an admin can create a key with administration",
		"Ключей слишком много: отзовите ненужные":                      "Too many keys: revoke the ones you no longer need",
		"Ключ не найден": "Key not found",
	})
}
