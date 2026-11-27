package transcript

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeLLM answers in turn (the last answer again), or fails with err.
type fakeLLM struct {
	answers []string
	reply   func(user string) string
	err     error
	asked   []string
	system  []string
}

func (f *fakeLLM) AskSchema(_ context.Context, system, user string, schema map[string]any) (string, error) {
	f.asked, f.system = append(f.asked, user), append(f.system, system)
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		return "", errors.New("a schema every provider accepts is needed")
	}
	if f.err != nil {
		return "", f.err
	}
	if f.reply != nil {
		return f.reply(user), nil
	}
	return f.answers[min(len(f.asked), len(f.answers))-1], nil
}

// talk is a recording of n phrases, one every ten seconds, over files of
// perFile phrases.
func talk(n, perFile int, text string) []timed {
	var out []timed
	for i := range n {
		at := int64(i) * 10_000
		file := int64(i/perFile) + 1
		in := int64(i%perFile) * 10_000
		out = append(out, timed{Segment: Segment{at + 500, at + 9_500, fmt.Sprintf("%s %d", text, i)}, fileID: file, inFileMs: in + 500})
	}
	return out
}

func TestSplitChapters(t *testing.T) {
	llm := &fakeLLM{answers: []string{`{"chapters":[{"line":2,"title":"  «Начало» "},{"line":4,"title":"Конец."}]}`}}
	got, err := splitChapters(context.Background(), llm, "Книга", "ru", talk(6, 3, "фраза"))
	if err != nil {
		t.Fatal(err)
	}
	// the first starts the book whatever precedes its line; the others at
	// their line's first phrase
	want := []Chapter{{0, "Начало", 1, 0}, {1, "Конец", 2, 500}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(llm.system[0], "Russian") || !strings.Contains(llm.asked[0], "Книга") ||
		!strings.Contains(llm.asked[0], "\n1 [0:00] фраза 0\n2 [0:10] фраза 1\n") {
		t.Errorf("prompt %q\n%q", llm.system[0], llm.asked[0])
	}
}

// A title loses the quotes around the whole of it and a closing full stop,
// nothing else.
func TestChapterTitle(t *testing.T) {
	for in, want := range map[string]string{
		"  «Начало» ": "Начало", `"The Storm."`: "The Storm", "“Home”": "Home", "Конец.": "Конец",
		"Письмо «домой»": "Письмо «домой»", "«Море» и «Берег»": "«Море» и «Берег»", `The "Old" Man`: `The "Old" Man`,
		"И так далее...": "И так далее...", "Что дальше?": "Что дальше?", "«»": "", " . ": "",
	} {
		if got := chapterTitle(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// Phrases of a few seconds make one line, which never spans two files.
func TestChapterLines(t *testing.T) {
	segs := []timed{
		{Segment{0, 2000, "a"}, 1, 0},
		{Segment{2000, 4000, "b"}, 1, 2000},
		{Segment{11000, 12000, "c"}, 1, 11000},
		{Segment{14000, 15000, "d"}, 2, 0},
		{Segment{15000, 16000, "e"}, 2, 1000},
	}
	var got []string
	for _, l := range chapterLines(segs) {
		got = append(got, fmt.Sprintf("%d/%d %s", l.fileID, l.inFileMs, l.Text))
	}
	if strings.Join(got, "|") != "1/0 a b|1/11000 c|2/0 d e" {
		t.Fatalf("%q", got)
	}
}

// A long transcript is split in parts, each asked on its own; a part may
// start no chapter.
func TestSplitChaptersInParts(t *testing.T) {
	segs := talk(1500, 500, strings.Repeat("слово ", 20))
	llm := &fakeLLM{}
	llm.reply = func(user string) string {
		first := 0
		for _, l := range strings.Split(user, "\n") {
			if _, err := fmt.Sscanf(l, "%d [", &first); err == nil {
				break
			}
		}
		if len(llm.asked) == 2 {
			return `{"chapters":[]}`
		}
		return fmt.Sprintf(`{"chapters":[{"line":%d,"title":"Часть %d"}]}`, first, len(llm.asked))
	}
	got, err := splitChapters(context.Background(), llm, "Книга", "", segs)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.asked) < 4 || len(got) != len(llm.asked)-1 || got[0].StartMs != 0 || got[1].Title != "Часть 3" {
		t.Fatalf("%d parts, %+v", len(llm.asked), got)
	}
	for _, u := range llm.asked {
		if len([]rune(u)) > windowRunes+2000 {
			t.Errorf("a part of %d runes", len([]rune(u)))
		}
	}
	// the next part is told the chapter it continues
	if !strings.Contains(llm.asked[2], "Часть 1") {
		t.Errorf("part 3 %q", llm.asked[2][:200])
	}
}

func TestSplitChaptersRejects(t *testing.T) {
	segs := talk(360, 360, "фраза") // an hour: a line for each phrase
	for name, answer := range map[string]string{
		"not JSON":           `sure!`,
		"no list":            `{}`,
		"none":               `{"chapters":[]}`,
		"unknown line":       `{"chapters":[{"line":1,"title":"a"},{"line":999,"title":"b"}]}`,
		"line zero":          `{"chapters":[{"line":0,"title":"a"}]}`,
		"going back":         `{"chapters":[{"line":1,"title":"a"},{"line":90,"title":"b"},{"line":40,"title":"c"}]}`,
		"twice":              `{"chapters":[{"line":1,"title":"a"},{"line":90,"title":"b"},{"line":90,"title":"c"}]}`,
		"no title":           `{"chapters":[{"line":1,"title":"a"},{"line":90,"title":"  "}]}`,
		"a paragraph":        `{"chapters":[{"line":1,"title":"` + strings.Repeat("a", 200) + `"}]}`,
		"the start left out": `{"chapters":[{"line":100,"title":"a"},{"line":200,"title":"b"}]}`,
		"one for hours":      `{"chapters":[{"line":1,"title":"a"}]}`,
		"every line": func() string {
			var s []string
			for i := range 100 {
				s = append(s, fmt.Sprintf(`{"line":%d,"title":"t"}`, i+1))
			}
			return `{"chapters":[` + strings.Join(s, ",") + `]}`
		}(),
	} {
		llm := &fakeLLM{answers: []string{answer}}
		got, err := splitChapters(context.Background(), llm, "Книга", "ru", segs)
		if !errors.Is(err, errBadAnswer) || got != nil {
			t.Errorf("%s: %v %v", name, got, err)
		}
		if len(llm.asked) != 2 {
			t.Errorf("%s: asked %d times", name, len(llm.asked))
		}
	}
	// a model that cannot be asked is not a bad answer
	llm := &fakeLLM{err: errors.New("llm: 401")}
	if _, err := splitChapters(context.Background(), llm, "Книга", "ru", segs); err == nil || errors.Is(err, errBadAnswer) || len(llm.asked) != 1 {
		t.Errorf("%v after %d", err, len(llm.asked))
	}
}
