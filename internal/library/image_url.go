package library

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ImageCacheName is a file of the image cache (DataDir/images: TMDB and
// other downloaded art, covers the scanners extracted).
var ImageCacheName = regexp.MustCompile(`^[0-9a-f]{40}\.(jpg|png|webp|gif|svg)$`)

// ImageURL maps a stored artwork reference of an item to the URL the
// browser may load. Everything is served by /api/images: local files only
// through an opaque per-item id (so a "file:" value needs the id of the
// item that holds it), never by path; anything else is dropped.
func ImageURL(itemID int64, slot, v string) string {
	switch {
	case v == "":
		return ""
	case strings.HasPrefix(v, "file:"):
		return "/api/images/local/" + strconv.FormatInt(itemID, 10) + "/" + slot
	case ImageCacheName.MatchString(v):
		return "/api/images/" + url.PathEscape(v)
	}
	return ""
}
