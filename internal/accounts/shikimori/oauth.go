package shikimori

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/antresol-app/antresol/internal/accounts"
)

// AuthorizeURL is where the user is sent to grant the user_rates scope.
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{
		"client_id":     {c.ClientID},
		"redirect_uri":  {c.RedirectURI},
		"response_type": {"code"},
		"scope":         {"user_rates"},
		"state":         {state},
	}
	return c.base() + "/oauth/authorize?" + q.Encode()
}

// Exchange trades an authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, code string) (accounts.Tokens, error) {
	return c.token(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.RedirectURI},
	})
}

// Refresh trades a refresh token for new tokens. Shikimori rotates the
// refresh token on every call: the caller must store the returned one.
func (c *Client) Refresh(ctx context.Context, refresh string) (accounts.Tokens, error) {
	return c.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
}

func (c *Client) token(ctx context.Context, form url.Values) (accounts.Tokens, error) {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	var out struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int64  `json:"expires_in"`
		CreatedAt int64  `json:"created_at"`
	}
	err := c.do(ctx, http.MethodPost, "/oauth/token", nil, "application/x-www-form-urlencoded", []byte(form.Encode()), "", &out)
	if err != nil {
		return accounts.Tokens{}, err
	}
	if out.Access == "" {
		return accounts.Tokens{}, errors.New("shikimori: token response without access_token")
	}
	t := accounts.Tokens{Access: out.Access, Refresh: out.Refresh}
	base := time.Now()
	if out.CreatedAt > 0 {
		base = time.Unix(out.CreatedAt, 0)
	}
	if out.ExpiresIn > 0 {
		t.Expires = base.Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return t, nil
}
