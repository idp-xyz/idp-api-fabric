package postgres_test

import (
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// Among the reviews asked for at one instant the queue is in the order of
// their identifiers whichever plan reads them. The index of the queue puts
// them in that order as it reads them, which proves nothing of the statement:
// a plan that sorts what it reads, as the database may choose for a page kept
// to the open reviews, puts them in it only if the statement asks for it, and
// so does a page that starts behind a review of the instant. The statement is
// run as it is, and with the reads of an index forbidden, over reviews that
// were all asked for at one instant and stored in the order of nothing.
func TestTheOrderOfTheQueueDoesNotDependOnThePlanItIsReadWith(t *testing.T) {
	f, database := newReviewFixture(t)
	where := newPlace()
	instant := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	id := func(n int) string { return "00000000-0000-4000-8000-00000000000" + string(rune('0'+n)) }
	for _, n := range []int{5, 2, 8, 1, 7, 3, 6, 4} {
		f.askIn(where, id(n), carol, instant)
	}
	want := []string{id(1), id(2), id(3), id(4), id(5), id(6), id(7), id(8)}

	for name, c := range map[string]struct {
		settings []string
		sorted   bool
	}{
		"the plan the database chooses": {},
		"no index is read":              {settings: []string{"enable_indexscan", "enable_bitmapscan", "enable_indexonlyscan"}, sorted: true},
	} {
		t.Run(name, func(t *testing.T) {
			conn := pgtest.Connect(t, database.Application)
			for _, setting := range c.settings {
				if _, err := conn.Exec(t.Context(), "SET "+setting+" = off"); err != nil {
					t.Fatal(err)
				}
			}
			for _, status := range []string{"", "IN_REVIEW"} {
				statement, err := postgres.ListWorkspaceStatementForTest(status)
				if err != nil {
					t.Fatal(err)
				}
				// What is run is the statement of the queue, in a query that
				// keeps what a test needs to see: the order it returns.
				page := func(after *ports.Position, limit int) (found []string) {
					t.Helper()
					var at, identifier any
					if after != nil {
						at, identifier = after.At, after.ID
					}
					rows, err := conn.Query(t.Context(), "SELECT page.review_id::text FROM ("+statement+") AS page",
						where.product, where.workspace, at, identifier, "", "", limit)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					for rows.Next() {
						var review string
						if err := rows.Scan(&review); err != nil {
							t.Fatal(err)
						}
						found = append(found, review)
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					return found
				}
				// The plan is the one the test says it is: a page that is read
				// without an index is sorted.
				explained, err := conn.Query(t.Context(), "EXPLAIN "+statement, where.product, where.workspace, nil, nil, "", "", 100)
				if err != nil {
					t.Fatal(err)
				}
				var plan []string
				for explained.Next() {
					var line string
					if err := explained.Scan(&line); err != nil {
						t.Fatal(err)
					}
					plan = append(plan, line)
				}
				explained.Close()
				if c.sorted && (!strings.Contains(strings.Join(plan, "\n"), "Sort") || strings.Contains(strings.Join(plan, "\n"), "review_request_queue_order")) {
					t.Fatalf("the plan of the statement kept to %q was meant to sort what it reads and not to read the index:\n%s", status, strings.Join(plan, "\n"))
				}

				// The whole queue, and every page of it in sizes that end among
				// the reviews of the instant, each behind the one before.
				// Kept to the open reviews it is the same, as all are open.
				if got := page(nil, 100); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("status %q: the queue is %v, want %v", status, got, want)
				}
				for _, size := range []int{1, 2, 3, 5} {
					var read []string
					var after *ports.Position
					for len(read) < len(want) {
						got := page(after, size)
						if len(got) == 0 {
							break
						}
						read = append(read, got...)
						after = &ports.Position{At: instant, ID: got[len(got)-1]}
					}
					if strings.Join(read, ",") != strings.Join(want, ",") {
						t.Errorf("status %q: in pages of %d the queue is %v, want %v", status, size, read, want)
					}
				}
			}
		})
	}
}
