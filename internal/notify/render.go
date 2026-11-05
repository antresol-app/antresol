package notify

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// product names the server in letters.
const product = "Antresol"

// stored is a queued event read back.
type stored struct {
	id      int64
	topic   string
	itemID  int64
	groupID int64
	data    Data
	created int64
}

func (e *stored) decode(raw string) { _ = json.Unmarshal([]byte(raw), &e.data) }

// entry is one line of a letter.
type entry struct {
	Line   string
	Detail string
	Link   string
	Poster string
}

// section is a topic's lines.
type section struct {
	Topic       string
	Heading     string
	Entries     []entry
	Unsubscribe string // the link that turns the topic off ("" none)
	OffLabel    string
}

// renderer lists a topic's events as lines; topicHeading names it.
type renderer func(lang string, evs []stored, link func(path string) string) []entry

var (
	renderers = map[string]renderer{Episodes: renderEpisodes, Available: renderAvailable}
	headings  = map[string]func(lang string) string{
		Episodes: func(l string) string { return i18n.TL(l, "Новые серии") },
		Available: func(l string) string {
			return i18n.TL(l, "Появилось то, что вы хотели посмотреть")
		},
		TopicAll: func(l string) string { return i18n.TL(l, "Все уведомления") },
	}
)

// itemPath is the web page of a library item ("" none).
func itemPath(kind string, id int64) string {
	if id <= 0 {
		return ""
	}
	n := strconv.FormatInt(id, 10)
	switch kind {
	case library.KindMovie, library.KindSeries:
		return "/item/" + n
	case library.KindBook:
		return "/books/" + n
	case library.KindAudiobook:
		return "/books/audiobook/" + n
	case library.KindComic:
		return "/books/comic/" + n
	case library.KindAlbum:
		return "/music/album/" + n
	}
	return ""
}

func titleYear(d Data) string {
	if d.Year > 0 {
		return d.Title + " (" + strconv.Itoa(d.Year) + ")"
	}
	return d.Title
}

type se struct{ s, e int }

