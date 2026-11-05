// Package release разбирает имена релизов (русские и scene)
// в структуру Info. Парсер основан на regexp и не паникует ни на каких входных данных.
package release

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Info — результат разбора имени релиза. Неизвестные поля остаются нулевыми.
type Info struct {
	Title        string
	AltTitle     string // вторая часть вида "Рус / Eng"
	Year         int
	Season       int
	Episode      int
	EpisodeEnd   int
	IsSeasonPack bool
	Resolution   int    // 480, 576, 720, 1080, 2160
	Source       string // BDRip, WEB-DL, HDTV, Remux, CAM, TS ...
	Codec        string // x264, x265, HEVC, AV1 ...
	HDR          bool
	Audio        []string // Dub, MVO, AVO, Original
	Subs         []string // языки субтитров (ru, en, ...) или "sub" без языка; только для показа
	IsCam        bool
	Group        string
	Studios      []string // студии озвучки (канонические имена) в порядке появления
}

const (
	lb = `(?:^|[^\p{L}\p{N}])` // левая граница слова (юникодная)
	rb = `(?:$|[^\p{L}\p{N}])` // правая граница слова
)

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var (
	reExt = rx(`(?i)\.(mkv|avi|mp4|m4v|mov|wmv|ts|m2ts|mpg|mpeg|flv|webm|iso|torrent)$`)

	reYearBracket = rx(`[(\[]\s*((?:19|20)\d\d)`)
	reYearBare    = rx(lb + `((?:19|20)\d\d)` + rb)

	reSE      = rx(`(?i)` + lb + `(S\d{1,2})[ ._]?E(\d{1,4})(?:\s?-\s?E?(\d{1,4})|E(\d{1,4}))?` + rb)
	reNxN     = rx(`(?i)` + lb + `(\d{1,2})x(\d{2,3})(?:-(\d{2,3}))?` + rb)
	reSeasonS = rx(`(?i)` + lb + `S(\d{1,2})` + rb)
	reSeasonR = rx(`(?i)сезон\p{L}*\s*[:№#]?\s*(\d{1,2})` + rb)
	reSeasonN = rx(`(?i)` + lb + `(\d{1,2})[\s-]*(?:й\s*)?сезон`)
	// Число серии не может сразу переходить в букву/цифру (720p — это не серия 720).
	reEpisode = rx(`(?i)(?:сери[яийю]|эпизод\p{L}*)\s*[:№#]?\s*(\d{1,4})(?:\s*[-–]\s*(\d{1,4}))?(?:\s*из\s*(\d{1,4}))?` + rb)
	// "3 серия", "1-10 серии из 10".
	reEpisodN = rx(`(?i)` + lb + `(?:(\d{1,4})\s*[-–]\s*)?(\d{1,4})\s*(?:-я\s*)?сери[яийю](?:\s*из\s*(\d{1,4}))?`)

	reRes1 = rx(`(?i)` + lb + `(2160|1080|720|576|480)[pi]` + rb)
	reRes2 = rx(`(?i)` + lb + `\d{3,4}x(2160|1080|720|576|480)` + rb)
	reRes4 = rx(`(?i)` + lb + `(4k|uhd)` + rb)

	reHDR = rx(`(?i)` + lb + `(hdr10\+?|hdr|dolby[ ._]?vision|dovi)` + rb)

	reCamCI = rx(`(?i)` + lb + `(telesync|hdts|hdcam|camrip|cam-rip)` + rb)
	reCamCS = rx(lb + `(CAM|TS)` + rb)

	reAudioDub = rx(`(?i)` + lb + `(дубляж\p{L}*|дублирован\p{L}*|dub|dubbed)` + rb)
	reAudioD   = rx(lb + `(D)` + rb) // одиночная заглавная D (трекерная пометка)
	reAudioMVO = rx(`(?i)` + lb + `(mvo|многоголос\p{L}*)` + rb)
	reAudioAVO = rx(`(?i)` + lb + `(avo|авторск\p{L}*)` + rb)
	reAudioOrg = rx(`(?i)` + lb + `(original|оригинал\p{L}*)` + rb)
	// Русские сокращения (только заглавными): ДБ/ПД — дубляж,
	// ПМ/ЛМ — многоголосый, АП/АО — авторский.
	reAudioDubRu = rx(lb + `(ДБ|ПД)` + rb)
	reAudioMVORu = rx(lb + `(ПМ|ЛМ)` + rb)
	reAudioAVORu = rx(lb + `(АП|АО)` + rb)

	// Субтитры: маркер и язык рядом с ним (токен сразу после списка
	// разделителей или один токен сразу перед маркером).
	reSubMark   = rx(`(?i)` + lb + `(sub|subs|subtitles|субтитр\p{L}*|сабы)` + rb)
	reSubAfter  = rx(`^[\s,/:()+]*([\p{L}\p{N}]+)`)
	reSubBefore = rx(`([\p{L}\p{N}]+)[ ._-]*$`)

	reGroup  = rx(`-([A-Za-z][A-Za-z0-9]{1,15})$`)
	reLeadBr = rx(`^\s*\[([^\]]*)\]`)
)

