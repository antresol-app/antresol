package addons

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/playlistimport"
)

// The import.* hooks behind the playlist import's own call sites
// (playlistimport.Service's Connectors, Reads, Resolver and Catalog). All
// are called for the household (no X-Antresol-User). The services' sign-ins
// and their tokens stay with the addon: no hook carries one.

// ImportServices is the sign-in tiles of GET /api/import/services over
// import.services (2 s): every addon's, in addon-id order, the first addon
// to list a service owning it. A service the server does not know is
// dropped. An addon that is down, slow or answers something else adds none:
// with none at all the page shows the file import.
func (h *Hooks) ImportServices() playlistimport.ConnectorStatus { return importServices{h} }

type importServices struct{ h *Hooks }

func (s importServices) Services(ctx context.Context) []playlistimport.ServiceInfo {
	out, _, _ := s.h.importServices(ctx)
	return out
}

// importServices are the merged tiles (never nil), each service's addon,
// and the addons that did not answer for lack of time (a timeout now, or
// a down period after one). Each addon's answer (a 200, 204 or 404) is
// kept as its last known list of services.
func (h *Hooks) importServices(ctx context.Context) ([]playlistimport.ServiceInfo, map[string]string, []string) {
	out := []playlistimport.ServiceInfo{}
	owners := map[string]string{}
	var slow []string
	res, _, _ := h.fanout(ctx, "import.services", nil, nil, nil)
	for _, r := range res {
		switch {
		case errors.Is(r.err, errCooling):
			slow = append(slow, r.id)
			continue
		case errors.Is(r.err, ErrHookTimeout):
			slow = append(slow, r.id)
			h.log().Warn("addon hook: left out", "addon", r.id, "hook", "import.services", "err", r.err)
			continue
		case r.err != nil:
			h.log().Warn("addon hook: left out", "addon", r.id, "hook", "import.services", "err", r.err)
			continue
		}
		var a addonkit.ImportServicesAnswer
		if r.raw != nil {
			if err := json.Unmarshal(r.raw, &a); err != nil {
				h.log().Warn("addon hook: left out", "addon", r.id, "hook", "import.services", "err", err)
				continue
			}
		}
		var listed []string
		for _, x := range a.Services {
			if !slices.Contains(playlistimport.Services, x.ID) {
				continue
			}
			listed = append(listed, x.ID)
			if owners[x.ID] != "" {
				continue
			}
			owners[x.ID] = r.id
			out = append(out, playlistimport.ServiceInfo{ID: x.ID, Configured: x.Configured, Connect: x.Connect,
				Setup: nonNil(x.Setup), Files: nonNil(x.Files)})
		}
		h.ownersMu.Lock()
		if h.lastSvcs == nil {
			h.lastSvcs = map[string][]string{}
		}
		h.lastSvcs[r.id] = listed
		h.ownersMu.Unlock()
	}
	return out, owners, slow
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ServiceOwner is the addon whose import.services lists service now (asked
// within 2 s): where a sign-in's callback is sent on to. When no answer
// lists it but an addon that listed it last time did not answer in time
// (a timeout, or that hook's down period after one), that addon (the first
// in id order) is still the owner: a sign-in in progress is not lost to a
// slow tile list. False when none does: an addon that answered without the
// service no longer owns it, and a stopped or failing addon owns nothing.
func (h *Hooks) ServiceOwner(service string) (addonID string, ok bool) {
	if !slices.Contains(playlistimport.Services, service) {
		return "", false
	}
	_, owners, slow := h.importServices(context.Background())
	if addonID, ok = owners[service]; ok {
		return addonID, true
	}
	h.ownersMu.Lock()
	defer h.ownersMu.Unlock()
	for _, id := range slow { // in addon-id order
		if slices.Contains(h.lastSvcs[id], service) {
			return id, true
		}
	}
	return "", false
}

// ImportStopper stops the addons' reads of a job cancelled or deleted:
// import.stop {jobId} (5 s) on every addon that declares it, fired in the
// background (the cancel never waits for an addon). A down addon misses
// it; its read then fails on its own when the job is gone.
func (h *Hooks) ImportStopper() playlistimport.Stopper { return importStopper{h} }

type importStopper struct{ h *Hooks }

func (s importStopper) Stop(jobID int64) {
	go s.h.Each(context.Background(), "import.stop", nil, addonkit.ImportStopRequest{JobID: jobID}, func(string, json.RawMessage) {})
}

// importResolveBatch is how many ids one import.resolve call asks for: an
// addon that names a video in a few hundred milliseconds answers a batch
// well within the hook's 30 s.
var importResolveBatch = 25

// ImportResolver names the videos an uploaded Takeout gives only by id,
// over import.resolve (30 s per call, the ids in batches): for each id the
// first addon (in id order) to name it, with a title. An id no addon names
// (down, slow, failing, or unknown to it) stays unknown, and that is no
// error; only the caller's context ending is.
func (h *Hooks) ImportResolver() playlistimport.IDResolver { return importResolver{h} }

type importResolver struct{ h *Hooks }

func (r importResolver) Resolve(ctx context.Context, ids []string) (map[string]playlistimport.SnapTrack, error) {
	out := map[string]playlistimport.SnapTrack{}
	for chunk := range slices.Chunk(ids, max(importResolveBatch, 1)) {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		asked := map[string]bool{}
		for _, id := range chunk {
			asked[id] = true
		}
		r.h.Each(ctx, "import.resolve", nil, addonkit.ImportResolveRequest{IDs: chunk}, func(addon string, raw json.RawMessage) {
			var a addonkit.ImportResolveAnswer
			if err := json.Unmarshal(raw, &a); err != nil {
				r.h.log().Warn("addon hook: left out", "addon", addon, "hook", "import.resolve", "err", err)
				return
			}
			for _, t := range a.Tracks {
				if _, named := out[t.Key]; !asked[t.Key] || named || strings.TrimSpace(t.Title) == "" {
					continue
				}
				out[t.Key] = playlistimport.SnapTrack{Key: t.Key, Artist: t.Artist, Title: t.Title, Album: t.Album,
					AlbumArtist: t.AlbumArtist, ISRC: t.ISRC, DurationMs: t.DurationMs}
			}
		})
	}
	return out, ctx.Err()
}

// ImportCatalog is the review's album search («Найти вручную», a song's
// candidates) over import.search {kind, q} (30 s): every addon's
// candidates, in addon-id order. An addon that is down, slow or fails adds
// none (no error). With no answer at all, an addon's refusal (409) comes
// back as the *HookRefusal (the API maps playlistimport.CatalogOffline's
// text to 503); with no enabled addon declaring the hook it is
// playlistimport.ErrCatalogOffline, as without a catalog.
func (h *Hooks) ImportCatalog() playlistimport.Catalog { return importCatalog{h} }

type importCatalog struct{ h *Hooks }

func (c importCatalog) DiscoverMedia(ctx context.Context, kind, q string) ([]playlistimport.Candidate, error) {
	res, declared, _ := c.h.fanout(ctx, "import.search", nil, nil, addonkit.ImportSearchRequest{Kind: kind, Q: q})
	if declared == 0 {
		return nil, playlistimport.ErrCatalogOffline
	}
	out := []playlistimport.Candidate{}
	var refused error
	answered := false
	for _, r := range res {
		var ref *HookRefusal
		switch {
		case errors.As(r.err, &ref):
			if refused == nil {
				refused = ref
			}
			continue
		case errors.Is(r.err, errCooling):
			continue
		case r.err != nil:
			c.h.log().Warn("addon hook: left out", "addon", r.id, "hook", "import.search", "err", r.err)
			continue
		case r.raw == nil:
			answered = true // nothing to add
			continue
		}
		var a addonkit.ImportSearchAnswer
		if err := json.Unmarshal(r.raw, &a); err != nil {
			c.h.log().Warn("addon hook: left out", "addon", r.id, "hook", "import.search", "err", err)
			continue
		}
		answered = true
		for _, x := range a.Candidates {
			out = append(out, playlistimport.Candidate{TMDBID: x.TMDBID, ExtID: x.ExtID, Kind: x.Kind, Title: x.Title,
				Subtitle: x.Subtitle, Year: x.Year, Poster: x.Poster, Overview: x.Overview, InLibrary: x.InLibrary,
				LibraryItemID: x.LibraryItemID, Aliases: x.Aliases, MatchedTrack: x.MatchedTrack, ByArtist: x.ByArtist})
		}
	}
	if !answered && refused != nil {
		return nil, refused
	}
	return out, nil
}
