//go:build !sources

package notify

import "github.com/antresol-app/antresol/internal/auth"

// The core build has only the shared topics (episodes, available).
func editionTopics(auth.User) []string { return nil }
