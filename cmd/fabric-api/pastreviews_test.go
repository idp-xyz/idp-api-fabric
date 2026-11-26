package main

import (
	"net/http"
	"strings"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// No review is asked for, withdrawn or decided any more, and nobody is made a
// reviewer (CR-0001). What a workspace holds of them from before is read as it
// was: the reviewers that were granted, the reviews that were asked for and
// how they ended, and the releases prepared under an approval. The helpers of
// this file put such a past in the tables, as the build before wrote it, so
// that the reads of it can be held to what they answer.

// unnamedSource is a contract whose one operation has no operationId: a
// rule that can be waived fails (F04-GOV-003), and no rule that nothing
// waives does.
const unnamedSource = `openapi: 3.1.2
info: {title: Shipments, version: v1}
security: [{token: []}]
paths:
  /shipments:
    get:
      responses:
        '200': {description: OK}
components:
  securitySchemes:
    token: {type: http, scheme: bearer}
`

// reviewerBefore makes the subject an internal_reviewer of the workspace, as
// the owner granted it before the role stopped being granted. It returns
// where the membership is read and revoked.
func (s *stack) reviewerBefore(productID, workspaceID, owner, subject string) string {
	s.t.Helper()

	id := ids.NewUUID()
	if _, err := s.owner.Exec(s.t.Context(), `
INSERT INTO productgovernance.workspace_membership (
    membership_id, product_id, workspace_id, principal_issuer, principal_subject, role, membership_version,
    granted_at, granted_by_issuer, granted_by_subject
) VALUES ($1, $2, $3, $4, $5, 'internal_reviewer', 1, now(), $4, $6)`,
		id, productID, workspaceID, s.issuer.URL(), subject, owner); err != nil {
		s.t.Fatalf("make %s a reviewer as it was made before: %v", subject, err)
	}
	return "/v1/products/" + productID + "/workspaces/" + workspaceID + "/memberships/" + id
}

// submitted is a revision that was put in review: where it and its review are
// read, the check the review rests on and what the import answered.
type submitted struct {
	revision, review, check string
	imported                answer
}

// inReview imports a revision of the source and checks it, each through the
// API as the member that is named, and records the review the requester asked
// for on that check, for the partner sandbox.
func (s *reviewFixture) inReview(source, recorder, checker, requester string) submitted {
	s.t.Helper()
	imported := s.call(http.MethodPost, s.revisions, recorder, uploadRevision("openapi.yaml", source))
	if imported.status != http.StatusCreated {
		s.t.Fatalf("import a revision as %s: %d %v", recorder, imported.status, imported.body)
	}
	revision := s.revisions + "/" + imported.id("revision_id")
	check := s.call(http.MethodPost, revision+"/check-runs", checker, "")
	if check.status != http.StatusCreated {
		s.t.Fatalf("check the revision as %s: %d %v", checker, check.status, check.body)
	}
	id := check.body["check_run"].(map[string]any)["check_run_id"].(string)
	return submitted{revision: revision, review: s.asked(revision, id, requester, partnerSandbox), check: id, imported: imported}
}

// asked records a review of the revision that the requester asked for on the
// check, for the scope, and returns where it is read. It is recorded as the
// build before recorded one: a revision that was approved is reviewed again
// in a supplementary review, which moves nothing, and any other revision is in
// review from then on, at the next version of its status.
func (s *reviewFixture) asked(revision, check, requester string, scope ...[2]string) string {
	s.t.Helper()
	revisionID := last(revision)
	var status string
	if err := s.owner.QueryRow(s.t.Context(),
		`SELECT status FROM contractregistry.contract_revision WHERE revision_id = $1`, revisionID).Scan(&status); err != nil {
		s.t.Fatalf("read the revision %s: %v", revisionID, err)
	}
	supplementary := status == "APPROVED"
	if !supplementary {
		s.moveRevision(revisionID, "IN_REVIEW")
	}
	id := ids.NewUUID()
	if _, err := s.owner.Exec(s.t.Context(), `
INSERT INTO policyreview.review_request (
    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
    input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary
)
SELECT $1, r.product_id, r.workspace_id, r.api_asset_id, r.revision_id, r.status_version, c.check_run_id,
       c.input_digest, 'ready for partners', clock_timestamp(), $4, $5, $6
FROM contractregistry.contract_revision r
JOIN policyreview.check_run c ON c.revision_id = r.revision_id AND c.check_run_id = $3
WHERE r.revision_id = $2`, id, revisionID, check, s.issuer.URL(), requester, supplementary); err != nil {
		s.t.Fatalf("record the review of %s: %v", revisionID, err)
	}
	for _, target := range scope {
		if _, err := s.owner.Exec(s.t.Context(),
			`INSERT INTO policyreview.review_scope (review_id, audience, environment) VALUES ($1, $2, $3)`, id, target[0], target[1]); err != nil {
			s.t.Fatalf("record the scope of the review %s: %v", id, err)
		}
	}
	return revision + "/reviews/" + id
}

// concluded records how the open review at the path ended: the outcome, who
// ended it and why, and for an approval the rules it waived, each for a reason
// and until a time inside the approval. A review that was not supplementary
// takes its revision to the outcome, as the build before moved it. It returns
// the review as whoever concluded it reads it.
func (s *reviewFixture) concluded(review, by, outcome, reason string, waived ...string) answer {
	s.t.Helper()
	id := last(review)
	var optional *string
	if reason != "" {
		optional = &reason
	}
	if _, err := s.owner.Exec(s.t.Context(), `
INSERT INTO policyreview.review_conclusion (
    review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until
) VALUES ($1, $2, $3, clock_timestamp(), $4, $5,
          CASE WHEN $2 = 'APPROVED' THEN date_trunc('second', clock_timestamp()) + interval '90 days' END)`,
		id, outcome, optional, s.issuer.URL(), by); err != nil {
		s.t.Fatalf("conclude the review %s: %v", id, err)
	}
	for _, rule := range waived {
		if _, err := s.owner.Exec(s.t.Context(), `
INSERT INTO policyreview.review_waiver (review_id, rule_id, reason, expires_at)
SELECT review_id, $2, 'generated clients are not used yet', valid_until - interval '30 days'
FROM policyreview.review_conclusion WHERE review_id = $1`, id, rule); err != nil {
			s.t.Fatalf("waive %s in the review %s: %v", rule, id, err)
		}
	}
	var revisionID string
	var supplementary bool
	if err := s.owner.QueryRow(s.t.Context(),
		`SELECT revision_id::text, supplementary FROM policyreview.review_request WHERE review_id = $1`, id).Scan(&revisionID, &supplementary); err != nil {
		s.t.Fatalf("read the review %s: %v", id, err)
	}
	if !supplementary {
		s.moveRevision(revisionID, outcome)
	}
	got := s.call(http.MethodGet, review, by, "")
	if got.status != http.StatusOK {
		s.t.Fatalf("read the review %s as %s: %d %v", id, by, got.status, got.body)
	}
	return got
}

// moveRevision moves the status of the revision one step, at the next version
// of its status, which is the one move the table lets anybody make.
func (s *reviewFixture) moveRevision(revisionID, to string) {
	s.t.Helper()
	tag, err := s.owner.Exec(s.t.Context(), `
UPDATE contractregistry.contract_revision SET status = $2, status_version = status_version + 1 WHERE revision_id = $1`,
		revisionID, to)
	if err != nil || tag.RowsAffected() != 1 {
		s.t.Fatalf("move the revision %s to %s: %v, %d rows", revisionID, to, err, tag.RowsAffected())
	}
}

// releasedUnderApproval records a release of the revision under the version,
// as the build before prepared one: under the review that approved it, on the
// check that review rests on, with the bytes of a release prepared since of
// the same revision (sameBytes), which are the bytes it would have had. It
// returns the identifier of the release.
func (s *reviewFixture) releasedUnderApproval(review, sameBytes, version, publisher string) string {
	s.t.Helper()
	id := ids.NewUUID()
	tag, err := s.owner.Exec(s.t.Context(), `
INSERT INTO contractregistry.contract_release (
    release_id, product_id, workspace_id, api_asset_id, revision_id, review_id, check_run_id,
    policy_snapshot_id, policy_digest, input_digest, artifact_digest, release_version,
    prepared_at, prepared_by_issuer, prepared_by_subject
)
SELECT $1, l.product_id, l.workspace_id, l.api_asset_id, q.revision_id, q.review_id, q.check_run_id,
       c.policy_snapshot_id, p.policy_digest, l.input_digest, l.artifact_digest, $4, clock_timestamp(), $5, $6
FROM policyreview.review_request q
JOIN policyreview.check_run c USING (check_run_id)
JOIN policyreview.policy_snapshot p ON p.policy_snapshot_id = c.policy_snapshot_id
JOIN contractregistry.contract_release l ON l.release_id = $3 AND l.revision_id = q.revision_id
WHERE q.review_id = $2`, id, last(review), sameBytes, version, s.issuer.URL(), publisher)
	if err != nil || tag.RowsAffected() != 1 {
		s.t.Fatalf("record a release under the review %s: %v, %d rows", last(review), err, tag.RowsAffected())
	}
	return id
}

// pathOf is the path of a revision of the fixture, from its identifier.
func (s *reviewFixture) pathOf(revisionID string) string {
	return s.revisions + "/" + strings.TrimPrefix(revisionID, s.revisions+"/")
}
