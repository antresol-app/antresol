package jellyfin

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestQueryHelpers(t *testing.T) {
	r := httptest.NewRequest("GET", "/Items?StartIndex=20&limit=x&EnableUserData=TRUE&IncludeItemTypes=Movie,,%20Series&isFolder=0", nil)
	if qv(r, "startindex") != "20" || qInt(r, "startIndex", 0) != 20 || qInt(r, "Limit", 50) != 50 || qInt(r, "none", 7) != 7 {
		t.Fatal("qv/qInt")
	}
	if !qBool(r, "enableUserData", false) || qBool(r, "IsFolder", true) || !qBool(r, "none", true) {
		t.Fatal("qBool")
	}
	if got := qList(r, "includeItemTypes"); !reflect.DeepEqual(got, []string{"Movie", "Series"}) {
		t.Fatalf("qList %q", got)
	}
	if qList(r, "none") != nil {
		t.Fatal("empty list")
	}
}
