package jellyfin

import (
	"regexp"
	"strings"
	"testing"
)

func TestIDsRoundTrip(t *testing.T) {
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)
	for _, id := range []string{ItemID(5), ViewID("movies"), ViewID("tvshows"), UserID(3), FileID(5), ItemID(1 << 62)} {
		if !hex32.MatchString(id) {
			t.Fatalf("%q is not 32 lowercase hex", id)
		}
	}
	if k, id, ok := ParseID(ItemID(5)); k != kindItem || id != 5 || !ok {
		t.Fatalf("item: %d %d %v", k, id, ok)
	}
	if k, id, ok := ParseID(ItemID(1 << 62)); k != kindItem || id != 1<<62 || !ok {
		t.Fatalf("big: %d %d %v", k, id, ok)
	}
	if ItemID(5) == FileID(5) || ItemID(5) == UserID(5) {
		t.Fatal("kinds collide")
	}
	if k, id, ok := ParseID(ViewID("tvshows")); k != kindView || id != 2 || !ok {
		t.Fatalf("view: %d %d %v", k, id, ok)
	}
	s := UserID(42)
	dashed := strings.ToUpper(s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:])
	if k, id, ok := ParseID(dashed); k != kindUser || id != 42 || !ok {
		t.Fatalf("dashed %s: %d %d %v", dashed, k, id, ok)
	}
	for _, bad := range []string{"", "5", "zz41554c000100000000000000000005", "00000000000100000000000000000005",
		"5641554c0001ffffffffffffffffffff", "5641554c0009000000000000000000005"[:32], "5641554c000100000000000000000005ab"} {
		if _, _, ok := ParseID(bad); ok {
			t.Fatalf("%q parsed", bad)
		}
	}
}
