package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsTraversal(t *testing.T) {
	s, mr := testSandbox(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), 3)
	movies := filepath.Join(mr, "media/movies")
	if err := os.Symlink(outside, filepath.Join(movies, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nope"), filepath.Join(movies, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mr, ".vault-trash/1"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []struct{ root, rel string }{
		{"movies", "../series"},
		{"movies", "a/../../x"},
		{"movies", "a/.."},
		{"movies", ".."},
		{"movies", "/etc/passwd"},
		{"movies", "evil"},
		{"movies", "evil/secret.txt"},
		{"movies", "evil/new-dir"},
		{"movies", "dangling"},
		{"movies", "dangling/x"},
		{"movies", "a\x00b"},
		{"disk", ".vault-trash"},
		{"disk", ".vault-trash/1"},
	}
	for _, c := range bad {
		if _, err := s.Resolve(c.root, c.rel); !errors.Is(err, ErrOutside) {
			t.Errorf("Resolve(%q, %q) = %v, want ErrOutside", c.root, c.rel, err)
		}
	}
	if _, err := s.Resolve("nope", ""); !errors.Is(err, ErrUnknownRoot) {
		t.Errorf("unknown root: %v", err)
	}

	if err := os.Mkdir(filepath.Join(movies, "Дюна (2024)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(movies, "Дюна (2024)"), filepath.Join(movies, "link-inside")); err != nil {
		t.Fatal(err)
	}
	good := map[string]string{
		"":                    movies,
		"Дюна (2024)":         filepath.Join(movies, "Дюна (2024)"),
		"./Дюна (2024)/":      filepath.Join(movies, "Дюна (2024)"),
		"Дюна (2024)/new.mkv": filepath.Join(movies, "Дюна (2024)/new.mkv"),
		"not/yet/there":       filepath.Join(movies, "not/yet/there"),
		"link-inside":         filepath.Join(movies, "link-inside"),
		"[Brat 2] 1080p..mkv": filepath.Join(movies, "[Brat 2] 1080p..mkv"),
	}
	for rel, want := range good {
		got, err := s.Resolve("movies", rel)
		if err != nil || got != want {
			t.Errorf("Resolve(movies, %q) = %q, %v; want %q", rel, got, err, want)
		}
	}
	if rel, ok := s.Rel("movies", filepath.Join(movies, "Дюна (2024)/a.mkv")); !ok || rel != "Дюна (2024)/a.mkv" {
		t.Errorf("Rel = %q %v", rel, ok)
	}
	if _, ok := s.Rel("movies", outside); ok {
		t.Error("Rel outside must fail")
	}
}

func TestValidName(t *testing.T) {
	for _, n := range []string{"", ".", "..", "a/b", "a\x00", " a", "a ", ".hidden", "a\nb", string(make([]byte, 256)),
		"evil\u202egpj.exe", "a\u200bb", "x\u2066y", "\ufeffbom"} {
		if err := ValidName(n); !errors.Is(err, ErrBadName) {
			t.Errorf("ValidName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"Брат 2 (2000) BDRip 1080p.mkv", "Сёгун.S01E03.WEB-DL.1080p.mkv", "[x] y"} {
		if err := ValidName(n); err != nil {
			t.Errorf("ValidName(%q) = %v", n, err)
		}
	}
}

// A case variant of the trash folder is still the trash on a
// case-insensitive disk (APFS default).
func TestResolveRejectsTrashCaseVariant(t *testing.T) {
	s, mr := testSandbox(t)
	write(t, filepath.Join(mr, TrashDirName, "1/0/a.mkv"), 1)
	if _, err := os.Stat(filepath.Join(mr, ".VAULT-TRASH")); err != nil {
		t.Skip("case-sensitive file system")
	}
	for _, rel := range []string{".VAULT-TRASH", ".Vault-Trash/1/0/a.mkv"} {
		if _, err := s.Resolve("disk", rel); !errors.Is(err, ErrOutside) {
			t.Errorf("Resolve(disk, %q) = %v", rel, err)
		}
	}
}
