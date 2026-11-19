package extsvc

import "testing"

// TestSwitchesPrecedence: the environment, then the choice, then Default.
func TestSwitchesPrecedence(t *testing.T) {
	s := New(map[ID]bool{Lyrics: false})
	for _, id := range All {
		want := Default(id)
		if id == Lyrics {
			want = false
		}
		if s.On(id) != want {
			t.Fatalf("%s before any choice: %v", id, s.On(id))
		}
	}
	yes, no := true, false
	s.Apply(Choice{IMDb: &yes, Wikidata: &no, Lyrics: &yes, "nope": &yes})
	if !s.On(IMDb) || s.On(Wikidata) || s.On(Lyrics) || s.On(Segments) != Default(Segments) {
		t.Fatalf("after a choice: imdb %v wikidata %v lyrics %v", s.On(IMDb), s.On(Wikidata), s.On(Lyrics))
	}
	gate := s.Gate(Wikidata)
	s.Apply(Choice{Wikidata: &yes})
	if !gate() || s.On(IMDb) != Default(IMDb) {
		t.Fatal("a new choice does not reach the gate at once")
	}
	if on, set := s.Env(Lyrics); on || !set {
		t.Fatal("lyrics lock lost")
	}
	var none *Switches
	if none.On(Wikidata) != Default(Wikidata) {
		t.Fatal("nil switches are not the defaults")
	}
	if (Choice{IMDb: nil}).Chosen() || !(Choice{IMDb: &no}).Chosen() {
		t.Fatal("Chosen")
	}
}
