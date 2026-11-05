package jellyfin

import (
	"context"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// Jellyfin clients have no badge: a cleaned voice-over says so in its title.
func TestCleanedTrackTitle(t *testing.T) {
	p := media.ProbeResult{Container: "mkv", DurationMs: 60_000,
		Audio: []media.AudioStream{{Index: 1, Codec: "aac", Lang: "rus", Title: "LostFilm", Channels: 2, Default: true}}}
	audio := []media.ExternalAudio{
		{Path: "/d/audio/1-1.mka", Studio: "LostFilm", Kind: "cleaned", CleanedOf: 1,
			Stream: media.AudioStream{Codec: "eac3", Lang: "rus", Title: "LostFilm", Channels: 6}},
		{Path: "/m/Film.AlexFilm.mka", Studio: "AlexFilm", Stream: media.AudioStream{Codec: "ac3", Lang: "ru", Title: "AlexFilm", Channels: 6}},
	}
	ms := mediaSource(context.Background(), library.File{ID: 1, Path: "/m/Film.mkv"}, p, audio)
	var got []MediaStream
	for _, st := range ms.MediaStreams {
		if st.Type == "Audio" {
			got = append(got, st)
		}
	}
	if len(got) != 3 || got[1].Index != 2000 || got[1].Title != "LostFilm (очищено)" || !strings.HasPrefix(got[1].DisplayTitle, "LostFilm (очищено) - RUS") {
		t.Fatalf("%+v", got)
	}
	if got[0].Title != "LostFilm" || got[2].Title != "AlexFilm" {
		t.Fatalf("others renamed: %+v", got)
	}
	// in the client's language (Accept-Language)
	en := mediaSource(i18n.WithLanguage(context.Background(), "en"), library.File{ID: 1, Path: "/m/Film.mkv"}, p, audio)
	if st := en.MediaStreams[1]; st.Index != 2000 || st.Title != "LostFilm (cleaned)" {
		t.Fatalf("%+v", st)
	}
}
