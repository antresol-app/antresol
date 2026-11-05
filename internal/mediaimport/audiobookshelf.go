package mediaimport

import (
	"context"
	"database/sql"
	"math"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/antresol-app/antresol/internal/library"
)

// absRow is one book position of Audiobookshelf (seconds; ebook share 0..1).
type absRow struct {
	userID, userName    string
	duration, current   float64
	finished            bool
	ebookLoc            string
	ebookProgress       float64
	itemPath, itemTitle string
	at                  time.Time
}

func (r absRow) userKey() string { return SourceABS + ":" + r.userID }

func (r absRow) title() string {
	if r.itemTitle != "" {
		return r.itemTitle
	}
	return path.Base(r.itemPath)
}

func (r absRow) lineName() string { return r.userName + ": " + r.title() }

func readABS(ctx context.Context, d *sql.DB) ([]absRow, error) {
	rows, err := d.QueryContext(ctx, `SELECT m.userId, COALESCE(u.username, ''), COALESCE(m.duration, 0), COALESCE(m.currentTime, 0),
		COALESCE(m.isFinished, 0), COALESCE(m.ebookLocation, ''), COALESCE(m.ebookProgress, 0),
		COALESCE(li.path, ''), COALESCE(li.title, ''), COALESCE(CAST(m.updatedAt AS TEXT), '')
		FROM mediaProgresses m JOIN libraryItems li ON li.mediaId = m.mediaItemId JOIN users u ON u.id = m.userId
		WHERE m.mediaItemType = 'book'
		ORDER BY m.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []absRow
	for rows.Next() {
		var r absRow
		var at string
		if err := rows.Scan(&r.userID, &r.userName, &r.duration, &r.current, &r.finished, &r.ebookLoc, &r.ebookProgress,
			&r.itemPath, &r.itemTitle, &at); err != nil {
			return nil, err
		}
		r.at, _ = parseSourceTime(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// readABSOthers counts the positions the import does not carry over
// (podcast episodes), by kind.
func readABSOthers(ctx context.Context, d *sql.DB) (map[string]int, error) {
	rows, err := d.QueryContext(ctx, `SELECT COALESCE(mediaItemType, ''), count(*) FROM mediaProgresses
		WHERE COALESCE(mediaItemType, '') <> 'book' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, err
		}
		if typ == "podcastEpisode" {
			typ = "подкасты"
		}
		out[typ] += n
	}
	return out, rows.Err()
}

// absPageRe is an ebookLocation of a PDF: a page number.
var absPageRe = regexp.MustCompile(`^\d+$`)

// ImportABS carries over the listening and reading positions of the
// mapped Audiobookshelf users.
func ImportABS(ctx context.Context, o ProgressOptions, lib LibraryLookup, ps ProgressSink, rep *Report) error {
	d := openProgressDB(o, SourceABS, "Audiobookshelf", o.ABSDB, rep)
	if d == nil {
		return nil
	}
	defer d.close()
	list, err := readABS(ctx, d.DB)
	var others map[string]int
	if err == nil {
		others, err = readABSOthers(ctx, d.DB)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		progressReadFailed(SourceABS, "Audiobookshelf", rep, err)
		return nil
	}
	if w := othersWarning("Audiobookshelf", others); w != "" {
		rep.Add(Line{Source: SourceABS, Kind: "leftover", Name: "Другие отметки", Status: StatusWarning, Reason: w})
	}
	if len(list) == 0 {
		rep.Add(Line{Source: SourceABS, Kind: "progress", Name: "Audiobookshelf", Status: StatusSkipped, Reason: reasonNoData})
		return nil
	}
	for _, r := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !mapped(o, rep, SourceABS, r.userKey(), r.lineName()) {
			continue
		}
		it, _, ok, err := itemUnder(ctx, lib, o.Paths.Map(r.itemPath))
		if err != nil {
			return ctxOr(ctx, err)
		}
		if !ok {
			rep.Add(Line{Source: SourceABS, Kind: "progress", Name: r.lineName(), Status: StatusFailed, Reason: notFoundInVault(r.title(), r.itemPath)})
			continue
		}
		rec := record{source: SourceABS, userKey: r.userKey(), name: r.lineName(), itemID: it.ID, at: r.at}
		switch {
		case r.duration > 0 && it.Kind != library.KindBook: // audio
			dur := int64(math.Round(r.duration * 1000))
			pos := int64(math.Round(r.current * 1000))
			if r.finished {
				pos = dur
			}
			rec.write = func(ctx context.Context, uid int64, at time.Time) error {
				return ps.SaveListenAt(ctx, uid, it.ID, pos, dur, at)
			}
		case r.ebookLoc != "" || r.ebookProgress > 0 || r.finished: // ebook
			loc, frac := r.ebookLoc, r.ebookProgress
			if absPageRe.MatchString(loc) { // a PDF page; Vault counts pages from 1
				n, _ := strconv.ParseInt(loc, 10, 64)
				loc = "page:" + strconv.FormatInt(max(n, 1), 10)
			}
			if r.finished {
				frac = 1
			}
			rec.write = func(ctx context.Context, uid int64, at time.Time) error {
				return ps.SaveLocator(ctx, uid, it.ID, loc, frac, at)
			}
		default:
			rep.Add(Line{Source: SourceABS, Kind: "progress", Name: r.lineName(), Ref: itemRef(it.ID), Status: StatusSkipped, Reason: reasonNoPosition})
			continue
		}
		if err := put(ctx, o, ps, rep, rec); err != nil {
			return ctxOr(ctx, err)
		}
	}
	return nil
}
