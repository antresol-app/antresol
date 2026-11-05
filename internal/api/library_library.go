//go:build !sources

package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/antresol-app/antresol/internal/auth"
)

// onlineContinue: «Продолжить» holds library titles only.
func (h *lib) onlineContinue(*http.Request, auth.User) ([]datedCard, error) { return nil, nil }

// followOf: title pages have no "follow" field.
func (h *lib) followOf(context.Context, auth.User, string, int64) json.RawMessage { return nil }

// mayDiscover: no card leads outside the library.
func (h *lib) mayDiscover(auth.User) bool { return false }
