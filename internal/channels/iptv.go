package channels

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// TvgID is a channel's id in the M3U and the guide.
func TvgID(c Channel) string { return "antresol." + strconv.FormatInt(c.ID, 10) }

// DefaultCategory is the group of a channel without one.
const DefaultCategory = "Каналы"

func attr(s string) string {
	return strings.NewReplacer(`"`, "'", "\n", " ", "\r", " ").Replace(s)
}

// External is a channel of an external playlist as an IPTV link carries
// it: its row, its guide id, its logo (an uploaded one wins) and its
// stream (the provider's URL, or Antresol's redirect when the source
// hides provider addresses) with the headers it needs.
type External struct {
	Channel
	TvgID               string // "": ext-<id>
	Logo                string // the playlist's tvg-logo ("": Antresol's tile)
	URL                 string
	UserAgent, Referrer string
	// Programmes of the guide (epg.xml).
	Programmes []ExternalProgramme
}

// ExternalProgramme is a programme of an external channel's guide.
type ExternalProgramme struct {
	Start, End                            time.Time
	Title, SubTitle, Desc, Category, Icon string
}

// ExternalTvgID is an external channel's id in the M3U and the guide.
func ExternalTvgID(x External) string {
	if x.TvgID != "" {
		return x.TvgID
	}
	return "ext-" + strconv.FormatInt(x.ID, 10)
}

func (x External) logo(base string) string {
	if x.LogoVersion == 0 && x.Logo != "" {
		return x.Logo
	}
	return fmt.Sprintf("%s/logo/%d.png%s", base, x.ID, LogoQuery(x.Channel))
}

// WriteM3U writes the extended M3U playlist of chans for IPTV players:
// base is the absolute URL of the profile's IPTV root (…/iptv/<token>).
func WriteM3U(w io.Writer, base string, chans []Channel) error {
	return WriteM3UWith(w, base, chans, nil)
}

// WriteM3UWith writes own channels and external ones in one list by
// number. An external entry carries its headers as #EXTVLCOPT lines.
func WriteM3UWith(w io.Writer, base string, chans []Channel, ext []External) error {
	bw := bufio.NewWriter(w)
	epg := base + "/epg.xml"
	fmt.Fprintf(bw, "#EXTM3U url-tvg=\"%s\" x-tvg-url=\"%s\"\n", epg, epg)
	ei := 0
	writeExt := func(x External) {
		cat := x.Category
		if cat == "" {
			cat = DefaultCategory
		}
		fmt.Fprintf(bw, "#EXTINF:-1 tvg-id=\"%s\" tvg-chno=\"%d\" tvg-name=\"%s\" tvg-logo=\"%s\" group-title=\"%s\",%s\n",
			attr(ExternalTvgID(x)), x.Number, attr(x.Name), attr(x.logo(base)), attr(cat), strings.ReplaceAll(attr(x.Name), ",", " "))
		if x.UserAgent != "" {
			fmt.Fprintf(bw, "#EXTVLCOPT:http-user-agent=%s\n", line(x.UserAgent))
		}
		if x.Referrer != "" {
			fmt.Fprintf(bw, "#EXTVLCOPT:http-referrer=%s\n", line(x.Referrer))
		}
		fmt.Fprintf(bw, "%s\n", line(x.URL))
	}
	for _, c := range chans {
		for ei < len(ext) && ext[ei].Number < c.Number {
			writeExt(ext[ei])
			ei++
		}
		cat := c.Category
		if cat == "" {
			cat = DefaultCategory
		}
		name := strings.ReplaceAll(attr(c.Name), ",", " ")
		fmt.Fprintf(bw, "#EXTINF:-1 tvg-id=\"%s\" tvg-chno=\"%d\" tvg-name=\"%s\" tvg-logo=\"%s/logo/%d.png%s\" group-title=\"%s\",%s\n",
			TvgID(c), c.Number, attr(c.Name), base, c.ID, LogoQuery(c), attr(cat), name)
		fmt.Fprintf(bw, "%s/live/%d/index.m3u8\n", base, c.ID)
	}
	for ; ei < len(ext); ei++ {
		writeExt(ext[ei])
	}
	return bw.Flush()
}

