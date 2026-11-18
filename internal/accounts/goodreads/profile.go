package goodreads

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	reBareID = regexp.MustCompile(`^(\d{1,12})(?:-([A-Za-z0-9_.-]+))?$`)
	// the pages of a profile that carry its id: the profile, a shelf (the
	// old RSS address too), the updates
	reProfilePath = regexp.MustCompile(`^/(?:user/show|review/list|review/list_rss|user_status/list|user/updates_rss|updates/list)/(\d{1,12})(?:-([A-Za-z0-9_.-]+))?/?$`)
)

// ParseProfile reads what a profile pastes to say which Goodreads account is
// hers: the profile address (…/user/show/12345-name), any shelf address, or
// the bare id with or without its name (12345-name). name is the name part
// of the address (the id again when it has none).
func ParseProfile(s string) (id int64, name string, ok bool) {
	s = strings.TrimSpace(s)
	var digits, slug string
	if m := reBareID.FindStringSubmatch(s); m != nil {
		digits, slug = m[1], m[2]
	} else {
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		u, err := url.Parse(s)
		if err != nil || !(u.Host == "goodreads.com" || strings.HasSuffix(u.Host, ".goodreads.com")) {
			return 0, "", false
		}
		m := reProfilePath.FindStringSubmatch(u.Path)
		if m == nil {
			return 0, "", false
		}
		digits, slug = m[1], m[2]
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	if slug == "" {
		slug = digits
	}
	return id, slug, true
}
