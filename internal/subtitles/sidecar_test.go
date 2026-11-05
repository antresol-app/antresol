package subtitles

import (
	"os"
	"path/filepath"
	"testing"
)

func touchFile(t *testing.T, p string, b string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseName(t *testing.T) {
	for _, c := range []struct {
		name, lang, title string
		forced, ok        bool
	}{
		{"Movie.srt", "", "", false, true},
		{"Movie.en.srt", "en", "", false, true},
		{"Movie.rus.forced.srt", "ru", "", true, true},
		{"Movie.Russian.ass", "ru", "", false, true},
		{"Movie.рус.srt", "ru", "", false, true},
		{"Movie.en.commentary.srt", "en", "commentary", false, true},
		{"Movie.uk.srt", "uk", "", false, true},
		{"Movie.Ukrainian.srt", "uk", "", false, true},
		{"Movie.tr.sdh.srt", "tr", "sdh", false, true},
		{"Movie.Extended.ru.srt", "", "", false, false},
		{"Movie 2.ru.srt", "", "", false, false},
		{"Movie.en.mkv", "", "", false, false},
	} {
		lang, title, forced, ok := ParseName(c.name, "Movie")
		if ok != c.ok || lang != c.lang || title != c.title || forced != c.forced {
			t.Errorf("%s: %q %q %v %v", c.name, lang, title, forced, ok)
		}
	}
}

func TestLangOf(t *testing.T) {
	for name, want := range map[string]string{
		"2_Russian.srt": "ru", "3_English.srt": "en", "Movie.en.srt": "en", "Movie.rus.forced.srt": "ru",
		"4_Ukrainian.srt": "uk", "It.2017.srt": "", "Moscow Does Not Believe in Tears CD1.srt": "",
		"Фильм.рус.srt": "ru", "Show.S01E02.eng.sdh.srt": "en",
	} {
		if got := LangOf(name); got != want {
			t.Errorf("%s: %q want %q", name, got, want)
		}
	}
}

func TestSniffLang(t *testing.T) {
	cp1251 := "1\n00:00:01,000 --> 00:00:02,000\n\xcf\xf0\xe8\xe2\xe5\xf2, \xea\xe0\xea \xe4\xe5\xeb\xe0?\n"
	for in, want := range map[string]string{
		"1\n00:00:01,000 --> 00:00:02,000\nПривет, как дела?\n": "ru",
		cp1251: "ru",
		"1\n00:00:01,000 --> 00:00:02,000\nПривіт, як справи? Їжак і ґанок, єнот\n": "",
		"1\n00:00:01,000 --> 00:00:02,000\nHello there\n":                           "",
	} {
		if got := SniffLang([]byte(in)); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestFindExternal(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	video := filepath.Join(root, "Movie (2020)", "Movie (2020).mkv")
	touchFile(t, video, "v")
	touchFile(t, filepath.Join(root, "Movie (2020)", "Movie (2020).en.srt"), "x")
	touchFile(t, filepath.Join(root, "Movie (2020)", "Movie (2020).en.commentary.srt"), "x")
	touchFile(t, filepath.Join(root, "Movie (2020)", "Other.ru.srt"), "x")
	touchFile(t, filepath.Join(root, "Movie (2020)", "Subs", "2_Russian.srt"), "x")
	touchFile(t, filepath.Join(root, "Movie (2020)", "Subs", "Movie (2020)", "3_English.srt"), "x")
	touchFile(t, filepath.Join(root, "Movie (2020)", "Subs", "notes.txt"), "x")
	touchFile(t, filepath.Join(data, "subs", "ext", "7.ru.srt"), "x")
	touchFile(t, filepath.Join(data, "subs", "ext", "77.ru.srt"), "x")
	got := FindExternal(video, data, 7)
	want := []struct{ rel, lang, title string }{
		{"Movie (2020)/Movie (2020).en.commentary.srt", "en", "commentary"},
		{"Movie (2020)/Movie (2020).en.srt", "en", ""},
		{"Movie (2020)/Subs/2_Russian.srt", "ru", ""},
		{"Movie (2020)/Subs/Movie (2020)/3_English.srt", "en", ""},
		{"", "ru", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, w := range want {
		p := filepath.Join(root, w.rel)
		if w.rel == "" {
			p = filepath.Join(data, "subs", "ext", "7.ru.srt")
		}
		if got[i].Path != p || got[i].Lang != w.lang || got[i].Title != w.title || got[i].Codec != "subrip" {
			t.Errorf("%d: %+v want %s %s %s", i, got[i], p, w.lang, w.title)
		}
	}
}

func TestFindExternalSubsFolderSharedBySeveralVideos(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Season 01")
	touchFile(t, filepath.Join(dir, "Show - S01E01.mkv"), "v")
	touchFile(t, filepath.Join(dir, "Show - S01E02.mkv"), "v")
	touchFile(t, filepath.Join(dir, "Subs", "Show - S01E01.ru.srt"), "x")
	touchFile(t, filepath.Join(dir, "Subs", "2_Russian.srt"), "x") // whose? not guessed
	got := FindExternal(filepath.Join(dir, "Show - S01E01.mkv"), "", 1)
	if len(got) != 1 || got[0].Lang != "ru" || filepath.Base(got[0].Path) != "Show - S01E01.ru.srt" {
		t.Fatalf("%+v", got)
	}
}

func TestWriteSidecar(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	video := filepath.Join(root, "Дюна (2024).mkv")
	touchFile(t, video, "v")
	p, err := WriteSidecar(video, data, 5, "ru", []byte("a"))
	if err != nil || p != filepath.Join(root, "Дюна (2024).ru.srt") {
		t.Fatalf("%s %v", p, err)
	}
	if p, _ := WriteSidecar(video, data, 5, "ru", []byte("a")); p != filepath.Join(root, "Дюна (2024).ru.srt") {
		t.Fatalf("same content again: %s", p)
	}
	if p, _ := WriteSidecar(video, data, 5, "ru", []byte("b")); p != filepath.Join(root, "Дюна (2024).ru.2.srt") {
		t.Fatalf("never overwrites: %s", p)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	p, err = WriteSidecar(video, data, 5, "en", []byte("c"))
	if err != nil || p != filepath.Join(data, "subs", "ext", "5.en.srt") {
		t.Fatalf("read-only: %s %v", p, err)
	}
}
