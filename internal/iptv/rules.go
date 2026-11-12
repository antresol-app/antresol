package iptv

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Why a channel is shown or hidden (channels.why; catalog keys, shown
// through i18n.TL).
const (
	WhyAdult         = "пресет: для взрослых"
	WhyTimeshift     = "пресет: таймшифт-копия"
	WhySourceDefault = "по умолчанию источника"
	WhyManual        = "вручную"
)

// BigPlaylist: a playlist with this many entries or more hides new
// channels by default (unless the source says otherwise).
const BigPlaylist = 300

// Ident is the key a member is known by across refreshes, so manual
// decisions survive a rename: tvg:<tvg-id>, else url:<the URL without
// credentials>, else name:<NormName(name)>.
func Ident(e Entry) string {
	if id := strings.TrimSpace(e.TvgID); id != "" {
		return "tvg:" + id
	}
	if u := StripCreds(e.URL); e.URL != "" && u != "<invalid url>" {
		return "url:" + u
	}
	return "name:" + NormName(e.Name)
}

// DedupeKey is the logical channel of an entry across sources
// (channels.auto_key of an external row).
func DedupeKey(name string) string { return "ext:" + NormName(name) }

var (
	// «+2», «(+12)»: not «+20», not «Пример+».
	plusHours = regexp.MustCompile(`(?:^|[^\pL\pN+])\+(?:1[0-2]|[1-9])(?:[^\pN]|$)`)
	// «-2h», «-3 h», «-4ч».
	minusHours = regexp.MustCompile(`(?:^|[^\pL\pN])-\d{1,2}\s*(?:h|\x{0447})(?:[^\pL\pN]|$)`)
	// Words of timeshift copies (whole words, lower case).
	timeshiftWords = []string{"орбита", "дубль"}
	// Marks of adult channels (anywhere in the name or group, lower case).
	adultMarks = []string{"18+", "xxx", "для взрослых", "эротика"}
)

// hasWord: w is in s as a whole word.
func hasWord(s, w string) bool {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		a, b := i+j, i+j+len(w)
		before, _ := utf8.DecodeLastRuneInString(s[:a])
		after, _ := utf8.DecodeRuneInString(s[b:])
		if (a == 0 || !isWord(before)) && (b == len(s) || !isWord(after)) {
			return true
		}
		i = b
	}
}

// IsTimeshift: the name is a timeshift copy («+2», «Орбита», «Дубль», «-2h»).
func IsTimeshift(name string) bool {
	l := strings.ToLower(name)
	if plusHours.MatchString(l) || minusHours.MatchString(l) {
		return true
	}
	for _, w := range timeshiftWords {
		if hasWord(l, w) {
			return true
		}
	}
	return false
}

// IsAdult: the name or group carries an adult mark.
func IsAdult(name, group string) bool {
	l := strings.ToLower(name + "\n" + group)
	for _, m := range adultMarks {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// Defaults is a new entry's decision by the source's rules: the adult
// preset, then the timeshift preset, then the source's default (shown
// under BigPlaylist entries unless DefaultShown says otherwise). total:
// the playlist's size.
func Defaults(src Source, e Entry, total int) Decision {
	d := Decision{Group: e.Group, Name: e.Name}
	hidden, shown := false, true
	switch {
	case src.PresetAdult && IsAdult(e.Name, e.Group):
		d.Shown, d.Why = &hidden, WhyAdult
	case src.PresetTimeshift && IsTimeshift(e.Name):
		d.Shown, d.Why = &hidden, WhyTimeshift
	default:
		show := total < BigPlaylist
		if src.DefaultShown != nil {
			show = *src.DefaultShown
		}
		if show {
			d.Shown = &shown
		} else {
			d.Shown = &hidden
		}
		d.Why = WhySourceDefault
	}
	return d
}

// ApplyManual lays an admin's decision over the default, field by field
// (an unset field keeps the default); Why becomes WhyManual when it set
// anything.
func ApplyManual(def Decision, manual *Decision) Decision {
	if manual == nil {
		return def
	}
	set := false
	if manual.Shown != nil {
		v := *manual.Shown
		def.Shown, set = &v, true
	}
	if manual.Group != "" {
		def.Group, set = manual.Group, true
	}
	if manual.Name != "" {
		def.Name, set = manual.Name, true
	}
	if manual.Number != 0 {
		def.Number, set = manual.Number, true
	}
	if set {
		def.Why = WhyManual
	}
	return def
}
