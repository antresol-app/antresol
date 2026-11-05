package metadata

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		err  error
		want Class
	}{
		{ErrNotFound, Miss},
		{fmt.Errorf("x: %w", ErrNotFound), Miss},
		{&HTTPError{Status: 400}, Miss},
		{&HTTPError{Status: 429}, ItemFail},
		{&HTTPError{Status: 500}, ItemFail},
		{&HTTPError{Status: 503}, ItemFail},
		{&DecodeError{Err: errors.New("bad")}, ItemFail},
		{errors.New("dial tcp: connection refused"), Offline},
	} {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestMetaHelpers(t *testing.T) {
	if _, err := ParseMeta("{broken"); err == nil {
		t.Fatal("corrupt meta accepted")
	}
	m, err := ParseMeta(`{"a":" x ","n":3,"l":["p"," ",1],"t":"2026-10-25T12:00:00Z"}`)
	if err != nil || MetaStr(m, "a") != "x" || MetaInt(m, "n") != 3 || fmt.Sprint(MetaStrings(m, "l")) != "[p]" {
		t.Fatalf("%v %v", m, err)
	}
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if !Fresh(m, "t", now, 13*time.Hour) || Fresh(m, "t", now, 12*time.Hour) || Fresh(m, "none", now, time.Hour) {
		t.Fatal("Fresh")
	}
	if Stamp(time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))) != "2026-01-02T02:04:05Z" {
		t.Fatal("Stamp")
	}
	if Jitter(1) != time.Hour || Jitter(48) != 0 || Jitter(-5) != 5*time.Hour {
		t.Fatal("Jitter")
	}
	if NormTitle("Мой̆") != NormTitle("Моий") && NormTitle("Ной") != "ной" {
		t.Fatalf("NFC: %q", NormTitle("Ной"))
	}
}
