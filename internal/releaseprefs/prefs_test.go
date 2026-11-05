package releaseprefs

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/db"
)

func newStore(t *testing.T) Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return Store{DB: d}
}

func TestSaveStudiosCanonical(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	p := Default()
	p.Studios = []string{" lostfilm ", "LostFilm", "", "Кубик в кубе", "  ", "Лостфильм", " Гоблин ", "гоблин"}
	if err := s.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load(ctx)
	if want := []string{"LostFilm", "Кубик в Кубе", "Гоблин"}; !slices.Equal(got.Studios, want) {
		t.Fatalf("stored %q, want %q", got.Studios, want)
	}

	p.Studios = nil
	for i := range 31 {
		p.Studios = append(p.Studios, "Студия "+string(rune('А'+i)))
	}
	if err := s.Save(ctx, p); err == nil {
		t.Error("31 studios saved")
	}
	p.Studios = p.Studios[:30]
	if err := s.Save(ctx, p); err != nil {
		t.Errorf("30 studios: %v", err)
	}
	p.Studios = []string{strings.Repeat("я", 41)}
	if err := s.Save(ctx, p); err == nil {
		t.Error("a 41-character name saved")
	}
	p.Studios = []string{strings.Repeat("я", 40)}
	if err := s.Save(ctx, p); err != nil {
		t.Errorf("a 40-character name: %v", err)
	}
}

func TestLoadDefaultsAndPartialJSON(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	p, err := s.Load(ctx)
	if err != nil || p.MaxHeight != 1080 || !p.PreferDub || p.Studios != nil {
		t.Fatalf("no row: %+v %v", p, err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, SettingsKey, `{"studios":["LostFilm"]}`); err != nil {
		t.Fatal(err)
	}
	p, err = s.Load(ctx)
	if err != nil || p.MaxHeight != 1080 || !p.PreferDub || !slices.Equal(p.Studios, []string{"LostFilm"}) {
		t.Fatalf("partial JSON: %+v %v", p, err)
	}
	if got, err := s.Studios(ctx); err != nil || !slices.Equal(got, []string{"LostFilm"}) {
		t.Fatalf("Studios: %q %v", got, err)
	}
}

func TestSetStudiosKeepsOtherFields(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	p := Default()
	p.MaxHeight = 2160
	if err := s.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStudios(ctx, []string{" лостфильм ", "Гоблин"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(ctx)
	if err != nil || got.MaxHeight != 2160 || !slices.Equal(got.Studios, []string{"LostFilm", "Гоблин"}) {
		t.Fatalf("%+v %v", got, err)
	}
}
