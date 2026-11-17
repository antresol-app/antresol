package addons

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The disk.usage answers become the usage report's segments, named
// addon:<addonId>:<id>, in addon-id order; bad segments, and addons that
// are down or answer garbage, are left out.
func TestDiskUsage_Segments(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"disk.usage": 2 * time.Second}
	long := strings.Repeat("я", 80)
	hookAddon(t, tg, "dl", `{"disk.usage": {"body": {"segments": [
		{"id": "data", "label": {"ru": "Загрузки", "en": "Downloads"}, "dir": "/m/dl"},
		{"id": "cache", "label": {"ru": " Кеш\u0007 "}, "bytes": 500},
		{"id": "neg", "label": {"en": "`+long+`"}, "bytes": -3},
		{"id": "data", "label": {"ru": "Снова"}, "bytes": 1},
		{"id": "Bad:id", "label": {"ru": "Плохой"}, "bytes": 1},
		{"id": "nolabel", "label": {"ru": " ", "en": ""}, "bytes": 1}
	]}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "alpha", `{"disk.usage": {"body": {"segments": [{"id": "a", "label": {"ru": "А"}, "bytes": 1}]}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "garbage", `{"disk.usage": {"body": {"segments": "nope"}}}`, []string{"disk.usage"}, true)
	hookAddon(t, tg, "down", `{"disk.usage": {"body": {"segments": [{"id": "x", "label": {"ru": "X"}}]}}}`, []string{"disk.usage"}, false)
	got := h.DiskUsage(context.Background())
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "addon:alpha:a,addon:dl:data,addon:dl:cache,addon:dl:neg" {
		t.Fatalf("ids %v", ids)
	}
	data, cache, neg := got[1], got[2], got[3]
	if data.Dir != "/m/dl" || data.Label["ru"] != "Загрузки" || data.Label["en"] != "Downloads" {
		t.Errorf("data %+v", data)
	}
	if _, ok := cache.Label["en"]; cache.Bytes != 500 || cache.Label["ru"] != "Кеш" || ok {
		t.Errorf("cache %+v", cache)
	}
	if neg.Bytes != 0 || len([]rune(neg.Label["en"])) != maxDiskLabel {
		t.Errorf("neg %+v", neg)
	}
	if cs := calls(t, tg, "dl"); len(cs) != 1 || cs[0].User != "" {
		t.Errorf("calls %+v (the report is the household's: no user)", cs)
	}
}

// At most maxDiskSegments segments of one addon count.
func TestDiskUsage_Bounded(t *testing.T) {
	h, tg := newHooks(t)
	var segs []string
	for i := range maxDiskSegments + 3 {
		segs = append(segs, `{"id": "s`+string(rune('a'+i))+`", "label": {"ru": "С"}, "bytes": 1}`)
	}
	hookAddon(t, tg, "dl", `{"disk.usage": {"body": {"segments": [`+strings.Join(segs, ",")+`]}}}`, []string{"disk.usage"}, true)
	if got := h.DiskUsage(context.Background()); len(got) != maxDiskSegments {
		t.Fatalf("%d segments", len(got))
	}
}