// subLangs: коды языков (3 и 2 буквы) -> ISO 639-1.
var subLangs = map[string]string{
	"rus": "ru", "ru": "ru", "eng": "en", "en": "en",
	"ukr": "uk", "ua": "uk", "uk": "uk",
	"ger": "de", "deu": "de", "de": "de", "fre": "fr", "fra": "fr", "fr": "fr",
	"spa": "es", "es": "es", "ita": "it", "it": "it", "por": "pt", "pt": "pt",
	"pol": "pl", "pl": "pl", "chi": "zh", "zho": "zh", "zh": "zh",
	"kor": "ko", "ko": "ko", "jpn": "ja", "jap": "ja", "ja": "ja",
}

// parseSubs: языки субтитров из s; маркеры левее from (внутри названия) не
// считаются. Порядок первого появления, без повторов; нет языка — "sub".
func parseSubs(s string, from int) []string {
	var out []string
	add := func(c string) {
		for _, o := range out {
			if o == c {
				return
			}
		}
		out = append(out, c)
	}
	lang := func(tok string) (string, bool) {
		c, ok := subLangs[strings.ToLower(tok)]
		return c, ok
	}
	for _, m := range reSubMark.FindAllStringSubmatchIndex(s, -1) {
		ms, me := m[2], m[3] // сам маркер без границ
		if ms < from {
			continue
		}
		found := false
		if b := reSubBefore.FindStringSubmatch(s[from:ms]); b != nil {
			if c, ok := lang(b[1]); ok {
				add(c)
				found = true
			}
		}
		// Языки после маркера идут подряд: останавливаемся на первом чужом слове.
		for rest := s[me:]; ; {
			a := reSubAfter.FindStringSubmatchIndex(rest)
			if a == nil {
				break
			}
			c, ok := lang(rest[a[2]:a[3]])
			if !ok {
				break
			}
			add(c)
			found = true
			rest = rest[a[3]:]
		}
		if !found {
			add("sub")
		}
	}
	return out
}

type namedRe struct {
	re   *regexp.Regexp
	name string
}

var sources = []namedRe{
	{rx(`(?i)` + lb + `(remux|bd[- ]?remux)` + rb), "Remux"},
	{rx(`(?i)` + lb + `(web[-_ ]?dl[-_ ]?rip)` + rb), "WEBRip"}, // пережатый WEB-DL
	{rx(`(?i)` + lb + `(web[-_ ]?dl)` + rb), "WEB-DL"},
	{rx(`(?i)` + lb + `(web[-_ ]?rip)` + rb), "WEBRip"},
	{rx(`(?i)` + lb + `(bd[- ]?rip|bdrip)` + rb), "BDRip"},
	{rx(`(?i)` + lb + `(blu[- ]?ray|bd)` + rb), "BluRay"},
	{rx(`(?i)` + lb + `(hd[- ]?rip)` + rb), "HDRip"},
	{rx(`(?i)` + lb + `(dvd[- ]?rip)` + rb), "DVDRip"},
	{rx(`(?i)` + lb + `(dvd[- ]?scr)` + rb), "DVDScr"},
	{rx(`(?i)` + lb + `(hdtv(?:rip)?)` + rb), "HDTV"},
	{rx(`(?i)` + lb + `(tv[- ]?rip)` + rb), "TVRip"},
	{rx(`(?i)` + lb + `(sat[- ]?rip)` + rb), "SATRip"},
	{rx(`(?i)` + lb + `(vhs[- ]?rip)` + rb), "VHSRip"},
}

