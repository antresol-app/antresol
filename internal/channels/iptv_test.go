package channels

import (
	"bytes"
	"encoding/xml"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestWriteM3U(t *testing.T) {
	var b bytes.Buffer
	chans := []Channel{{ID: 3, Number: 12, Name: `Кино "90-х", хиты`, Category: "Кино по десятилетиям"}, {ID: 4, Number: 300, Name: "Мой"}}
	if err := WriteM3U(&b, "https://tv.example/iptv/tok", chans); err != nil {
		t.Fatal(err)
	}
	want := `#EXTM3U url-tvg="https://tv.example/iptv/tok/epg.xml" x-tvg-url="https://tv.example/iptv/tok/epg.xml"
#EXTINF:-1 tvg-id="antresol.3" tvg-chno="12" tvg-name="Кино '90-х', хиты" tvg-logo="https://tv.example/iptv/tok/logo/3.png" group-title="Кино по десятилетиям",Кино '90-х'  хиты
https://tv.example/iptv/tok/live/3/index.m3u8
#EXTINF:-1 tvg-id="antresol.4" tvg-chno="300" tvg-name="Мой" tvg-logo="https://tv.example/iptv/tok/logo/4.png" group-title="Каналы",Мой
https://tv.example/iptv/tok/live/4/index.m3u8
`
	if b.String() != want {
		t.Fatalf("got\n%s", b.String())
	}
}

func TestWriteXMLTVParsesBack(t *testing.T) {
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ch := Channel{ID: 3, Number: 12, Name: "Сериалы & <кино>"}
	g := Guide{Base: "https://h/iptv/t", Channels: []Channel{ch},
		Entries: map[int64][]Entry{3: {
			{ItemID: 10, Start: base, End: base.Add(time.Hour)},
			{ItemID: 11, Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)},
			{ItemID: 11, Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour)}, // part 2
		}},
		Info: map[int64]Info{
			10: {ItemID: 10, Kind: "episode", Title: "Шоу", Episode: "Пилот", Season: 1, Number: 2, Overview: "О чём", Year: 2001, Rating: "16+", Genres: []string{"драма"}, ArtItem: 9, ArtPoster: "x.jpg"},
			11: {ItemID: 11, Kind: "movie", Title: "Фильм"},
		}}
	var b bytes.Buffer
	if err := WriteXMLTV(&b, g); err != nil {
		t.Fatal(err)
	}
	var doc xmltvDoc
	if err := xml.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if len(doc.Channels) != 1 || doc.Channels[0].ID != "antresol.3" || doc.Channels[0].Names[0].Text != ch.Name {
		t.Fatalf("channels %+v", doc.Channels)
	}
	if len(doc.Programmes) != 2 {
		t.Fatalf("programmes %+v", doc.Programmes)
	}
	p := doc.Programmes[0]
	if p.Start != "20261110120000 +0000" || p.Title.Text != "Шоу" || p.SubTitle.Text != "S1 E2 · Пилот" || p.EpisodeNums[0].Text != "0.1." ||
		p.Icon.Src != "https://h/iptv/t/art/9" || p.Rating.Value != "16+" || p.Date != "2001" {
		t.Fatalf("episode %+v", p)
	}
	if q := doc.Programmes[1]; q.Stop != "20261110150000 +0000" || q.Icon != nil {
		t.Fatalf("two-part movie %+v", q)
	}
	if !strings.Contains(b.String(), "<!DOCTYPE tv") {
		t.Fatal("no doctype")
	}
}

func TestLogoIsAPNG(t *testing.T) {
	for _, name := range []string{"Комедии", "Кино по десятилетиям: восьмидесятые", "Очень-очень-длинноеназваниеканалабезпробелов"} {
		b, err := Logo(Channel{ID: 5, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || img.Bounds().Dx() != LogoWidth || img.Bounds().Dy() != LogoHeight {
			t.Fatalf("%s: %v %v", name, err, img.Bounds())
		}
	}
}
