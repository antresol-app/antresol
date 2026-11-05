package i18n

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/antresol-app/antresol/internal/ru"
)

// ctxSep joins a context to a key (as gettext's msgctxt): "shelf\x04Продолжить".
const ctxSep = "\x04"

var (
	mu       sync.RWMutex
	catalogs = map[string]map[string]string{}
	// plurals[lang][ruOne] = the forms of that language (en: one, other)
	plurals = map[string]map[string][]string{}
	// ruForms[ruForm] = ruOne, for Localize of "5 томов"
	ruForms = map[string]string{}
	// conflicts found by Register (a key registered twice with two
	// translations); the catalog test fails on any
	conflicts []string
	patterns  []*pattern // lazily compiled format keys, see Localize
	patOK     bool
)

// Register adds translations for lang: Russian source text (or
// "context\x04text") → translation. Catalog files call it from init.
func Register(lang string, m map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	c := catalogs[lang]
	if c == nil {
		c = map[string]string{}
		catalogs[lang] = c
	}
	for k, v := range m {
		if old, ok := c[k]; ok && old != v {
			conflicts = append(conflicts, fmt.Sprintf("%s %q: %q vs %q", lang, k, old, v))
		}
		c[k] = v
	}
	patOK = false
}

// RegisterPlural adds a noun's forms: the Russian one/few/many and the
// translation's forms for lang (en: singular, plural). Plural looks them up
// by the Russian "one" form.
func RegisterPlural(lang string, ruOne, ruFew, ruMany string, forms ...string) {
	mu.Lock()
	defer mu.Unlock()
	p := plurals[lang]
	if p == nil {
		p = map[string][]string{}
		plurals[lang] = p
	}
	key := ruOne + "|" + ruFew + "|" + ruMany
	if old, ok := p[key]; ok && strings.Join(old, "|") != strings.Join(forms, "|") {
		conflicts = append(conflicts, fmt.Sprintf("%s plural %q: %v vs %v", lang, key, old, forms))
	}
	p[key] = forms
	for _, f := range []string{ruOne, ruFew, ruMany} {
		ruForms[f] = key
	}
}

func lookup(lang, key string) (string, bool) {
	if lang == RU {
		_, k, ok := strings.Cut(key, ctxSep)
		if ok {
			return k, true
		}
		return key, true
	}
	mu.RLock()
	v, ok := catalogs[lang][key]
	mu.RUnlock()
	return v, ok
}

// TL is key in lang, formatted with args. A key missing from the catalog
// shows its Russian text (the catalog test keeps that from happening).
func TL(lang, key string, args ...any) string {
	s, ok := lookup(lang, key)
	if !ok {
		s, _ = lookup(RU, key)
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, localizeArgs(lang, args)...)
}

// T is key in the language of ctx (see TL).
func T(ctx context.Context, key string, args ...any) string {
	return TL(FromContext(ctx), key, args...)
}

// TC is T for a key whose Russian text has more than one meaning: the
// context tells the translations apart ("shelf", "button"…).
func TC(ctx context.Context, context, key string, args ...any) string {
	return TL(FromContext(ctx), context+ctxSep+key, args...)
}

// Plural is "n <noun>" in the language of ctx: «5 томов», "5 volumes".
// The noun is given by its Russian forms (one, few, many), which must be
// registered with RegisterPlural for every other language.
func Plural(ctx context.Context, n int, one, few, many string) string {
	return PluralL(FromContext(ctx), n, one, few, many)
}

// PluralL is Plural for a language code.
func PluralL(lang string, n int, one, few, many string) string {
	if lang == RU {
		return ru.Plural(n, one, few, many)
	}
	mu.RLock()
	forms := plurals[lang][one+"|"+few+"|"+many]
	mu.RUnlock()
	if len(forms) == 0 {
		return ru.Plural(n, one, few, many)
	}
	return strconv.Itoa(n) + " " + pluralForm(lang, n, forms)
}

// PluralWord is only the noun's form for n (no number): "volumes".
func PluralWord(ctx context.Context, n int, one, few, many string) string {
	s := PluralL(FromContext(ctx), n, one, few, many)
	_, w, _ := strings.Cut(s, " ")
	return w
}

