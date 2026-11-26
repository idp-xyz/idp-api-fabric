package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

type Reviews struct {
	database *bentopostgres.DB
}

var _ ports.Reviews = (*Reviews)(nil)

func NewReviews(database *bentopostgres.DB) *Reviews {
	return &Reviews{database: database}
}

// A review is read under the product, the asset, the workspace and the
// revision it belongs to, never by its identifier alone. Its scope is read
// in the order the model keeps it in.
//
// reviewColumns are the columns of a review and where they come from, for
// every statement that reads reviews: what narrows them is the statement's
// own, so that a review is the same review wherever it is read.
const reviewColumns = `
SELECT q.review_id, q.product_id, q.workspace_id, q.api_asset_id, q.revision_id, q.check_run_id, q.input_digest,
       coalesce(q.note, ''), q.requested_at, q.requested_by_issuer, q.requested_by_subject, q.supplementary,
       ARRAY(SELECT s.audience || '/' || s.environment FROM policyreview.review_scope s
             WHERE s.review_id = q.review_id ORDER BY s.audience COLLATE "C", s.environment COLLATE "C"),
       c.outcome, coalesce(c.reason, ''), c.concluded_at, c.concluded_by_issuer, c.concluded_by_subject, c.valid_until,
       ARRAY(SELECT w.rule_id FROM policyreview.review_waiver w WHERE w.review_id = q.review_id ORDER BY w.rule_id COLLATE "C"),
       ARRAY(SELECT w.reason FROM policyreview.review_waiver w WHERE w.review_id = q.review_id ORDER BY w.rule_id COLLATE "C"),
       ARRAY(SELECT coalesce(w.compensating_control, '') FROM policyreview.review_waiver w
             WHERE w.review_id = q.review_id ORDER BY w.rule_id COLLATE "C"),
       ARRAY(SELECT w.expires_at FROM policyreview.review_waiver w WHERE w.review_id = q.review_id ORDER BY w.rule_id COLLATE "C")
FROM policyreview.review_request q
LEFT JOIN policyreview.review_conclusion c USING (review_id)`

// selectReview reads the reviews of one revision.
const selectReview = reviewColumns + `
WHERE q.product_id = $1 AND q.api_asset_id = $2 AND q.workspace_id = $3 AND q.revision_id = $4`

func scanReview(row pgx.CollectableRow) (domain.Review, error) {
	var (
		review                   domain.Review
		scope                    []string
		outcome                  *string
		reason                   string
		concludedAt, validUntil  *time.Time
		issuer, subject          *string
		rules, reasons, controls []string
		expiries                 []time.Time
	)
	request := &review.Request
	if err := row.Scan(&request.ID, &request.Revision.ProductID, &request.Revision.WorkspaceID, &request.Revision.AssetID,
		&request.Revision.RevisionID, &request.CheckRunID, &request.InputDigest, &request.Note, &request.RequestedAt,
		&request.RequestedBy.Issuer, &request.RequestedBy.Subject, &request.Supplementary, &scope,
		&outcome, &reason, &concludedAt, &issuer, &subject, &validUntil, &rules, &reasons, &controls, &expiries); err != nil {
		return domain.Review{}, err
	}
	request.RequestedAt = request.RequestedAt.UTC()
	for _, target := range scope {
		audience, environment, _ := strings.Cut(target, "/")
		request.Scope = append(request.Scope, domain.Target{Audience: audience, Environment: environment})
	}
	if outcome != nil && concludedAt != nil && issuer != nil && subject != nil {
		conclusion := &domain.Conclusion{
			ReviewID: request.ID, Outcome: domain.ReviewOutcome(*outcome), Reason: reason, ConcludedAt: concludedAt.UTC(),
			ConcludedBy: domain.Principal{Issuer: *issuer, Subject: *subject},
		}
		if validUntil != nil {
			conclusion.ValidUntil = validUntil.UTC()
		}
		for i, rule := range rules {
			conclusion.Waivers = append(conclusion.Waivers, domain.Waiver{
				RuleID: rule, Reason: reasons[i], CompensatingControl: controls[i], ExpiresAt: expiries[i].UTC(),
			})
		}
		review.Conclusion = conclusion
	} else if len(rules) > 0 {
		// Waivers of a review that has no conclusion are no review the
		// model recorded.
		return domain.Review{}, fmt.Errorf("review %s: %w", request.ID, domain.ErrReviewDamaged)
	}
	recorded, err := domain.RecordedReview(review)
	if err != nil {
		return domain.Review{}, fmt.Errorf("review %s: %w", request.ID, err)
	}
	return recorded, nil
}

func (s *Reviews) Find(ctx context.Context, revision domain.RevisionRef, id string) (domain.Review, bool, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return domain.Review{}, false, err
	}
	rows, err := reader.Query(ctx, selectReview+" AND q.review_id = $5",
		revision.ProductID, revision.AssetID, revision.WorkspaceID, revision.RevisionID, id)
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("find a review: %w", err)
	}
	reviews, err := pgx.CollectRows(rows, scanReview)
	if err != nil {
		return domain.Review{}, false, fmt.Errorf("read a review: %w", err)
	}
	if len(reviews) == 0 {
		return domain.Review{}, false, nil
	}
	return reviews[0], true, nil
}

func (s *Reviews) List(ctx context.Context, revision domain.RevisionRef, after *ports.Position, limit int) ([]domain.Review, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	const list = selectReview + `
  AND (q.requested_at, q.review_id) >
      (COALESCE($5::timestamptz, '-infinity'), COALESCE($6::uuid, '00000000-0000-0000-0000-000000000000'))
ORDER BY q.requested_at, q.review_id
LIMIT $7`
	var at, id any
	if after != nil {
		at, id = after.At, after.ID
	}
	rows, err := reader.Query(ctx, list, revision.ProductID, revision.AssetID, revision.WorkspaceID, revision.RevisionID, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	reviews, err := pgx.CollectRows(rows, scanReview)
	if err != nil {
		return nil, fmt.Errorf("read reviews: %w", err)
	}
	return reviews, nil
}