var codecs = []namedRe{
	{rx(`(?i)` + lb + `(x265)` + rb), "x265"},
	{rx(`(?i)` + lb + `(hevc|h[ .]?265)` + rb), "HEVC"},
	{rx(`(?i)` + lb + `(x264|h[ .]?264|avc)` + rb), "x264"},
	{rx(`(?i)` + lb + `(av1)` + rb), "AV1"},
	{rx(`(?i)` + lb + `(xvid)` + rb), "XviD"},
}

// find возвращает границы первой группы submatch (или -1,-1).
func find(re *regexp.Regexp, s string) (int, int) {
	m := re.FindStringSubmatchIndex(s)
	if len(m) < 4 || m[2] < 0 {
		return -1, -1
	}
	return m[2], m[3]
}

func sub(re *regexp.Regexp, s string, n int) string {
	m := re.FindStringSubmatch(s)
	if len(m) <= n {
		return ""
	}
	return m[n]
}

// num безопасно разбирает число; при переполнении или мусоре — 0.
func num(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 || v > 99999 {
		return 0
	}
	return v
}

func year(s string) (cut, y int) {
	if m := reYearBracket.FindStringSubmatchIndex(s); m != nil {
		return m[0], num(s[m[2]:m[3]])
	}
	for off := 0; off < len(s); {
		m := reYearBare.FindStringSubmatchIndex(s[off:])
		if m == nil {
			break
		}
		if st := off + m[2]; st > 0 {
			return st, num(s[st : off+m[3]])
		}
		off += m[3] // год в самом начале — это часть названия
	}
	return -1, 0
}

// Parse разбирает имя релиза. Никогда не паникует.
func Parse(name string) Info {
	info, _ := parse(name)
	return info
}

// Tail возвращает часть имени релиза, где названы студии: от первой
// технической пометки (год, качество, озвучка, «[», «|») или хвостовых
// «(Студия)» / «Студия.TV», без названия и его других имён, вместе с
// ведущими [префиксами].
func Tail(name string) string {
	_, tail := parse(name)
	return tail
}

