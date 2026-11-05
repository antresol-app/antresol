package jellyfin

import (
	"net/http"
	"net/url"
	"strings"
)

// ClientInfo is what a Jellyfin client says about itself. Any field may be
// empty: clients differ, and a missing Client must not break the login.
type ClientInfo struct {
	Client, Device, DeviceID, Version, Token string
}

// ParseAuth reads the MediaBrowser/Emby authorization header
// (Authorization or X-Emby-Authorization: `MediaBrowser k=v, k="v"`, in any
// order, quotes optional, values URL-encoded or not), then the token from
// X-Emby-Token, X-MediaBrowser-Token or the ApiKey/api_key query parameter
// (players cannot send headers), in that order.
func ParseAuth(r *http.Request) ClientInfo {
	var c ClientInfo
	for _, h := range []string{"X-Emby-Authorization", "Authorization"} {
		v := strings.TrimSpace(r.Header.Get(h))
		scheme, rest, _ := strings.Cut(v, " ")
		if !strings.EqualFold(scheme, "MediaBrowser") && !strings.EqualFold(scheme, "Emby") {
			continue
		}
		for _, kv := range splitPairs(rest) {
			k, val, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			val = strings.TrimSpace(val)
			if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
				val = val[1 : len(val)-1]
			}
			if d, err := url.PathUnescape(val); err == nil {
				val = d
			}
			var dst *string
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "client":
				dst = &c.Client
			case "device":
				dst = &c.Device
			case "deviceid":
				dst = &c.DeviceID
			case "version":
				dst = &c.Version
			case "token":
				dst = &c.Token
			}
			if dst != nil && val != "" {
				*dst = val
			}
		}
	}
	for _, h := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		if c.Token == "" {
			c.Token = strings.TrimSpace(r.Header.Get(h))
		}
	}
	if c.Token == "" {
		if c.Token = qv(r, "ApiKey"); c.Token == "" {
			c.Token = qv(r, "api_key")
		}
	}
	return c
}

// splitPairs splits on commas outside double quotes.
func splitPairs(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return append(out, b.String())
}
