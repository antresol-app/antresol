package iptv

import (
	"net/url"
	"strings"
)

// XtreamURLs are an Xtream Codes server's playlist and guide URLs.
// A server without a scheme is taken as http.
func XtreamURLs(server, user, pass string) (m3u, xmltv string) {
	s := strings.TrimRight(strings.TrimSpace(server), "/")
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	cred := "username=" + url.QueryEscape(user) + "&password=" + url.QueryEscape(pass)
	return s + "/get.php?" + cred + "&type=m3u_plus&output=ts", s + "/xmltv.php?" + cred
}
