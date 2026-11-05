package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
)

// shuffle: GET /api/music/shuffle?source=all|genre|artist&genre=&artist=
// → up to music.ShuffleLimit distinct tracks of the source in random order.
func (h *lib) shuffle(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	q := r.URL.Query()
	src := music.ShuffleSource{Kind: q.Get("source")}
	switch src.Kind {
	case "all":
	case "genre": // an unknown or blank genre has no tracks → 404 below
		src.Genre = q.Get("genre")
	case "artist":
		id, err := strconv.ParseInt(q.Get("artist"), 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, r, http.StatusBadRequest, "Неверный id")
			return
		}
		it, ok := h.item(w, r, id) // 404 for unknown and hidden
		if !ok {
			return
		}
		if it.Kind != library.KindArtist {
			writeErr(w, r, http.StatusNotFound, "Не найдено")
			return
		}
		src.ArtistID, src.ArtistName = it.ID, it.Title
	default:
		writeErr(w, r, http.StatusBadRequest, "Неизвестный источник")
		return
	}
	ids, err := h.Music.ShuffleTrackIDs(ctx, src, ratingLimit(u), music.ShuffleLimit)
	if err != nil {
		internalErr(w, r, "shuffle", err)
		return
	}
	tracks, err := h.Library.Tracks(ctx, ids)
	if err != nil {
		internalErr(w, r, "shuffle tracks", err)
		return
	}
	dtos, err := h.trackDTOs(ctx, u, tracks)
	if err != nil {
		internalErr(w, r, "shuffle tracks", err)
		return
	}
	if len(dtos) == 0 {
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tracks": dtos})
}

type GenreDTO struct {
	Name   string `json:"name"`
	Albums int    `json:"albums"`
	Songs  int    `json:"songs"`
}

// genres: GET /api/music/genres → the genres of the visible albums, most
// songs first, then by name.
func (h *lib) genres(w http.ResponseWriter, r *http.Request) {
	gs, err := h.Music.Genres(r.Context(), ratingLimit(userOf(r)))
	if err != nil {
		internalErr(w, r, "genres", err)
		return
	}
	out := make([]GenreDTO, 0, len(gs))
	for _, g := range gs {
		out = append(out, GenreDTO{Name: g.Name, Albums: g.Albums, Songs: g.Songs})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Songs != out[j].Songs {
			return out[i].Songs > out[j].Songs
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"genres": out})
}

// starredSongs: GET /api/music/songs?starred=1 → the user's starred tracks,
// newest star first; paged after the visibility filter.
func (h *lib) starredSongs(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userOf(r)
	limit, offset := pageParams(r, 100)
	ids, err := h.Music.Starred(ctx, u.ID)
	if err != nil {
		internalErr(w, r, "starred songs", err)
		return
	}
	tracks, err := h.Library.Tracks(ctx, ids)
	if err != nil {
		internalErr(w, r, "starred songs", err)
		return
	}
	dtos, err := h.trackDTOs(ctx, u, tracks)
	if err != nil {
		internalErr(w, r, "starred songs", err)
		return
	}
	total := len(dtos)
	start := min(offset, total)
	page := dtos[start : start+min(limit, total-start)] // no offset+limit overflow
	writeJSON(w, http.StatusOK, map[string]any{"items": page, "total": total})
}

// trackDetail: GET /api/music/tracks/{id} → the TrackDTO of a visible track.
func (h *lib) trackDetail(w http.ResponseWriter, r *http.Request) {
	it, ok := h.kindParam(w, r, "id", library.KindTrack)
	if !ok {
		return
	}
	ctx := r.Context()
	tracks, err := h.Library.Tracks(ctx, []int64{it.ID})
	if err != nil {
		internalErr(w, r, "track", err)
		return
	}
	dtos, err := h.trackDTOs(ctx, userOf(r), tracks)
	if err != nil {
		internalErr(w, r, "track", err)
		return
	}
	if len(dtos) == 0 { // its album is missing or hidden
		writeErr(w, r, http.StatusNotFound, "Не найдено")
		return
	}
	writeJSON(w, http.StatusOK, dtos[0])
}
