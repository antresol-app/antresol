package lang

import (
	"reflect"
	"testing"
)

func TestValid(t *testing.T) {
	for _, c := range []struct {
		l    []string
		min  int
		want bool
	}{
		{[]string{"ru", "en"}, 1, true},
		{[]string{}, 0, true},
		{[]string{}, 1, false},
		{[]string{"ru", "ru"}, 1, false},
		{[]string{"xx"}, 1, false},
	} {
		if got := Valid(c.l, c.min); got != c.want {
			t.Errorf("Valid(%v, %d) = %v", c.l, c.min, got)
		}
	}
}

func TestOrderedFollowsTheViewersOrder(t *testing.T) {
	if got := Ordered([]string{"en", "ja", "de"}, []string{"ru", "ja", "en"}); !reflect.DeepEqual(got, []string{"ja", "en"}) {
		t.Fatalf("got %v", got)
	}
}

func TestBonusFallsWithRank(t *testing.T) {
	if !(Bonus(0) > Bonus(1) && Bonus(1) > Bonus(2) && Bonus(2) > Bonus(3) && Bonus(3) > Bonus(-1)) {
		t.Fatal("bonus must fall with rank")
	}
	if In("ja") != "на японском" || In("xx") != "" {
		t.Fatal(In("ja"))
	}
}
