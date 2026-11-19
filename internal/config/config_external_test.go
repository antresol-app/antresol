package config

import (
	"testing"

	"github.com/antresol-app/antresol/internal/extsvc"
)

// TestConfigExternalServicesEnv: in both editions a service without its
// variable has its public address and no lock (the settings decide);
// "off" clears the address and locks it off; "on" or an address locks it on.
func TestConfigExternalServicesEnv(t *testing.T) {
	for _, k := range []string{"VAULT_WIKIDATA_BASEURL", "VAULT_IMDB_RATINGS_URL", "VAULT_LRCLIB_URL", "VAULT_SEGMENTS_ONLINE"} {
		t.Setenv(k, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.WikidataURL != DefaultWikidataURL || c.IMDbRatingsURL != DefaultIMDbRatingsURL || c.LRCLibURL != DefaultLRCLibURL || !c.SegmentsOnline {
		t.Fatalf("unset: %q %q %q %v", c.WikidataURL, c.IMDbRatingsURL, c.LRCLibURL, c.SegmentsOnline)
	}
	if len(c.ExternalEnv) != 0 {
		t.Fatalf("unset locks: %v", c.ExternalEnv)
	}

	t.Setenv("VAULT_WIKIDATA_BASEURL", "off")
	t.Setenv("VAULT_IMDB_RATINGS_URL", "on")
	t.Setenv("VAULT_LRCLIB_URL", "http://lyrics.lan:3300/")
	t.Setenv("VAULT_SEGMENTS_ONLINE", "off")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.WikidataURL != "" || c.IMDbRatingsURL != DefaultIMDbRatingsURL || c.LRCLibURL != "http://lyrics.lan:3300" || c.SegmentsOnline {
		t.Fatalf("set: %q %q %q %v", c.WikidataURL, c.IMDbRatingsURL, c.LRCLibURL, c.SegmentsOnline)
	}
	want := map[extsvc.ID]bool{extsvc.Wikidata: false, extsvc.IMDb: true, extsvc.Lyrics: true, extsvc.Segments: false}
	for id, on := range want {
		if got, ok := c.ExternalEnv[id]; !ok || got != on {
			t.Fatalf("%s: lock %v %v, want %v", id, got, ok, on)
		}
	}

	// the environment beats the settings, which beat the default
	sw := extsvc.New(c.ExternalEnv)
	no, yes := false, true
	sw.Apply(extsvc.Choice{extsvc.Wikidata: &yes, extsvc.IMDb: &no, extsvc.Lyrics: &no, extsvc.Segments: &yes})
	for id, on := range want {
		if sw.On(id) != on {
			t.Fatalf("%s: settings beat the environment", id)
		}
	}
	t.Setenv("VAULT_LRCLIB_URL", "on")
	if c, err := Load(); err != nil || c.LRCLibURL != DefaultLRCLibURL || !c.ExternalEnv[extsvc.Lyrics] {
		t.Fatalf("lyrics on: %q %v %v", c.LRCLibURL, c.ExternalEnv, err)
	}
}
