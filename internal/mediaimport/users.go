package mediaimport

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/i18n"
)

// UserProposal pairs a user of an old service with the Vault profile of
// the same name (VaultUserID nil: none, the admin picks one or «Не
// переносить»). Rows counts the user's positions and favourites.
type UserProposal struct {
	Source      string `json:"source"`
	SourceID    string `json:"sourceId"`
	SourceName  string `json:"sourceName"`
	VaultUserID *int64 `json:"vaultUserId"`
	Rows        int    `json:"rows"`
	// Error is set on a row that stands for a whole source whose database
	// could not be read (SourceName is the service; no user, Rows 0).
	Error string `json:"error,omitempty"`
}

// ProposeUsers lists, for each configured source, every user with
// progress or favourites to carry over, matched to a Vault profile by name
// (case-insensitive, Unicode). A missing database adds nobody; an
// unreadable one adds a row with Error, so the mapping screen can say so.
//
// A proposal may name a kid profile: the admin confirms it, and the
// favourites go to its «Хочу посмотреть» unchecked — the lists show only
// what the profile may see (Repo.Visible), so nothing hidden appears.
func ProposeUsers(ctx context.Context, o ProgressOptions, users *auth.Store) ([]UserProposal, error) {
	type key struct{ source, id string }
	found := map[key]*UserProposal{}
	add := func(source, id, name string) {
		k := key{source, id}
		if p := found[k]; p != nil {
			p.Rows++
			return
		}
		found[k] = &UserProposal{Source: source, SourceID: id, SourceName: name, Rows: 1}
	}
	var unreadable []UserProposal
	read := func(app, path string, fn func(*srcDB) error) error {
		source := map[string]string{"Jellyfin": SourceJellyfin, "Kavita": SourceKavita, "Audiobookshelf": SourceABS}[app]
		failed := UserProposal{Source: source, SourceName: app, Error: i18n.T(ctx, "Не удалось прочитать базу %s", app)}
		d, err := snapshotDB(o.Tmp, path)
		if errors.Is(err, errNoSourceDB) {
			return nil
		}
		if err != nil {
			impLog().Warn("snapshot", "app", app, "file", path, "err", err)
			unreadable = append(unreadable, failed)
			return nil
		}
		defer d.close()
		if err := fn(d); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			impLog().Warn("read", "app", app, "err", err)
			unreadable = append(unreadable, failed)
		}
		return nil
	}
	err := read("Jellyfin", o.JellyfinDB, func(d *srcDB) error {
		data, err := readJellyfin(ctx, d.DB)
		if err != nil {
			return err
		}
		for _, r := range append(data.progress, data.favorites...) {
			add(SourceJellyfin, r.userID, r.userName)
		}
		return nil
	})
	if err == nil {
		err = read("Kavita", o.KavitaDB, func(d *srcDB) error {
			list, err := readKavita(ctx, d.DB)
			for _, r := range list {
				add(SourceKavita, strconv.FormatInt(r.userID, 10), r.userName)
			}
			return err
		})
	}
	if err == nil {
		err = read("Audiobookshelf", o.ABSDB, func(d *srcDB) error {
			list, err := readABS(ctx, d.DB)
			for _, r := range list {
				add(SourceABS, r.userID, r.userName)
			}
			return err
		})
	}
	if err != nil {
		return nil, err
	}
	order := map[string]int{SourceJellyfin: 0, SourceKavita: 1, SourceABS: 2}
	out := make([]UserProposal, 0, len(found)+len(unreadable))
	out = append(out, unreadable...)
	for _, p := range found {
		u, err := users.ByName(ctx, p.SourceName)
		switch {
		case err == nil:
			id := u.ID
			p.VaultUserID = &id
		case !errors.Is(err, auth.ErrNotFound):
			return nil, err
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Source != b.Source {
			return order[a.Source] < order[b.Source]
		}
		if (a.Error != "") != (b.Error != "") {
			return a.Error != ""
		}
		if an, bn := strings.ToLower(a.SourceName), strings.ToLower(b.SourceName); an != bn {
			return an < bn
		}
		return a.SourceID < b.SourceID
	})
	return out, nil
}
