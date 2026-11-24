package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var _ ports.ReviewQueue = (*Reviews)(nil)

// A page of the reviews of one workspace, oldest first: by the instant a
// review was asked for and then by its identifier, which tells apart the
// reviews of one instant. A request is written once and never changed, so
// neither column ever changes: a review keeps its place for as long as it is
// listed, and a page starts behind the last review of the page before,
// wherever the reviews around it are by then. What is asked for while the
// list is read is later than any position that was handed out and comes
// after it: it repeats nothing and takes nothing from what is behind.
//
// The product and the workspace are in the statement ahead of the position
// and of the limit, so that no page is cut from the reviews of others and then
// loses them: every page but the last is full, and a review of another
// workspace is in none. The index of the list (migration 006) is in the order
// of the columns, so a page that no filter narrows costs what it returns,
// however far into the queue it is. A status is no column: a review is open
// when it has no conclusion, and otherwise stands as its conclusion ended, so
// a page kept to one costs the reviews it scans for the ones it returns.
//
// The position is compared as a pair, the way the order compares it: a page
// that names none starts behind the first instant there could be and the
// smallest identifier, so one statement reads the first page as it reads the
// others. A filter that is not set is the empty string and lets everything
// through; one that is set is an exact match. The columns are those of every
// read of a review, and so is the way a row is read back.
//
// $1 and $2 are the product and the workspace, $3 and $4 the position, $5 and
// $6 who asked for the review, and $7 how many reviews the page takes. The
// status is a piece of the statement that is chosen and never built from what
// was said (see statusConditions).
const (
	listWorkspaceHead = reviewColumns + `
WHERE q.product_id = $1 AND q.workspace_id = $2`

	listWorkspaceTail = `
  AND ($5::text = '' OR q.requested_by_issuer = $5)
  AND ($6::text = '' OR q.requested_by_subject = $6)
  AND (q.requested_at, q.review_id) >
      (COALESCE($3::timestamptz, '-infinity'), COALESCE($4::uuid, '00000000-0000-0000-0000-000000000000'))
ORDER BY q.requested_at, q.review_id
LIMIT $7`
)

// statusConditions are what keeps a page to a status, one piece of statement
// for each there is. An open review is one whose conclusion is not there,
// which the join says; the rest are the outcomes of a conclusion. Nothing a
// caller says reaches the text of a statement: a status that is none of
// these is an error and does not let every review through.
var statusConditions = map[string]string{
	"":                             "",
	domain.ReviewOpen:              "\n  AND c.review_id IS NULL",
	string(domain.ReviewWithdrawn): "\n  AND c.outcome = 'WITHDRAWN'",
	string(domain.ReviewApproved):  "\n  AND c.outcome = 'APPROVED'",
	string(domain.ReviewRejected):  "\n  AND c.outcome = 'REJECTED'",
}

// listWorkspaceStatement is the statement of the queue kept to the status.
func listWorkspaceStatement(status string) (string, error) {
	condition, known := statusConditions[status]
	if !known {
		return "", fmt.Errorf("list the reviews of a workspace: %q is no status of a review", status)
	}
	return listWorkspaceHead + condition + listWorkspaceTail, nil
}

// ListInWorkspace returns one page of the reviews of the workspace that the
// filter lets through, oldest first.
func (s *Reviews) ListInWorkspace(ctx context.Context, productID, workspaceID string, filter ports.ReviewFilter, after *ports.Position, limit int) ([]domain.Review, error) {
	if limit < 1 {
		return nil, errors.New("list the reviews of a workspace: a page takes at least one review")
	}
	statement, err := listWorkspaceStatement(filter.Status)
	if err != nil {
		return nil, err
	}
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	// A page that names no position starts behind the first there could be.
	var at, id any
	if after != nil {
		at, id = after.At, after.ID
	}
	rows, err := reader.Query(ctx, statement, productID, workspaceID, at, id,
		filter.RequestedByIssuer, filter.RequestedBySubject, limit)
	if err != nil {
		return nil, fmt.Errorf("list the reviews of a workspace: %w", err)
	}
	reviews, err := pgx.CollectRows(rows, scanReview)
	if err != nil {
		return nil, fmt.Errorf("read the reviews of a workspace: %w", err)
	}
	return reviews, nil
}
