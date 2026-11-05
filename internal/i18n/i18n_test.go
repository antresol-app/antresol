package i18n

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name, query, header string
		noHeader            bool
		want                string
	}{
		{name: "ru-RU", header: "ru-RU", want: "ru"},
		{name: "ru", header: "ru", want: "ru"},
		{name: "en-US", header: "en-US", want: "en"},
		{name: "en-GB with q", header: "en-GB,en;q=0.9", want: "en"},
		{name: "uk then ru", header: "uk,ru;q=0.8", want: "ru"},
		{name: "uk then en then ru", header: "uk, en;q=0.9, ru;q=0.8", want: "en"},
		{name: "q order beats list order", header: "en;q=0.5, ru;q=0.9", want: "ru"},
		{name: "de", header: "de", want: "en"},
		{name: "be is not Russian", header: "be", want: "en"},
		{name: "wildcard", header: "*", want: "en"},
		{name: "q=0 is refused", header: "ru;q=0, de", want: "en"},
		{name: "underscore", header: "ru_RU", want: "ru"},
		{name: "case", header: "EN-us", want: "en"},
		{name: "no header", noHeader: true, want: "ru"},
		{name: "empty header", header: "", want: "ru"},
		{name: "lang wins over header", query: "en", header: "ru-RU", want: "en"},
		{name: "lang=ru wins", query: "ru", header: "en-US", want: "ru"},
		{name: "lang without header", query: "en", noHeader: true, want: "en"},
		{name: "unknown lang ignored", query: "fr", header: "en", want: "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := "/api/home"
			if tc.query != "" {
				u += "?lang=" + tc.query
			}
			r := httptest.NewRequest(http.MethodGet, u, nil)
			if !tc.noHeader {
				r.Header.Set("Accept-Language", tc.header)
			}
			if got := Resolve(r); got != tc.want {
				t.Errorf("Resolve = %q, want %q", got, tc.want)
			}
			var got string
			Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = FromContext(r.Context())
			})).ServeHTTP(httptest.NewRecorder(), r)
			if got != tc.want {
				t.Errorf("middleware context = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFromContextDefault(t *testing.T) {
	if got := FromContext(context.Background()); got != RU {
		t.Errorf("no language = %q, want ru", got)
	}
	if got := FromContext(WithLanguage(context.Background(), "fr")); got != RU {
		t.Errorf("unsupported language = %q, want ru", got)
	}
}

func init() {
	// test-only keys (the catalog test checks them like any other)
	Register(EN, map[string]string{
		"Тестовая раздача не найдена": "Test torrent not found",
		"Тестовый файл %s не найден":  "Test file %s not found",
		"Тест: скачано %d из %d":      "Test: %d of %d downloaded",
		"тест\x04Продолжить":          "Continue Watching",
		"Тестовая ошибка: %w":         "Test error: %w",
		"Тест: в папке %s":            "Test: in %s",
		"Тест: отмечено %s":           "Test: %s marked",
	})
	RegisterPlural(EN, "тестовый том", "тестовых тома", "тестовых томов", "test volume", "test volumes")
}

func TestT(t *testing.T) {
	ru := WithLanguage(context.Background(), RU)
	en := WithLanguage(context.Background(), EN)
	if got := T(ru, "Тестовая раздача не найдена"); got != "Тестовая раздача не найдена" {
		t.Errorf("ru = %q", got)
	}
	if got := T(en, "Тестовая раздача не найдена"); got != "Test torrent not found" {
		t.Errorf("en = %q", got)
	}
	if got := T(en, "Тестовый файл %s не найден", "a.mkv"); got != "Test file a.mkv not found" {
		t.Errorf("en format = %q", got)
	}
	if got := TC(ru, "тест", "Продолжить"); got != "Продолжить" {
		t.Errorf("ru context = %q", got)
	}
	if got := TC(en, "тест", "Продолжить"); got != "Continue Watching" {
		t.Errorf("en context = %q", got)
	}
	if got := T(en, "Нет такого ключа"); got != "Нет такого ключа" {
		t.Errorf("missing key = %q, want the Russian", got)
	}
}

func TestPlural(t *testing.T) {
	ru := WithLanguage(context.Background(), RU)
	en := WithLanguage(context.Background(), EN)
	for n, want := range map[int]string{1: "1 тестовый том", 3: "3 тестовых тома", 5: "5 тестовых томов", 21: "21 тестовый том"} {
		if got := Plural(ru, n, "тестовый том", "тестовых тома", "тестовых томов"); got != want {
			t.Errorf("ru %d = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int]string{0: "0 test volumes", 1: "1 test volume", 3: "3 test volumes", 21: "21 test volumes"} {
		if got := Plural(en, n, "тестовый том", "тестовых тома", "тестовых томов"); got != want {
			t.Errorf("en %d = %q, want %q", n, got, want)
		}
	}
	if got := PluralWord(en, 2, "тестовый том", "тестовых тома", "тестовых томов"); got != "test volumes" {
		t.Errorf("en word = %q", got)
	}
}

func TestErrorf(t *testing.T) {
	base := errors.New("disk")
	err := Errorf("Тестовая ошибка: %w", base)
	if err.Error() != "Тестовая ошибка: disk" {
		t.Errorf("Error() = %q", err.Error())
	}
	if !errors.Is(err, base) {
		t.Error("Errorf lost %w")
	}
	en := WithLanguage(context.Background(), EN)
	if got := Message(en, err); got != "Test error: disk" {
		t.Errorf("en = %q", got)
	}
	wrapped := fmt.Errorf("acquire: %w", err)
	if got := Message(en, wrapped); got != "acquire: Test error: disk" {
		t.Errorf("en wrapped = %q", got)
	}
	nested := Errorf("Тестовая ошибка: %w", Errorf("Тестовая раздача не найдена"))
	if got := Message(en, nested); got != "Test error: Test torrent not found" {
		t.Errorf("en nested = %q", got)
	}
	if got := Message(en, errors.New("Тестовая раздача не найдена")); got != "Test torrent not found" {
		t.Errorf("en plain = %q", got)
	}
	if got := Message(WithLanguage(context.Background(), RU), nested); got != "Тестовая ошибка: Тестовая раздача не найдена" {
		t.Errorf("ru nested = %q", got)
	}
}

func TestLocalize(t *testing.T) {
	for in, want := range map[string]string{
		"Тестовая раздача не найдена":               "Test torrent not found",
		"Тестовый файл Мой фильм.mkv не найден":     "Test file Мой фильм.mkv not found",
		"Тест: скачано 3 из 10":                     "Test: 3 of 10 downloaded",
		"Тест: в папке Тестовая раздача не найдена": "Test: in Test torrent not found",
		"Тест: отмечено 5 тестовых томов":           "Test: 5 test volumes marked",
		"5 тестовых томов":                          "5 test volumes",
		"Совсем неизвестный текст":                  "Совсем неизвестный текст",
		"plain english": "plain english",
	} {
		if got := LocalizeL(EN, in); got != want {
			t.Errorf("LocalizeL(%q) = %q, want %q", in, got, want)
		}
		if got := LocalizeL(RU, in); got != in {
			t.Errorf("ru LocalizeL(%q) = %q", in, got)
		}
	}
}

func TestVerbs(t *testing.T) {
	for in, want := range map[string]string{
		"%s и %d":      "sd",
		"100%% и %.1f": "f",
		"%[2]s, %[1]d": "sd",
		"нет глаголов": "",
		"%q: %v":       "qv",
	} {
		if got := string(Verbs(in)); got != want {
			t.Errorf("Verbs(%q) = %q, want %q", in, got, want)
		}
	}
}
