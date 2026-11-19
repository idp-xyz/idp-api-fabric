// Package postgres keeps the check runs of Policy & Review in PostgreSQL.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

type CheckRuns struct {
	database *bentopostgres.DB
}

var _ ports.CheckRuns = (*CheckRuns)(nil)

func NewCheckRuns(database *bentopostgres.DB) *CheckRuns {
	return &CheckRuns{database: database}
}

// A run is read under the product, the asset, the workspace and the revision
// it belongs to, never by its identifier alone.
const selectRun = `
SELECT c.check_run_id, c.policy_snapshot_id, c.product_id, c.workspace_id, c.api_asset_id, c.revision_id,
       c.input_digest, c.tool_manifest, c.status, c.started_at, c.completed_at,
       c.requested_by_issuer, c.requested_by_subject, p.policy_digest, p.policy_document,
       coalesce(c.baseline_revision_id::text, ''), c.compatibility_document
FROM policyreview.check_run c
JOIN policyreview.policy_snapshot p USING (product_id, policy_snapshot_id)
WHERE c.product_id = $1 AND c.api_asset_id = $2 AND c.workspace_id = $3 AND c.revision_id = $4`

func (s *CheckRuns) Recorded(ctx context.Context, revision domain.RevisionRef, policyDigest, toolDigest, baselineID string) (domain.CheckRun, bool, error) {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return domain.CheckRun{}, false, err
	}
	// The checks of a revision are recorded one after the other, so that
	// whoever finds none here and records one is the only one to do so.
	const lock = `SELECT pg_advisory_xact_lock(hashtextextended('policyreview/check_run/' || $1::uuid::text, 0))`
	if _, err := writer.Exec(ctx, lock, revision.RevisionID); err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("lock the checks of a revision: %w", err)
	}
	return s.read(ctx, writer, selectRun+" AND p.policy_digest = $5 AND c.tool_digest = $6 AND c.baseline_revision_id IS NOT DISTINCT FROM $7::uuid",
		revision.ProductID, revision.AssetID, revision.WorkspaceID, revision.RevisionID, policyDigest, toolDigest, optional(baselineID))
}

func (s *CheckRuns) Find(ctx context.Context, revision domain.RevisionRef, id string) (domain.CheckRun, bool, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return domain.CheckRun{}, false, err
	}
	return s.read(ctx, reader, selectRun+" AND c.check_run_id = $5",
		revision.ProductID, revision.AssetID, revision.WorkspaceID, revision.RevisionID, id)
}

// read returns the one run the statement selects, with its outcomes and its
// findings, as the model reads it back: a run that is not what was recorded
// is an error that names the run.
func (s *CheckRuns) read(ctx context.Context, reader bentopostgres.Querier, statement string, arguments ...any) (domain.CheckRun, bool, error) {
	rows, err := reader.Query(ctx, statement, arguments...)
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("find a check run: %w", err)
	}
	type stored struct {
		run                               domain.CheckRun
		baselineID                        string
		manifest, document, compatibility []byte
	}
	found, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (stored, error) {
		var read stored
		run := &read.run
		err := row.Scan(&run.ID, &run.PolicySnapshotID, &run.Revision.ProductID, &run.Revision.WorkspaceID, &run.Revision.AssetID,
			&run.Revision.RevisionID, &run.InputDigest, &read.manifest, &run.Status, &run.StartedAt, &run.CompletedAt,
			&run.RequestedBy.Issuer, &run.RequestedBy.Subject, &run.PolicyDigest, &read.document, &read.baselineID, &read.compatibility)
		return read, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CheckRun{}, false, nil
	}
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("read a check run: %w", err)
	}
	run := found.run
	run.StartedAt, run.CompletedAt = run.StartedAt.UTC(), run.CompletedAt.UTC()

	policy, err := domain.RecordedPolicy(string(found.document), run.PolicyDigest)
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("the policy of check run %s: %w", run.ID, err)
	}
	if run.Tools, err = domain.RecordedTools(string(found.manifest)); err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("the tools of check run %s: %w", run.ID, err)
	}
	if run.Compatibility, err = readCompatibility(found.baselineID, found.compatibility); err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("the comparison of check run %s: %w", run.ID, err)
	}

	const outcomes = `
SELECT rule_id, status, finding_count, coalesce(tool_name, ''), coalesce(tool_version, '')
FROM policyreview.check_rule_outcome WHERE check_run_id = $1 ORDER BY rule_position`
	rows, err = reader.Query(ctx, outcomes, run.ID)
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("read the outcomes of a check run: %w", err)
	}
	run.Outcomes, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Outcome, error) {
		var outcome domain.Outcome
		err := row.Scan(&outcome.Rule.ID, &outcome.Status, &outcome.Findings, &outcome.Tool.Name, &outcome.Tool.Version)
		return outcome, err
	})
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("read the outcomes of a check run: %w", err)
	}

	const findings = `
SELECT rule_id, level, file_path, coalesce(pointer, ''), coalesce(line, 0), message
FROM policyreview.check_finding WHERE check_run_id = $1 ORDER BY ordinal`
	rows, err = reader.Query(ctx, findings, run.ID)
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("read the findings of a check run: %w", err)
	}
	run.Findings, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Finding, error) {
		var finding domain.Finding
		err := row.Scan(&finding.RuleID, &finding.Level, &finding.Path, &finding.Pointer, &finding.Line, &finding.Message)
		return finding, err
	})
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("read the findings of a check run: %w", err)
	}

	recorded, err := domain.Recorded(run, policy)
	if err != nil {
		return domain.CheckRun{}, false, fmt.Errorf("check run %s: %w", run.ID, err)
	}
	return recorded, true, nil
}