func pluralForm(lang string, n int, forms []string) string {
	// English and the like: one for 1, other otherwise
	if (n == 1 || n == -1) || len(forms) == 1 {
		return forms[0]
	}
	return forms[1]
}

// localizeArgs translates error arguments (an *Error inside, Russian text
// of a known key) so a message reads in one language.
func localizeArgs(lang string, args []any) []any {
	if lang == RU {
		return args
	}
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case error:
			out[i] = localizedErr{msg: MessageL(lang, v), err: v}
		default:
			out[i] = a
		}
	}
	return out
}

type localizedErr struct {
	msg string
	err error
}

func (e localizedErr) Error() string { return e.msg }
func (e localizedErr) Unwrap() error { return e.err }

// Error is a user-facing error whose text is a catalog key: Error() is the
// Russian text, Message/MessageL render it in another language.
type Error struct {
	Key  string
	Args []any
	err  error // fmt.Errorf of the Russian text (keeps %w)
}

// Errorf is a user-facing error: fmt.Errorf with a catalog key as the
// format (%w works). Error() is Russian; Message(ctx, err) translates it,
// even wrapped in other errors.
func Errorf(key string, args ...any) error {
	ruKey, _ := lookup(RU, key)
	return &Error{Key: key, Args: args, err: fmt.Errorf(ruKey, args...)}
}

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return errors.Unwrap(e.err) }

// In renders the error in lang.
func (e *Error) In(lang string) string {
	if lang == RU {
		return e.Error()
	}
	s, ok := lookup(lang, e.Key)
	if !ok {
		return e.Error()
	}
	return fmt.Errorf(s, localizeArgs(lang, e.Args)...).Error()
}

// Message is err's text in the language of ctx (see MessageL).
func Message(ctx context.Context, err error) string { return MessageL(FromContext(ctx), err) }

// MessageL is err's text in lang: an *Error in its chain is rendered in
// lang (and replaced inside the outer text, when other errors wrap it);
// otherwise the Russian text goes through Localize.
func MessageL(lang string, err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if lang == RU {
		return s
	}
	var e *Error
	if errors.As(err, &e) {
		inner := e.Error()
		tr := e.In(lang)
		if s == inner {
			return tr
		}
		if strings.Contains(s, inner) {
			return strings.Replace(s, inner, tr, 1)
		}
	}
	return LocalizeL(lang, s)
}

// Localize is LocalizeL in the language of ctx.
func Localize(ctx context.Context, s string) string { return LocalizeL(FromContext(ctx), s) }

// LocalizeL translates Russian text made earlier (a stored status, a
// background job's message, an error's text): a key as is, the rendering
// of a format key (its arguments are taken back from the text and
// translated themselves), or a plural "5 томов". Anything else (no
// Cyrillic, unknown text) stays as it is.
func LocalizeL(lang, s string) string {
	if lang == RU || s == "" || !hasCyrillic(s) {
		return s
	}
	if v, ok := lookup(lang, s); ok {
		return v
	}
	if v, ok := localizePlural(lang, s); ok {
		return v
	}
	for _, p := range compiledPatterns() {
		if !strings.Contains(s, p.anchor) {
			continue
		}
		m := p.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		args := make([]any, len(m)-1)
		for i, a := range m[1:] {
			switch p.verbs[i] {
			case 'd':
				if n, err := strconv.Atoi(a); err == nil {
					args[i] = n
					continue
				}
				args[i] = a
			default:
				args[i] = LocalizeL(lang, a)
			}
		}
		tr, ok := lookup(lang, p.key)
		if !ok {
			return s
		}
		return fmt.Sprintf(stringVerbs(tr), args...)
	}
	return s
}

