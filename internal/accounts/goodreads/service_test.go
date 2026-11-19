package goodreads

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/accounts"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/progress"
)

type env struct {
	t    *testing.T
	svc  *Service
	repo *library.Repo
	prog *progress.Store
	rem  *fakeRemote
	uid  int64
	now  time.Time
}

// fakeRemote plays the widget: a list per shelf, or one error.
type fakeRemote struct {
	shelves map[string][]Entry
	err     error
	reads   []int64
}

func (f *fakeRemote) Shelf(_ context.Context, id int64, shelf string) ([]Entry, error) {
	f.reads = append(f.reads, id)
	if f.err != nil {
		return nil, f.err
	}
	var out []Entry
	for _, e := range f.shelves[shelf] {
		e.Shelf = shelf
		out = append(out, e)
	}
	return out, nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	r, err := d.Exec(`INSERT INTO users(name, avatar) VALUES('u', 'a')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := r.LastInsertId()
	e := &env{t: t, repo: library.NewRepo(d), prog: progress.New(d), rem: &fakeRemote{shelves: map[string][]Entry{}}, uid: uid,
		now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	e.prog.Now = func() time.Time { return e.now }
	e.svc = &Service{Accounts: &accounts.Store{DB: d, Key: bytes.Repeat([]byte{7}, 32)}, Library: e.repo, Progress: e.prog, Remote: e.rem,
		Now: func() time.Time { return e.now }, Gap: time.Nanosecond}
	return e
}

func (e *env) book(path, title, isbn string, authors ...string) int64 {
	e.t.Helper()
	id, err := e.repo.UpsertBook(context.Background(), library.BookInput{Files: []library.FileInput{{Path: path, Size: 1, Mtime: 1}},
		Title: title, Authors: authors, ISBN: isbn})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) state(id int64) (p progress.Entry, has bool, wanted bool) {
	e.t.Helper()
	p, err := e.prog.Get(context.Background(), e.uid, id)
	if err != nil && !errors.Is(err, progress.ErrNotFound) {
		e.t.Fatal(err)
	}
	has = err == nil
	ids, err := e.prog.Watchlist(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, w := range ids {
		wanted = wanted || w == id
	}
	return p, has, wanted
}

func (e *env) imp(es ...Entry) accounts.ShelfSummary {
	e.t.Helper()
	s, err := e.svc.Import(context.Background(), e.uid, es)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func ymd(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestImportAppliesShelvesToLibraryBooks(t *testing.T) {
	e := newEnv(t)
	dune := e.book("/b/Dune.epub", "Dune", "978-0-441-17271-9", "Frank Herbert")
	emma := e.book("/b/Emma.epub", "Emma", "", "Jane Austen")
	solaris := e.book("/b/Solaris.epub", "Solaris", "")
	sum := e.imp(
		// found by ISBN (the title differs: a series note, another edition)
		Entry{Title: "Dune (Dune, #1)", Author: "F. Herbert", ISBN13: "9780441172719", Shelf: ShelfRead, ReadAt: ymd(2023, 5, 14), AddedAt: ymd(2023, 1, 2)},
		// found by title and author
		Entry{Title: "Emma", Author: "Jane Austen", Shelf: ShelfToRead},
		Entry{Title: "Solaris", Author: "Stanislaw Lem", Shelf: ShelfReading},
		// not in the library
		Entry{Title: "Ulysses", Author: "James Joyce", Shelf: ShelfRead},
	)
	if sum != (accounts.ShelfSummary{Total: 4, Read: 2, Reading: 1, ToRead: 1, InLibrary: 3}) {
		t.Fatalf("summary: %+v", sum)
	}
	p, has, wanted := e.state(dune)
	if !has || !p.Finished || wanted || p.UpdatedAt.Format("2006-01-02") != "2023-05-14" {
		t.Errorf("read book: %+v has=%v wanted=%v", p, has, wanted)
	}
	if p, has, wanted := e.state(emma); has || !wanted {
		t.Errorf("to-read book: %+v has=%v wanted=%v", p, has, wanted)
	}
	// «Читаю сейчас» at the start: no position is made up
	if p, has, wanted := e.state(solaris); !has || p.Finished || p.PositionMs != 0 || wanted {
		t.Errorf("reading book: %+v has=%v wanted=%v", p, has, wanted)
	}
	now, err := e.prog.InProgress(context.Background(), e.uid, []string{library.KindBook}, 0)
	if err != nil || len(now) != 1 || now[0].ItemID != solaris {
		t.Errorf("reading shelf: %+v %v", now, err)
	}
	done, err := e.prog.FinishedOf(context.Background(), e.uid, []string{library.KindBook}, 0)
	if err != nil || len(done) != 1 || done[0].ItemID != dune {
		t.Errorf("read shelf: %+v %v", done, err)
	}
}

// What the profile did here comes first: Goodreads fills only a book with no
// state, and a book the profile marked unread stays unread.
func TestFirstApplyKeepsWhatTheProfileDid(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	partly := e.book("/b/Partly.epub", "Partly", "", "A Author")
	done := e.book("/b/Done.epub", "Done", "", "A Author")
	unread := e.book("/b/Unread.epub", "Unread", "", "A Author")
	started := e.book("/b/Started.epub", "Started", "", "A Author")
	if err := e.prog.SaveLocator(ctx, e.uid, partly, "cfi-40", 0.4, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := e.prog.SaveLocator(ctx, e.uid, done, "cfi-end", 1, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := e.prog.SaveLocator(ctx, e.uid, started, "cfi-10", 0.1, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := e.prog.MarkWatchedAt(ctx, e.uid, unread, false, e.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.imp(
		Entry{Title: "Partly", Author: "A Author", Shelf: ShelfRead, ReadAt: ymd(2020, 1, 1)},
		Entry{Title: "Done", Author: "A Author", Shelf: ShelfToRead},
		Entry{Title: "Unread", Author: "A Author", Shelf: ShelfRead, ReadAt: ymd(2020, 1, 1)},
		Entry{Title: "Started", Author: "A Author", Shelf: ShelfReading},
	)
	if p, _, _ := e.state(partly); p.Finished || p.Locator != "cfi-40" {
		t.Errorf("a position of the profile's: %+v", p)
	}
	if p, _, wanted := e.state(done); !p.Finished || wanted {
		t.Errorf("a read book put on the to-read shelf: %+v wanted=%v", p, wanted)
	}
	if _, has, _ := e.state(unread); has {
		t.Error("a book the profile marked unread came back")
	}
	if p, _, _ := e.state(started); p.Locator != "cfi-10" {
		t.Errorf("a position of the profile's reset: %+v", p)
	}
}

// A shelf is applied once: a book the profile takes off «Хочу прочитать»
// stays off at the next read; a shelf that changed on Goodreads is applied.
func TestShelfAppliedOnceAndOnChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.book("/b/Emma.epub", "Emma", "", "Jane Austen")
	entry := Entry{Title: "Emma", Author: "Jane Austen", Shelf: ShelfToRead}
	e.imp(entry)
	if _, _, wanted := e.state(id); !wanted {
		t.Fatal("not wanted")
	}
	if err := e.prog.RemoveFromWatchlist(ctx, e.uid, id); err != nil {
		t.Fatal(err)
	}
	e.imp(entry)
	if err := e.svc.Reconcile(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	if _, _, wanted := e.state(id); wanted {
		t.Error("the same shelf was applied again")
	}
	// now read on Goodreads: marked read now (the day of the change)
	if err := e.prog.AddToWatchlist(ctx, e.uid, id); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(48 * time.Hour)
	entry.Shelf = ShelfRead
	e.imp(entry)
	if p, has, wanted := e.state(id); !has || !p.Finished || wanted {
		t.Errorf("after the shelf changed: %+v has=%v wanted=%v", p, has, wanted)
	}
	// currently-reading, then read: the position made at the start does not
	// block the finish
	e2 := newEnv(t)
	id2 := e2.book("/b/Solaris.epub", "Solaris", "")
	e2.imp(Entry{Title: "Solaris", Author: "S Lem", Shelf: ShelfReading})
	e2.now = e2.now.Add(time.Hour)
	e2.imp(Entry{Title: "Solaris", Author: "S Lem", Shelf: ShelfRead, ReadAt: ymd(2026, 11, 17)})
	if p, _, _ := e2.state(id2); !p.Finished {
		t.Errorf("reading then read: %+v", p)
	}
}

// A book the library gets after the import is found by the next re-match.
func TestReconcileFindsBooksAddedLater(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if s := e.imp(Entry{Title: "Dune (Dune, #1)", Author: "Frank Herbert", Shelf: ShelfRead, ReadAt: ymd(2023, 5, 14)}); s.InLibrary != 0 || s.Total != 1 {
		t.Fatalf("summary: %+v", s)
	}
	id := e.book("/b/Dune.epub", "Dune", "", "Frank Herbert")
	if err := e.svc.Reconcile(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	if p, has, _ := e.state(id); !has || !p.Finished {
		t.Errorf("late book: %+v %v", p, has)
	}
	if s, _ := e.svc.Summary(ctx, e.uid); s.InLibrary != 1 {
		t.Errorf("summary: %+v", s)
	}
}

func TestMatchIsStrict(t *testing.T) {
	e := newEnv(t)
	messiah := e.book("/b/Messiah.epub", "Dune Messiah", "", "Frank Herbert")
	e.repo.UpsertBook(context.Background(), library.BookInput{Files: []library.FileInput{{Path: "/b/Messiah.epub", Size: 1, Mtime: 1}},
		Title: "Dune Messiah", Authors: []string{"Frank Herbert"}, Series: "Dune"})
	other := e.book("/b/Other.epub", "Foundation", "", "Isaac Asimov")
	s := e.imp(
		// the series is named Dune, the book is not
		Entry{Title: "Dune (Dune, #1)", Author: "Frank Herbert", Shelf: ShelfRead},
		// a subtitle is not cut off: another book
		Entry{Title: "Foundation: Prelude", Author: "Isaac Asimov", Shelf: ShelfRead},
		// the same title by someone else
		Entry{Title: "Foundation", Author: "Somebody Else", Shelf: ShelfRead},
	)
	if s.InLibrary != 0 {
		t.Errorf("summary: %+v", s)
	}
	for _, id := range []int64{messiah, other} {
		if _, has, _ := e.state(id); has {
			t.Errorf("book %d marked", id)
		}
	}
}

func TestAudiobooks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Dir: "/a/Dune", Files: []library.FileInput{{Path: "/a/Dune/01.mp3", Size: 1, Mtime: 1}},
		Title: "Dune", Authors: []string{"Frank Herbert"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Dir: "/a/Emma", Files: []library.FileInput{{Path: "/a/Emma/01.mp3", Size: 1, Mtime: 1}},
		Title: "Emma", Authors: []string{"Jane Austen"}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.repo.UpsertAudiobook(ctx, library.AudiobookInput{Dir: "/a/Solaris", Files: []library.FileInput{{Path: "/a/Solaris/01.mp3", Size: 1, Mtime: 1}},
		Title: "Solaris", Authors: []string{"Stanislaw Lem"}})
	if err != nil {
		t.Fatal(err)
	}
	e.imp(Entry{Title: "Dune", Author: "Frank Herbert", Shelf: ShelfRead, ReadAt: ymd(2023, 5, 14)},
		Entry{Title: "Emma", Author: "Jane Austen", Shelf: ShelfToRead},
		Entry{Title: "Solaris", Author: "Stanislaw Lem", Shelf: ShelfReading})
	if p, has, _ := e.state(a); !has || !p.Finished {
		t.Errorf("heard: %+v %v", p, has)
	}
	if _, _, wanted := e.state(b); !wanted {
		t.Error("audiobook not wanted")
	}
	// an audiobook has no start to put «reading» at
	if _, has, _ := e.state(c); has {
		t.Error("audiobook given a position")
	}
}

func TestPullReadsEveryShelfAndKeepsExportData(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.book("/b/Dune.epub", "Dune", "9780441172719", "Frank Herbert")
	// the export came first and knows the ISBN and the day
	e.imp(Entry{Title: "Dune (Dune, #1)", Author: "Frank Herbert", ISBN13: "9780441172719", Shelf: ShelfToRead})
	if err := e.svc.Link(ctx, e.uid, 777, "some-name"); err != nil {
		t.Fatal(err)
	}
	e.rem.shelves[ShelfRead] = []Entry{{Title: "Dune (Dune, #1)", Author: "Frank Herbert"}, {Title: "Emma", Author: "Jane Austen"}}
	e.rem.shelves[ShelfReading] = []Entry{{Title: "Solaris", Author: "Stanislaw Lem"}}
	if err := e.svc.PullOne(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	if len(e.rem.reads) != 3 || e.rem.reads[0] != 777 {
		t.Errorf("reads: %v", e.rem.reads)
	}
	if p, _, wanted := e.state(id); !p.Finished || wanted {
		t.Errorf("moved to read: %+v wanted=%v", p, wanted)
	}
	var isbn string
	if err := e.svc.Accounts.DB.QueryRow(`SELECT isbn13 FROM goodreads_entries WHERE user_id=? AND title LIKE 'Dune%'`, e.uid).Scan(&isbn); err != nil || isbn != "9780441172719" {
		t.Errorf("isbn kept: %q %v", isbn, err)
	}
	l, ok, err := e.svc.Accounts.Link(ctx, e.uid, Provider)
	if err != nil || !ok || l.Status != accounts.StatusOK || l.LastSync.IsZero() || !l.FirstPullDone || l.ExternalID != 777 || l.Nickname != "some-name" {
		t.Errorf("link: %+v %v %v", l, ok, err)
	}
	if s, _ := e.svc.Summary(ctx, e.uid); s.Total != 3 || s.Read != 2 || s.Reading != 1 || s.InLibrary != 1 {
		t.Errorf("summary: %+v", s)
	}
}

func TestPullFailuresSetStatusAndChangeNothing(t *testing.T) {
	for _, c := range []struct {
		err    error
		status string
		msg    string
	}{
		{ErrNotFound, accounts.StatusNotFound, msgNotFound},
		{ErrUnavailable, accounts.StatusError, msgUnavailable},
		{ErrFormat, accounts.StatusError, msgFormat},
	} {
		e := newEnv(t)
		ctx := context.Background()
		e.book("/b/Dune.epub", "Dune", "", "Frank Herbert")
		if err := e.svc.Link(ctx, e.uid, 777, "x"); err != nil {
			t.Fatal(err)
		}
		e.rem.err = c.err
		if err := e.svc.PullOne(ctx, e.uid); !errors.Is(err, c.err) {
			t.Fatalf("%v: %v", c.err, err)
		}
		l, _, _ := e.svc.Accounts.Link(ctx, e.uid, Provider)
		if l.Status != c.status || l.LastError != c.msg || l.FirstPullDone {
			t.Errorf("%v: link %+v", c.err, l)
		}
		if s, _ := e.svc.Summary(ctx, e.uid); s.Total != 0 {
			t.Errorf("%v: stored %+v", c.err, s)
		}
	}
	e := newEnv(t)
	if err := e.svc.PullOne(context.Background(), e.uid); err == nil {
		t.Error("an unlinked profile was pulled")
	}
}

// A book that left the first hundred of a shelf says nothing: nothing is
// taken back.
func TestPullNeverTakesAMarkBack(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.book("/b/Dune.epub", "Dune", "", "Frank Herbert")
	e.svc.Link(ctx, e.uid, 777, "x")
	e.rem.shelves[ShelfRead] = []Entry{{Title: "Dune", Author: "Frank Herbert"}}
	if err := e.svc.PullOne(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	e.rem.shelves = map[string][]Entry{}
	if err := e.svc.PullOne(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := e.state(id); !p.Finished {
		t.Errorf("mark taken back: %+v", p)
	}
	if s, _ := e.svc.Summary(ctx, e.uid); s.Total != 1 {
		t.Errorf("summary: %+v", s)
	}
}

func TestLinkAnotherAccountStartsAfreshAndForgetKeepsMarks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.book("/b/Dune.epub", "Dune", "", "Frank Herbert")
	e.svc.Link(ctx, e.uid, 777, "first")
	e.rem.shelves[ShelfRead] = []Entry{{Title: "Dune", Author: "Frank Herbert"}, {Title: "Emma", Author: "Jane Austen"}}
	if err := e.svc.PullOne(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	// the same account again: nothing is reset
	e.svc.Link(ctx, e.uid, 777, "first-renamed")
	if l, _, _ := e.svc.Accounts.Link(ctx, e.uid, Provider); !l.FirstPullDone || l.LastSync.IsZero() || l.Nickname != "first-renamed" {
		t.Errorf("same account: %+v", l)
	}
	if s, _ := e.svc.Summary(ctx, e.uid); s.Total != 2 {
		t.Errorf("same account: %+v", s)
	}
	// another one
	e.svc.Link(ctx, e.uid, 888, "second")
	l, _, _ := e.svc.Accounts.Link(ctx, e.uid, Provider)
	if l.FirstPullDone || !l.LastSync.IsZero() || l.ExternalID != 888 {
		t.Errorf("another account: %+v", l)
	}
	if s, _ := e.svc.Summary(ctx, e.uid); s.Total != 0 {
		t.Errorf("another account's books: %+v", s)
	}
	// forget: link and books go, the mark stays
	e.rem.shelves[ShelfRead] = []Entry{{Title: "Emma", Author: "Jane Austen"}}
	e.svc.PullOne(ctx, e.uid)
	if err := e.svc.Forget(ctx, e.uid); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.svc.Accounts.Link(ctx, e.uid, Provider); ok {
		t.Error("link kept")
	}
	if s, _ := e.svc.Summary(ctx, e.uid); s.Total != 0 {
		t.Errorf("books kept: %+v", s)
	}
	if p, _, _ := e.state(id); !p.Finished {
		t.Errorf("mark removed: %+v", p)
	}
}

func TestAccountImportAndCheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.book("/b/Dune.epub", "Dune", "9780441172719", "Frank Herbert")
	acct := Account{S: e.svc}
	if _, err := acct.Import(ctx, e.uid, strings.NewReader("a,b\n1,2\n")); !errors.Is(err, accounts.ErrBadImport) {
		t.Fatalf("not an export: %v", err)
	}
	sum, err := acct.Import(ctx, e.uid, strings.NewReader(exportCSV))
	if err != nil || sum.Total != 3 || sum.InLibrary != 1 {
		t.Fatalf("import: %+v %v", sum, err)
	}
	if p, _, _ := e.state(id); !p.Finished {
		t.Errorf("imported book: %+v", p)
	}
	e.rem.err = ErrNotFound
	if err := acct.Check(ctx, 5); !errors.Is(err, accounts.ErrRemoteNotFound) {
		t.Errorf("check: %v", err)
	}
	e.rem.err = ErrUnavailable
	if err := acct.Check(ctx, 5); !errors.Is(err, ErrUnavailable) || errors.Is(err, accounts.ErrRemoteNotFound) {
		t.Errorf("check: %v", err)
	}
}

// Run's due check: a read profile waits hours, a failed one half an hour.
func TestPullDueCadence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Link(ctx, e.uid, 777, "x")
	e.rem.shelves[ShelfRead] = []Entry{{Title: "Emma", Author: "Jane Austen"}}
	due := func() bool { return e.svc.pullDue(ctx)[e.uid] }
	if !due() { // never pulled
		t.Fatal("a new link is due at once")
	}
	e.now = e.now.Add(5 * time.Hour)
	if due() {
		t.Fatal("a pulled profile is read again within hours")
	}
	e.now = e.now.Add(2 * time.Hour)
	if !due() {
		t.Fatal("not due after six hours")
	}
	e.rem.err = ErrUnavailable
	e.now = e.now.Add(7 * time.Hour)
	if !due() { // fails
		t.Fatal("not due")
	}
	e.now = e.now.Add(10 * time.Minute)
	if due() {
		t.Fatal("a failed read is retried within minutes")
	}
	e.now = e.now.Add(25 * time.Minute)
	e.rem.err = nil
	if !due() {
		t.Fatal("a failed read is not retried after half an hour")
	}
	if l, _, _ := e.svc.Accounts.Link(ctx, e.uid, Provider); l.Status != accounts.StatusOK {
		t.Errorf("link: %+v", l)
	}
}

// A profile whose sync is running (its «sync now», the pull after linking) is
// left to it by the loop: that one reads the shelves and stamps the link.
func TestPullDueSkipsProfileWithRunningSync(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var runs accounts.Runs
	e.svc.Runs = &runs
	e.svc.Link(ctx, e.uid, 777, "x")
	e.rem.shelves[ShelfRead] = []Entry{{Title: "Emma", Author: "Jane Austen"}}
	end, ok := runs.Start(e.uid, Provider)
	if !ok {
		t.Fatal("not startable")
	}
	if e.svc.pullDue(ctx)[e.uid] {
		t.Fatal("pulled during a running sync")
	}
	if l, _, _ := e.svc.Accounts.Link(ctx, e.uid, Provider); !l.LastSync.IsZero() {
		t.Fatalf("link stamped by a skipped pull: %+v", l)
	}
	end()
	if !e.svc.pullDue(ctx)[e.uid] {
		t.Fatal("not pulled after the sync ended")
	}
	if runs.Active(e.uid, Provider) {
		t.Fatal("the loop left its run registered")
	}
}
