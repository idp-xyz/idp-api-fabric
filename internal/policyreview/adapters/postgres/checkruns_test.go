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
		if _, found, err := store.Recorded(ctx, revision, policy.Digest(), toolDigest, ""); err != nil || found {
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
		again, found, err := store.Recorded(ctx, revision, policy.Digest(), toolDigest, "")
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

	comparator := domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"}
	comparedTools := domain.ToolDigest([]domain.Tool{tool, comparator})
	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		for range 2 {
			evidence := &domain.ComparisonEvidence{
				Baseline:   domain.ComparisonBaseline{RevisionID: ids.NewUUID(), InputDigest: source.Digest},
				Tool:       comparator,
				Comparison: domain.Comparison{Classification: domain.Compatible},
			}
			baselineID := evidence.Baseline.RevisionID
			if _, found, err := store.Recorded(ctx, revision, policy.Digest(), comparedTools, baselineID); err != nil || found {
				t.Fatalf("lookup for a new baseline: found %v, error %v", found, err)
			}
			run, err := domain.Complete(domain.Draft{
				ID: ids.NewUUID(), PolicySnapshotID: original.PolicySnapshotID, Revision: revision,
				RequestedBy: original.RequestedBy, StartedAt: at, Compatibility: evidence,
			}, policy, source, tool, nil, at)
			if err != nil {
				return err
			}
			if err := store.Record(ctx, run); err != nil {
				return err
			}
			got, found, err := store.Recorded(ctx, revision, policy.Digest(), comparedTools, baselineID)
			if err != nil || !found || got.ID != run.ID || !reflect.DeepEqual(got.Compatibility, evidence) {
				t.Fatalf("stored comparison for %s: found %v, evidence %+v, error %v", baselineID, found, got.Compatibility, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := store.List(t.Context(), revision, nil, 4)
	if err != nil || len(runs) != 3 || runs[0].Compatibility == nil || runs[1].Compatibility == nil || runs[2].Compatibility != nil {
		t.Fatalf("comparison history: %+v, error %v", runs, err)
	}
}
