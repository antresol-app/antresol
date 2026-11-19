package goodreads

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/antresol-app/antresol/internal/accounts"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/metadata/booknames"
	"github.com/antresol-app/antresol/internal/progress"
)

// Link status messages (i18n catalog keys).
const (
	msgNotFound    = "Профиль Goodreads не найден или скрыт"
	msgUnavailable = "Goodreads не отвечает, попробуйте позже"
	msgFormat      = "Goodreads изменил список книг профиля: пока подойдёт только импорт файла CSV"
	msgLocal       = "Не удалось прочитать данные"
)

// Pull cadence: the widget is a public page, so a linked profile is read
// gently; the books a shelf gains between two reads wait for the next one.
const (
	pullEvery  = 6 * time.Hour
	retryEvery = 30 * time.Minute // after a failed read
	// lookupsPerTick: books asked about (Names.Lookup, a few requests each)
	// per minute, so a large import is translated gently, in the background
	lookupsPerTick = 10
	reconcileEvery = time.Hour
	runTick        = time.Minute
	shelfGap       = time.Second
)

// Library is the part of *library.Repo the matching uses.
type Library interface {
	BooksByISBN(ctx context.Context) (map[string][]library.BookRef, error)
	FindBooks(ctx context.Context, author, title string) ([]library.BookRef, error)
	BooksTitled(ctx context.Context, title string) ([]library.TitledBook, error)
	BooksByWork(ctx context.Context) (map[string][]library.BookRef, error)
}

// Names finds the other names of a book's work (*booknames.Resolver):
// Goodreads lists a book under its original title, the library has its
// translation. Cached answers only during a match; Lookup asks the sources.
type Names interface {
	Cached(ctx context.Context, b booknames.Book) (booknames.Names, bool, error)
	Lookup(ctx context.Context, b booknames.Book) (booknames.Names, error)
}

// Progress is the part of *progress.Store the marks are written through.
type Progress interface {
	Get(ctx context.Context, userID, itemID int64) (progress.Entry, error)
	ClearedAt(ctx context.Context, userID, itemID int64) (time.Time, bool, error)
	SaveLocator(ctx context.Context, userID, itemID int64, locator string, fraction float64, at time.Time) error
	MarkWatchedAt(ctx context.Context, userID, itemID int64, watched bool, at time.Time) error
	AddToWatchlist(ctx context.Context, userID, itemID int64) error
	RemoveFromWatchlist(ctx context.Context, userID, itemID int64) error
}

// Remote reads a public shelf (Widget).
type Remote interface {
	Shelf(ctx context.Context, userID int64, shelf string) ([]Entry, error)
}

// Service brings Goodreads shelves into a profile's books. Whole operations
// of one profile (a pull, an import, a re-match) run one at a time.
type Service struct {
	Accounts *accounts.Store
	Library  Library
	Progress Progress
	Remote   Remote
	// Names (nil: none) finds the books whose title is in another language
	// than the library's edition: looked up in the background (Run), the
	// answers used by every match after.
	Names Names
	Now   func() time.Time
	// Gap is the pause between the reads of one pull's shelves (default 1 s).
	Gap time.Duration
	// Runs is the registry of syncs in flight (accounts.Syncer.Runs): a
	// profile whose sync is running is left to it (nil = no registry).
	Runs *accounts.Runs
	Log  *slog.Logger

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
	// lookupPause: a source failed; lookups wait until then
	lookupPause time.Time
}