func localizePlural(lang, s string) (string, bool) {
	num, word, ok := strings.Cut(s, " ")
	if !ok {
		return "", false
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return "", false
	}
	mu.RLock()
	key, ok := ruForms[word]
	forms := plurals[lang][key]
	mu.RUnlock()
	if !ok || len(forms) == 0 {
		return "", false
	}
	return strconv.Itoa(n) + " " + pluralForm(lang, n, forms), true
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// verbRE matches a fmt verb (with flags, width, precision, an explicit
// argument index) or %%.
var verbRE = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*\d*(?:\.\d+)?(?:\[\d+\])?[a-zA-Z%]`)

// Verbs is the verb letters of a format string in order, %% left out.
func Verbs(format string) []byte {
	var out []byte
	for _, v := range verbRE.FindAllString(format, -1) {
		if v == "%%" {
			continue
		}
		out = append(out, v[len(v)-1])
	}
	return out
}

// stringVerbs turns every verb but %d into %v-like rendering of the
// captured text (the arguments Localize recovers are strings): %q → %s,
// %.1f → %s. Explicit indexes are kept.
func stringVerbs(format string) string {
	return verbRE.ReplaceAllStringFunc(format, func(v string) string {
		last := v[len(v)-1]
		if v == "%%" || last == 'd' || last == 's' || last == 'v' {
			return v
		}
		idx := ""
		if i := strings.Index(v, "["); i >= 0 {
			idx = v[i : strings.Index(v, "]")+1]
		}
		return "%" + idx + "s"
	})
}

type pattern struct {
	key    string
	re     *regexp.Regexp
	verbs  []byte
	anchor string // the longest literal part, a cheap prefilter
}

func compiledPatterns() []*pattern {
	mu.RLock()
	if patOK {
		p := patterns
		mu.RUnlock()
		return p
	}
	mu.RUnlock()
	mu.Lock()
	defer mu.Unlock()
	if patOK {
		return patterns
	}
	keys := map[string]bool{}
	for _, c := range catalogs {
		for k := range c {
			keys[k] = true
		}
	}
	var out []*pattern
	for k := range keys {
		ruKey := k
		if _, after, ok := strings.Cut(k, ctxSep); ok {
			ruKey = after
		}
		verbs := Verbs(ruKey)
		if len(verbs) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString("^")
		anchor := ""
		last := 0
		for _, loc := range verbRE.FindAllStringIndex(ruKey, -1) {
			lit := ruKey[last:loc[0]]
			if len(lit) > len(anchor) {
				anchor = lit
			}
			b.WriteString(regexp.QuoteMeta(lit))
			v := ruKey[loc[0]:loc[1]]
			switch {
			case v == "%%":
				b.WriteString("%")
			case v[len(v)-1] == 'd':
				b.WriteString(`(-?\d+)`)
			default:
				b.WriteString(`(.+?)`)
			}
			last = loc[1]
		}
		tail := ruKey[last:]
		if len(tail) > len(anchor) {
			anchor = tail
		}
		b.WriteString(regexp.QuoteMeta(tail))
		b.WriteString("$")
		if strings.TrimSpace(anchor) == "" || !hasCyrillic(ruKey) {
			continue // "%s: %s" would match anything
		}
		re, err := regexp.Compile(b.String())
		if err != nil {
			continue
		}
		// explicit indexes are not supported in the Russian keys
		if strings.Contains(ruKey, "%[") {
			continue
		}
		out = append(out, &pattern{key: k, re: re, verbs: verbs, anchor: anchor})
	}
	// longest anchors first: the most specific key wins
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].anchor) != len(out[j].anchor) {
			return len(out[i].anchor) > len(out[j].anchor)
		}
		return out[i].key < out[j].key
	})
	patterns, patOK = out, true
	return out
}

// Keys is every key registered for lang (tests).
func Keys(lang string) []string {
	mu.RLock()
	defer mu.RUnlock()
	var out []string
	for k := range catalogs[lang] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Translation is lang's text for key and whether it has one (tests).
func Translation(lang, key string) (string, bool) { return lookup(lang, key) }

// HasPlural says whether the noun has forms in lang (tests).
func HasPlural(lang, one, few, many string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return len(plurals[lang][one+"|"+few+"|"+many]) > 0
}

// Conflicts are keys registered twice with different translations (tests).
func Conflicts() []string {
	mu.RLock()
	defer mu.RUnlock()
	return append([]string(nil), conflicts...)
}
