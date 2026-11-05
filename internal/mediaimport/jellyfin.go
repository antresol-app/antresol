package mediaimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/sqlitesnap"
)

// Jellyfin BaseItems.Type values (full CLR names) the importer reads.
const (
	jfTypeMovie   = "MediaBrowser.Controller.Entities.Movies.Movie"
	jfTypeEpisode = "MediaBrowser.Controller.Entities.TV.Episode"
	jfTypeSeries  = "MediaBrowser.Controller.Entities.TV.Series"
)

// jfRow is one UserData row joined with its item and user.
type jfRow struct {
	itemID, userID, userName string
	typ, name, path          string
	seriesName, seriesID     string
	runTicks, posTicks       int64
	index, parentIndex       sql.NullInt64
	played, favorite         bool
	at                       time.Time
}

func (r jfRow) userKey() string { return SourceJellyfin + ":" + r.userID }

// title is the item's name for the report ("Series, S01E02: Name" for an
// episode).
func (r jfRow) title() string {
	if r.typ == jfTypeEpisode && r.seriesName != "" {
		return fmt.Sprintf("%s, S%02dE%02d: %s", r.seriesName, r.parentIndex.Int64, r.index.Int64, r.name)
	}
	return r.name
}

func (r jfRow) lineName() string { return r.userName + ": " + r.title() }

// jfData is what the importer reads from jellyfin.db.
type jfData struct {
	progress  []jfRow          // movies and episodes played or started, one per (item, user)
	favorites []jfRow          // favourite movies and series, one per (item, user)
	tmdb      map[string]int64 // BaseItems.Id → TMDB id
	channels  int              // Live TV channels
	// others counts the marks (played, started, favourite) on items Vault
	// has no place for — videos, music, a favourite season — by kind.
	others map[string]int
}

// jfOtherKinds names Jellyfin types in the warning about marks not
// carried over.
var jfOtherKinds = map[string]string{
	"Video": "видео", "Audio": "музыка", "MusicAlbum": "альбомы", "MusicArtist": "исполнители",
	"MusicVideo": "клипы", "Season": "сезоны", "Episode": "серии", "Series": "сериалы", "Movie": "фильмы",
	"Person": "люди", "BoxSet": "коллекции", "Folder": "папки", "CollectionFolder": "папки",
	"Book": "книги", "AudioBook": "аудиокниги", "Photo": "фото", "Trailer": "трейлеры",
}

