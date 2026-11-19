package application_test

import (
	"context"
	"errors"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// A revision is moved along its review where it is: under its asset, from
// the status it has. One the asset does not have is not found, and a move
// the review does not have is the conflict the model reports. The move
// authorizes nothing and leaves no record of its own: both are for the
// review it is made for.
func TestARevisionIsMovedOnlyWhereItIsAndAlongItsReview(t *testing.T) {
	const revisionID = "c0ffee00-0000-4000-8000-000000000001"
	holding := func() *registryDouble {
		revision := domain.Revision{ID: revisionID, Asset: shipments, Sequence: 1, Status: domain.RevisionDraft, StatusVersion: 1}
		return &registryDouble{recorded: &revision}
	}

	double := holding()
	registry := newRegistry(t, double)
	moved, err := registry.MoveRevision(context.Background(), shipments, revisionID, domain.RevisionInReview)
	if err != nil || moved.ID != revisionID || moved.Status != domain.RevisionInReview || moved.StatusVersion != 2 {
		t.Fatalf("a draft submitted: %+v, %v; want it in review at version 2", moved, err)
	}
	if _, err := registry.MoveRevision(context.Background(), shipments, revisionID, domain.RevisionInReview); !errors.Is(err, domain.ErrStatusConflict) {
		t.Errorf("a revision in review submitted: %v, want %v", err, domain.ErrStatusConflict)
	}
	if got := *double.recorded; got.Status != domain.RevisionInReview || got.StatusVersion != 2 {
		t.Errorf("after the refused move the revision is %s at version %d", got.Status, got.StatusVersion)
	}

	elsewhere := shipments
	elsewhere.AssetID = "another-asset"
	for name, move := range map[string]func(*application.Registry) error{
		"a revision nobody recorded": func(r *application.Registry) error {
			_, err := r.MoveRevision(context.Background(), shipments, "c0ffee00-0000-4000-8000-000000000002", domain.RevisionInReview)
			return err
		},
		"the revision under another asset": func(r *application.Registry) error {
			_, err := r.MoveRevision(context.Background(), elsewhere, revisionID, domain.RevisionInReview)
			return err
		},
	} {
		double := holding()
		if err := move(newRegistry(t, double)); !errors.Is(err, application.ErrRevisionNotFound) {
			t.Errorf("%s: %v, want %v", name, err, application.ErrRevisionNotFound)
		}
		if got := *double.recorded; got.Status != domain.RevisionDraft || got.StatusVersion != 1 {
			t.Errorf("%s: the revision is %s at version %d, want it untouched", name, got.Status, got.StatusVersion)
		}
	}
	if len(double.audited) != 0 || double.events != 0 {
		t.Errorf("the moves left %d audit records and %d events of their own", len(double.audited), double.events)
	}
}