func readCompatibility(baselineID string, document []byte) (*domain.ComparisonEvidence, error) {
	if baselineID == "" && document == nil {
		return nil, nil
	}
	var evidence domain.ComparisonEvidence
	if err := json.Unmarshal(document, &evidence); err != nil || baselineID == "" || evidence.Baseline.RevisionID != baselineID {
		return nil, domain.ErrCheckRunDamaged
	}
	canonical, err := json.Marshal(evidence)
	if err != nil || !bytes.Equal(canonical, document) {
		return nil, domain.ErrCheckRunDamaged
	}
	return &evidence, nil
}

func (s *CheckRuns) Snapshot(ctx context.Context, productID string, policy domain.Policy, id string, at time.Time) (string, error) {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return "", err
	}
	// Whoever records the snapshot of a policy for a product first records
	// it for everyone: a second statement waits for the first transaction
	// and then finds its row.
	const insert = `
INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (product_id, policy_digest) DO NOTHING`
	document := policy.Document()
	if _, err := writer.Exec(ctx, insert, id, productID, policy.SetID(), policy.Digest(), []byte(document),
		at.UTC().Truncate(time.Microsecond)); err != nil {
		return "", fmt.Errorf("store a policy snapshot: %w", err)
	}
	const recorded = `
SELECT policy_snapshot_id, policy_document FROM policyreview.policy_snapshot WHERE product_id = $1 AND policy_digest = $2`
	var snapshot string
	var stored []byte
	if err := writer.QueryRow(ctx, recorded, productID, policy.Digest()).Scan(&snapshot, &stored); err != nil {
		return "", fmt.Errorf("read a policy snapshot: %w", err)
	}
	if string(stored) != document {
		return "", fmt.Errorf("policy snapshot %s: %w", snapshot, domain.ErrPolicyDamaged)
	}
	return snapshot, nil
}