func readJellyfin(ctx context.Context, d *sql.DB) (jfData, error) {
	var out jfData
	cols, err := sqlitesnap.Columns(ctx, d, "UserData")
	if err != nil {
		return out, err
	}
	retention := cols["retentiondate"]
	q := `SELECT u.ItemId, u.UserId, COALESCE(us.Username, ''), b.Type, COALESCE(b.Name, ''), COALESCE(b.Path, ''),
		COALESCE((SELECT s.Name FROM BaseItems s WHERE s.Id = b.SeriesId), ''), COALESCE(b.SeriesId, ''),
		COALESCE(b.RunTimeTicks, 0), COALESCE(u.PlaybackPositionTicks, 0), b.IndexNumber, b.ParentIndexNumber,
		COALESCE(u.Played, 0), COALESCE(u.IsFavorite, 0), COALESCE(u.LastPlayedDate, '')
		FROM UserData u JOIN BaseItems b ON u.ItemId = b.Id JOIN Users us ON us.Id = u.UserId
		WHERE b.Type IN (?, ?, ?)`
	if retention {
		q += ` AND u.RetentionDate IS NULL`
	}
	rows, err := d.QueryContext(ctx, q+` ORDER BY u.rowid`, jfTypeMovie, jfTypeEpisode, jfTypeSeries)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	prog := map[string]int{} // (item, user) → index in out.progress
	fav := map[string]bool{}
	for rows.Next() {
		var r jfRow
		var last string
		if err := rows.Scan(&r.itemID, &r.userID, &r.userName, &r.typ, &r.name, &r.path, &r.seriesName, &r.seriesID,
			&r.runTicks, &r.posTicks, &r.index, &r.parentIndex, &r.played, &r.favorite, &last); err != nil {
			return out, err
		}
		r.at, _ = parseSourceTime(last)
		key := r.itemID + "\x00" + r.userID
		// several rows per (item, user) differ by CustomDataKey: the most
		// recently played one wins
		if (r.typ == jfTypeMovie || r.typ == jfTypeEpisode) && (r.played || r.posTicks > 0) {
			if i, ok := prog[key]; !ok {
				prog[key] = len(out.progress)
				out.progress = append(out.progress, r)
			} else if r.at.After(out.progress[i].at) {
				out.progress[i] = r
			}
		}
		if (r.typ == jfTypeMovie || r.typ == jfTypeSeries) && r.favorite && !fav[key] {
			fav[key] = true
			out.favorites = append(out.favorites, r)
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows.Close()

	out.tmdb = map[string]int64{}
	prov, err := d.QueryContext(ctx, `SELECT ItemId, ProviderValue FROM BaseItemProviders WHERE lower(ProviderId) = 'tmdb'`)
	if err != nil {
		return out, err
	}
	defer prov.Close()
	for prov.Next() {
		var id, v string
		if err := prov.Scan(&id, &v); err != nil {
			return out, err
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
			out.tmdb[id] = n
		}
	}
	if err := prov.Err(); err != nil {
		return out, err
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM BaseItems WHERE Type LIKE '%LiveTvChannel'`).Scan(&out.channels); err != nil {
		return out, err
	}
	out.others, err = readJellyfinOthers(ctx, d, retention)
	return out, err
}

// readJellyfinOthers counts the UserData marks the import does not carry
// over (Live TV has a warning of its own).
func readJellyfinOthers(ctx context.Context, d *sql.DB, retention bool) (map[string]int, error) {
	q := `SELECT b.Type, COALESCE(u.Played, 0), COALESCE(u.PlaybackPositionTicks, 0), COALESCE(u.IsFavorite, 0)
		FROM UserData u JOIN BaseItems b ON u.ItemId = b.Id
		WHERE (u.Played = 1 OR u.PlaybackPositionTicks > 0 OR u.IsFavorite = 1) AND b.Type NOT LIKE '%LiveTvChannel'`
	if retention {
		q += ` AND u.RetentionDate IS NULL`
	}
	rows, err := d.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var typ string
		var played, fav bool
		var pos int64
		if err := rows.Scan(&typ, &played, &pos, &fav); err != nil {
			return nil, err
		}
		watched := played || pos > 0
		if watched && typ != jfTypeMovie && typ != jfTypeEpisode || fav && typ != jfTypeMovie && typ != jfTypeSeries {
			short := typ[strings.LastIndex(typ, ".")+1:]
			if ru, ok := jfOtherKinds[short]; ok {
				short = ru
			}
			out[short]++
		}
	}
	return out, rows.Err()
}

// othersWarning is "<app>: не переносятся N отметок (видео, музыка)", or ""
// when there are none.
func othersWarning(app string, others map[string]int) string {
	n := 0
	var kinds []string
	for k, c := range others {
		n += c
		kinds = append(kinds, k)
	}
	if n == 0 {
		return ""
	}
	sort.Strings(kinds)
	return fmt.Sprintf(othersKey, app, i18n.PluralL(i18n.RU, n, "отметка", "отметки", "отметок"), strings.Join(kinds, ", "))
}

// othersKey is the format of othersWarning (see localizeReason).
const othersKey = "%s: не переносятся %s (%s)"

// ImportJellyfin carries over the watch positions of movies and episodes
// and the favourite movies and series («Хочу посмотреть») of the mapped
// Jellyfin users, at Jellyfin's own times: nothing newer in Vault is
// overwritten and a second run changes nothing. Only internal errors and
// ctx's are returned; everything else is a line of rep.
func ImportJellyfin(ctx context.Context, o ProgressOptions, lib LibraryLookup, ps ProgressSink, rep *Report) error {
	d := openProgressDB(o, SourceJellyfin, "Jellyfin", o.JellyfinDB, rep)
	if d == nil {
		return nil
	}
	defer d.close()
	data, err := readJellyfin(ctx, d.DB)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		progressReadFailed(SourceJellyfin, "Jellyfin", rep, err)
		return nil
	}
	if data.channels > 0 {
		rep.Add(JellyfinChannelsLine(i18n.RU, data.channels))
	}
	if w := othersWarning("Jellyfin", data.others); w != "" {
		rep.Add(Line{Source: SourceJellyfin, Kind: "leftover", Name: "Другие отметки", Status: StatusWarning, Reason: w})
	}
	if len(data.progress) == 0 && len(data.favorites) == 0 {
		rep.Add(Line{Source: SourceJellyfin, Kind: "progress", Name: "Jellyfin", Status: StatusSkipped, Reason: reasonNoData})
		return nil
	}
	m := jfMatcher{lib: lib, paths: o.Paths, tmdb: data.tmdb}
	wl := watchlists{ps: ps, seen: map[int64]map[int64]bool{}}
	for _, r := range data.progress {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !mapped(o, rep, SourceJellyfin, r.userKey(), r.lineName()) {
			continue
		}
		it, _, ok, err := m.video(ctx, r)
		if err != nil {
			return ctxOr(ctx, err)
		}
		if !ok {
			rep.Add(Line{Source: SourceJellyfin, Kind: "progress", Name: r.lineName(), Status: StatusFailed, Reason: notFoundInVault(r.title(), r.path)})
			continue
		}
		dur, err := m.duration(ctx, it.ID, r.runTicks)
		if err != nil {
			return ctxOr(ctx, err)
		}
		pos := r.posTicks / 10_000
		if r.played {
			pos = dur
			if dur <= 0 { // finished, length unknown
				pos, dur = 1, 1
			}
		}
		rec := record{source: SourceJellyfin, userKey: r.userKey(), name: r.lineName(), itemID: it.ID, at: r.at,
			write: func(ctx context.Context, uid int64, at time.Time) error {
				return ps.SaveAt(ctx, uid, it.ID, pos, dur, at)
			}}
		if err := put(ctx, o, ps, rep, rec); err != nil {
			return ctxOr(ctx, err)
		}
	}
	for _, r := range data.favorites {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !mapped(o, rep, SourceJellyfin, r.userKey(), r.lineName()) {
			continue
		}
		var it library.Item
		var ok bool
		if r.typ == jfTypeSeries {
			it, ok, err = m.series(ctx, r)
		} else {
			it, _, ok, err = m.video(ctx, r)
		}
		if err != nil {
			return ctxOr(ctx, err)
		}
		l := Line{Source: SourceJellyfin, Kind: "progress", Name: r.lineName(), Reason: reasonToWatchlist}
		if !ok {
			l.Status, l.Reason = StatusFailed, notFoundInVault(r.title(), r.path)
			rep.Add(l)
			continue
		}
		// A favourite the profile removed from «Хочу посмотреть» after an
		// earlier run comes back on a re-run: the watchlist keeps no
		// removals (accepted by the plan).
		uid := o.UserMap[r.userKey()]
		l.Ref = itemRef(it.ID)
		in, err := wl.has(ctx, uid, it.ID)
		if err != nil {
			return ctxOr(ctx, err)
		}
		switch {
		case in:
			l.Status, l.Reason = StatusSkipped, reasonInWatchlist
		case o.DryRun:
			l.Status = StatusPlanned
			wl.add(uid, it.ID)
		default:
			if err := ps.AddToWatchlist(ctx, uid, it.ID); err != nil {
				return ctxOr(ctx, err)
			}
			l.Status = StatusImported
			wl.add(uid, it.ID)
		}
		rep.Add(l)
	}
	return nil
}

// watchlists caches the profiles' «Хочу посмотреть».
type watchlists struct {
	ps   ProgressSink
	seen map[int64]map[int64]bool
}

func (w watchlists) has(ctx context.Context, uid, item int64) (bool, error) {
	if w.seen[uid] == nil {
		ids, err := w.ps.Watchlist(ctx, uid)
		if err != nil {
			return false, err
		}
		w.seen[uid] = map[int64]bool{}
		for _, id := range ids {
			w.seen[uid][id] = true
		}
	}
	return w.seen[uid][item], nil
}

func (w watchlists) add(uid, item int64) { w.seen[uid][item] = true }

// duration is the length of a Vault movie or episode: the sum of its
// parts (cd1 + cd2: Vault and Jellyfin count a position across all of
// them) when every part's length is known, else Jellyfin's RunTimeTicks.
func (m jfMatcher) duration(ctx context.Context, itemID, runTicks int64) (int64, error) {
	files, err := m.lib.FilesOf(ctx, itemID)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, f := range files {
		if f.DurationMs <= 0 {
			return runTicks / 10_000, nil
		}
		sum += f.DurationMs
	}
	if sum == 0 {
		return runTicks / 10_000, nil
	}
	return sum, nil
}

// jfMatcher finds the Vault item of a Jellyfin item: by its path, else by
// TMDB id.
type jfMatcher struct {
	lib   LibraryLookup
	paths PathMap
	tmdb  map[string]int64
}

func notFound(err error) bool { return errors.Is(err, library.ErrNotFound) }

// video matches a movie or an episode; f is set when the match is by path.
func (m jfMatcher) video(ctx context.Context, r jfRow) (library.Item, library.File, bool, error) {
	if r.path != "" {
		it, f, err := m.lib.ItemByPath(ctx, m.paths.Map(r.path))
		if err == nil {
			return it, f, true, nil
		}
		if !notFound(err) {
			return it, f, false, err
		}
	}
	if r.typ == jfTypeMovie {
		id := m.tmdb[r.itemID]
		if id == 0 {
			return library.Item{}, library.File{}, false, nil
		}
		it, err := m.lib.ItemByTMDB(ctx, "movie", id)
		if notFound(err) {
			return it, library.File{}, false, nil
		}
		return it, library.File{}, err == nil, err
	}
	id := m.tmdb[r.seriesID]
	if id == 0 || !r.index.Valid || !r.parentIndex.Valid {
		return library.Item{}, library.File{}, false, nil
	}
	series, err := m.lib.ItemByTMDB(ctx, "series", id)
	if err != nil {
		if notFound(err) {
			err = nil
		}
		return series, library.File{}, false, err
	}
	ep, ok, err := m.child(ctx, series.ID, func(it library.Item) bool { return it.SeasonNo == int(r.parentIndex.Int64) },
		func(it library.Item) bool { return it.EpisodeNo == int(r.index.Int64) })
	return ep, library.File{}, ok, err
}

// child finds the first child of id passing first, then its first child
// passing second.
func (m jfMatcher) child(ctx context.Context, id int64, first, second func(library.Item) bool) (library.Item, bool, error) {
	seasons, err := m.lib.Children(ctx, id)
	if err != nil {
		return library.Item{}, false, err
	}
	for _, s := range seasons {
		if !first(s) {
			continue
		}
		eps, err := m.lib.Children(ctx, s.ID)
		if err != nil {
			return library.Item{}, false, err
		}
		for _, e := range eps {
			if second(e) {
				return e, true, nil
			}
		}
	}
	return library.Item{}, false, nil
}

// series matches a series by TMDB id, else by a file under its folder.
func (m jfMatcher) series(ctx context.Context, r jfRow) (library.Item, bool, error) {
	if id := m.tmdb[r.itemID]; id != 0 {
		it, err := m.lib.ItemByTMDB(ctx, "series", id)
		if err == nil {
			return it, true, nil
		}
		if !notFound(err) {
			return it, false, err
		}
	}
	if r.path == "" {
		return library.Item{}, false, nil
	}
	files, err := m.lib.FilesUnder(ctx, m.paths.Map(r.path))
	if err != nil || len(files) == 0 {
		return library.Item{}, false, err
	}
	it, err := m.lib.Item(ctx, files[0].ItemID)
	for err == nil && it.Kind != library.KindSeries && it.ParentID != 0 {
		it, err = m.lib.Item(ctx, it.ParentID)
	}
	if err != nil {
		if notFound(err) {
			err = nil
		}
		return library.Item{}, false, err
	}
	return it, it.Kind == library.KindSeries, nil
}

// JellyfinChannelsLine is the TV channels warning in lang, the same line
// the Jellyfin importer writes (in Russian, into the report).
func JellyfinChannelsLine(lang string, n int) Line {
	return Line{Source: SourceJellyfin, Kind: "leftover", Name: i18n.TL(lang, "ТВ-каналы"), Status: StatusWarning,
		Reason: i18n.TL(lang, "Jellyfin: ТВ-каналы не переносятся (%s)", i18n.PluralL(lang, n, "канал", "канала", "каналов"))}
}
