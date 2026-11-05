package extaudio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
)

// tree writes empty files at the given paths under a temp dir (folders
// made as needed) and returns the dir.
func tree(t *testing.T, paths ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rels(dir string, ts []Track) []string {
	out := []string{}
	for _, t := range ts {
		r, _ := filepath.Rel(dir, t.Path)
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func TestFindSameStemStudio(t *testing.T) {
	dir := tree(t, "Show.S01E02.mkv", "Show.S01E02.LostFilm.mka", "Show.S01E02.ru.srt")
	got := Find(filepath.Join(dir, "Show.S01E02.mkv"))
	if len(got) != 1 {
		t.Fatalf("tracks: %v", rels(dir, got))
	}
	if g := got[0]; g.Lang != "ru" || g.Studio != "LostFilm" || g.Title != "LostFilm" || filepath.Base(g.Path) != "Show.S01E02.LostFilm.mka" {
		t.Fatalf("%+v", g)
	}
}

// Review focus 2: a season pack's sound folder gives each episode its own
// track only.
func TestFindSeasonPackByEpisode(t *testing.T) {
	dir := tree(t, "Show.S01E01.mkv", "Show.S01E02.mkv", "Show.S01E03.mkv",
		"RUS Sound/Show.S01E01.ac3", "RUS Sound/Show.S01E02.ac3", "RUS Sound/Show.S01E03.ac3")
	for _, ep := range []string{"01", "02", "03"} {
		got := rels(dir, Find(filepath.Join(dir, "Show.S01E"+ep+".mkv")))
		if len(got) != 1 || got[0] != "RUS Sound/Show.S01E"+ep+".ac3" {
			t.Fatalf("E%s: %v", ep, got)
		}
	}
	// the sound folder's name says Russian
	if g := Find(filepath.Join(dir, "Show.S01E02.mkv")); g[0].Lang != "ru" {
		t.Fatalf("%+v", g[0])
	}
}

// A season pack whose episode files are renamed (the library's
// «Show - S01E02.mkv») still finds the track by its episode code.
func TestFindEpisodeCodeOtherName(t *testing.T) {
	dir := tree(t, "Show - S01E02.mkv", "Show - S01E03.mkv", "Rus Sound [LostFilm]/show.s01e02.rus.ac3", "Rus Sound [LostFilm]/show.s01e03.rus.ac3")
	got := Find(filepath.Join(dir, "Show - S01E02.mkv"))
	if r := rels(dir, got); len(r) != 1 || r[0] != "Rus Sound [LostFilm]/show.s01e02.rus.ac3" {
		t.Fatalf("%v", r)
	}
	if got[0].Studio != "LostFilm" || got[0].Lang != "ru" {
		t.Fatalf("%+v", got[0])
	}
}

func TestFindSingleVideoAnyInSoundFolder(t *testing.T) {
	dir := tree(t, "Film.2020.1080p.mkv", "Audio/Rus/Кубик в Кубе.ac3", "Audio/Rus/readme.txt")
	got := Find(filepath.Join(dir, "Film.2020.1080p.mkv"))
	if len(got) != 1 {
		t.Fatalf("%v", rels(dir, got))
	}
	if g := got[0]; g.Studio != "Кубик в Кубе" || g.Title != "Кубик в Кубе" || g.Lang != "ru" {
		t.Fatalf("%+v", g)
	}
}

// A music album next to a film is not a dub.
func TestFindIgnoresAlbumFolder(t *testing.T) {
	dir := tree(t, "Film.mkv", "Album/01 Song.flac", "Album/02 Song.mp3", "OST.mp3")
	if got := Find(filepath.Join(dir, "Film.mkv")); len(got) != 0 {
		t.Fatalf("%v", rels(dir, got))
	}
}

func TestFindLanguageFromName(t *testing.T) {
	dir := tree(t, "Film.mkv", "Film.eng.ac3", "Film.ukr.mka", "Film.Commentary.mka")
	got := Find(filepath.Join(dir, "Film.mkv"))
	if r := rels(dir, got); len(r) != 3 {
		t.Fatalf("%v", r)
	}
	// sorted by path
	if got[0].Lang != "und" || got[0].Title != "Commentary" || got[1].Lang != "en" || got[1].Title != "" || got[2].Lang != "uk" {
		t.Fatalf("%+v", got)
	}
}

func TestFindNestedTwoLevels(t *testing.T) {
	dir := tree(t, "Show.S01E01.mkv", "Show.S01E02.mkv", "RUS Sound/[LostFilm]/Show.S01E01.mka", "RUS Sound/[LostFilm]/Show.S01E02.mka",
		"RUS Sound/[LostFilm]/deeper/Show.S01E01.mka")
	got := Find(filepath.Join(dir, "Show.S01E01.mkv"))
	if r := rels(dir, got); len(r) != 1 || r[0] != "RUS Sound/[LostFilm]/Show.S01E01.mka" {
		t.Fatalf("%v", r)
	}
	if got[0].Studio != "LostFilm" || got[0].Lang != "ru" {
		t.Fatalf("%+v", got[0])
	}
}

// An unknown studio's folder names the track.
func TestFindUnknownStudioFolderTitle(t *testing.T) {
	dir := tree(t, "Film.mkv", "Sound/Гоблин/track.ac3")
	got := Find(filepath.Join(dir, "Film.mkv"))
	if len(got) != 1 || got[0].Title != "Гоблин" || got[0].Lang != "ru" || got[0].Studio != "" {
		t.Fatalf("%+v", got)
	}
}

// One episode of a pack downloaded with the whole sound folder: the other
// episodes' tracks are not its own.
func TestFindSingleVideoOtherEpisode(t *testing.T) {
	dir := tree(t, "Show.S01E02.mkv", "RUS Sound/Show.S01E01.ac3", "RUS Sound/Show.S01E02.ac3")
	if r := rels(dir, Find(filepath.Join(dir, "Show.S01E02.mkv"))); len(r) != 1 || r[0] != "RUS Sound/Show.S01E02.ac3" {
		t.Fatalf("%v", r)
	}
}

// «Брат.2.ru.ac3» is the track of «Брат.2.mkv», not of «Брат.mkv».
func TestFindLongerStemIsAnotherVideos(t *testing.T) {
	dir := tree(t, "Брат.mkv", "Брат.2.mkv", "Брат.ru.ac3", "Брат.2.ru.ac3")
	if r := rels(dir, Find(filepath.Join(dir, "Брат.mkv"))); len(r) != 1 || r[0] != "Брат.ru.ac3" {
		t.Fatalf("%v", r)
	}
	if r := rels(dir, Find(filepath.Join(dir, "Брат.2.mkv"))); len(r) != 1 || r[0] != "Брат.2.ru.ac3" {
		t.Fatalf("%v", r)
	}
}

// Sound folders by whole words; soundtracks, music and audiobooks are not.
func TestIsSoundFolder(t *testing.T) {
	for name, want := range map[string]bool{
		"RUS Sound": true, "Rus Sound [LostFilm]": true, "Audio": true, "AUDIO TRACKS": true, "Звук": true, "Звуковые дорожки": true,
		"Озвучка": true, "Dub": true, "Dubs": true, "snd": true, "Rus Snd": true, "Voiceover": true, "Voice-over": true, "Sounds": true,
		"Soundtrack": false, "Dubrovnik": false, "The Voice": false, "Audiobooks": false, "Audio Books": false, "OST": false,
		"Music": false, "Sound OST": false, "Soundtrack Audio": false, "Bonus": false, "Audioslave": false,
	} {
		if IsSoundFolder(name) != want {
			t.Errorf("%q: want %v", name, want)
		}
	}
}

// In a sound folder an mp3 or flac is a track only when named after the
// video or with its episode code: «OST/…» albums are not voices.
func TestFindMusicInSoundFolder(t *testing.T) {
	dir := tree(t, "Film.mkv", "Audio/01 Main Theme.mp3", "Audio/02 End.flac", "Audio/Film.rus.mp3", "Audio/Rus.ac3")
	if r := rels(dir, Find(filepath.Join(dir, "Film.mkv"))); len(r) != 2 || r[0] != "Audio/Film.rus.mp3" || r[1] != "Audio/Rus.ac3" {
		t.Fatalf("%v", r)
	}
	dir = tree(t, "Film.mkv", "Soundtrack/01.mp3", "OST/02.flac", "Music/03.ac3")
	if r := rels(dir, Find(filepath.Join(dir, "Film.mkv"))); len(r) != 0 {
		t.Fatalf("%v", r)
	}
}

func TestFindAtMost16(t *testing.T) {
	var paths []string
	for i := range 20 {
		paths = append(paths, filepath.Join("Audio", "Voice "+string(rune('a'+i))+".ac3"))
	}
	dir := tree(t, append(paths, "Film.mkv")...)
	if got := Find(filepath.Join(dir, "Film.mkv")); len(got) != MaxTracks || MaxTracks != 16 {
		t.Fatalf("%d", len(got))
	}
}

func TestFindUnreadableDir(t *testing.T) {
	if got := Find("/nonexistent/dir/Film.mkv"); got != nil {
		t.Fatalf("%v", got)
	}
}

func TestIDStable(t *testing.T) {
	if ID("/a/b.mka") != ID("/a/b.mka") || ID("/a/b.mka") == ID("/a/c.mka") || len(ID("/a/b.mka")) != 8 {
		t.Fatal(ID("/a/b.mka"), ID("/a/c.mka"))
	}
}

// Review focus 3: a track that cannot be read, or of another cut, is left out.
func TestDescribe(t *testing.T) {
	tr := Track{Path: "/x/Film.LostFilm.mka", Lang: "ru", Title: "LostFilm", Studio: "LostFilm"}
	ctx := context.Background()
	fail := func(context.Context, string) (media.ProbeResult, error) {
		return media.ProbeResult{}, errors.New("bad")
	}
	if _, ok := Describe(ctx, tr, fail, 60_000); ok {
		t.Fatal("probe error kept")
	}
	half := func(context.Context, string) (media.ProbeResult, error) {
		return media.ProbeResult{DurationMs: 30_000, Audio: []media.AudioStream{{Index: 0, Codec: "ac3", Channels: 6}}}, nil
	}
	if _, ok := Describe(ctx, tr, half, 60_000); ok {
		t.Fatal("50 % shorter kept")
	}
	silent := func(context.Context, string) (media.ProbeResult, error) {
		return media.ProbeResult{DurationMs: 60_000}, nil
	}
	if _, ok := Describe(ctx, tr, silent, 60_000); ok {
		t.Fatal("no audio kept")
	}
	good := func(_ context.Context, p string) (media.ProbeResult, error) {
		if p != tr.Path {
			t.Fatalf("probed %s", p)
		}
		return media.ProbeResult{DurationMs: 62_000, Audio: []media.AudioStream{{Index: 0, Codec: "eac3", Lang: "eng", Title: "x", Channels: 6}}}, nil
	}
	a, ok := Describe(ctx, tr, good, 60_000)
	if !ok || a.Codec != "eac3" || a.Channels != 6 || a.Lang != "ru" || a.Title != "LostFilm" || a.Index != 0 {
		t.Fatalf("%v %+v", ok, a)
	}
	// unknown durations: kept; the file's own tags when the name says nothing
	und := Track{Path: tr.Path, Lang: "und"}
	tagged := func(context.Context, string) (media.ProbeResult, error) {
		return media.ProbeResult{Audio: []media.AudioStream{{Codec: "ac3", Lang: "rus", Title: "Дубляж"}}}, nil
	}
	if a, ok := Describe(ctx, und, tagged, 0); !ok || a.Lang != "ru" || a.Title != "Дубляж" {
		t.Fatalf("%v %+v", ok, a)
	}
}