func parse(name string) (Info, string) {
	var info Info
	rangePack := false
	s := strings.TrimSpace(reExt.ReplaceAllString(strings.TrimSpace(name), ""))

	// Ведущие [префиксы] отбрасываем, но ищем в них метаданные.
	lead := ""
	for {
		m := reLeadBr.FindStringSubmatchIndex(s)
		if m == nil {
			break
		}
		lead += " " + s[m[2]:m[3]]
		s = s[m[1]:]
	}
	s = strings.TrimSpace(s)
	if strings.ContainsRune(s, ' ') {
		s = strings.ReplaceAll(s, "_", " ")
	} else {
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	}

	cuts := []int{}
	add := func(i int) {
		if i > 0 {
			cuts = append(cuts, i)
		}
	}

	// Год.
	cut, y := year(s)
	info.Year = y
	add(cut)
	if info.Year == 0 {
		_, info.Year = year(" " + lead + " ")
	}

	// Сезон / серия.
	if m := reSE.FindStringSubmatchIndex(s); m != nil {
		add(m[2])
		info.Season = num(s[m[2]+1 : m[3]])
		info.Episode = num(s[m[4]:m[5]])
		for _, g := range []int{3, 4} {
			if m[2*g] >= 0 {
				info.EpisodeEnd = num(s[m[2*g]:m[2*g+1]])
			}
		}
	} else if m := reNxN.FindStringSubmatchIndex(s); m != nil {
		add(m[2])
		info.Season = num(s[m[2]:m[3]])
		info.Episode = num(s[m[4]:m[5]])
		if m[6] >= 0 {
			info.EpisodeEnd = num(s[m[6]:m[7]])
		}
	} else {
		if st, e := find(reSeasonN, s); st >= 0 {
			add(st)
			info.Season = num(s[st:e])
		} else if m := reSeasonR.FindStringSubmatchIndex(s); m != nil {
			add(m[0])
			info.Season = num(s[m[2]:m[3]])
		} else if st, e := find(reSeasonS, s); st >= 0 {
			add(st - 1)
			info.Season = num(s[st:e])
		}
		if m := reEpisode.FindStringSubmatchIndex(s); m != nil {
			add(m[0])
			info.Episode = num(s[m[2]:m[3]])
			if m[4] >= 0 {
				info.EpisodeEnd = num(s[m[4]:m[5]])
				// Диапазон с первой серии — пачка сезона; "1-4 из 10" — частичная раздача.
				rangePack = info.Episode == 1 && info.EpisodeEnd > 1
				if m[6] >= 0 && info.EpisodeEnd < num(s[m[6]:m[7]]) {
					rangePack = false
				}
			}
		} else if m := reEpisodN.FindStringSubmatchIndex(s); m != nil {
			if m[2] >= 0 { // диапазон "1-10 серии"
				add(m[2])
				info.Episode = num(s[m[2]:m[3]])
				info.EpisodeEnd = num(s[m[4]:m[5]])
				rangePack = info.Episode == 1 && info.EpisodeEnd > 1
				if m[6] >= 0 && info.EpisodeEnd < num(s[m[6]:m[7]]) {
					rangePack = false
				}
			} else {
				add(m[4])
				info.Episode = num(s[m[4]:m[5]])
			}
		}
	}
	if info.EpisodeEnd != 0 && info.EpisodeEnd <= info.Episode {
		info.EpisodeEnd = 0
	}
	info.IsSeasonPack = (info.Season > 0 && info.Episode == 0) || rangePack

	// Разрешение.
	if st, e := find(reRes1, s); st >= 0 {
		add(st)
		info.Resolution = num(s[st:e])
	} else if m := reRes2.FindStringSubmatchIndex(s); m != nil {
		add(m[2])
		info.Resolution = num(s[m[2]:m[3]])
	} else if st, _ := find(reRes4, s); st >= 0 {
		add(st)
		info.Resolution = 2160
	}

	// Источник, камрип.
	for _, src := range sources {
		if st, _ := find(src.re, s); st >= 0 {
			add(st)
			info.Source = src.name
			break
		}
	}
	if st, e := find(reCamCI, s); st >= 0 && info.Source == "" {
		add(st)
		info.Source = "CAM"
		if t := strings.ToLower(s[st:e]); strings.Contains(t, "ts") && !strings.Contains(t, "cam") {
			info.Source = "TS"
		}
	} else if st, e := find(reCamCS, s); st >= 0 && info.Source == "" {
		add(st)
		info.Source = s[st:e]
	}
	info.IsCam = info.Source == "CAM" || info.Source == "TS"

	// Кодек, HDR.
	for _, c := range codecs {
		if st, _ := find(c.re, s); st >= 0 {
			add(st)
			info.Codec = c.name
			break
		}
	}
	if st, _ := find(reHDR, s); st >= 0 {
		add(st)
		info.HDR = true
	}

	// Трекерные скобки: " [" после непустого названия.
	if i := strings.Index(s, "["); i > 0 {
		add(i)
	}

	// Где кончается название. Маркеры озвучки режут только как запасной вариант.
	end := len(s)
	for _, c := range cuts {
		if c < end {
			end = c
		}
	}
	audioRes := []*regexp.Regexp{reAudioDub, reAudioMVO, reAudioAVO, reAudioOrg}
	if end == len(s) {
		for _, re := range append(audioRes, reAudioD) {
			if st, _ := find(re, s); st > 0 && st < end {
				end = st
			}
		}
	}

	title := s[:end]
	rest := s[end:] + " " + lead

	// Озвучка (только после названия).
	hasAudio := func(re *regexp.Regexp) bool { st, _ := find(re, rest); return st >= 0 }
	if hasAudio(reAudioDub) || hasAudio(reAudioD) || hasAudio(reAudioDubRu) {
		info.Audio = append(info.Audio, "Dub")
	}
	if hasAudio(reAudioMVO) || hasAudio(reAudioMVORu) {
		info.Audio = append(info.Audio, "MVO")
	}
	if hasAudio(reAudioAVO) || hasAudio(reAudioAVORu) {
		info.Audio = append(info.Audio, "AVO")
	}
	if hasAudio(reAudioOrg) {
		info.Audio = append(info.Audio, "Original")
	}

	info.Subs = parseSubs(s+" "+lead, end)
	// Studios only where a release names them: «Кубик в кубе» may be the
	// film itself, «TV Shows Story» its English title.
	named := studioText(s, end) + " " + lead
	info.Studios = Studios(named)

	// Релиз-группа: "-GRP" в конце, только если есть технические метаданные.
	tech := info.Resolution != 0 || info.Source != "" || info.Codec != "" || info.Season != 0 || info.Episode != 0
	if tech {
		if m := reGroup.FindStringSubmatchIndex(s); m != nil && m[0] >= end {
			g := s[m[2]:m[3]]
			switch strings.ToLower(g) {
			case "dl", "rip", "ray", "dub", "rus", "eng":
			default:
				info.Group = g
			}
		}
	}

	// Название и альтернативное название.
	const trim = " \t-–—_|/\\:,.;([{"
	parts := strings.SplitN(title, " / ", 3)
	info.Title = strings.Trim(parts[0], trim)
	if len(parts) > 1 {
		info.AltTitle = strings.Trim(parts[1], trim)
	}
	return info, named
}

