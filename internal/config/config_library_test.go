//go:build !sources

package config

import "testing"

// TestLibraryEditionAlwaysLibraryOnly: the core build serves its
// library alone whatever VAULT_LIBRARY_ONLY says, and looks song words up
// online only when VAULT_LRCLIB_URL asks for it.
func TestLibraryEditionAlwaysLibraryOnly(t *testing.T) {
	for _, v := range []string{"", "off", "on"} {
		t.Setenv("VAULT_LIBRARY_ONLY", v)
		t.Setenv("VAULT_LRCLIB_URL", "")
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !c.LibraryOnly || c.LRCLibURL != "" {
			t.Fatalf("VAULT_LIBRARY_ONLY=%q: libraryOnly %v, lrclib %q", v, c.LibraryOnly, c.LRCLibURL)
		}
	}
	// ratings: Rotten Tomatoes (Wikidata) and the IMDb dataset only when
	// asked for: an App Store install contacts no third party by default
	t.Setenv("VAULT_IMDB_RATINGS_URL", "")
	t.Setenv("VAULT_WIKIDATA_BASEURL", "")
	if c, err := Load(); err != nil || c.IMDbRatingsURL != "" || c.WikidataURL != "" {
		t.Fatalf("ratings: imdb %q wikidata %q %v", c.IMDbRatingsURL, c.WikidataURL, err)
	}
	t.Setenv("VAULT_WIKIDATA_BASEURL", "on")
	if c, err := Load(); err != nil || c.WikidataURL != DefaultWikidataURL {
		t.Fatalf("wikidata on: %q %v", c.WikidataURL, err)
	}
	t.Setenv("VAULT_IMDB_RATINGS_URL", "on")
	if c, err := Load(); err != nil || c.IMDbRatingsURL != DefaultIMDbRatingsURL {
		t.Fatalf("imdb on: %q %v", c.IMDbRatingsURL, err)
	}
	t.Setenv("VAULT_LRCLIB_URL", "http://lrclib.lan:3300/")
	if c, err := Load(); err != nil || c.LRCLibURL != "http://lrclib.lan:3300" {
		t.Fatalf("own lrclib: %q %v", c.LRCLibURL, err)
	}
}

// TestLibraryReadsImportVars: «Переезд» imports from the media servers in
// both editions, so VAULT_IMPORT_DIR and VAULT_IMPORT_PATHS are read here.
func TestLibraryReadsImportVars(t *testing.T) {
	t.Setenv("VAULT_IMPORT_DIR", "/x")
	t.Setenv("VAULT_IMPORT_PATHS", "")
	c, err := Load()
	if err != nil || c.ImportDir != "/x" || len(c.ImportPaths) != 4 {
		t.Fatalf("dir %q paths %+v %v", c.ImportDir, c.ImportPaths, err)
	}
	t.Setenv("VAULT_IMPORT_PATHS", "/data/books=/media/books")
	if c, err := Load(); err != nil || len(c.ImportPaths) != 1 || c.ImportPaths.Map("/data/books/x") != "/media/books/x" {
		t.Fatalf("own paths %+v %v", c.ImportPaths, err)
	}
	t.Setenv("VAULT_IMPORT_PATHS", "no-equals-sign")
	if _, err := Load(); err == nil {
		t.Fatal("a bad VAULT_IMPORT_PATHS loaded")
	}
}
