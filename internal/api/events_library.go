//go:build !sources

package api

import "github.com/antresol-app/antresol/internal/auth"

// eventPayload: every event goes out as published.
func eventPayload(_ string, _ auth.User, _ string, payload any) any { return payload }
