package main

import (
	"net/http"
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

const checkSource = `openapi: 3.1.2
info: {title: Shipments, version: v1}
security: []
paths:
  /shipments:
    get:
      responses:
        '200': {description: OK}
`

type checkFixture struct {
	*stack
	product    string
	workspace  string
	revisions  string
	membership string
}

func checksFor(t *testing.T) checkFixture {
	t.Helper()
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "workspace_maintainer")
	membership := s.join(product, workspace, "owner", "author", "internal_author")
	return checkFixture{
		stack: s, product: product, workspace: workspace, membership: membership,
		revisions: s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP"),
	}
}

func (s checkFixture) checkPath(source string) (string, answer) {
	s.t.Helper()
	revision := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", source))
	if revision.status != http.StatusCreated || revision.id("revision_id") == "" {
		s.t.Fatalf("import a revision to check: %d %v", revision.status, revision.body)
	}
	return s.revisions + "/" + revision.id("revision_id") + "/check-runs", revision
}

func TestARevisionCheckRecordsAndReusesItsEvidence(t *testing.T) {
	s := checksFor(t)
	path, revision := s.checkPath(checkSource)
	s.expect("only the current author can run a check", "owner", []exchange{
		{http.MethodPost, path, "", http.StatusForbidden, "FORBIDDEN"},
	})
	token := s.token("author")
	results := make(chan answer, 2)
	for range 2 {
		go func() { results <- s.callWith(http.MethodPost, path, token, "") }()
	}
	created, reused := <-results, <-results
	if reused.status == http.StatusCreated {
		created, reused = reused, created
	}
	if created.status != http.StatusCreated || reused.status != http.StatusOK ||
		created.header.Get("Cache-Control") != "no-store" || !reflect.DeepEqual(created.body, reused.body) {
		t.Fatalf("create and repeat a check: %d %v / %d %v", created.status, created.body, reused.status, reused.body)
	}
	run := created.body["check_run"].(map[string]any)
	if run["input_digest"] != revision.body["ref_closure_digest"] || run["status"] != "FAIL" ||
		run["revision_id"] != revision.id("revision_id") || run["policy_digest"] == "" || run["policy_snapshot_id"] == "" {
		t.Fatalf("check evidence: %v", run)
	}
	outcomes := map[string]any{}
	for _, raw := range created.body["outcomes"].([]any) {
		item := raw.(map[string]any)
		outcomes[item["rule_id"].(string)] = item["status"]
	}
	if outcomes["F04-GOV-003"] != "FAIL" || outcomes["F04-GOV-011"] != "PASS" ||
		outcomes["F04-GOV-012"] != "PASS" || outcomes["F04-GOV-013"] != "NOT_RUN" ||
		len(created.body["findings"].([]any)) == 0 {
		t.Fatalf("rule evidence: %v, findings %v", outcomes, created.body["findings"])
	}

	var events int
	err := s.owner.QueryRow(t.Context(), `
SELECT count(*) FROM bento.outbox
WHERE event_type = 'fabric.policyreview.check_run.completed' AND scope = $1
  AND payload->>'aggregate_id' = $2 AND payload->>'status' = 'FAIL'`,
		s.product, run["check_run_id"]).Scan(&events)
	audit := s.recorded(func(row audited) bool { return row.Action == "revision.check" && row.Outcome == "SUCCEEDED" })
	if err != nil || events != 1 || len(audit) != 1 || s.count("policyreview.check_run") != 1 {
		t.Fatalf("check run evidence: %d events, %d audit facts, error %v", events, len(audit), err)
	}
}

func TestARevisionCheckWaitsForAuthorRevocationAndRollsBackOnOutboxFailure(t *testing.T) {
	s := checksFor(t)
	path, _ := s.checkPath(checkSource)
	ctx := t.Context()
	conn := pgtest.Connect(t, s.database.Migrator)
	revoking, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revoking.Rollback(ctx) }()
	tag, err := revoking.Exec(ctx, `UPDATE productgovernance.workspace_membership
		SET revoked_at = greatest(granted_at, clock_timestamp()), revoked_by_issuer = $2,
		    revoked_by_subject = 'owner', membership_version = membership_version + 1
		WHERE membership_id = $1`, last(s.membership), s.issuer.URL())
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("stage the author revocation: %v %v", tag, err)
	}
	token := s.token("author")
	waiting := make(chan answer, 1)
	go func() { waiting <- s.callWith(http.MethodPost, path, token, "") }()
	s.waiting(1)
	if err := revoking.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := <-waiting; got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
		t.Fatalf("run the check after revocation: %d %v", got.status, got.body)
	}

	s.join(s.product, s.workspace, "owner", "author", "internal_author")
	if _, err := s.owner.Exec(ctx, `ALTER TABLE bento.outbox
		ADD CONSTRAINT reject_check_events CHECK (event_type <> 'fabric.policyreview.check_run.completed')`); err != nil {
		t.Fatal(err)
	}
	s.expect("an outbox error rolls back the evidence", "author", []exchange{
		{http.MethodPost, path, "", http.StatusInternalServerError, "INTERNAL_ERROR"},
	})
	for _, table := range []string{"policyreview.policy_snapshot", "policyreview.check_run", "policyreview.check_rule_outcome", "policyreview.check_finding"} {
		if count := s.count(table); count != 0 {
			t.Fatalf("failed check left %d rows in %s", count, table)
		}
	}
	if audits := s.recorded(func(row audited) bool {
		return row.Action == "revision.check" && row.Outcome == "SUCCEEDED"
	}); len(audits) != 0 {
		t.Fatalf("failed check left %d success audit facts", len(audits))
	}
}
