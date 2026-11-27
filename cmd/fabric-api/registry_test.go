package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

const revisionSource = "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\npaths: {}\n"

func uploadRevision(entrypoint, content string) string {
	request := struct {
		Entrypoint string              `json:"entrypoint"`
		Files      []map[string]string `json:"files"`
	}{
		Entrypoint: entrypoint,
		Files: []map[string]string{
			{"path": entrypoint, "content_base64": base64.StdEncoding.EncodeToString([]byte(content))},
		},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (s *stack) revisionAsset(product, workspace, maintainer, key, kind string) string {
	s.t.Helper()
	path := "/v1/products/" + product + "/assets"
	asset := s.call(http.MethodPost, path, maintainer, assetRegistration(workspace, key, key, kind))
	if asset.status != http.StatusCreated || asset.id("api_asset_id") == "" {
		s.t.Fatalf("create the asset: %d %v", asset.status, asset.body)
	}
	return path + "/" + asset.id("api_asset_id") + "/revisions"
}

func TestContractRevisionImportUsesTheCurrentAssetAuthorAndDeduplicates(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "internal_author")
	other := s.createWorkspace(product, "owner", "operations")
	s.join(product, other, "owner", "elsewhere", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")
	body := uploadRevision("openapi.yaml", revisionSource)
	for _, caller := range []string{"owner", "maintainer"} {
		s.expect("an author grant is required", caller, []exchange{{http.MethodPost, path, body, http.StatusForbidden, "FORBIDDEN"}})
	}
	s.expect("a different Workspace author does not see this asset", "elsewhere", []exchange{
		{http.MethodPost, path, body, http.StatusNotFound, "API_ASSET_NOT_FOUND"},
	})
	s.expect("sources must meet the capture requirements", "author", []exchange{
		{http.MethodPost, path, uploadRevision("openapi.yaml", strings.Replace(revisionSource, "3.1.2", "3.2.1", 1)), http.StatusUnprocessableEntity, "UNSUPPORTED_SPEC_VERSION"},
		{http.MethodPost, path, uploadRevision("openapi.yaml", revisionSource+"schema: {$ref: 'https://example.test/contract'}\n"), http.StatusUnprocessableEntity, "CONTRACT_SOURCE_INVALID"},
		{http.MethodPost, path, `{"entrypoint":"openapi.yaml","files":[{"path":"openapi.yaml","content_base64":"!!!!"}]}`, http.StatusBadRequest, "REQUEST_INVALID"},
	})
	created := s.call(http.MethodPost, path, "author", body)
	retried := s.call(http.MethodPost, path, "author", body)
	if created.status != http.StatusCreated || created.id("revision_id") == "" || created.id("source_snapshot_id") == "" ||
		created.body["status"] != "DRAFT" || created.body["revision_sequence"] != float64(1) ||
		created.header.Get("Cache-Control") != "no-store" ||
		retried.status != http.StatusOK || !reflect.DeepEqual(created.body, retried.body) {
		t.Fatalf("created %d %v, retried %d %v", created.status, created.body, retried.status, retried.body)
	}
	eventPath := s.revisionAsset(product, workspace, "maintainer", "shipping-events", "EVENT")
	s.expect("OpenAPI imports apply to HTTP assets", "author", []exchange{
		{http.MethodPost, eventPath, body, http.StatusUnprocessableEntity, "UNSUPPORTED_SPEC_VERSION"},
	})

	var events int
	err := s.owner.QueryRow(t.Context(), `
SELECT count(*) FROM bento.outbox
WHERE event_type = 'fabric.contractregistry.revision.registered' AND scope = $1
  AND payload->>'aggregate_id' = $2 AND payload->>'ref_closure_digest' = $3`,
		product, created.id("revision_id"), created.body["ref_closure_digest"]).Scan(&events)
	audits := s.recorded(func(row audited) bool {
		return row.Action == "revision.create" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-01"
	})
	if err != nil || events != 1 || len(audits) != 1 || s.count("contractregistry.contract_revision") != 1 {
		t.Fatalf("registration evidence: %d events, %d audit facts, %v", events, len(audits), err)
	}
}

func TestAnImportWaitsForRevocationAndRollsBackOnOutboxFailure(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "workspace_maintainer")
	membership := s.join(product, workspace, "owner", "author", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")
	body := uploadRevision("openapi.yaml", revisionSource)

	ctx := t.Context()
	conn := pgtest.Connect(t, s.database.Migrator)
	revocation, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revocation.Rollback(ctx) }()
	tag, err := revocation.Exec(ctx, `UPDATE productgovernance.workspace_membership
		SET revoked_at = greatest(granted_at, clock_timestamp()), revoked_by_issuer = $2,
		    revoked_by_subject = 'owner', membership_version = membership_version + 1
		WHERE membership_id = $1`, last(membership), s.issuer.URL())
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("stage the author revocation: %v %v", tag, err)
	}
	token := s.token("author")
	pending := make(chan answer, 1)
	go func() { pending <- s.callWith(http.MethodPost, path, token, body) }()
	s.waiting(1)
	if err := revocation.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := <-pending; got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
		t.Fatalf("import after revocation: %d %v", got.status, got.body)
	}

	s.join(product, workspace, "owner", "author", "internal_author")
	if _, err := s.owner.Exec(ctx, `ALTER TABLE bento.outbox
		ADD CONSTRAINT reject_revision_events CHECK (event_type <> 'fabric.contractregistry.revision.registered')`); err != nil {
		t.Fatal(err)
	}
	s.expect("an outbox failure rolls back the import", "author", []exchange{
		{http.MethodPost, path, body, http.StatusInternalServerError, "INTERNAL_ERROR"},
	})
	for _, table := range []string{"contractregistry.contract_revision", "contractregistry.source_snapshot", "contractregistry.source_file"} {
		if count := s.count(table); count != 0 {
			t.Fatalf("failed import left %d rows in %s", count, table)
		}
	}
	if audits := s.recorded(func(row audited) bool {
		return row.Action == "revision.create" && row.Outcome == "SUCCEEDED"
	}); len(audits) != 0 {
		t.Fatalf("failed import left %d success audit facts", len(audits))
	}
}
