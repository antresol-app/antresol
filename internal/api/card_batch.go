package api

import (
	"context"
	"log"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

// cardBatch is what card reads item by item, fetched for a page of cards
// at once: the profile's progress, the items' files (the badges' probes)
// and the parents of episodes, seasons, albums and comic volumes (their
// series, artist). A grid of 120 cards took two or three point queries a
// card; with the batch it takes a handful in all. An id the batch did not
// fetch is looked up as before.
type cardBatch struct {
	uid      int64
	progress map[int64]progress.Entry // of uid; an asked id without one has none
	askedP   map[int64]bool
	files    map[int64][]library.File
	askedF   map[int64]bool
	items    map[int64]library.Item // parents; an asked id missing is not shown
	askedI   map[int64]bool
	showKeys map[int64]string // series and seasons: Library.ShowFilesKey
}

type cardBatchKey struct{}

func batchOf(ctx context.Context) *cardBatch {
	b, _ := ctx.Value(cardBatchKey{}).(*cardBatch)
	return b
}

// withCards returns ctx with the batch of items' cards for the profile
// uid (and items' parents up to their root). A failed prefetch leaves the
// lookups to card, one by one.
func (h *lib) withCards(ctx context.Context, uid int64, items []library.Item) context.Context {
	if len(items) == 0 || h.Library == nil {
		return ctx
	}
	b := &cardBatch{uid: uid, progress: map[int64]progress.Entry{}, askedP: map[int64]bool{},
		askedF: map[int64]bool{}, items: map[int64]library.Item{}, askedI: map[int64]bool{}}
	if old := batchOf(ctx); old != nil && old.uid == uid {
		b = old // a second page of cards adds to the first
	}
	var withProgress, withFiles, parents, shows []int64
	for _, it := range items {
		if it.Kind == library.KindSeries || it.Kind == library.KindSeason {
			if _, ok := b.showKeys[it.ID]; !ok {
				shows = append(shows, it.ID)
			}
		}
		b.items[it.ID], b.askedI[it.ID] = it, true
		switch it.Kind {
		case library.KindMovie, library.KindEpisode, library.KindBook, library.KindAudiobook, library.KindComicVolume:
			if !b.askedP[it.ID] {
				withProgress = append(withProgress, it.ID)
			}
		}
		switch it.Kind {
		case library.KindMovie, library.KindEpisode, library.KindBook, library.KindAudiobook:
			if !b.askedF[it.ID] {
				withFiles = append(withFiles, it.ID)
			}
		}
		if it.ParentID != 0 && !b.askedI[it.ParentID] {
			parents = append(parents, it.ParentID)
		}
	}
	if len(withProgress) > 0 && h.Progress != nil {
		es, err := h.Progress.Of(ctx, []int64{uid}, withProgress)
		if err != nil {
			log.Printf("api: cards: progress: %v", err)
		} else {
			for _, id := range withProgress {
				b.askedP[id] = true
			}
			for _, e := range es {
				b.progress[e.ItemID] = e
			}
		}
	}
	if len(withFiles) > 0 {
		fs, err := h.Library.FilesOfItems(ctx, withFiles)
		if err != nil {
			log.Printf("api: cards: files: %v", err)
		} else {
			if b.files == nil {
				b.files = map[int64][]library.File{}
			}
			for _, id := range withFiles {
				b.askedF[id], b.files[id] = true, fs[id]
			}
		}
	}
	if len(shows) > 0 {
		keys, err := h.Library.ShowFilesKeys(ctx, shows)
		if err != nil {
			log.Printf("api: cards: show keys: %v", err)
		} else {
			if b.showKeys == nil {
				b.showKeys = map[int64]string{}
			}
			for _, id := range shows {
				b.showKeys[id] = keys[id]
			}
		}
	}
	// the parents, and theirs (an episode's season, then its series)
	for depth := 0; depth < 3 && len(parents) > 0; depth++ {
		got, err := h.Library.ItemsByID(ctx, parents)
		if err != nil {
			log.Printf("api: cards: parents: %v", err)
			break
		}
		var next []int64
		for _, id := range parents {
			b.askedI[id] = true
			if it, ok := got[id]; ok {
				b.items[id] = it
				if it.ParentID != 0 && !b.askedI[it.ParentID] {
					next = append(next, it.ParentID)
				}
			}
		}
		parents = next
	}
	return context.WithValue(ctx, cardBatchKey{}, b)
}

// withItems is withCards of the items ids (one Library.ItemsByID); an id
// it did not return then reads as not found, as Library.Item says.
func (h *lib) withItems(ctx context.Context, uid int64, ids []int64) context.Context {
	if len(ids) == 0 || h.Library == nil {
		return ctx
	}
	got, err := h.Library.ItemsByID(ctx, ids)
	if err != nil {
		log.Printf("api: cards: items: %v", err)
		return ctx
	}
	items := make([]library.Item, 0, len(got))
	for _, id := range ids {
		if it, ok := got[id]; ok {
			items = append(items, it)
		}
	}
	ctx = h.withCards(ctx, uid, items)
	if b := batchOf(ctx); b != nil {
		for _, id := range ids {
			b.askedI[id] = true
		}
	}
	return ctx
}

// itemByID is Library.Item, from the batch when it has the id.
func (h *lib) itemByID(ctx context.Context, id int64) (library.Item, error) {
	if b := batchOf(ctx); b != nil && b.askedI[id] {
		if it, ok := b.items[id]; ok {
			return it, nil
		}
		return library.Item{}, library.ErrNotFound
	}
	return h.Library.Item(ctx, id)
}

// filesOf is Library.FilesOf, from the batch when it has the item.
func (h *lib) filesOf(ctx context.Context, id int64) ([]library.File, error) {
	if b := batchOf(ctx); b != nil && b.askedF[id] {
		return b.files[id], nil
	}
	return h.Library.FilesOf(ctx, id)
}

// showFilesKey is Library.ShowFilesKey, from the batch when it has the
// series or season.
func (h *lib) showFilesKey(ctx context.Context, id int64) (string, error) {
	if b := batchOf(ctx); b != nil {
		if k, ok := b.showKeys[id]; ok {
			return k, nil
		}
	}
	return h.Library.ShowFilesKey(ctx, id)
}

// progressEntry is Progress.Get, from the batch when it has the item.
func (h *lib) progressEntry(ctx context.Context, uid, id int64) (progress.Entry, error) {
	if b := batchOf(ctx); b != nil && b.uid == uid && b.askedP[id] {
		if e, ok := b.progress[id]; ok {
			return e, nil
		}
		return progress.Entry{}, progress.ErrNotFound
	}
	return h.Progress.Get(ctx, uid, id)
}