func (s *Service) lock(uid int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	l, ok := s.locks[uid]
	if !ok {
		l = &sync.Mutex{}
		s.locks[uid] = l
	}
	return l
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Check reads one shelf of the user id once: ErrNotFound for an unknown or
// non-public profile.
func (s *Service) Check(ctx context.Context, id int64) error {
	_, err := s.Remote.Shelf(ctx, id, ShelfRead)
	return err
}

// Link records the profile's Goodreads account. Another account than the one
// linked before starts afresh: its first pull runs again and the old one's
// books are forgotten (what they already marked stays).
func (s *Service) Link(ctx context.Context, uid, id int64, name string) error {
	lk := s.lock(uid)
	lk.Lock()
	defer lk.Unlock()
	old, ok, err := s.Accounts.Link(ctx, uid, Provider)
	if err != nil {
		return err
	}
	if ok && old.ExternalID != id {
		if _, err := s.Accounts.DB.ExecContext(ctx, `DELETE FROM goodreads_entries WHERE user_id=?`, uid); err != nil {
			return err
		}
		if _, err := s.Accounts.DB.ExecContext(ctx,
			`UPDATE account_links SET first_pull_done=0, status='ok', last_error='', last_sync_at=0 WHERE user_id=? AND provider=?`, uid, Provider); err != nil {
			return err
		}
	}
	return s.Accounts.SetNickname(ctx, uid, Provider, name, id)
}

// Forget removes the link and the books read from Goodreads. The marks
// already made stay.
func (s *Service) Forget(ctx context.Context, uid int64) error {
	lk := s.lock(uid)
	lk.Lock()
	defer lk.Unlock()
	if _, err := s.Accounts.DB.ExecContext(ctx, `DELETE FROM goodreads_entries WHERE user_id=?`, uid); err != nil {
		return err
	}
	return s.Accounts.Unlink(ctx, uid, Provider)
}

// Import brings the books of a library export into the profile's marks. It
// needs no link.
func (s *Service) Import(ctx context.Context, uid int64, entries []Entry) (accounts.ShelfSummary, error) {
	lk := s.lock(uid)
	lk.Lock()
	defer lk.Unlock()
	if err := s.store(ctx, uid, entries); err != nil {
		return accounts.ShelfSummary{}, err
	}
	if err := s.apply(ctx, uid); err != nil {
		return accounts.ShelfSummary{}, err
	}
	return s.summary(ctx, uid)
}

// Summary counts the profile's Goodreads books.
func (s *Service) Summary(ctx context.Context, uid int64) (accounts.ShelfSummary, error) {
	return s.summary(ctx, uid)
}

// PullOne reads the linked profile's public shelves into its marks. The
// widget gives the 100 newest books of a shelf, so a book missing from it
// says nothing: no mark is ever taken back. A failed read (or a widget that
// changed its shape) sets the link's status and changes nothing.
func (s *Service) PullOne(ctx context.Context, uid int64) error {
	lk := s.lock(uid)
	lk.Lock()
	defer lk.Unlock()
	link, ok, err := s.Accounts.Link(ctx, uid, Provider)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("goodreads: profile not linked")
	}
	var all []Entry
	gap := s.Gap
	if gap == 0 {
		gap = shelfGap
	}
	for i, shelf := range Shelves {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(gap):
			}
		}
		es, err := s.Remote.Shelf(ctx, link.ExternalID, shelf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			status, msg := accounts.StatusError, msgUnavailable
			switch {
			case errors.Is(err, ErrNotFound):
				status, msg = accounts.StatusNotFound, msgNotFound
			case errors.Is(err, ErrFormat):
				msg = msgFormat
			}
			if serr := s.Accounts.SetStatus(ctx, uid, Provider, status, msg, s.now().Truncate(time.Second)); serr != nil {
				return serr
			}
			return err
		}
		all = append(all, es...)
	}
	now := s.now().Truncate(time.Second)
	if err := s.store(ctx, uid, all); err != nil {
		return s.local(ctx, uid, now, err)
	}
	if err := s.apply(ctx, uid); err != nil {
		return s.local(ctx, uid, now, err)
	}
	if !link.FirstPullDone {
		if err := s.Accounts.SetFirstPullDone(ctx, uid, Provider); err != nil {
			return err
		}
	}
	return s.Accounts.SetStatus(ctx, uid, Provider, accounts.StatusOK, "", now)
}

func (s *Service) local(ctx context.Context, uid int64, now time.Time, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if serr := s.Accounts.SetStatus(ctx, uid, Provider, accounts.StatusError, msgLocal, now); serr != nil {
		s.log().Warn("goodreads: status", "user", uid, "err", serr)
	}
	return err
}