// episodeRanges writes episodes as runs: S02E01–E03, S02E05, S03E01–E02.
func episodeRanges(eps []se) string {
	slices.SortFunc(eps, func(a, b se) int { return cmp.Or(cmp.Compare(a.s, b.s), cmp.Compare(a.e, b.e)) })
	eps = slices.Compact(eps)
	var parts []string
	for i := 0; i < len(eps); {
		j := i
		for j+1 < len(eps) && eps[j+1].s == eps[i].s && eps[j+1].e == eps[j].e+1 {
			j++
		}
		p := fmt.Sprintf("S%02dE%02d", eps[i].s, eps[i].e)
		if j > i {
			p += fmt.Sprintf("–E%02d", eps[j].e)
		}
		parts = append(parts, p)
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

func renderEpisodes(lang string, evs []stored, link func(string) string) []entry {
	type group struct {
		first  stored
		eps    []se
		titled []string // episodes without numbers, by their titles
	}
	var order []int64
	groups := map[int64]*group{}
	for _, e := range evs {
		g := groups[e.groupID]
		if g == nil {
			g = &group{first: e}
			groups[e.groupID] = g
			order = append(order, e.groupID)
		}
		if e.data.Episode > 0 {
			g.eps = append(g.eps, se{e.data.Season, e.data.Episode})
		} else if !slices.Contains(g.titled, e.data.EpisodeTitle) {
			g.titled = append(g.titled, e.data.EpisodeTitle)
		}
		if g.first.data.Poster == "" {
			g.first.data.Poster = e.data.Poster
		}
	}
	var out []entry
	for _, id := range order {
		g := groups[id]
		uniq := map[se]bool{}
		for _, x := range g.eps {
			uniq[x] = true
		}
		var parts []string
		if len(g.eps) > 0 {
			parts = append(parts, episodeRanges(g.eps))
		}
		for _, t := range g.titled {
			if t = strings.TrimSpace(t); t != "" {
				parts = append(parts, "«"+t+"»")
			}
		}
		n := len(uniq) + len(g.titled)
		line := g.first.data.Title + " — " + i18n.PluralL(lang, n, "новая серия", "новые серии", "новых серий")
		if len(parts) > 0 {
			line += ": " + strings.Join(parts, ", ")
		}
		out = append(out, entry{
			Line:   line,
			Link:   link(itemPath(library.KindSeries, g.first.itemID)),
			Poster: g.first.data.Poster,
		})
	}
	return out
}

func renderAvailable(lang string, evs []stored, link func(string) string) []entry {
	out := make([]entry, 0, len(evs))
	seen := map[int64]bool{}
	for _, e := range evs {
		if e.itemID > 0 && seen[e.itemID] {
			continue
		}
		seen[e.itemID] = true
		out = append(out, entry{Line: titleYear(e.data), Link: link(itemPath(e.data.Kind, e.itemID)), Poster: e.data.Poster})
	}
	return out
}

// letter is a digest ready to send.
type letter struct {
	Subject, Text, HTML string
	// Unsubscribe: the first section's link (List-Unsubscribe).
	Unsubscribe string
}

type letterView struct {
	Lang, Heading, Intro, Footer, Settings string
	Sections                               []section
}

var letterHTML = template.Must(template.New("letter").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>{{.Heading}}</title></head>
<body style="margin:0;padding:24px 16px;background:#f5f5f7;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#1d1d1f">
<div style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:14px;padding:24px">
<h1 style="font-size:20px;margin:0 0 4px">{{.Heading}}</h1>
<p style="margin:0 0 20px;color:#6e6e73;font-size:14px">{{.Intro}}</p>
{{range .Sections}}<h2 style="font-size:16px;margin:20px 0 8px">{{.Heading}}</h2>
<table role="presentation" cellpadding="0" cellspacing="0" style="width:100%;border-collapse:collapse">
{{range .Entries}}<tr>
<td style="width:56px;padding:6px 12px 6px 0;vertical-align:top">{{if .Poster}}<img src="{{.Poster}}" alt="" width="46" style="display:block;width:46px;border-radius:6px">{{end}}</td>
<td style="padding:6px 0;vertical-align:top;font-size:15px">{{if .Link}}<a href="{{.Link}}" style="color:#0066cc;text-decoration:none">{{.Line}}</a>{{else}}{{.Line}}{{end}}{{if .Detail}}<div style="color:#6e6e73;font-size:13px;margin-top:2px">{{.Detail}}</div>{{end}}</td>
</tr>{{end}}
</table>{{end}}
<p style="margin:28px 0 0;color:#6e6e73;font-size:12px;line-height:1.5">{{.Footer}}<br>{{.Settings}}{{range .Sections}}{{if .Unsubscribe}}<br><a href="{{.Unsubscribe}}" style="color:#6e6e73">{{.OffLabel}}</a>{{end}}{{end}}</p>
</div></body></html>
`))

// compose writes the letter of sections for the profile name.
func compose(lang, name string, sections []section) (letter, error) {
	total := 0
	var only entry
	for _, s := range sections {
		total += len(s.Entries)
		if len(s.Entries) > 0 {
			only = s.Entries[0]
		}
	}
	subject := only.Line
	if total != 1 {
		subject = product + ": " + i18n.PluralL(lang, total, "новость", "новости", "новостей")
	}
	v := letterView{
		Lang:     lang,
		Heading:  i18n.TL(lang, "Новое в %s", product),
		Intro:    i18n.TL(lang, "Профиль «%s»", name),
		Footer:   i18n.TL(lang, "Вы получили это письмо, потому что включили уведомления в профиле «%s».", name),
		Settings: i18n.TL(lang, "Изменить их можно в меню профиля: «Уведомления на почту»."),
		Sections: sections,
	}
	var t strings.Builder
	t.WriteString(v.Heading + "\n" + v.Intro + "\n")
	for _, s := range sections {
		t.WriteString("\n" + s.Heading + "\n")
		for _, e := range s.Entries {
			t.WriteString("• " + e.Line + "\n")
			if e.Detail != "" {
				t.WriteString("  " + e.Detail + "\n")
			}
			if e.Link != "" {
				t.WriteString("  " + e.Link + "\n")
			}
		}
	}
	t.WriteString("\n—\n" + v.Footer + "\n" + v.Settings + "\n")
	unsub := ""
	for _, s := range sections {
		if s.Unsubscribe != "" {
			t.WriteString(s.OffLabel + ": " + s.Unsubscribe + "\n")
			if unsub == "" {
				unsub = s.Unsubscribe
			}
		}
	}
	var h bytes.Buffer
	if err := letterHTML.Execute(&h, v); err != nil {
		return letter{}, err
	}
	return letter{Subject: subject, Text: t.String(), HTML: h.String(), Unsubscribe: unsub}, nil
}

// linker makes absolute links on the public address ("" without one).
func (s *Service) linker() func(string) string {
	base := strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	u, err := url.Parse(base)
	if base == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return func(string) string { return "" }
	}
	return func(p string) string {
		if p == "" {
			return ""
		}
		return base + p
	}
}
