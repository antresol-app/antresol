package addonkit

import (
	"encoding/json"
	"testing"
)

func TestHistoryUser(t *testing.T) {
	var u User
	h := `{"id":1,"uid":"p1","name":"Папа","maxRating":"12+","history":{"id":9,"uid":"h9","name":"Гостиная"}}`
	if err := json.Unmarshal([]byte(h), &u); err != nil {
		t.Fatal(err)
	}
	hu := u.HistoryUser()
	if hu.ID != 9 || hu.UID != "h9" || hu.MaxRating != "12+" || hu.Name != "Папа" || hu.History != nil {
		t.Fatalf("history user %+v", hu)
	}
	if u.ID != 1 || u.History == nil {
		t.Fatal("HistoryUser changed its receiver")
	}
	own := User{ID: 1, UID: "p1"}
	if got := own.HistoryUser(); got.ID != 1 || got.UID != "p1" {
		t.Fatalf("own %+v", got)
	}
	// a history without a uid is not one an addon can key by
	bad := User{ID: 1, UID: "p1", History: &History{ID: 9}}
	if got := bad.HistoryUser(); got.ID != 1 || got.History != nil {
		t.Fatalf("broken history %+v", got)
	}
}

func TestHistoryProfiles(t *testing.T) {
	us := []User{
		{ID: 1, UID: "p1", Name: "Папа", MaxRating: "16+", Histories: []History{{ID: 9, UID: "h9", Name: "Гостиная"}, {ID: 10, UID: "h10", Name: "Кухня"}}},
		{ID: 2, UID: "p2", Name: "Мама"},
	}
	got := HistoryProfiles(us)
	if len(got) != 4 || got[0].ID != 1 || got[1].ID != 9 || got[1].UID != "h9" || got[1].Name != "Гостиная" ||
		got[1].MaxRating != "16+" || got[2].ID != 10 || got[3].ID != 2 || got[1].Histories != nil {
		t.Fatalf("%+v", got)
	}
}