// Reconcile matches the profile's books that found no library book yet (the
// library grows) and applies the ones whose shelf changed.
func (s *Service) Reconcile(ctx context.Context, uid int64) error {
	lk := s.lock(uid)
	lk.Lock()
	defer lk.Unlock()
	return s.apply(ctx, uid)
}

// store writes the books of one read. A book already known keeps what the
// new read does not say (the widget has no ISBN and no dates).
func (s *Service) store(ctx context.Context, uid int64, entries []Entry) error {
	tx, err := s.Accounts.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO goodreads_entries(user_id, entry_key, title, author, isbn13, shelf, read_at, added_at)
	  VALUES(?,?,?,?,?,?,?,?)
	  ON CONFLICT(user_id, entry_key) DO UPDATE SET title=excluded.title, author=excluded.author, shelf=excluded.shelf,
	    isbn13=CASE WHEN excluded.isbn13<>'' THEN excluded.isbn13 ELSE goodreads_entries.isbn13 END,
	    read_at=CASE WHEN excluded.read_at>0 THEN excluded.read_at ELSE goodreads_entries.read_at END,
	    added_at=CASE WHEN excluded.added_at>0 THEN excluded.added_at ELSE goodreads_entries.added_at END`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		k := e.key()
		if e.Title == "" || k == "|" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, uid, k, e.Title, e.Author, e.ISBN13, e.Shelf, unix(e.ReadAt), unix(e.AddedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// row is a stored book.
type row struct {
	key, title, author, isbn13 string
	shelf, applied             string
	readAt, addedAt            time.Time
	matched                    bool
}

// apply finds library books for the profile's books that have none yet or
// whose shelf changed since they were applied, and applies the shelf.
func (s *Service) apply(ctx context.Context, uid int64) error {
	rows, err := s.Accounts.DB.QueryContext(ctx, `SELECT entry_key, title, author, isbn13, shelf, applied_shelf, read_at, added_at, matched
	  FROM goodreads_entries WHERE user_id=? AND (matched=0 OR applied_shelf<>shelf) ORDER BY entry_key`, uid)
	if err != nil {
		return err
	}
	var todo []row
	for rows.Next() {
		var r row
		var read, added int64
		if err := rows.Scan(&r.key, &r.title, &r.author, &r.isbn13, &r.shelf, &r.applied, &read, &added, &r.matched); err != nil {
			rows.Close()
			return err
		}
		if read > 0 {
			r.readAt = time.Unix(read, 0)
		}
		if added > 0 {
			r.addedAt = time.Unix(added, 0)
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(todo) == 0 {
		return err
	}
	var idx index
	if idx.isbn, err = s.Library.BooksByISBN(ctx); err != nil {
		return err
	}
	if s.Names != nil {
		if idx.work, err = s.Library.BooksByWork(ctx); err != nil {
			return err
		}
	}
	for _, r := range todo {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		refs, err := s.match(ctx, idx, r)
		if err != nil {
			return err
		}
		if len(refs) == 0 {
			continue
		}
		for _, ref := range refs {
			if err := s.applyShelf(ctx, uid, r, ref); err != nil {
				return err
			}
		}
		if _, err := s.Accounts.DB.ExecContext(ctx, `UPDATE goodreads_entries SET matched=1, applied_shelf=? WHERE user_id=? AND entry_key=?`,
			r.shelf, uid, r.key); err != nil {
			return err
		}
	}
	return nil
}

// index: the library's books by ISBN-13 and by Open Library work.
type index struct {
	isbn, work map[string][]library.BookRef
}

// book is how a stored book is looked up elsewhere.
func (r row) book() booknames.Book {
	return booknames.Book{Title: baseTitle(r.title), Author: r.author, ISBN13: r.isbn13}
}

// match finds the library books of a Goodreads book: by ISBN when the
// export gave one, else by title and author; failing both, by its work
// (Names, as looked up before): an ISBN or an Open Library work of another
// edition, or a title the work has in another language by an author whose
// name, in any script, is one of the work's or Goodreads'.
func (s *Service) match(ctx context.Context, idx index, r row) ([]library.BookRef, error) {
	if r.isbn13 != "" {
		if refs := idx.isbn[r.isbn13]; len(refs) > 0 {
			return refs, nil
		}
	}
	for _, t := range titleForms(r.title) {
		refs, err := s.Library.FindBooks(ctx, r.author, t)
		if err != nil || len(refs) > 0 {
			return refs, err
		}
	}
	if s.Names == nil {
		return nil, nil
	}
	n, _, err := s.Names.Cached(ctx, r.book()) // an old answer still matches
	if err != nil || n.Empty() {
		return nil, err
	}
	var out []library.BookRef
	add := func(refs []library.BookRef) {
		for _, ref := range refs {
			if !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
	}
	for _, i := range n.ISBNs {
		add(idx.isbn[i])
	}
	for _, w := range n.Works {
		add(idx.work[w])
	}
	authors := append([]string{r.author}, n.Authors...)
	for _, t := range n.Titles {
		books, err := s.Library.BooksTitled(ctx, t)
		if err != nil {
			return nil, err
		}
		for _, b := range books {
			if library.SameAuthor(authors, b.Authors) {
				add([]library.BookRef{b.BookRef})
			}
		}
	}
	return out, nil
}

// lookupPending looks up (Names.Lookup) the profiles' books that found no
// library book and have no answer yet, at most lookupsPerTick; true when an
// answer named something (a match may find the book now). A failed source
// pauses the lookups for retryEvery.
func (s *Service) lookupPending(ctx context.Context) bool {
	if s.Names == nil || s.now().Before(s.lookupPause) {
		return false
	}
	rows, err := s.Accounts.DB.QueryContext(ctx, `SELECT DISTINCT title, author, isbn13 FROM goodreads_entries WHERE matched=0 ORDER BY title`)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("goodreads: unmatched", "err", err)
		}
		return false
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.title, &r.author, &r.isbn13); err == nil {
			todo = append(todo, r)
		}
	}
	rows.Close()
	found, asked := false, 0
	for _, r := range todo {
		if ctx.Err() != nil || asked >= lookupsPerTick {
			break
		}
		b := r.book()
		if _, ok, err := s.Names.Cached(ctx, b); err != nil || ok {
			continue
		}
		asked++
		n, err := s.Names.Lookup(ctx, b)
		if err != nil {
			if ctx.Err() == nil {
				s.log().Info("goodreads: names lookup", "title", b.Title, "err", err)
				s.lookupPause = s.now().Add(retryEvery)
			}
			break
		}
		found = found || !n.Empty()
	}
	return found
}

// applyShelf puts a Goodreads shelf on one library book:
//
//   - read marks the book read (a book is read to its end; an audiobook is
//     marked heard) and takes it off «Хочу прочитать»;
//   - currently-reading puts a book into «Читаю сейчас» at its start (Goodreads
//     keeps no position, so none is made up), and an audiobook is left alone
//     (it has no place to start from);
//   - to-read puts the book on «Хочу прочитать».
//
// What the profile did here comes first: a book that has a reading position
// or a mark, or that the profile marked unread, is not changed when its
// shelf is applied the first time. A shelf that changed on Goodreads since
// (a book now read) is a fresh fact and is applied over it.
func (s *Service) applyShelf(ctx context.Context, uid int64, r row, ref library.BookRef) error {
	first := r.applied == ""
	cur, err := s.Progress.Get(ctx, uid, ref.ID)
	has := err == nil
	if err != nil && !errors.Is(err, progress.ErrNotFound) {
		return err
	}
	_, cleared, err := s.Progress.ClearedAt(ctx, uid, ref.ID)
	if err != nil {
		return err
	}
	switch r.shelf {
	case ShelfRead:
		if has && cur.Finished {
			return nil
		}
		at := time.Time{} // a shelf that changed: read now
		if first {
			if has || cleared {
				return nil
			}
			at = r.readAt
			if at.IsZero() {
				at = r.addedAt
			}
		}
		if ref.Kind == library.KindBook {
			err = s.Progress.SaveLocator(ctx, uid, ref.ID, cur.Locator, 1, at)
		} else {
			err = s.Progress.MarkWatchedAt(ctx, uid, ref.ID, true, at)
		}
		if err != nil {
			return err
		}
		return s.Progress.RemoveFromWatchlist(ctx, uid, ref.ID)
	case ShelfReading:
		if has || cleared || ref.Kind != library.KindBook {
			return nil
		}
		if err := s.Progress.SaveLocator(ctx, uid, ref.ID, "", 0, time.Time{}); err != nil {
			return err
		}
		return s.Progress.RemoveFromWatchlist(ctx, uid, ref.ID)
	case ShelfToRead:
		if has {
			return nil
		}
		return s.Progress.AddToWatchlist(ctx, uid, ref.ID)
	}
	return nil
}

// summary counts a profile's Goodreads books by shelf.
func (s *Service) summary(ctx context.Context, uid int64) (accounts.ShelfSummary, error) {
	var out accounts.ShelfSummary
	rows, err := s.Accounts.DB.QueryContext(ctx,
		`SELECT shelf, COUNT(*), COALESCE(SUM(matched),0) FROM goodreads_entries WHERE user_id=? GROUP BY shelf`, uid)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var shelf string
		var n, m int
		if err := rows.Scan(&shelf, &n, &m); err != nil {
			return out, err
		}
		out.Total += n
		out.InLibrary += m
		switch shelf {
		case ShelfRead:
			out.Read = n
		case ShelfReading:
			out.Reading = n
		case ShelfToRead:
			out.ToRead = n
		}
	}
	return out, rows.Err()
}

// Run pulls every linked profile every few hours, looks up the other names
// of the books that found no library book (a few a minute, with Names),
// and matches the books of every profile that still has some the library
// lacks (a book added to the library later is found within the hour, one
// just named at once), until ctx ends. Books imported before Names existed
// are looked up alike: every unmatched book is.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(runTick)
	defer t.Stop()
	var reconciled time.Time
	for {
		pulled := s.pullDue(ctx)
		if s.lookupPending(ctx) {
			// the books just named are matched now, not within the hour
			s.reconcileAll(ctx, nil)
		}
		if s.now().Sub(reconciled) >= reconcileEvery {
			reconciled = s.now()
			s.reconcileAll(ctx, pulled)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pullDue pulls the linked profiles that are due; it returns the ones it
// pulled (their books are matched already).
func (s *Service) pullDue(ctx context.Context) map[int64]bool {
	pulled := map[int64]bool{}
	links, err := s.Accounts.Links(ctx, Provider)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("goodreads: links", "err", err)
		}
		return pulled
	}
	for _, l := range links {
		if ctx.Err() != nil {
			return pulled
		}
		wait := pullEvery
		if l.Status != accounts.StatusOK {
			wait = retryEvery
		}
		if l.ExternalID == 0 || !l.LastSync.IsZero() && s.now().Before(l.LastSync.Add(wait)) {
			continue
		}
		end, ok := s.Runs.Start(l.UserID, Provider)
		if !ok {
			continue // its sync is running (or was just asked for): it stamps the link when it ends
		}
		pulled[l.UserID] = true
		err := s.PullOne(ctx, l.UserID)
		end()
		if err != nil && ctx.Err() == nil {
			s.log().Info("goodreads: pull", "user", l.UserID, "err", err)
		}
	}
	return pulled
}

func (s *Service) reconcileAll(ctx context.Context, skip map[int64]bool) {
	rows, err := s.Accounts.DB.QueryContext(ctx, `SELECT DISTINCT user_id FROM goodreads_entries WHERE matched=0 OR applied_shelf<>shelf`)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("goodreads: pending", "err", err)
		}
		return
	}
	var uids []int64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err == nil && !skip[uid] {
			uids = append(uids, uid)
		}
	}
	rows.Close()
	for _, uid := range uids {
		if ctx.Err() != nil {
			return
		}
		if err := s.Reconcile(ctx, uid); err != nil && ctx.Err() == nil {
			s.log().Info("goodreads: reconcile", "user", uid, "err", err)
		}
	}
}