func (s *CheckRuns) Record(ctx context.Context, run domain.CheckRun) error {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	const insertRun = `
INSERT INTO policyreview.check_run (
    check_run_id, product_id, workspace_id, api_asset_id, revision_id, policy_snapshot_id, input_digest,
    tool_manifest, tool_digest, status, started_at, completed_at, requested_by_issuer, requested_by_subject,
    baseline_revision_id, compatibility_document
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
	var baselineID *string
	var compatibility []byte
	if run.Compatibility != nil {
		baselineID = &run.Compatibility.Baseline.RevisionID
		compatibility, err = json.Marshal(run.Compatibility)
		if err != nil {
			return fmt.Errorf("encode the compatibility evidence: %w", err)
		}
	}
	revision := run.Revision
	if _, err := writer.Exec(ctx, insertRun, run.ID, revision.ProductID, revision.WorkspaceID, revision.AssetID, revision.RevisionID,
		run.PolicySnapshotID, run.InputDigest, []byte(domain.ToolManifest(run.Tools)), domain.ToolDigest(run.Tools), string(run.Status),
		run.StartedAt, run.CompletedAt, run.RequestedBy.Issuer, run.RequestedBy.Subject, baselineID, compatibility); err != nil {
		return fmt.Errorf("store a check run: %w", err)
	}

	// The outcomes and the findings go in one statement each: a run has an
	// outcome for every rule of its policy and may have hundreds of
	// findings.
	var (
		rules, statuses []string
		positions       []int32
		counts          []int32
		names, versions []*string
	)
	for position, outcome := range run.Outcomes {
		rules, statuses = append(rules, outcome.Rule.ID), append(statuses, string(outcome.Status))
		positions, counts = append(positions, int32(position)), append(counts, int32(outcome.Findings))
		names, versions = append(names, optional(outcome.Tool.Name)), append(versions, optional(outcome.Tool.Version))
	}
	const insertOutcomes = `
INSERT INTO policyreview.check_rule_outcome (check_run_id, rule_id, rule_position, status, finding_count, tool_name, tool_version)
SELECT $1::uuid, * FROM unnest($2::text[], $3::integer[], $4::text[], $5::integer[], $6::text[], $7::text[])`
	if _, err := writer.Exec(ctx, insertOutcomes, run.ID, rules, positions, statuses, counts, names, versions); err != nil {
		return fmt.Errorf("store the outcomes of a check run: %w", err)
	}
	if len(run.Findings) == 0 {
		return nil
	}

	var (
		ordinals                      []int32
		ofRules, levels, files, texts []string
		pointers                      []*string
		lines                         []*int32
	)
	for index, finding := range run.Findings {
		ordinals = append(ordinals, int32(index+1))
		ofRules, levels = append(ofRules, finding.RuleID), append(levels, string(finding.Level))
		files, texts = append(files, finding.Path), append(texts, finding.Message)
		pointers = append(pointers, optional(finding.Pointer))
		var line *int32
		if finding.Line > 0 {
			known := int32(finding.Line)
			line = &known
		}
		lines = append(lines, line)
	}
	const insertFindings = `
INSERT INTO policyreview.check_finding (check_run_id, ordinal, rule_id, level, file_path, pointer, line, message)
SELECT $1::uuid, * FROM unnest($2::integer[], $3::text[], $4::text[], $5::text[], $6::text[], $7::integer[], $8::text[])`
	if _, err := writer.Exec(ctx, insertFindings, run.ID, ordinals, ofRules, levels, files, pointers, lines, texts); err != nil {
		return fmt.Errorf("store the findings of a check run: %w", err)
	}
	return nil
}

// optional stores what is not said as NULL.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *CheckRuns) List(ctx context.Context, revision domain.RevisionRef, after *ports.Position, limit int) ([]domain.Overview, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	// The counts are taken from the outcomes, not kept beside them: a
	// history shows what the outcomes of a run add up to.
	const list = `
SELECT c.check_run_id, c.policy_snapshot_id, c.product_id, c.workspace_id, c.api_asset_id, c.revision_id,
       c.input_digest, c.tool_manifest, c.status, c.started_at, c.completed_at,
       c.requested_by_issuer, c.requested_by_subject, p.policy_set_id, p.policy_digest,
       o.passed, o.warned, o.failed, o.unknown, o.not_run, o.findings,
       coalesce(c.baseline_revision_id::text, ''), c.compatibility_document
FROM policyreview.check_run c
JOIN policyreview.policy_snapshot p USING (product_id, policy_snapshot_id)
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE status = 'PASS') AS passed,
           count(*) FILTER (WHERE status = 'WARN') AS warned,
           count(*) FILTER (WHERE status = 'FAIL') AS failed,
           count(*) FILTER (WHERE status = 'UNKNOWN') AS unknown,
           count(*) FILTER (WHERE status = 'NOT_RUN') AS not_run,
           coalesce(sum(finding_count), 0) AS findings
    FROM policyreview.check_rule_outcome WHERE check_run_id = c.check_run_id
) o
WHERE c.product_id = $1 AND c.api_asset_id = $2 AND c.workspace_id = $3 AND c.revision_id = $4
  AND (c.completed_at, c.check_run_id) >
      (COALESCE($5::timestamptz, '-infinity'), COALESCE($6::uuid, '00000000-0000-0000-0000-000000000000'))
ORDER BY c.completed_at, c.check_run_id
LIMIT $7`
	var at, id any
	if after != nil {
		at, id = after.At, after.ID
	}
	rows, err := reader.Query(ctx, list, revision.ProductID, revision.AssetID, revision.WorkspaceID, revision.RevisionID, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list check runs: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Overview, error) {
		var overview domain.Overview
		var manifest, compatibility []byte
		var baselineID string
		summary := &overview.Summary
		if err := row.Scan(&overview.ID, &overview.PolicySnapshotID, &overview.Revision.ProductID, &overview.Revision.WorkspaceID,
			&overview.Revision.AssetID, &overview.Revision.RevisionID, &overview.InputDigest, &manifest, &overview.Status,
			&overview.StartedAt, &overview.CompletedAt, &overview.RequestedBy.Issuer, &overview.RequestedBy.Subject,
			&overview.PolicySetID, &overview.PolicyDigest,
			&summary.Pass, &summary.Warn, &summary.Fail, &summary.Unknown, &summary.NotRun, &summary.Findings,
			&baselineID, &compatibility); err != nil {
			return domain.Overview{}, err
		}
		overview.StartedAt, overview.CompletedAt = overview.StartedAt.UTC(), overview.CompletedAt.UTC()
		tools, err := domain.RecordedTools(string(manifest))
		if err != nil {
			return domain.Overview{}, fmt.Errorf("the tools of check run %s: %w", overview.ID, err)
		}
		overview.Tools = tools
		if overview.Compatibility, err = readCompatibility(baselineID, compatibility); err != nil {
			return domain.Overview{}, fmt.Errorf("the comparison of check run %s: %w", overview.ID, err)
		}
		recorded, err := domain.RecordedOverview(overview)
		if err != nil {
			return domain.Overview{}, fmt.Errorf("check run %s: %w", overview.ID, err)
		}
		return recorded, nil
	})
}
