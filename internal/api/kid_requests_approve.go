package api

import (
	"context"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/notify"
)

// approveFor approves the requests of what an admin started getting
// (pending ones, and declined ones: the admin changed their mind): by the
// title's key, or for an album or book by artist or author and title
// alone (a download without the catalog id, or a request without one).
// An addon calls it (POST /requests/approve) after an admin's download.
func (k *kidRequests) approveFor(ctx context.Context, adminID int64, req addonkit.ApproveRequest) ([]notify.Request, error) {
	key := requestKey(req.Kind, req.TMDBID, req.ExtID, req.Title, req.Artist)
	name := nameKey(req.Kind, req.Title, req.Artist)
	if name == "" {
		name = key // movies and series: the TMDB id only
	}
	where, args := `kind=? AND (title_key=? OR (name_key<>'' AND name_key=?))`, []any{req.Kind, key, name}
	return k.approveWhere(ctx, adminID, where, args...)
}