// techMarkers start the technical part of a name: season and episode,
// year, quality, source, codec, HDR, the voice-over; before them (after the
// title) come only other names of the title («/ TV Shows Story»).
var techMarkers = func() []*regexp.Regexp {
	out := []*regexp.Regexp{reSE, reNxN, reSeasonS, reSeasonR, reSeasonN, reEpisode, reEpisodN,
		reYearBracket, reYearBare, reRes1, reRes2, reRes4, reHDR, reCamCI,
		reAudioDub, reAudioD, reAudioMVO, reAudioAVO, reAudioOrg, reAudioDubRu, reAudioMVORu, reAudioAVORu}
	for _, l := range [][]namedRe{sources, codecs} {
		for _, n := range l {
			out = append(out, n.re)
		}
	}
	return out
}()

// reStudioTV: a scene name's «Studio.TV» (dots made spaces), anywhere.
var reStudioTV = rx(`(?i)(?:[\p{L}\p{N}]+\s+){1,2}TV` + rb)

// firstMarker is the offset in t of the first technical marker, «[», «|»
// or «(»; len(t) when there is none.
func firstMarker(t string, parens bool) int {
	first := len(t)
	at := func(i int) { first = min(first, i) }
	for _, re := range techMarkers {
		if m := re.FindStringSubmatchIndex(t); m != nil {
			if len(m) >= 4 && m[2] >= 0 {
				at(m[2])
			} else {
				at(m[0])
			}
		}
	}
	cut := "[|"
	if parens {
		cut += "("
	}
	if i := strings.IndexAny(t, cut); i >= 0 {
		at(i)
	}
	return first
}

// studioText is the part of s where its studios may be named (end is the
// title's end): from the first technical marker, «[», «|» or «Studio TV»
// after the title, without the title's other names (« / Alt » up to the
// next marker or bracket); plus a «(Studio)» closing a name with an alt
// title and nothing else («Сериал / Show (Кубик в Кубе)»).
func studioText(s string, end int) string {
	tail := s[end:]
	from := firstMarker(tail, false)
	if m := reStudioTV.FindStringIndex(tail); m != nil {
		from = min(from, m[0])
	}
	var b strings.Builder
	for t := tail[from:]; ; {
		i := strings.Index(t, " / ")
		if i < 0 {
			b.WriteString(t)
			break
		}
		b.WriteString(t[:i])
		b.WriteByte(' ')
		alt := t[i+3:]
		t = alt[firstMarker(alt, true):] // the alt name ends at the next marker or bracket
	}
	if end == len(s) && strings.Contains(s, " / ") {
		if t := strings.TrimRight(s, " "); strings.HasSuffix(t, ")") {
			if i := strings.LastIndex(t, "("); i > strings.Index(s, " / ") {
				b.WriteString(" " + t[i:])
			}
		}
	}
	return b.String()
}

// Normalize приводит название к виду для сопоставления: нижний регистр,
// ё→е, без пунктуации, схлопнутые пробелы.
func Normalize(title string) string {
	title = strings.ToLower(title)
	title = strings.ReplaceAll(title, "ё", "е")
	return strings.Join(strings.FieldsFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}
