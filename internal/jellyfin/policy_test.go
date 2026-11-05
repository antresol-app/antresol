package jellyfin

import (
	"net/http/httptest"
	"testing"
)

// jellyfin-sdk-swift decodes UserPolicy.AuthenticationProviderId and
// PasswordResetProviderId as required strings: a login without them fails
// in Swiftfin before the token is stored.
func TestUserPolicyHasRequiredProviderIDs(t *testing.T) {
	e := newEnv(t)
	login := e.do("POST", "/Users/AuthenticateByName", swiftfinHdr(""), map[string]string{"Username": "Аня", "Pw": e.anyaApp})
	var res struct{ AccessToken string }
	decode(t, login, &res)
	for _, w := range []*httptest.ResponseRecorder{login, e.do("GET", "/Users/Me", swiftfinHdr(res.AccessToken), nil)} {
		var v struct {
			User   *struct{ Policy map[string]any }
			Policy map[string]any
		}
		decode(t, w, &v)
		pol := v.Policy
		if v.User != nil {
			pol = v.User.Policy
		}
		for _, k := range []string{"AuthenticationProviderId", "PasswordResetProviderId"} {
			if s, _ := pol[k].(string); s == "" {
				t.Fatalf("%s missing in %s", k, w.Body)
			}
		}
	}
}
