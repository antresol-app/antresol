package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKeyForbiddenPaths(t *testing.T) {
	for _, c := range []struct {
		method, path string
		forbidden    bool
	}{
		{"GET", "/api/me/app-password", true},
		{"POST", "/api/me/managed/4/app-password", true},
		{"POST", "/api/me/api-keys", true},
		{"POST", "/api/auth/pair/approve", true},
		{"PATCH", "/api/users/3", true},
		{"GET", "/api/users", false},
		{"GET", "/api/items/app-passwords", false},
		{"POST", "/api/items/5/watched", false},
	} {
		if got := keyForbidden(httptest.NewRequest(c.method, c.path, nil)); got != c.forbidden {
			t.Errorf("%s %s: forbidden %v, want %v", c.method, c.path, got, c.forbidden)
		}
	}
}

func TestAdminKeyMay(t *testing.T) {
	for _, c := range []struct {
		method, path string
		ok           bool
	}{
		{"POST", "/api/library/scan", true},
		{"POST", "/mcp", true},
		{"POST", "/api/addons/sample/disable", true},
		{"POST", "/api/addons/sample/enable", true},
		{"POST", "/api/addons/sample/x/acquire", true},
		{"POST", "/api/addons", false},
		{"POST", "/api/addons/preview", false},
		{"POST", "/api/addons/sample/update", false},
		{"DELETE", "/api/addons/sample", false},
		{"GET", "/api/addons/sample/enable", false},
		{"POST", "/api/addons/a/b/enable", false},
		{"POST", "/api/addons/sample/x/admin/settings", false},
		{"PUT", "/api/settings", false},
		{"POST", "/api/files/trash", false},
		{http.MethodPost, "/api/library/scan/", false},
	} {
		if got := adminKeyMay(httptest.NewRequest(c.method, c.path, nil)); got != c.ok {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.ok)
		}
	}
}
