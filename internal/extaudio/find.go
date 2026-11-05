// Package extaudio finds the audio tracks that lie beside a video as files
// of their own (a release's «RUS Sound» folder, «Film.LostFilm.mka») and
// describes them for the player: they play like the file's own tracks,
// taken by ffmpeg as a second input. Discovery happens at play time, the
// way subtitles.FindExternal finds subtitle files.
package extaudio

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/release"
	"github.com/antresol-app/antresol/internal/subtitles"
)

// Track is an audio file of a video. Lang is an ISO 639-1 code or "und";
// Title is what the player shows beside the language (the studio, else
// what the name adds); Studio is the canonical studio name or "".
type Track struct {
	Path, Lang, Title, Studio string
}

// Exts are the audio file extensions taken as tracks.
var Exts = map[string]bool{".mka": true, ".ac3": true, ".eac3": true, ".dts": true, ".aac": true, ".flac": true,
	".m4a": true, ".mp3": true, ".opus": true, ".wav": true}

// musicExts are taken only inside a sound folder or named after the video:
// a music album next to a film is not a dub.
var musicExts = map[string]bool{".mp3": true, ".flac": true}

// IsAudio reports whether name has an extension of Exts.
func IsAudio(name string) bool { return Exts[strings.ToLower(filepath.Ext(name))] }

var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true, ".ts": true, ".m2ts": true}

// soundWords are the whole words that make a folder a sound folder;
// soundStems the Russian stems («Звуковые», «Озвучка»).
var (
	soundWords = map[string]bool{"sound": true, "sounds": true, "audio": true, "dub": true, "dubs": true, "snd": true,
		"voiceover": true, "voiceovers": true}
	soundStems = []string{"звук", "озвуч"}
	// notSound: words of music and book folders («Soundtrack Audio», «Audio Books», «Sound OST»)
	notSound = map[string]bool{"soundtrack": true, "soundtracks": true, "ost": true, "music": true, "audiobook": true,
		"audiobooks": true, "books": true, "book": true, "саундтрек": true, "музыка": true, "аудиокниги": true, "аудиокнига": true}
)

// IsSoundFolder reports whether a folder name is a release's sound folder
// («RUS Sound», «Audio», «Озвучка», «snd», «Voice-over»): one of its whole
// words says so and none says music or books («Soundtrack», «OST»,
// «Audiobooks»; «Dubrovnik», «The Voice» are just names).
func IsSoundFolder(name string) bool {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	found := false
	for i, w := range words {
		if notSound[w] {
			return false
		}
		if soundWords[w] || w == "voice" && i+1 < len(words) && words[i+1] == "over" {
			found = true
		}
		for _, st := range soundStems {
			found = found || strings.HasPrefix(w, st)
		}
	}
	return found
}

// MaxTracks bounds the tracks of one video (a folder of a hundred files is
// not a release's voices).
const MaxTracks = 16

