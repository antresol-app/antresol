package api

import "unicode"

// russian reports whether a message is meant for the user (Russian text).
func russian(msg string) bool {
	for _, r := range msg {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}
