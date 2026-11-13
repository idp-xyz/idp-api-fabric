package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// A list is read a page at a time, in an order that is fixed: by the instant
// a row was created or granted, then by its identifier, which tells apart
// the rows of one instant. Neither column ever changes, so a row keeps its
// place for as long as it is listed, and a page starts behind the last row of
// the page before, wherever the rows around it are by then.
//
// The position is compared as a row, the way the order compares it, and
// every list has an index in that order (migration 004). PostgreSQL enters
// the index behind the position, so a page costs what it returns, however
// far into the list it is. An offset would cost what it skips, and would
// repeat or skip a row whenever one before it is added or ends.
//
// after is the position a page starts behind, as the first two parameters of
// every list statement; the third is how many rows the page takes, and the
// parameters of the list itself follow from $4. A page that names no
// position starts behind one that precedes every row: the beginning of time
// and the smallest identifier. So one statement reads the first page as it
// reads the others.
const after = `(coalesce($1, '-infinity'::timestamptz), coalesce($2, '00000000-0000-0000-0000-000000000000'::uuid))`

// paged returns the parameters of a list statement: those of the page, then
// those the list itself takes.
func paged(page ports.Page, own ...any) ([]any, error) {
	if page.Limit < 1 {
		return nil, errors.New("a page takes at least one row")
	}
	parameters := []any{nil, nil, page.Limit}
	if page.After != nil {
		parameters[0], parameters[1] = page.After.At, page.After.ID
	}
	return append(parameters, own...), nil
}

// list reads one page of a list: the rows of statement, each read by scan.
// It runs in the transaction ctx carries if there is one and on the pool
// otherwise, as every read does.
func list[T any](ctx context.Context, framework *bentopostgres.DB, statement string,
	scan func(row interface{ Scan(dest ...any) error }) (T, error), page ports.Page, own ...any) ([]T, error) {
	parameters, err := paged(page, own...)
	if err != nil {
		return nil, err
	}
	reader, err := framework.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := reader.Query(ctx, statement, parameters...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (T, error) { return scan(row) })
}
