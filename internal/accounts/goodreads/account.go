package goodreads

import (
	"context"
	"errors"
	"io"

	"github.com/antresol-app/antresol/internal/accounts"
)

// Account is the accounts API's view of a Service (accounts.Goodreads).
type Account struct{ S *Service }

var _ accounts.Goodreads = Account{}

func (a Account) Check(ctx context.Context, id int64) error {
	err := a.S.Check(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return accounts.ErrRemoteNotFound
	}
	return err
}

func (a Account) Link(ctx context.Context, uid, id int64, name string) error {
	return a.S.Link(ctx, uid, id, name)
}

func (a Account) Pull(ctx context.Context, uid int64) error { return a.S.PullOne(ctx, uid) }

func (a Account) Import(ctx context.Context, uid int64, csv io.Reader) (accounts.ShelfSummary, error) {
	entries, _, err := ParseCSV(csv)
	if errors.Is(err, ErrNotExport) {
		return accounts.ShelfSummary{}, accounts.ErrBadImport
	}
	if err != nil {
		return accounts.ShelfSummary{}, err
	}
	return a.S.Import(ctx, uid, entries)
}

func (a Account) Forget(ctx context.Context, uid int64) error { return a.S.Forget(ctx, uid) }

func (a Account) Summary(ctx context.Context, uid int64) (accounts.ShelfSummary, error) {
	return a.S.Summary(ctx, uid)
}

func (a Account) ParseProfile(s string) (int64, string, bool) { return ParseProfile(s) }
