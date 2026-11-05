// Package i18n is the server's interface language: Russian (the source) and
// English. A request's language is resolved once by Middleware and kept in
// its context; every text a user reads goes through T (or Localize, for
// text made earlier in Russian).
//
// Keys are the Russian source text itself, so a call site reads as the
// message it shows: i18n.T(ctx, "Раздача не найдена"). The Russian catalog
// is the key (identity); other languages are maps from the Russian text to
// the translation (catalog_*.go, registered with Register). Format verbs
// work as in fmt: i18n.T(ctx, "Файл %s не найден", name). A key whose
// Russian is shared by two different English meanings gets a context:
// TC(ctx, "shelf", "Продолжить").
//
// Adding a language is a data change: a code in Languages and a catalog.
package i18n

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Supported language codes.
const (
	RU = "ru"
	EN = "en"
)

// Default is the language of text made without a request (background
// work) and of requests that state no language at all.
const Default = RU

// Languages are the supported codes, the source first.
var Languages = []string{RU, EN}

func supported(code string) bool {
	for _, l := range Languages {
		if l == code {
			return true
		}
	}
	return false
}

type ctxKey struct{}

// WithLanguage returns ctx carrying the language code (an unsupported one
// is ignored).
func WithLanguage(ctx context.Context, code string) context.Context {
	if !supported(code) {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, code)
}

// FromContext is the language in ctx; Default (ru) when none was set.
func FromContext(ctx context.Context) string {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(string); ok {
			return l
		}
	}
	return Default
}

// Resolve picks the language of a request: ?lang= (when supported) wins,
// else Accept-Language by the shared rule (the first preferred language
// whose primary subtag is supported; none → en), no header at all → ru.
func Resolve(r *http.Request) string {
	if l := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))); supported(l) {
		return l
	}
	h := r.Header.Values("Accept-Language")
	if len(h) == 0 || strings.TrimSpace(strings.Join(h, "")) == "" {
		return Default
	}
	return FromAcceptLanguage(strings.Join(h, ","))
}

// FromAcceptLanguage applies the rule to an Accept-Language value: the
// languages in preference order (q descending, ties keep their order); the
// first whose primary subtag is supported wins; none → en.
func FromAcceptLanguage(h string) string {
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(h, ",") {
		fields := strings.Split(part, ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		q := 1.0
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if v, ok := strings.CutPrefix(f, "q="); ok {
				if _, err := fmt.Sscanf(v, "%g", &q); err != nil {
					q = 0
				}
			}
		}
		if q <= 0 {
			continue
		}
		prefs = append(prefs, pref{tag, q})
	}
	// stable insertion sort by q, descending
	for i := 1; i < len(prefs); i++ {
		for j := i; j > 0 && prefs[j].q > prefs[j-1].q; j-- {
			prefs[j], prefs[j-1] = prefs[j-1], prefs[j]
		}
	}
	for _, p := range prefs {
		primary, _, _ := strings.Cut(p.tag, "-")
		primary, _, _ = strings.Cut(primary, "_")
		if supported(primary) {
			return primary
		}
	}
	return EN
}

// Middleware puts the request's language (Resolve) into its context
// (FromContext) and marks the response as varying by Accept-Language.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := Resolve(r)
		w.Header().Add("Vary", "Accept-Language")
		next.ServeHTTP(w, r.WithContext(WithLanguage(r.Context(), l)))
	})
}

// TMDB is the TMDB language tag for a code: ru-RU, en-US.
func TMDB(code string) string {
	if code == EN {
		return "en-US"
	}
	return "ru-RU"
}
