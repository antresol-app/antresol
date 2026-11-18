//go:build !sources

package api

import (
	"context"
	"encoding/json"

	"github.com/antresol-app/antresol/internal/auth"
)

// followOf: title pages have no "follow" field.
func (h *lib) followOf(context.Context, auth.User, string, int64) json.RawMessage { return nil }

// mayDiscover: no card leads outside the library.
func (h *lib) mayDiscover(auth.User) bool { return false }
