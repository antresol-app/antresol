package jellyfin

import (
	"context"
	"net/http"

	"github.com/antresol-app/antresol/internal/i18n"
)

// viewNames are the library views Vault shows (Russian catalog keys) (Swiftfin hides
// collection types it does not know, such as music and books).
var viewNames = map[string]string{"movies": "Фильмы", "tvshows": "Сериалы"}

// viewDto is a library view ("movies", "tvshows") as an item.
// viewDto is in ctx's language (the client's Accept-Language).
func (s *Server) viewDto(ctx context.Context, name string) BaseItemDto {
	return BaseItemDto{Name: i18n.T(ctx, viewNames[name]), Id: ViewID(name), ServerId: s.sid(), Type: "CollectionFolder",
		CollectionType: name, IsFolder: true, ImageTags: map[string]string{}}
}

// views serves /UserViews, /Users/{id}/Views and /Library/MediaFolders.
func (s *Server) views(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, QueryResult[BaseItemDto]{Items: []BaseItemDto{s.viewDto(r.Context(), "movies"), s.viewDto(r.Context(), "tvshows")}, TotalRecordCount: 2})
}
