package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata"
)

// sizeProfile is the size of cast photos.
const sizeProfile = "w185"

// ProfileFile matches the file name of a TMDB profile path ("/abc.jpg"
// without the slash): what /api/images/cast/{name} accepts.
var ProfileFile = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}\.(jpg|jpeg|png|webp)$`)

// ArtFile matches the file name of a TMDB poster or logo path (logos may
// be SVG): what /api/images/art/{name} accepts.
var ArtFile = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}\.(jpg|jpeg|png|webp|svg)$`)

// ArtSize is the size of posters and logos: the enricher's, so a file it
// kept serves a viewer too.
const ArtSize = sizePoster

// Photos keeps the cast's photos in the image cache: downloaded when a
// title is enriched (Enricher), else on the first view (Fetch), then
// served from the disk with no network. With Size ArtSize it keeps posters
// and logos the same way (Translation.Poster and Logo: the art of a title
// in a language the library does not store).
type Photos struct {
	Client *Client // its ImageBase; nil: DefaultImageBase
	Images *metadata.Images
	Size   string // "": the cast photos' size

	mu       sync.Mutex
	missing  map[string]time.Time // path → when TMDB answered 404
	inflight map[string]*photoFetch
}

// photoFetch is a download other Fetches of the same path wait for.
type photoFetch struct {
	done chan struct{}
	name string
	err  error
}

const (
	// photoMissRetry: a path TMDB had no image for is not asked again for
	// this long.
	photoMissRetry = time.Hour
	// photoMissMem bounds the paths remembered as missing.
	photoMissMem = 4096
	// maxPhotoBytes: a bigger body is refused (TMDB's are far smaller).
	maxPhotoBytes = 10 << 20
)

func (p *Photos) url(profile string) string {
	size := p.Size
	if size == "" {
		size = sizeProfile
	}
	if p.Client == nil {
		return DefaultImageBase + "/" + size + profile
	}
	return p.Client.ImageURL(size, profile)
}

// Cached returns the cache file name of a profile's photo when it is on
// the disk.
func (p *Photos) Cached(profile string) (string, bool) {
	if p == nil || p.Images == nil || !strings.HasPrefix(profile, "/") {
		return "", false
	}
	return p.Images.Cached(p.url(profile))
}

// Fetch downloads a profile's photo into the cache (unless it is there)
// and returns its cache file name. Concurrent Fetches of a path download
// it once; a path TMDB answered 404 for is not asked again for an hour.
func (p *Photos) Fetch(ctx context.Context, profile string) (string, error) {
	if p == nil || p.Images == nil || !strings.HasPrefix(profile, "/") {
		return "", errors.New("tmdb: no cast photo")
	}
	if name, ok := p.Cached(profile); ok {
		return name, nil
	}
	p.mu.Lock()
	if at, ok := p.missing[profile]; ok && time.Since(at) < photoMissRetry {
		p.mu.Unlock()
		return "", &metadata.BadImageError{Msg: "tmdb: no image " + profile + " (lately)", Status: 404}
	}
	if f := p.inflight[profile]; f != nil {
		p.mu.Unlock()
		select {
		case <-f.done:
			return f.name, f.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f := &photoFetch{done: make(chan struct{})}
	if p.inflight == nil {
		p.inflight = map[string]*photoFetch{}
	}
	p.inflight[profile] = f
	p.mu.Unlock()

	f.name, f.err = p.Images.FetchMax(ctx, p.url(profile), maxPhotoBytes)
	var bad *metadata.BadImageError
	p.mu.Lock()
	delete(p.inflight, profile)
	if errors.As(f.err, &bad) && bad.Status == 404 {
		if p.missing == nil || len(p.missing) >= photoMissMem {
			p.missing = map[string]time.Time{}
		}
		p.missing[profile] = time.Now()
	}
	p.mu.Unlock()
	close(f.done)
	return f.name, f.err
}

// castProfiles are the profile paths of meta.cast (kept as []Person, or
// decoded JSON).
func castProfiles(meta map[string]any) []string {
	v, ok := meta["cast"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var cast []Person
	if json.Unmarshal(b, &cast) != nil {
		return nil
	}
	var out []string
	for _, c := range cast {
		if strings.HasPrefix(c.Profile, "/") {
			out = append(out, c.Profile)
		}
	}
	return out
}

// photosPending: the cast has photos not yet downloaded (or some failed
// a while ago, fewer than maxImageTries times).
func photosPending(meta map[string]any) bool {
	if len(castProfiles(meta)) == 0 {
		return false
	}
	at := metaTime(meta, "cast_photos_at")
	if at.IsZero() {
		return true
	}
	if meta["cast_photos_incomplete"] == true {
		tries, _ := meta["cast_photos_tries"].(float64)
		return tries < maxImageTries && time.Since(at) > imageRetry
	}
	return false
}

// cachePhotos downloads the cast photos of an enriched title (titles
// enriched before photos were kept get them on the next run too). The
// image host being down stops the run with nothing marked.
func (e *Enricher) cachePhotos(ctx context.Context, it library.Item, meta map[string]any) error {
	p := &Photos{Client: e.Client, Images: e.Images}
	failed := false
	for _, prof := range castProfiles(meta) {
		if _, err := p.Fetch(ctx, prof); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch e.classifyImage(ctx, err) {
			case imgItemLocal:
				failed = true
				e.log().Warn("tmdb: cast photo failed", "item", it.ID, "err", err)
			case imgLocalErr:
				return fmt.Errorf("%w: %v", errLocal, err)
			default:
				return fmt.Errorf("%w: image host: %v", errOffline, err)
			}
		}
	}
	err := e.Repo.UpdateMetadata(ctx, it.ID, func(_ library.Item, m map[string]any) library.Metadata {
		m["cast_photos_at"] = time.Now().UTC().Format(time.RFC3339)
		if failed {
			tries, _ := m["cast_photos_tries"].(float64)
			m["cast_photos_incomplete"], m["cast_photos_tries"] = true, tries+1
		} else {
			delete(m, "cast_photos_incomplete")
			delete(m, "cast_photos_tries")
		}
		return library.Metadata{}
	})
	if errors.Is(err, library.ErrNotFound) {
		return nil
	}
	return err
}
