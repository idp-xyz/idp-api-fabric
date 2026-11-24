package postgres_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// checkBetween records a check of the revision as check does, with the
// instant it started at and the instant it concluded at named by the test: the
// two are not always in the order of the checks, as a check that took longer
// than another that began in the middle of it shows.
func (f *latestFixture) checkBetween(revision domain.RevisionRef, started, concluded time.Time, failing bool) domain.CheckRun {
	f.t.Helper()

	f.versions++
	tool := domain.Tool{Name: "fabric-openapi-rules", Version: fmt.Sprintf("t%d", f.versions)}
	observations := []domain.Observation{{RuleID: "F04-GOV-001"}}
	if failing {
		observations = append(observations, domain.Observation{RuleID: "F04-GOV-003", Findings: []domain.Finding{
			{RuleID: "F04-GOV-003", Path: "openapi.yaml", Pointer: "/paths", Line: 1, Message: "operationId is missing."},
		}})
	}
	var run domain.CheckRun
	err := f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		snapshot, err := f.store.Snapshot(ctx, revision.ProductID, f.policy, ids.NewUUID(), concluded)
		if err != nil {
			return err
		}
		run, err = domain.Complete(domain.Draft{
			ID: ids.NewUUID(), PolicySnapshotID: snapshot, Revision: revision,
			RequestedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "author"}, StartedAt: started,
		}, f.policy, f.source, tool, observations, concluded)
		if err != nil {
			return err
		}
		return f.store.Record(ctx, run)
	})
	if err != nil {
		f.t.Fatalf("record a check: %v", err)
	}
	return run
}

// The last check of a revision is the one that concluded last, and not the
// one that started last: the history of the revision lists the checks by when
// they concluded, and what a revision is said to have been last checked as is
// what that history ends with. A check that ran for two hours and one that
// began in the middle of it and was done in half an hour started in one order
// and concluded in the other, so here the check that started first is the
// last. Where the two orders are the same, the check that started last is.
func TestTheLastCheckOfARevisionIsTheOneConcludedLastAndNotTheOneThatStartedLast(t *testing.T) {
	f := newLatestFixture(t)
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	revision, other := f.revision(), f.revision()

	// It fails; the one that began in its middle does not conclude a failure.
	slow := f.checkBetween(revision, morning, morning.Add(2*time.Hour), true)
	quick := f.checkBetween(revision, morning.Add(time.Hour), morning.Add(90*time.Minute), false)
	if !slow.StartedAt.Before(quick.StartedAt) || !quick.CompletedAt.Before(slow.CompletedAt) || slow.Status == quick.Status {
		t.Fatalf("the checks are not those the case was made of: started at %v and %v, concluded at %v and %v, as %s and %s",
			slow.StartedAt, quick.StartedAt, slow.CompletedAt, quick.CompletedAt, slow.Status, quick.Status)
	}
	// A revision whose checks started and concluded in the same order.
	f.checkBetween(other, morning, morning.Add(time.Hour), false)
	late := f.checkBetween(other, morning.Add(30*time.Minute), morning.Add(3*time.Hour), true)

	found := f.latest([]domain.RevisionRef{revision, other}, "")
	if got := found[revision.RevisionID]; got.ID != slow.ID || !reflect.DeepEqual(got, slow.Overview()) {
		t.Errorf("the last check of the revision is %+v, want the one that concluded last, %+v, though %s started later", got, slow.Overview(), quick.ID)
	}
	if got := found[other.RevisionID]; got.ID != late.ID || !reflect.DeepEqual(got, late.Overview()) {
		t.Errorf("the last check of the other revision is %+v, want %+v", got, late.Overview())
	}
	// What is read is the last item of the history of the revision.
	history, err := f.store.List(t.Context(), revision, nil, 10)
	if err != nil || len(history) != 2 || !reflect.DeepEqual(found[revision.RevisionID], history[1]) {
		t.Errorf("the history of the revision is %+v (%v), want it to end with %+v", history, err, found[revision.RevisionID])
	}
	// A status is asked of the last check: the one that concluded FAIL last is
	// kept for FAIL, and not for what the one that started last concluded.
	if kept := f.latest([]domain.RevisionRef{revision}, domain.StatusFail); len(kept) != 1 || kept[revision.RevisionID].ID != slow.ID {
		t.Errorf("the last checks that concluded %s are %+v, want the check %s of the revision", domain.StatusFail, kept, slow.ID)
	}
	if kept := f.latest([]domain.RevisionRef{revision}, quick.Status); len(kept) != 0 {
		t.Errorf("the last checks that concluded %s are %+v, want none: the check that concluded so is not the last", quick.Status, kept)
	}
}
