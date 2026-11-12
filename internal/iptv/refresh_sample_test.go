package iptv

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestEPGRealSampleStore (local only): IPTV_EPG_SAMPLE=<guide.xml[.gz]>
// is served to RefreshEPG for a synthetic playlist of its first 300
// channel ids; the time to parse and store, the heap and the database's
// growth are logged. Nothing of the sample is written anywhere but the
// test's temporary database.
func TestEPGRealSampleStore(t *testing.T) {
	path := os.Getenv("IPTV_EPG_SAMPLE")
	if path == "" {
		t.Skip("IPTV_EPG_SAMPLE unset")
	}
	ids := firstChannelIDs(t, path, 300)
	e := newSvcEnv(t)
	e.now = time.Now().UTC()
	guide := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, path) }))
	defer guide.Close()
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U url-tvg=\"%s/guide\"\n", guide.URL)
	for i, id := range ids {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=\"%s\",Channel %d\nhttp://stream.example.test/%d.ts\n", id, i, i)
	}
	e.p.put("/pl.m3u", b.String())
	src := e.source("Sample", "/pl.m3u", nil)
	e.refresh(src.ID)
	es, _ := e.svc.Store.EPGURLs(context.Background(), src.ID)
	dbFile := e.svc.Store.DB
	var dbPath string
	dbFile.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath)
	size := func() int64 {
		var n int64
		for _, suf := range []string{"", "-wal"} {
			if st, err := os.Stat(dbPath + suf); err == nil {
				n += st.Size()
			}
		}
		return n
	}
	before := size()
	runtime.GC()
	start := time.Now()
	if err := e.svc.RefreshEPG(context.Background(), es[0].ID); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	var progs, bound int
	dbFile.QueryRow(`SELECT count(*) FROM iptv_programmes`).Scan(&progs)
	dbFile.QueryRow(`SELECT count(*) FROM iptv_members WHERE epg_id<>''`).Scan(&bound)
	dbFile.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	t.Logf("guide refresh %v: %d programmes of %d bound channels stored; heap sys %d MB, total alloc %d MB; db growth %.1f MB (db %s)",
		took.Round(10*time.Millisecond), progs, bound, ms.HeapSys>>20, ms.TotalAlloc>>20, float64(size()-before)/(1<<20), filepath.Base(dbPath))
	// the refresh again (replaces the same programmes)
	start = time.Now()
	if err := e.svc.RefreshEPG(context.Background(), es[0].ID); err != nil {
		t.Fatal(err)
	}
	t.Logf("second guide refresh %v", time.Since(start).Round(10*time.Millisecond))
}

func firstChannelIDs(t *testing.T, path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var r io.Reader = br
	if m, _ := br.Peek(2); len(m) == 2 && m[0] == 0x1f && m[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			t.Fatal(err)
		}
		r = zr
	}
	d := xml.NewDecoder(r)
	d.Strict = false
	d.CharsetReader = charsetReader
	var ids []string
	for len(ids) < n {
		tok, err := d.RawToken()
		if err != nil {
			t.Fatal(err)
		}
		if s, ok := tok.(xml.StartElement); ok && s.Name.Local == "channel" {
			if id, ok := attr(s, "id"); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