// Find lists the audio tracks of video, sorted by path, at most MaxTracks:
// files next to it named after it (any audio extension) or with its
// episode code (not mp3/flac), and files in the sound folders beside it and
// in their subfolders (two levels) named after it, with its episode code,
// or — when the folder holds one video — any whose name has no other
// episode code (not mp3/flac). An unreadable folder gives nil.
func Find(video string) []Track {
	dir := filepath.Dir(video)
	base := filepath.Base(video)
	v := subject{stem: strings.TrimSuffix(base, filepath.Ext(base))}
	if info := release.Parse(base); info.Episode > 0 {
		v.season, v.episode = info.Season, info.Episode
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Track
	var sound []string
	for _, e := range es {
		n := e.Name()
		if e.IsDir() {
			if IsSoundFolder(n) {
				sound = append(sound, n)
			}
			continue
		}
		if videoExts[strings.ToLower(filepath.Ext(n))] {
			v.videos++
			if o := strings.TrimSuffix(n, filepath.Ext(n)); len(o) > len(v.stem) && namedAfter(o, v.stem) {
				v.longer = append(v.longer, o)
			}
		}
	}
	for _, e := range es {
		n := e.Name()
		if e.IsDir() || !IsAudio(n) {
			continue
		}
		m := v.match(n, false)
		if m == noMatch || (m == byEpisode && musicExts[strings.ToLower(filepath.Ext(n))]) {
			continue
		}
		out = append(out, v.track(filepath.Join(dir, n), m, nil))
	}
	for _, s := range sound {
		out = append(out, v.inFolder(filepath.Join(dir, s), []string{s}, 1)...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	if len(out) > MaxTracks {
		out = out[:MaxTracks]
	}
	return out
}

// subject is the video Find looks for.
type subject struct {
	stem            string
	season, episode int // its episode code, 0 = none
	videos          int // videos in its folder
	// longer: the stems of the folder's videos named after this one's
	// («Брат.2» beside «Брат»): their tracks are not its own
	longer []string
}

type match int

const (
	noMatch   match = iota
	byStem          // named after the video
	byEpisode       // the same episode code
	loose           // the folder's only video: any track
)

// match: how the file name n belongs to the video (inSound: n lies in a
// sound folder).
func (v subject) match(n string, inSound bool) match {
	rest := strings.TrimSuffix(n, filepath.Ext(n))
	if namedAfter(rest, v.stem) {
		for _, o := range v.longer {
			if namedAfter(rest, o) {
				return noMatch
			}
		}
		return byStem
	}
	info := release.Parse(n)
	if info.Episode > 0 {
		if v.episode > 0 && info.Episode == v.episode && (info.Season == v.season || info.Season == 0) {
			return byEpisode
		}
		return noMatch // another episode's
	}
	if inSound && v.videos == 1 {
		return loose
	}
	return noMatch
}

// namedAfter: rest is stem, or stem and a separator then more ("Show.S01E02.LostFilm").
func namedAfter(rest, stem string) bool {
	r, s := strings.ToLower(rest), strings.ToLower(stem)
	if r == s {
		return true
	}
	if !strings.HasPrefix(r, s) {
		return false
	}
	c := rune(r[len(s)])
	return !unicode.IsLetter(c) && !unicode.IsDigit(c)
}

// inFolder lists the tracks of the sound folder dir (folders: its path from
// the video's folder) and, at depth 1, of its subfolders.
func (v subject) inFolder(dir string, folders []string, depth int) []Track {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Track
	for _, e := range es {
		n := e.Name()
		if e.IsDir() {
			if depth < 2 {
				out = append(out, v.inFolder(filepath.Join(dir, n), append(append([]string{}, folders...), n), depth+1)...)
			}
			continue
		}
		if !IsAudio(n) {
			continue
		}
		// mp3 and flac: a voice only when the name says whose (an album otherwise)
		if m := v.match(n, true); m != noMatch && (m != loose || !musicExts[strings.ToLower(filepath.Ext(n))]) {
			out = append(out, v.track(filepath.Join(dir, n), m, folders))
		}
	}
	return out
}

// track describes the file at p, matched by m, in the sound folders
// folders (outermost first; nil: next to the video).
func (v subject) track(p string, m match, folders []string) Track {
	n := filepath.Base(p)
	rest := strings.TrimSuffix(n, filepath.Ext(n))
	if m == byStem {
		rest = strings.TrimLeftFunc(rest[len(v.stem):], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	}
	t := Track{Path: p}
	// language: the name, then the folders (nearest first)
	t.Lang = subtitles.LangOf(rest + ".x")
	if t.Lang == "" && rest != "" {
		t.Lang = subtitles.LangOf("x." + rest + ".x")
	}
	for i := len(folders) - 1; i >= 0 && t.Lang == ""; i-- {
		t.Lang = folderLang(folders[i])
	}
	// studio: the name, then the folders (nearest first)
	if s := release.Studios(rest); len(s) > 0 {
		t.Studio = s[0]
	}
	for i := len(folders) - 1; i >= 0 && t.Studio == ""; i-- {
		if s := release.Studios(folders[i]); len(s) > 0 {
			t.Studio = s[0]
		}
	}
	left := ""
	if m != byEpisode {
		left = leftover(rest)
	}
	folder := ""
	for i := len(folders) - 1; i >= 0 && folder == ""; i-- {
		folder = folderTitle(folders[i])
	}
	switch {
	case t.Studio != "":
		t.Title = t.Studio
	case m == byStem && left != "":
		t.Title = left
	case folder != "":
		t.Title = folder
	default:
		t.Title = left
	}
	if t.Lang == "" {
		t.Lang = "und"
		all := append([]string{rest}, folders...)
		if t.Studio != "" || cyrillic(strings.Join(all, " ")) {
			t.Lang = "ru"
		}
	}
	return t
}

// folderLang is the language a folder name states («Rus», «RUS Sound»,
// «English»), "" when none.
func folderLang(name string) string {
	return subtitles.LangOf("x." + strings.NewReplacer(".", " ").Replace(name) + ".x")
}

// folderTitle is a sound subfolder's name as a track title: not a sound or
// language folder («Audio», «Rus»), brackets trimmed («[Гоблин]» → «Гоблин»).
func folderTitle(name string) string {
	if IsSoundFolder(name) {
		return ""
	}
	n := strings.Trim(strings.TrimSpace(name), "[]()")
	words := strings.FieldsFunc(n, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return ""
	}
	if len(words) == 1 && subtitles.NormLang(words[0]) != "" {
		return ""
	}
	return strings.TrimSpace(n)
}

// leftover is what a name adds beyond language tags: "rus.Commentary" →
// "Commentary".
func leftover(rest string) string {
	var words []string
	for _, p := range strings.Split(rest, ".") {
		p = strings.TrimSpace(p)
		if p == "" || subtitles.NormLang(p) != "" {
			continue
		}
		words = append(words, p)
	}
	return strings.Join(words, " ")
}

func cyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// ID names an external track in the play API (?audioSrc): the fnv32a of
// its path, like the subtitle files' ids.
func ID(path string) string {
	h := fnv.New32a()
	h.Write([]byte(path))
	return fmt.Sprintf("%08x", h.Sum32())
}

// Prober probes a media file (media.Probe with the server's ffprobe; it
// reads the file protocol only).
type Prober func(ctx context.Context, path string) (media.ProbeResult, error)

// maxDurationDiff: a track whose duration differs from the video's by more
// than this share is of another cut.
const maxDurationDiff = 0.10

// Describe is the first audio stream of t's file with t's language and
// title (the file's own tags when the name says nothing). false: the probe
// failed, the file has no audio, or its duration differs from the video's
// by more than 10 % (when both are known).
func Describe(ctx context.Context, t Track, probe Prober, videoDurationMs int64) (media.AudioStream, bool) {
	p, err := probe(ctx, t.Path)
	if err != nil || len(p.Audio) == 0 {
		return media.AudioStream{}, false
	}
	if videoDurationMs > 0 && p.DurationMs > 0 {
		d := float64(p.DurationMs - videoDurationMs)
		if d < 0 {
			d = -d
		}
		if d > maxDurationDiff*float64(videoDurationMs) {
			return media.AudioStream{}, false
		}
	}
	a := p.Audio[0]
	if t.Lang != "" && t.Lang != "und" {
		a.Lang = t.Lang
	} else if c := subtitles.NormLang(a.Lang); c != "" {
		a.Lang = c
	} else {
		a.Lang = "und"
	}
	if t.Title != "" {
		a.Title = t.Title
	}
	a.Default = false
	return a, true
}
