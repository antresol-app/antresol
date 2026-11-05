package natsort

import (
	"slices"
	"testing"
)

func TestNatsort(t *testing.T) {
	s := []string{"10.mp3", "2.mp3", "1.mp3"}
	Sort(s)
	if !slices.Equal(s, []string{"1.mp3", "2.mp3", "10.mp3"}) {
		t.Fatalf("sort %v", s)
	}
	for _, c := range [][2]string{
		{"v2", "v10"},
		{"Глава 9", "Глава 10"},
		{"CD1/10", "CD2/1"},
		{"track 02", "Track 3"},
		{"ёж 1", "Еж 2"},
		{"a", "ab"},
	} {
		if !Less(c[0], c[1]) || Less(c[1], c[0]) {
			t.Fatalf("%q < %q expected", c[0], c[1])
		}
	}
	if Less("x", "x") {
		t.Fatal("x < x")
	}
}
