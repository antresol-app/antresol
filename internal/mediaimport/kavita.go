package mediaimport

import (
	"context"
	"database/sql"
	"path"
	"strconv"
	"strings"
	"time"
)

// kvRow is one reading position of Kavita (a chapter's first file).
type kvRow struct {
	userID           int64
	userName         string
	pagesRead, pages int64
	filePath         string
	at               time.Time
}

func (r kvRow) userKey() string { return SourceKavita + ":" + strconv.FormatInt(r.userID, 10) }

func (r kvRow) lineName() string { return r.userName + ": " + path.Base(r.filePath) }

func readKavita(ctx context.Context, d *sql.DB) ([]kvRow, error) {
	rows, err := d.QueryContext(ctx, `SELECT p.AppUserId, COALESCE(u.UserName, ''), COALESCE(p.PagesRead, 0), COALESCE(c.Pages, 0),
		COALESCE(f.FilePath, ''), COALESCE(p.LastModifiedUtc, '')
		FROM AppUserProgresses p JOIN Chapter c ON c.Id = p.ChapterId JOIN MangaFile f ON f.ChapterId = c.Id
		JOIN AspNetUsers u ON u.Id = p.AppUserId
		WHERE f.Id = (SELECT min(Id) FROM MangaFile WHERE ChapterId = c.Id)
		ORDER BY p.Id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []kvRow
	for rows.Next() {
		var r kvRow
		var at string
		if err := rows.Scan(&r.userID, &r.userName, &r.pagesRead, &r.pages, &r.filePath, &at); err != nil {
			return nil, err
		}
		r.at, _ = parseSourceTime(at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportKavita carries over the reading positions of the mapped Kavita
// users: comics and PDFs by page (Vault counts pages from 1), EPUB by the
// read share only (Kavita's position inside a chapter has no Vault form).
func ImportKavita(ctx context.Context, o ProgressOptions, lib LibraryLookup, ps ProgressSink, rep *Report) error {
	d := openProgressDB(o, SourceKavita, "Kavita", o.KavitaDB, rep)
	if d == nil {
		return nil
	}
	defer d.close()
	list, err := readKavita(ctx, d.DB)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		progressReadFailed(SourceKavita, "Kavita", rep, err)
		return nil
	}
	if len(list) == 0 {
		rep.Add(Line{Source: SourceKavita, Kind: "progress", Name: "Kavita", Status: StatusSkipped, Reason: reasonNoData})
		return nil
	}
	for _, r := range list {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !mapped(o, rep, SourceKavita, r.userKey(), r.lineName()) {
			continue
		}
		it, _, ok, err := itemUnder(ctx, lib, o.Paths.Map(r.filePath))
		if err != nil {
			return ctxOr(ctx, err)
		}
		if !ok {
			rep.Add(Line{Source: SourceKavita, Kind: "progress", Name: r.lineName(), Status: StatusFailed,
				Reason: notFoundInVault(path.Base(r.filePath), r.filePath)})
			continue
		}
		frac := 0.0
		if r.pages > 0 {
			frac = float64(r.pagesRead) / float64(r.pages)
		}
		loc, reason := "page:"+strconv.FormatInt(max(r.pagesRead, 1), 10), ""
		if strings.EqualFold(path.Ext(r.filePath), ".epub") {
			loc, reason = "", reasonByPercent
		}
		rec := record{source: SourceKavita, userKey: r.userKey(), name: r.lineName(), itemID: it.ID, at: r.at, reason: reason,
			write: func(ctx context.Context, uid int64, at time.Time) error {
				return ps.SaveLocator(ctx, uid, it.ID, loc, frac, at)
			}}
		if err := put(ctx, o, ps, rep, rec); err != nil {
			return ctxOr(ctx, err)
		}
	}
	return nil
}
