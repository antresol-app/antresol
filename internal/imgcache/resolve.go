// Package imgcache maps stored artwork references to files and makes
// resized copies of them. The web API (/api/images), Subsonic and the
// Jellyfin API share it, so they serve the same files by the same rules.
package imgcache

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
)

// ErrNoImage: the reference names no servable image file (404).
var ErrNoImage = errors.New("imgcache: no image")

// localImageExt are the extensions of artwork found next to the media.
var localImageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}

// Resolve maps an artwork reference (items.poster/backdrop, meta logo) to
// a file: a cache name (library.ImageCacheName) is imageDir/<name>; a
// "file:<abs>" value with an image extension is that path. Anything else,
// http URLs included, is ErrNoImage: nothing is fetched from the network.
// The file itself is not checked (the server answers 404 when it is gone).
func Resolve(imageDir, ref string) (string, error) {
	if p, ok := strings.CutPrefix(ref, "file:"); ok {
		if filepath.IsAbs(p) && localImageExt[strings.ToLower(filepath.Ext(p))] {
			return p, nil
		}
		return "", ErrNoImage
	}
	if imageDir != "" && library.ImageCacheName.MatchString(ref) {
		return filepath.Join(imageDir, ref), nil
	}
	return "", ErrNoImage
}
