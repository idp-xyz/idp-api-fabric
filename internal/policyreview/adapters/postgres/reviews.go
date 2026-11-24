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

func (s *Reviews) Request(ctx context.Context, request domain.ReviewRequest, statusVersion int64) error {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const insertRequest = `
INSERT INTO policyreview.review_request (
    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
    input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	revision := request.Revision
	if _, err := writer.Exec(ctx, insertRequest, request.ID, revision.ProductID, revision.WorkspaceID, revision.AssetID,
		revision.RevisionID, statusVersion, request.CheckRunID, request.InputDigest, optional(request.Note),
		request.RequestedAt, request.RequestedBy.Issuer, request.RequestedBy.Subject, request.Supplementary); err != nil {
		return fmt.Errorf("store a review request: %w", err)
	}
	var audiences, environments []string
	for _, target := range request.Scope {
		audiences, environments = append(audiences, target.Audience), append(environments, target.Environment)
	}
	const insertScope = `
INSERT INTO policyreview.review_scope (review_id, audience, environment)
SELECT $1::uuid, * FROM unnest($2::text[], $3::text[])`
	if _, err := writer.Exec(ctx, insertScope, request.ID, audiences, environments); err != nil {
		return fmt.Errorf("store the scope of a review: %w", err)
	}
	return nil
}

func (s *Reviews) Conclude(ctx context.Context, conclusion domain.Conclusion) error {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const insert = `
INSERT INTO policyreview.review_conclusion (
    review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until
) VALUES ($1,$2,$3,$4,$5,$6,$7)`
	var validUntil *time.Time
	if !conclusion.ValidUntil.IsZero() {
		validUntil = &conclusion.ValidUntil
	}
	if _, err := writer.Exec(ctx, insert, conclusion.ReviewID, string(conclusion.Outcome), optional(conclusion.Reason),
		conclusion.ConcludedAt, conclusion.ConcludedBy.Issuer, conclusion.ConcludedBy.Subject, validUntil); err != nil {
		return fmt.Errorf("store the conclusion of a review: %w", err)
	}
	if len(conclusion.Waivers) == 0 {
		return nil
	}
	var (
		rules, reasons []string
		controls       []*string
		expiries       []time.Time
	)
	for _, waiver := range conclusion.Waivers {
		rules, reasons = append(rules, waiver.RuleID), append(reasons, waiver.Reason)
		controls, expiries = append(controls, optional(waiver.CompensatingControl)), append(expiries, waiver.ExpiresAt)
	}
	const insertWaivers = `
INSERT INTO policyreview.review_waiver (review_id, rule_id, reason, compensating_control, expires_at)
SELECT $1::uuid, * FROM unnest($2::text[], $3::text[], $4::text[], $5::timestamptz[])`
	if _, err := writer.Exec(ctx, insertWaivers, conclusion.ReviewID, rules, reasons, controls, expiries); err != nil {
		return fmt.Errorf("store the waivers of a review: %w", err)
	}
	return nil
}

// Open reports whether a request of the revision has no conclusion. It says
// what the transaction it is called in sees: who decides on the answer
// holds the revision first, so that no other request is recorded meanwhile.
func (s *Reviews) Open(ctx context.Context, revision domain.RevisionRef) (bool, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return false, err
	}
	const open = `
SELECT EXISTS (
    SELECT 1 FROM policyreview.review_request q
    WHERE q.product_id = $1 AND q.api_asset_id = $2 AND q.workspace_id = $3 AND q.revision_id = $4
      AND NOT EXISTS (SELECT 1 FROM policyreview.review_conclusion c WHERE c.review_id = q.review_id))`
	var found bool
	if err := reader.QueryRow(ctx, open, revision.ProductID, revision.AssetID, revision.WorkspaceID, revision.RevisionID).Scan(&found); err != nil {
		return false, fmt.Errorf("look for an open review: %w", err)
	}
	return found, nil
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
