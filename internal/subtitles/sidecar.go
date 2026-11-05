package subtitles

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Codecs are the subtitle file extensions Vault reads, with their ffmpeg codec.
var Codecs = map[string]string{".srt": "subrip", ".ass": "ass", ".ssa": "ssa", ".vtt": "webvtt"}

// IsSubtitle says whether name is a subtitle file (by extension).
func IsSubtitle(name string) bool { return Codecs[strings.ToLower(filepath.Ext(name))] != "" }

// langCodes maps language tags seen in file names and streams to ISO 639-1.
var langCodes = map[string]string{
	"ru": "ru", "rus": "ru", "russian": "ru", "рус": "ru", "русский": "ru", "русские": "ru",
	"en": "en", "eng": "en", "english": "en", "англ": "en", "английский": "en",
	"uk": "uk", "ukr": "uk", "ukrainian": "uk", "укр": "uk", "украинский": "uk",
	"de": "de", "ger": "de", "deu": "de", "german": "de",
	"fr": "fr", "fre": "fr", "fra": "fr", "french": "fr",
	"es": "es", "spa": "es", "spanish": "es",
	"it": "it", "ita": "it", "italian": "it",
	"ja": "ja", "jpn": "ja", "japanese": "ja",
	"zh": "zh", "chi": "zh", "zho": "zh", "chinese": "zh",
	"ko": "ko", "kor": "ko", "korean": "ko",
	"pl": "pl", "pol": "pl", "polish": "pl",
	"pt": "pt", "por": "pt", "portuguese": "pt",
}

// NormLang maps a stream or file-name language tag to its ISO 639-1 code
// (rus, Russian, рус → ru; eng, English → en; ger → de); an unknown tag is "".
func NormLang(tag string) string {
	return langCodes[strings.ToLower(strings.TrimSpace(tag))]
}

// ExternalLang is the language of a subtitle file's tag: its ISO 639-1
// code for a known code or name ("rus", "Ukrainian" → "uk"), else the
// lower-cased tag when it looks like a code ("tr"), else "". The player
// names the language; automatic subtitles only care about NormLang.
func ExternalLang(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if c := langCodes[t]; c != "" {
		return c
	}
	if isLangTag(t) {
		return t
	}
	return ""
}

// isLangTag: a known language name or a 2–3 letter code.
func isLangTag(t string) bool {
	if langCodes[strings.ToLower(t)] != "" {
		return true
	}
	if len(t) < 2 || len(t) > 3 {
		return false
	}
	for _, c := range t {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// ignoredTitle are flags that say nothing beyond the track's flags.
var ignoredTitle = map[string]bool{"forced": true, "default": true}

// ParseName matches a subtitle file of the video with this stem:
// "<stem>.<ext>", "<stem>.<lang>.<ext>" and "<stem>.<lang>.<title…>.<ext>"
// ("Movie.en.commentary.srt", "X - cd1.ru.forced.srt"). The language is
// ExternalLang of the tag ("uk", "ru"). Another cut ("Movie.Extended.ru.srt")
// is not the video's.
func ParseName(name, stem string) (lang, title string, forced, ok bool) {
	ext := filepath.Ext(name)
	if Codecs[strings.ToLower(ext)] == "" {
		return "", "", false, false
	}
	rest := strings.TrimSuffix(name, ext)
	if rest == stem {
		return "", "", false, true
	}
	if !strings.HasPrefix(rest, stem+".") {
		return "", "", false, false
	}
	toks := strings.Split(rest[len(stem)+1:], ".")
	if !isLangTag(toks[0]) {
		return "", "", false, false
	}
	lang, toks = ExternalLang(toks[0]), toks[1:]
	var words []string
	for _, t := range toks {
		if t == "" {
			continue
		}
		if strings.EqualFold(t, "forced") {
			forced = true
		}
		if !ignoredTitle[strings.ToLower(t)] {
			words = append(words, t)
		}
	}
	return lang, strings.Join(words, " "), forced, true
}

// wordTags are the short tags LangOf also takes as words ("2_Rus.srt").
var wordTags = map[string]bool{"rus": true, "eng": true, "ukr": true, "рус": true, "укр": true, "англ": true}

// LangOf finds the language in a subtitle file name: a code or name among
// the last dot-separated parts ("Movie.en.srt", "X.rus.forced.srt"), else
// a spelled-out language word anywhere ("2_Russian.srt"). Returns an ISO
// 639-1 code ("ru", "en", "uk"…) or "".
func LangOf(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	parts := strings.Split(base, ".")
	for i := len(parts) - 1; i >= 1 && i >= len(parts)-3; i-- {
		if c := langCodes[strings.ToLower(parts[i])]; c != "" {
			return c
		}
	}
	words := strings.FieldsFunc(base, func(r rune) bool { return !unicode.IsLetter(r) })
	for i := len(words) - 1; i >= 0; i-- {
		// whole words only: two-letter title words ("It", "in") are not tags
		w := strings.ToLower(words[i])
		if c := langCodes[w]; c != "" && (utf8.RuneCountInString(w) > 3 || wordTags[w]) {
			return c
		}
	}
	return ""
}

// Decode returns subtitle bytes as UTF-8: UTF-16 with a BOM, UTF-8 (BOM
// dropped), otherwise Windows-1251 (old Russian .srt files).
func Decode(b []byte) string {
	le, be := bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF})
	switch {
	case le || be:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return string(utf16.Decode(u))
	case utf8.Valid(b):
		return string(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}))
	}
	s, err := charmap.Windows1251.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(s)
}

