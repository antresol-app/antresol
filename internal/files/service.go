package files

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strconv"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// ServiceConfig wires a Service. Library, Seeds, Changed and Probe are optional.
type ServiceConfig struct {
	Sandbox  *Sandbox
	DB       *sql.DB
	Library  *library.Repo
	Seeds    SeedInfo
	Torrents Torrents
	DataDir  string // thumbnails go to DataDir/thumbs
	FFmpeg   string
	FFprobe  string
	Changed  func(roots ...string)
	Probe    func(ctx context.Context, path string) (media.ProbeResult, error)
	// NoDiskUsage: the usage report leaves out the disk (see Usage.NoDisk).
	NoDiskUsage bool
	// LibraryRoots, Offline and Linked set up deleting library items
	// (Service.Items, see ItemDelete); without Library there is none.
	LibraryRoots []string
	Offline      func() string
	Linked       func(ctx context.Context, set library.DeleteSet) []Linked
	Hold         func(ctx context.Context) (release func(), err error)
}

// Service bundles the file browser parts used by the API.
type Service struct {
	Sandbox *Sandbox
	Browser *Browser
	Ops     *Ops
	Thumbs  *Thumbnailer
	Usage   *Usage
	Library *library.Repo // nil when not wired
	Items   *ItemDelete   // deletes library items; nil without Library
	FFprobe string
	Probe   func(ctx context.Context, path string) (media.ProbeResult, error)
}

// ThumbURL is the API URL of an entry's thumbnail.
func ThumbURL(root, rel string, v int64) string {
	return "/api/files/thumb?root=" + url.QueryEscape(root) + "&path=" + url.QueryEscape(rel) + "&v=" + strconv.FormatInt(v, 10)
}

func NewService(c ServiceConfig) *Service {
	b := &Browser{Sandbox: c.Sandbox, Seeds: c.Seeds, ThumbURL: ThumbURL}
	if c.Library != nil {
		b.Library = c.Library
	}
	ops := &Ops{Sandbox: c.Sandbox, DB: c.DB, Seeds: c.Seeds, Torrents: c.Torrents, Changed: c.Changed}
	var items *ItemDelete
	if c.Library != nil {
		items = &ItemDelete{Ops: ops, Library: c.Library, Roots: c.LibraryRoots, Offline: c.Offline, Linked: c.Linked, Hold: c.Hold}
	}
	return &Service{
		Sandbox: c.Sandbox,
		Browser: b,
		Ops:     ops,
		Items:   items,
		Thumbs:  &Thumbnailer{FFmpeg: c.FFmpeg, FFprobe: c.FFprobe, Dir: filepath.Join(c.DataDir, "thumbs"), Probe: c.Probe},
		Usage:   &Usage{Sandbox: c.Sandbox, NoDisk: c.NoDiskUsage},
		Library: c.Library,
		FFprobe: c.FFprobe,
		Probe:   c.Probe,
	}
}
