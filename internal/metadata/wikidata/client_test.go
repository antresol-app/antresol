package wikidata

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"github.com/antresol-app/antresol/internal/version"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata"
)

// fixtures replays the recorded Wikidata answers in testdata, keyed by the
// request's query (VAULT_RECORD_WIKIDATA=1 records them from the live API).
type fixtures struct {
	t      *testing.T
	record bool
}

func fixtureName(r *http.Request) string {
	q := r.URL.Query()
	q.Del("format")
	sum := sha1.Sum([]byte(q.Encode()))
	return filepath.Join("testdata", q.Get("action")+"-"+hex.EncodeToString(sum[:6])+".json")
}

func (f fixtures) RoundTrip(r *http.Request) (*http.Response, error) {
	name := fixtureName(r)
	if f.record {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err := os.WriteFile(name, body, 0o644); err != nil {
			f.t.Fatal(err)
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	body, err := os.ReadFile(name)
	if err != nil {
		f.t.Fatalf("no fixture for %s: %v", r.URL.RawQuery, err)
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: r}, nil
}

func fixtureClient(t *testing.T) *Client {
	return &Client{BaseURL: "https://www.wikidata.org", UserAgent: version.UserAgent,
		HTTP: &http.Client{Transport: fixtures{t, os.Getenv("VAULT_RECORD_WIKIDATA") == "1"}}, Limiter: &metadata.Limiter{}}
}

func TestSearchBooksRussianTitleFindsTheOriginal(t *testing.T) {
	works, err := fixtureClient(t).SearchBooks(context.Background(), "Шантарам", "ru")
	if err != nil {
		t.Fatal(err)
	}
	if len(works) == 0 {
		t.Fatal("nothing found")
	}
	w := works[0]
	if w.ID != "Q2373810" || w.TitleRU != "Шантарам" || w.TitleEN != "Shantaram" || w.Original != "Shantaram" ||
		w.Year != 2003 || w.OpenLibrary != "/works/OL19061442W" || w.Sitelinks == 0 {
		t.Fatalf("%+v", w)
	}
	if strings.Join(w.AuthorsEN, ";") != "Gregory David Roberts" || !strings.Contains(strings.Join(w.AuthorsRU, ";"), "Робертс") {
		t.Fatalf("authors %q %q", w.AuthorsRU, w.AuthorsEN)
	}
	for _, x := range works {
		if x.ID == "Q106582907" || x.ID == "Q126735031" { // the TV series; the e-book edition (its work is Q2373810)
			t.Fatalf("not a work: %+v", x)
		}
	}
}

func TestSearchBooksEnglishTitleHasTheRussianOne(t *testing.T) {
	works, err := fixtureClient(t).SearchBooks(context.Background(), "Shantaram", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(works) == 0 || works[0].ID != "Q2373810" || works[0].TitleRU != "Шантарам" {
		t.Fatalf("%+v", works)
	}
}

func TestSearchBooksNothing(t *testing.T) {
	works, err := fixtureClient(t).SearchBooks(context.Background(), "zzqxqzzqxw", "ru")
	if err != nil || len(works) != 0 {
		t.Fatalf("%+v %v", works, err)
	}
}

func TestLiveDump(t *testing.T) {
	if os.Getenv("WD_LIVE") == "" {
		t.Skip()
	}
	c := &Client{BaseURL: "https://www.wikidata.org", UserAgent: "Vault/0.1 test"}
	for _, q := range strings.Split(os.Getenv("WD_LIVE"), ",") {
		ws, err := c.SearchBooks(context.Background(), q, "ru")
		t.Logf("%s err=%v", q, err)
		for _, w := range ws {
			t.Logf("  %+v", w)
		}
	}
}
