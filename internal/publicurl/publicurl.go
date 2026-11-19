// Package publicurl holds the server's public address: how browsers and
// the apps reach it from outside the home network ("https://antresol.example",
// no trailing slash). VAULT_PUBLIC_URL, when set, is the address; else the
// one an admin saved in «Адрес сервера» (server settings), which applies at
// once: the links of letters, Shikimori's redirect URI and the addons' info
// read it each time.
package publicurl

import (
	"net/url"
	"strings"
	"sync/atomic"
)

// Address is the public address of a running server.
type Address struct {
	env   string
	saved atomic.Pointer[string]
}

// New is the address with VAULT_PUBLIC_URL's value env ("" when unset).
func New(env string) *Address {
	return &Address{env: strings.TrimRight(strings.TrimSpace(env), "/")}
}

// Env is VAULT_PUBLIC_URL ("" when unset): while set, it wins over the saved one.
func (a *Address) Env() string {
	if a == nil {
		return ""
	}
	return a.env
}

// Get is the address in force: VAULT_PUBLIC_URL, else the saved one, else "".
func (a *Address) Get() string {
	if a == nil {
		return ""
	}
	if a.env != "" {
		return a.env
	}
	if s := a.saved.Load(); s != nil {
		return *s
	}
	return ""
}

// SetSaved is the address saved in the settings ("" clears it).
func (a *Address) SetSaved(s string) {
	if a == nil {
		return
	}
	n, _ := Normalize(s)
	a.saved.Store(&n)
}

// Normalize trims s and its trailing slashes; ok is false unless s is ""
// or an http(s) URL with a host and nothing after the path (no query, no
// fragment, no user).
func Normalize(s string) (string, bool) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return "", true
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return s, true
}
