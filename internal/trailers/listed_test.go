package trailers

import (
	"path/filepath"
	"testing"
	"time"
)

const (
	keyA = "aaaaaaaaaaa"
	keyB = "bbbbbbbbbbb"
	keyC = "ccccccccccc"
)

func TestValidKey(t *testing.T) {
	for k, ok := range map[string]bool{keyA: true, "dQw4w9WgXc_": true, "a-b_c-d_e-f": true,
		"": false, "short": false, "twelvechars1": false, "../../etc/p": false, "aaaaaaaaaa/": false, "aaaa aaaaaa": false} {
		if ValidKey(k) != ok {
			t.Errorf("ValidKey(%q) = %v", k, !ok)
		}
	}
}

func TestListedPersistsAndExpires(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trailers", "listed.json")
	l := NewListed(p)
	l.Add(7, []string{keyA, "bad key", keyB})
	l.Add(9, []string{keyA})
	if got := l.ItemsOf(keyA); len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("%v", got)
	}
	if l.ItemsOf(keyC) != nil || l.ItemsOf("bad key") != nil {
		t.Fatal("unlisted key has items")
	}
	// a restart keeps them
	l2 := NewListed(p)
	if got := l2.ItemsOf(keyB); len(got) != 1 || got[0] != 7 {
		t.Fatalf("after restart %v", got)
	}
	// too long ago: gone, and not read back either
	later := time.Now().Add(31 * 24 * time.Hour)
	l2.now = func() time.Time { return later }
	if l2.ItemsOf(keyB) != nil {
		t.Fatal("expired listing")
	}
	l2.Add(9, []string{keyC}) // prunes and saves
	l3 := NewListed(p)
	if l3.ItemsOf(keyA) != nil || l3.ItemsOf(keyC) == nil {
		t.Fatal("pruned keys came back")
	}
}

func TestListedInMemory(t *testing.T) {
	l := NewListed("")
	l.Add(1, []string{keyA})
	if got := l.ItemsOf(keyA); len(got) != 1 {
		t.Fatalf("%v", got)
	}
}
