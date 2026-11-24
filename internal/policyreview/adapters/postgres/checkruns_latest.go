package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var _ ports.LatestChecks = (*CheckRuns)(nil)

// The last check of each of a set of revisions is read with one statement,
// however many there are: the revisions are the rows of a list, and each is
// looked up by the index of the checks of a revision, check_run_history,
// which leads with the product, the asset and the revision and then holds
// the checks in the order they were concluded. Read backwards it finds the
// last one without reading the others. The columns are those of the history
// of a revision, so that a check is read as a history reads it.
//
// The counts of the outcomes are taken only of the checks that are kept. A
// status that is asked for is a condition of the statement and not of what
// is made of the rows after it, so that the outcomes of a check that
// another status keeps out are not read: of the checks a status keeps out the
// stored status is read and not what the outcomes of the check add up to.
const latestOverviews = `
SELECT c.check_run_id, c.policy_snapshot_id, c.product_id, c.workspace_id, c.api_asset_id, c.revision_id,
       c.input_digest, c.tool_manifest, c.status, c.started_at, c.completed_at,
       c.requested_by_issuer, c.requested_by_subject, p.policy_set_id, p.policy_digest,
       o.passed, o.warned, o.failed, o.unknown, o.not_run, o.findings,
       coalesce(c.baseline_revision_id::text, ''), c.compatibility_document
FROM unnest($2::text[]::uuid[], $3::text[]::uuid[], $4::text[]::uuid[]) AS t(api_asset_id, workspace_id, revision_id)
CROSS JOIN LATERAL (
    SELECT * FROM policyreview.check_run
    WHERE product_id = $1 AND api_asset_id = t.api_asset_id AND workspace_id = t.workspace_id
      AND revision_id = t.revision_id
    ORDER BY completed_at DESC, check_run_id DESC
    LIMIT 1) c
JOIN policyreview.policy_snapshot p USING (product_id, policy_snapshot_id)
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE status = 'PASS') AS passed,
           count(*) FILTER (WHERE status = 'WARN') AS warned,
           count(*) FILTER (WHERE status = 'FAIL') AS failed,
           count(*) FILTER (WHERE status = 'UNKNOWN') AS unknown,
           count(*) FILTER (WHERE status = 'NOT_RUN') AS not_run,
           coalesce(sum(finding_count), 0) AS findings
    FROM policyreview.check_rule_outcome WHERE check_run_id = c.check_run_id
) o`

// The statement that keeps the checks of one status has its own text and not
// a condition that says "any" for an empty status: the planner knows what
// the status is when it plans, and no plan made for any status can be taken
// for this one.
const latestOverviewsOfStatus = latestOverviews + `
WHERE c.status = $5`

func (s *CheckRuns) Latest(ctx context.Context, product string, revisions []domain.RevisionRef, status domain.Status) ([]domain.Overview, error) {
	if len(revisions) == 0 {
		return nil, nil
	}
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	assets, workspaces, names := make([]string, 0, len(revisions)), make([]string, 0, len(revisions)), make([]string, 0, len(revisions))
	for _, revision := range revisions {
		assets, workspaces, names = append(assets, revision.AssetID), append(workspaces, revision.WorkspaceID), append(names, revision.RevisionID)
	}
	statement, arguments := latestOverviews, []any{product, assets, workspaces, names}
	if status != "" {
		statement, arguments = latestOverviewsOfStatus, append(arguments, string(status))
	}
	rows, err := reader.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("read the last check runs: %w", err)
	}
	return pgx.CollectRows(rows, overviewOf)
}
