package postgres_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

func TestARecordedCheckKeepsThePolicyOutcomesAndFindings(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)

	store := postgres.NewCheckRuns(fabric.Framework)
	revision := domain.RevisionRef{
		ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID(), RevisionID: ids.NewUUID(),
	}
	policy := domain.Baseline()
	tool := domain.Tool{Name: "fabric-openapi-rules", Version: "test"}
	toolDigest := domain.ToolDigest([]domain.Tool{tool})
	source := domain.Source{
		Entrypoint: "openapi.yaml",
		Digest:     "sha256:" + strings.Repeat("a", 64),
		Files:      []domain.SourceFile{{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"}},
	}
	at := time.Now().UTC()
	var original domain.CheckRun
	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if _, found, err := store.Recorded(ctx, revision, policy.Digest(), toolDigest); err != nil || found {
			t.Fatalf("look up the first check: found %v, error %v", found, err)
		}
		snapshot, err := store.Snapshot(ctx, revision.ProductID, policy, ids.NewUUID(), at)
		if err != nil {
			return err
		}
		original, err = domain.Complete(domain.Draft{
			ID: ids.NewUUID(), PolicySnapshotID: snapshot, Revision: revision,
			RequestedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "author"}, StartedAt: at,
		}, policy, source, tool, []domain.Observation{
			{RuleID: "F04-GOV-001"},
			{RuleID: "F04-GOV-003", Findings: []domain.Finding{
				{RuleID: "F04-GOV-003", Path: "openapi.yaml", Pointer: "/paths", Line: 1, Message: "operationId is missing."},
			}},
		}, at.Add(time.Second))
		if err != nil {
			return err
		}
		return store.Record(ctx, original)
	})
	if err != nil {
		t.Fatal(err)
	}

	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		again, found, err := store.Recorded(ctx, revision, policy.Digest(), toolDigest)
		if err != nil {
			return err
		}
		if !found || !reflect.DeepEqual(again, original) {
			t.Fatalf("recorded run does not match original: %+v, found %v", again, found)
		}
		snapshot, err := store.Snapshot(ctx, revision.ProductID, policy, ids.NewUUID(), at.Add(time.Minute))
		if err == nil && snapshot != original.PolicySnapshotID {
			t.Fatalf("the same policy used a new snapshot: %s", snapshot)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	got, found, err := store.Find(t.Context(), revision, original.ID)
	if err != nil || !found || !reflect.DeepEqual(got, original) {
		t.Fatalf("read the stored check: %+v, found %v, error %v", got, found, err)
	}
	history, err := store.List(t.Context(), revision, nil, 2)
	if err != nil || len(history) != 1 || !reflect.DeepEqual(history[0], original.Overview()) {
		t.Fatalf("history: %+v, error %v", history, err)
	}
	other := revision
	other.WorkspaceID = ids.NewUUID()
	if _, found, err := store.Find(t.Context(), other, original.ID); err != nil || found {
		t.Fatalf("another workspace could read the check: found %v, error %v", found, err)
	}
	app := pgtest.Connect(t, database.Application)
	if _, err := app.Exec(t.Context(), "UPDATE policyreview.check_run SET status = 'PASS' WHERE check_run_id = $1", original.ID); err == nil {
		t.Fatal("the application role can rewrite check evidence")
	}
}