// line: s on one line.
func line(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

// XMLTV document.
type xmltvDoc struct {
	XMLName    xml.Name       `xml:"tv"`
	Generator  string         `xml:"generator-info-name,attr"`
	Channels   []xmltvChannel `xml:"channel"`
	Programmes []xmltvProg    `xml:"programme"`
}

type xmltvText struct {
	Lang string `xml:"lang,attr,omitempty"`
	Text string `xml:",chardata"`
}

type xmltvIcon struct {
	Src string `xml:"src,attr"`
}

type xmltvChannel struct {
	ID    string      `xml:"id,attr"`
	Names []xmltvText `xml:"display-name"`
	Icon  *xmltvIcon  `xml:"icon,omitempty"`
}

type xmltvEpisodeNum struct {
	System string `xml:"system,attr"`
	Text   string `xml:",chardata"`
}

type xmltvRating struct {
	System string `xml:"system,attr"`
	Value  string `xml:"value"`
}

type xmltvProg struct {
	Start       string            `xml:"start,attr"`
	Stop        string            `xml:"stop,attr"`
	Channel     string            `xml:"channel,attr"`
	Title       xmltvText         `xml:"title"`
	SubTitle    *xmltvText        `xml:"sub-title,omitempty"`
	Desc        *xmltvText        `xml:"desc,omitempty"`
	Date        string            `xml:"date,omitempty"`
	Categories  []xmltvText       `xml:"category"`
	Icon        *xmltvIcon        `xml:"icon,omitempty"`
	EpisodeNums []xmltvEpisodeNum `xml:"episode-num"`
	Rating      *xmltvRating      `xml:"rating,omitempty"`
}

func xmltvTime(t time.Time) string { return t.UTC().Format("20060102150405 -0700") }

// Guide is what WriteXMLTV writes: the channels, their entries and the
// programmes' descriptions.
type Guide struct {
	Base     string // the profile's IPTV root URL
	Channels []Channel
	Entries  map[int64][]Entry // by channel id
	Info     map[int64]Info    // by item id
	Lang     string            // the text's language ("ru")
	// External: the external channels with their programmes.
	External []External
}

// WriteXMLTV writes the XMLTV guide.
func WriteXMLTV(w io.Writer, g Guide) error {
	lang := g.Lang
	if lang == "" {
		lang = "ru"
	}
	doc := xmltvDoc{Generator: "Antresol"}
	for _, c := range g.Channels {
		doc.Channels = append(doc.Channels, xmltvChannel{ID: TvgID(c), Names: []xmltvText{{Lang: lang, Text: c.Name}},
			Icon: &xmltvIcon{Src: fmt.Sprintf("%s/logo/%d.png%s", g.Base, c.ID, LogoQuery(c))}})
	}
	for _, x := range g.External {
		doc.Channels = append(doc.Channels, xmltvChannel{ID: ExternalTvgID(x), Names: []xmltvText{{Lang: lang, Text: x.Name}},
			Icon: &xmltvIcon{Src: x.logo(g.Base)}})
	}
	for _, x := range g.External {
		for _, p := range x.Programmes {
			xp := xmltvProg{Start: xmltvTime(p.Start), Stop: xmltvTime(p.End), Channel: ExternalTvgID(x), Title: xmltvText{Lang: lang, Text: p.Title}}
			if p.SubTitle != "" {
				xp.SubTitle = &xmltvText{Lang: lang, Text: p.SubTitle}
			}
			if p.Desc != "" {
				xp.Desc = &xmltvText{Lang: lang, Text: p.Desc}
			}
			if p.Category != "" {
				xp.Categories = append(xp.Categories, xmltvText{Lang: lang, Text: p.Category})
			}
			if p.Icon != "" {
				xp.Icon = &xmltvIcon{Src: p.Icon}
			}
			doc.Programmes = append(doc.Programmes, xp)
		}
	}
	for _, c := range g.Channels {
		es := g.Entries[c.ID]
		for i := 0; i < len(es); i++ {
			e := es[i]
			// the parts of one programme (cd1, cd2) are one guide entry
			end := e.End
			for i+1 < len(es) && es[i+1].ItemID == e.ItemID && (e.FileID == 0 || es[i+1].FileID != e.FileID) && es[i+1].Start.Equal(end) { // its first file again: a repeat
				i++
				end = es[i].End
			}
			info, ok := g.Info[e.ItemID]
			if !ok {
				continue
			}
			p := xmltvProg{Start: xmltvTime(e.Start), Stop: xmltvTime(end), Channel: TvgID(c), Title: xmltvText{Lang: lang, Text: info.Title}}
			if info.Kind == "episode" {
				sub := EpisodeLabel(info)
				p.SubTitle = &xmltvText{Lang: lang, Text: sub}
				if info.Season > 0 && info.Number > 0 {
					p.EpisodeNums = append(p.EpisodeNums,
						xmltvEpisodeNum{System: "xmltv_ns", Text: fmt.Sprintf("%d.%d.", info.Season-1, info.Number-1)},
						xmltvEpisodeNum{System: "onscreen", Text: fmt.Sprintf("S%02dE%02d", info.Season, info.Number)})
				}
			}
			if info.Overview != "" {
				p.Desc = &xmltvText{Lang: lang, Text: info.Overview}
			}
			if info.Year > 0 {
				p.Date = strconv.Itoa(info.Year)
			}
			for _, gn := range info.Genres {
				p.Categories = append(p.Categories, xmltvText{Lang: lang, Text: gn})
			}
			if info.ArtPoster != "" {
				p.Icon = &xmltvIcon{Src: fmt.Sprintf("%s/art/%d", g.Base, info.ArtItem)}
			}
			if info.Rating != "" {
				p.Rating = &xmltvRating{System: "RU", Value: info.Rating}
			}
			doc.Programmes = append(doc.Programmes, p)
		}
	}
	if _, err := io.WriteString(w, xml.Header+`<!DOCTYPE tv SYSTEM "xmltv.dtd">`+"\n"); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", " ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// EpisodeLabel is «S1 E5 · Title» (or the part there is).
func EpisodeLabel(i Info) string {
	var parts []string
	if i.Season > 0 && i.Number > 0 {
		parts = append(parts, fmt.Sprintf("S%d E%d", i.Season, i.Number))
	}
	if i.Episode != "" {
		parts = append(parts, i.Episode)
	}
	return strings.Join(parts, " · ")
}
