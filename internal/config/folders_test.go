package config

import "testing"

func loadAt(t *testing.T, root string) Config {
	t.Helper()
	for _, k := range []string{"VAULT_MOVIES_DIR", "VAULT_SERIES_DIR", "VAULT_MUSIC_DIR", "VAULT_BOOKS_DIR", "VAULT_EBOOKS_DIR",
		"VAULT_AUDIOBOOKS_DIR", "VAULT_COMICS_DIR", "VAULT_IMPORT_PATHS"} {
		t.Setenv(k, "")
	}
	t.Setenv("VAULT_MEDIA_ROOT", root)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFoldersOfTheEnvironment(t *testing.T) {
	t.Setenv("VAULT_MOVIES_DIR", "movies")
	t.Setenv("VAULT_SERIES_DIR", "series")
	t.Setenv("VAULT_MUSIC_DIR", "music")
	t.Setenv("VAULT_BOOKS_DIR", "books")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Folders{Movies: Dirs("movies"), Series: Dirs("series"), Music: Dirs("music"), Ebooks: Dirs("books/ebooks"), Audiobooks: Dirs("books/audiobooks"), Comics: Dirs("books/manga")}
	if got := c.Folders(); !got.Equal(want) {
		t.Fatalf("%+v", got)
	}
}

func TestWithFoldersOverridesOnlyWhatIsSet(t *testing.T) {
	c := loadAt(t, "/srv/media")
	got := c.WithFolders(Folders{Movies: Dirs("Фильмы"), Series: Dirs("tv")})
	if got.MoviesDir != "Фильмы" || got.SeriesDir != "tv" || got.MusicDir != c.MusicDir || got.EbooksDir != c.EbooksDir {
		t.Fatalf("%+v", got.Folders())
	}
	// the original is untouched (Config is copied by value)
	if c.MoviesDir == "Фильмы" {
		t.Fatal("changed the receiver")
	}
	if got.Abs(got.MoviesDir) != "/srv/media/Фильмы" {
		t.Fatalf("%q", got.Abs(got.MoviesDir))
	}
}

func TestWithFoldersMovesTheBooksRootAndImportPaths(t *testing.T) {
	c := loadAt(t, "/srv/media")
	got := c.WithFolders(Folders{Ebooks: Dirs("Книги/эл"), Audiobooks: Dirs("Книги/аудио"), Comics: Dirs("Книги/манга")})
	if got.BooksDir != "/srv/media/Книги" {
		t.Fatalf("books %q", got.BooksDir)
	}
	if got.Abs(got.BooksDir) != "/srv/media/Книги" {
		t.Fatalf("%q", got.Abs(got.BooksDir))
	}
	// the migration's default path map follows the folders
	var found bool
	for _, m := range got.ImportPaths {
		if m.From == "/audiobooks" && m.To == "/srv/media/Книги/аудио" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", got.ImportPaths)
	}
	// book folders that do not share a parent leave the books root alone
	scattered := c.WithFolders(Folders{Ebooks: Dirs("a/x"), Audiobooks: Dirs("b/y"), Comics: Dirs("c/z")})
	if scattered.BooksDir != c.BooksDir {
		t.Fatalf("%q", scattered.BooksDir)
	}
}

func TestWithFoldersKeepsAnExplicitImportPathMap(t *testing.T) {
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	t.Setenv("VAULT_IMPORT_PATHS", "/lib=/srv/media/lib")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := c.WithFolders(Folders{Audiobooks: Dirs("x/y"), Ebooks: Dirs("x/z"), Comics: Dirs("x/w")})
	if len(got.ImportPaths) != 1 || got.ImportPaths[0].To != "/srv/media/lib" {
		t.Fatalf("%+v", got.ImportPaths)
	}
}

func TestWithFoldersNeverMakesTheBooksRootTheMediaFolder(t *testing.T) {
	// the media folder written with a trailing slash: the book folders directly
	// in it still share the media folder, which is not a books folder
	for _, root := range []string{"/srv/media/", "/srv/media", "/srv//media/"} {
		c := loadAt(t, root)
		before := c.BooksDir
		got := c.WithFolders(Folders{Ebooks: Dirs("ebooks"), Audiobooks: Dirs("audiobooks"), Comics: Dirs("manga")})
		if got.BooksDir != before {
			t.Errorf("root %q: books root became %q", root, got.BooksDir)
		}
	}
	// the same with the folders written as absolute paths
	c := loadAt(t, "/srv/media/")
	got := c.WithFolders(Folders{Ebooks: Dirs("/srv/media/ebooks"), Audiobooks: Dirs("/srv/media/audiobooks"), Comics: Dirs("/srv/media/manga")})
	if got.BooksDir != c.BooksDir {
		t.Errorf("books root became %q", got.BooksDir)
	}
}

func TestWithFoldersKeepsFilmsOutOfTheBooksRoot(t *testing.T) {
	c := loadAt(t, "/srv/media")
	// one library folder holding the films and the books: «Книги» must not
	// become that folder, which every adult profile can read
	got := c.WithFolders(Folders{Movies: Dirs("library/movies"), Series: Dirs("library/series"), Music: Dirs("library/music"),
		Ebooks: Dirs("library/ebooks"), Audiobooks: Dirs("library/audiobooks"), Comics: Dirs("library/comics")})
	if got.BooksDir != c.BooksDir {
		t.Fatalf("books root became %q", got.BooksDir)
	}
	// even one kind of the others inside the books' parent is enough
	got = c.WithFolders(Folders{Music: Dirs("Книги/музыка"), Ebooks: Dirs("Книги/эл"), Audiobooks: Dirs("Книги/аудио"), Comics: Dirs("Книги/манга")})
	if got.BooksDir != c.BooksDir {
		t.Fatalf("books root became %q", got.BooksDir)
	}
	// the parent is the films' own folder
	got = c.WithFolders(Folders{Movies: Dirs("video"), Ebooks: Dirs("video/ebooks"), Audiobooks: Dirs("video/audiobooks"), Comics: Dirs("video/comics")})
	if got.BooksDir != c.BooksDir {
		t.Fatalf("books root became %q", got.BooksDir)
	}
	// a parent of nothing but books is still the books root
	got = c.WithFolders(Folders{Ebooks: Dirs("Книги/эл"), Audiobooks: Dirs("Книги/аудио"), Comics: Dirs("Книги/манга")})
	if got.BooksDir != "/srv/media/Книги" {
		t.Fatalf("books root %q", got.BooksDir)
	}
	// and a parent outside the media folder is not one
	got = c.WithFolders(Folders{Ebooks: Dirs("/data/b/e"), Audiobooks: Dirs("/data/b/a"), Comics: Dirs("/data/b/c")})
	if got.BooksDir != c.BooksDir {
		t.Fatalf("books root became %q", got.BooksDir)
	}
}
