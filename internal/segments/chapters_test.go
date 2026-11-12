package segments

import (
	"reflect"
	"testing"

	"github.com/antresol-app/antresol/internal/media"
)

func msp(v int64) *int64 { return &v }

func TestChapterKind(t *testing.T) {
	cases := map[string]string{
		"Recap": Recap, "Previously on Lost": Recap, "Ранее в сериале": Recap, "В предыдущих сериях": Recap,
		"Intro": Intro, "OP": Intro, "Opening": Intro, "Opening Credits": Intro, "Заставка": Intro, "Вступление": Intro,
		"Credits": Credits, "End Credits": Credits, "ED": Credits, "Ending": Credits, "Outro": Credits, "Титры": Credits,
		"Chapter 1": "", "Prologue": "", "Operation": "", "Edward": "", "": "",
	}
	for title, want := range cases {
		if got := chapterKind(title); got != want {
			t.Errorf("%q: %q, want %q", title, got, want)
		}
	}
}

func TestFromChapters(t *testing.T) {
	dur := int64(1_300_000)
	chs := []media.Chapter{
		{Title: "Previously", StartMs: 0, EndMs: msp(40_000)},
		{Title: "Chapter 2", StartMs: 40_000},
		{Title: "Opening", StartMs: 95_000},
		{Title: "Chapter 4", StartMs: 150_000},
		{Title: "Ending", StartMs: 1_200_000, EndMs: msp(1_250_000)},
		{Title: "Credits", StartMs: 1_250_000},
	}
	got := FromChapters(chs, dur)
	want := []Segment{
		{Recap, SourceChapters, 0, 40_000},
		{Intro, SourceChapters, 95_000, 150_000},
		{Credits, SourceChapters, 1_200_000, 1_300_000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestFromChaptersPositions(t *testing.T) {
	// an «Intro» late in the file and «Credits» early are not believed
	chs := []media.Chapter{{Title: "Credits", StartMs: 0}, {Title: "Body", StartMs: 60_000}, {Title: "Intro", StartMs: 900_000}}
	if got := FromChapters(chs, 1_000_000); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	if got := FromChapters(nil, 1_000_000); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestPick(t *testing.T) {
	stored := []Segment{
		{Credits, SourceAnalysis, 1_200_000, 1_290_000},
		{Intro, SourceAnalysis, 60_000, 90_000},
		{Intro, SourceTheIntroDB, 61_000, 91_000},
		{Recap, SourceIntroDB, 0, 2_000}, // too short
		{Credits, SourceChapters, 1_210_000, 1_400_000},
	}
	got := Pick(stored, 1_300_000, false)
	want := []Play{{Intro, 61, 91}, {Credits, 1210, 1300}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	got = Pick(stored, 1_300_000, true)
	if len(got) != 1 || got[0].Kind != Credits {
		t.Fatalf("movie: %+v", got)
	}
	if got := Pick(nil, 0, false); got == nil || len(got) != 0 {
		t.Fatalf("empty: %#v", got)
	}
}

// An online answer measured on another cut of the episode (Suits S2E1) is
// dropped whole: its intro and its credits are both off; the file's own
// analysis plays. One that agrees within a few seconds keeps its rank.
func TestPickDropsAnotherCut(t *testing.T) {
	stored := []Segment{
		{Kind: Intro, Source: SourceIntroDB, StartMs: 387500, EndMs: 417900},
		{Kind: Credits, Source: SourceIntroDB, StartMs: 2551000, EndMs: 2587000},
		{Kind: Intro, Source: SourceAnalysis, StartMs: 523594, EndMs: 554059},
		{Kind: Credits, Source: SourceAnalysis, StartMs: 2789373, EndMs: 2822720},
	}
	got := Pick(stored, 2822720, false)
	if len(got) != 2 || got[0].StartSec != 523.594 || got[1].StartSec != 2789.373 {
		t.Fatalf("another cut: %+v", got)
	}
	// one kind off (Drain the Oceans: a sting taken for the theme) keeps the online answer
	one := []Segment{
		{Kind: Intro, Source: SourceTheIntroDB, StartMs: 46000, EndMs: 71000},
		{Kind: Intro, Source: SourceAnalysis, StartMs: 106500, EndMs: 116000},
	}
	if got := Pick(one, 2834000, false); len(got) != 1 || got[0].StartSec != 46 {
		t.Fatalf("one kind off: %+v", got)
	}
	agree := []Segment{
		{Kind: Intro, Source: SourceTheIntroDB, StartMs: 241032, EndMs: 269018},
		{Kind: Intro, Source: SourceAnalysis, StartMs: 238887, EndMs: 269351},
	}
	if got := Pick(agree, 2568832, false); len(got) != 1 || got[0].StartSec != 241.032 {
		t.Fatalf("agreeing online intro lost its rank: %+v", got)
	}
}
