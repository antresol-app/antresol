package mediaimport

import (
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
)

func TestReportLocalize(t *testing.T) {
	ctx := i18n.WithLanguage(context.Background(), i18n.EN)
	rep := &Report{Lines: []Line{
		{Name: "Другие отметки", Reason: othersWarning("Jellyfin", map[string]int{"видео": 2, "музыка": 1})},
		{Name: "ТВ-каналы", Reason: JellyfinChannelsLine(i18n.RU, 5).Reason},
	}}
	rep.Localize(ctx)
	want := []Line{
		{Name: "Other Marks", Reason: "Jellyfin: 3 marks not moved (videos, music)"},
		{Name: "TV Channels", Reason: "Jellyfin: TV channels aren’t moved (5 channels)"},
	}
	for i, l := range rep.Lines {
		if l != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, l, want[i])
		}
	}
	ru := &Report{Lines: []Line{{Name: "ТВ-каналы", Reason: "Уже в Antresol"}}}
	ru.Localize(context.Background())
	if ru.Lines[0].Name != "ТВ-каналы" || ru.Lines[0].Reason != "Уже в Antresol" {
		t.Errorf("ru changed: %+v", ru.Lines[0])
	}
}

func TestLeftoversEnglish(t *testing.T) {
	ctx := i18n.WithLanguage(context.Background(), i18n.EN)
	l := JellyfinChannelsLine(i18n.FromContext(ctx), 1)
	if l.Name != "TV Channels" || l.Reason != "Jellyfin: TV channels aren’t moved (1 channel)" {
		t.Errorf("got %+v", l)
	}
	if r := JellyfinChannelsLine(i18n.RU, 2).Reason; r != "Jellyfin: ТВ-каналы не переносятся (2 канала)" {
		t.Errorf("ru = %q", r)
	}
}