// SniffLang guesses the language of a subtitle file without one in its
// name: "ru" when the text is mostly Cyrillic without Ukrainian or
// Belarusian letters, else "" (Latin script says too little).
func SniffLang(b []byte) string {
	if len(b) > 256<<10 {
		b = b[:256<<10]
	}
	var cyr, lat, other int
	for _, r := range Decode(b) {
		switch {
		case strings.ContainsRune("іїєґўІЇЄҐЎ", r):
			other++
			cyr++
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case r < 0x80 && unicode.IsLetter(r):
			lat++
		}
	}
	if cyr >= 5 && cyr > lat && other*50 < cyr {
		return "ru"
	}
	return ""
}

// External is a subtitle file of a video.
type External struct {
	Path, Lang, Title, Codec string // Lang: an ISO 639 code ("ru", "uk") or ""
	Forced                   bool
}

var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true, ".ts": true, ".m2ts": true}

// subDirs are the subtitle folders of common release layouts.
var subDirs = map[string]bool{"subs": true, "subtitles": true, "sub": true}

// FindExternal lists the subtitle files of video, in a stable order: next
// to it (ParseName), in Subs/ or Subtitles/ beside it (named after it, or
// any file when it is the only video there), in Subs/<stem>/, and Auto's
// fallback DataDir/subs/ext/<fileID>.*.
func FindExternal(video, dataDir string, fileID int64) []External {
	dir := filepath.Dir(video)
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	es, err := os.ReadDir(dir)
	if err != nil {
		return extDir(dataDir, fileID)
	}
	var out []External
	videos := 0
	var folders []string
	for _, e := range es { // ReadDir is sorted by name
		n := e.Name()
		if e.IsDir() {
			if subDirs[strings.ToLower(n)] {
				folders = append(folders, n)
			}
			continue
		}
		if videoExts[strings.ToLower(filepath.Ext(n))] {
			videos++
		}
		if s, ok := named(filepath.Join(dir, n), stem); ok {
			out = append(out, s)
		}
	}
	for _, f := range folders {
		sd := filepath.Join(dir, f)
		ses, _ := os.ReadDir(sd)
		var nested string
		for _, e := range ses {
			n := e.Name()
			if e.IsDir() {
				if n == stem {
					nested = n
				}
				continue
			}
			if s, ok := named(filepath.Join(sd, n), stem); ok {
				out = append(out, s)
			} else if videos == 1 && IsSubtitle(n) {
				out = append(out, loose(filepath.Join(sd, n)))
			}
		}
		if nested != "" {
			nd := filepath.Join(sd, nested)
			nes, _ := os.ReadDir(nd)
			for _, e := range nes {
				if !e.IsDir() && IsSubtitle(e.Name()) {
					out = append(out, loose(filepath.Join(nd, e.Name())))
				}
			}
		}
	}
	return append(out, extDir(dataDir, fileID)...)
}

func named(path, stem string) (External, bool) {
	lang, title, forced, ok := ParseName(filepath.Base(path), stem)
	if !ok {
		return External{}, false
	}
	return External{Path: path, Lang: lang, Title: title, Forced: forced, Codec: Codecs[strings.ToLower(filepath.Ext(path))]}, true
}

// loose is a file in a subtitle folder named by the release ("2_Russian.srt").
func loose(path string) External {
	n := filepath.Base(path)
	s := External{Path: path, Codec: Codecs[strings.ToLower(filepath.Ext(n))]}
	stem := strings.TrimSuffix(n, filepath.Ext(n))
	if s.Lang = LangOf(n); s.Lang == "" {
		s.Title = stem
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(stem), func(r rune) bool { return !unicode.IsLetter(r) }) {
		s.Forced = s.Forced || w == "forced"
	}
	return s
}

func extDir(dataDir string, fileID int64) []External {
	if dataDir == "" || fileID <= 0 {
		return nil
	}
	dir := filepath.Join(dataDir, "subs", "ext")
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	stem := strconv.FormatInt(fileID, 10)
	var out []External
	for _, e := range es {
		if e.IsDir() {
			continue
		}
		if s, ok := named(filepath.Join(dir, e.Name()), stem); ok {
			out = append(out, s)
		}
	}
	return out
}

// readOnly: the media folder cannot be written (a container may mount it
// read-only; a permissions problem).
func readOnly(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrPermission)
}

// WriteSidecar saves a downloaded subtitle as "<video stem>.<lang>.srt"
// next to the video (".<lang>.2.srt"… when that name is taken by another
// file; never overwrites), or as DataDir/subs/ext/<fileID>.<lang>.srt when
// the folder is read-only. Returns the path written (or the existing file
// with the same content).
func WriteSidecar(video, dataDir string, fileID int64, lang string, data []byte) (string, error) {
	stem := strings.TrimSuffix(video, filepath.Ext(video))
	p, err := writeNew(stem, lang, data)
	if err == nil || !readOnly(err) || dataDir == "" {
		return p, err
	}
	dir := filepath.Join(dataDir, "subs", "ext")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return writeNew(filepath.Join(dir, strconv.FormatInt(fileID, 10)), lang, data)
}

func writeNew(stem, lang string, data []byte) (string, error) {
	for n := 1; n <= 20; n++ {
		p := stem + "." + lang + ".srt"
		if n > 1 {
			p = fmt.Sprintf("%s.%s.%d.srt", stem, lang, n)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			if old, rerr := os.ReadFile(p); rerr == nil && bytes.Equal(old, data) {
				return p, nil
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			os.Remove(p)
			return "", err
		}
		if err := f.Close(); err != nil {
			os.Remove(p)
			return "", err
		}
		return p, nil
	}
	return "", fmt.Errorf("subtitles: too many %s subtitles next to %s", lang, stem)
}
