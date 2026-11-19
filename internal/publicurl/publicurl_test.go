package publicurl

import "testing"

func TestAddress(t *testing.T) {
	a := New("")
	if a.Get() != "" {
		t.Fatalf("empty: %q", a.Get())
	}
	a.SetSaved(" https://media.example.org/ ")
	if got := a.Get(); got != "https://media.example.org" {
		t.Errorf("saved: %q", got)
	}
	a.SetSaved("")
	if a.Get() != "" {
		t.Errorf("cleared: %q", a.Get())
	}
	e := New("https://env.example.org/")
	e.SetSaved("https://saved.example.org")
	if e.Get() != "https://env.example.org" || e.Env() != "https://env.example.org" {
		t.Errorf("env wins: %q", e.Get())
	}
	var none *Address
	if none.Get() != "" || none.Env() != "" {
		t.Error("nil address")
	}
	none.SetSaved("https://x.example.org")
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, "https://a.example.org": true, "http://192.0.2.1:8420/": true, "https://a.example.org/antresol": true,
		"a.example.org": false, "ftp://a.example.org": false, "https://": false, "https://a.example.org/?x=1": false,
		"https://u:p@a.example.org": false, "https://a.example.org/#x": false,
	} {
		if _, ok := Normalize(in); ok != want {
			t.Errorf("Normalize(%q) ok = %v, want %v", in, ok, want)
		}
	}
}
