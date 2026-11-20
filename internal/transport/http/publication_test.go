package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	reviewdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

const (
	publicationTargetID = "2b3c4d5e-6f70-4a81-9b2c-3d4e5f6a7b8c"
	publicationSlotID   = "4d5e6f70-8192-4a3b-8c4d-5e6f7a8b9c0d"
	releaseID           = "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e2f"
	// publicationSlot is where the slot of the asset on the target is read.
	publicationSlot = "/v1/products/" + productID + "/publication-targets/" + publicationTargetID + "/assets/" + apiAssetID
)

// publications stands in for the use cases of publication. It answers the
// slot the test set, and refuses a change with the error the test set.
type publications struct {
	Publications

	slot domain.SlotView
	err  error

	read    []domain.Request
	changed []domain.Change
}

func (p *publications) GetSlot(_ context.Context, request domain.Request) (domain.SlotView, error) {
	p.read = append(p.read, request)
	return p.slot, p.err
}

func (p *publications) ChangeSlot(_ context.Context, _ domain.Request, change domain.Change) (domain.Operation, error) {
	p.changed = append(p.changed, change)
	return domain.Operation{}, p.err
}

func newPublicationService(t *testing.T, fake *publications) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: unusedRegistry{}, PolicyReview: unusedPolicyReview{},
		Releases: unusedReleases{}, Publication: fake,
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// activeSlot is a slot that points at a release, at its third generation.
func activeSlot() domain.SlotView {
	return domain.SlotView{Slot: domain.Slot{
		ID: publicationSlotID, ProductID: productID, TargetID: publicationTargetID, AssetID: apiAssetID,
		ActiveReleaseID: releaseID, Generation: 3,
	}}
}

// A slot says why the approval of the release it points at does not hold, in
// the words an activation of that release is refused with: the same cause
// has the same code in both. A slot whose release is approved says nothing
// of it, and a cause the contract has no word for on a slot is a failure of
// the service.
func TestASlotSaysWhyTheApprovalOfItsReleaseDoesNotHoldAsAnActivationWould(t *testing.T) {
	shown := map[string]any{
		"slot_id": publicationSlotID, "publication_target_id": publicationTargetID, "api_asset_id": apiAssetID,
		"active_release_id": releaseID, "generation": float64(3), "visibility": "ACTIVE", "blocked": false,
		"delivery_status": "UNKNOWN",
	}
	read := call{Method: http.MethodGet, Target: publicationSlot, Token: alice.Subject}

	fake := &publications{slot: activeSlot()}
	recorder := newPublicationService(t, fake).do(t, read)
	if got := members(t, recorder); recorder.Code != http.StatusOK || !reflect.DeepEqual(got, shown) ||
		recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("a slot whose release is approved: %d\n %v\nwant 200\n %v", recorder.Code, got, shown)
	}
	if len(fake.read) != 1 || fake.read[0].CorrelationID == "" {
		t.Fatalf("the use case was asked %+v, want one read under the identifier of the request", fake.read)
	}
	asked := fake.read[0]
	asked.CorrelationID = ""
	if want := (domain.Request{
		Caller: domain.Principal(alice), ProductID: productID, TargetID: publicationTargetID, AssetID: apiAssetID,
	}); asked != want {
		t.Errorf("the use case was asked for %+v, want %+v", asked, want)
	}

	causes := map[string]struct {
		err  error
		code string
	}{
		"no approval":                          {checks.ErrApprovalRequired, "APPROVAL_REQUIRED"},
		"an approval that ran out":             {checks.ErrApprovalExpired, "APPROVAL_EXPIRED"},
		"a waiver that ran out":                {checks.ErrWaiverExpired, "WAIVER_EXPIRED"},
		"a reviewer who lost the grant":        {checks.ErrReviewerGrantRevoked, "REVIEWER_AUTH_REVOKED"},
		"a target the approval does not name":  {checks.ErrApprovalScope, "APPROVAL_SCOPE_MISMATCH"},
		"a check the tools have moved on from": {fmt.Errorf("review %s: %w", reviewID, reviewdomain.ErrCheckStale), "POLICY_STALE"},
	}
	activate := call{
		Method: http.MethodPost, Target: publicationSlot + "/operations", Token: alice.Subject,
		Body:   `{"action":"ACTIVATE","expected_generation":3,"release_id":"` + releaseID + `"}`,
		Header: map[string]string{"Idempotency-Key": "again"},
	}
	var said []any
	for name, c := range causes {
		t.Run(name, func(t *testing.T) {
			slot := activeSlot()
			slot.Unapproved = c.err
			recorder := newPublicationService(t, &publications{slot: slot}).do(t, read)
			want := map[string]any{"approval_problem": c.code}
			for member, value := range shown {
				want[member] = value
			}
			if got := members(t, recorder); recorder.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
				t.Fatalf("the slot: %d\n %v\nwant 200\n %v", recorder.Code, got, want)
			}
			// An activation that is refused for the same cause is
			// answered with the same code.
			refused := &publications{err: c.err}
			wantRefusal(t, newPublicationService(t, refused).do(t, activate), http.StatusConflict, c.code)
			if len(refused.changed) != 1 {
				t.Errorf("the use case was asked for %d changes, want the one", len(refused.changed))
			}
		})
		said = append(said, c.code)
	}

	// The contract states the causes a slot can name, and no other.
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	declared := slices.Clone(contract.Components.Schemas["PublicationSlot"].Value.Properties["approval_problem"].Value.Enum)
	byName := func(a, b any) int { return strings.Compare(a.(string), b.(string)) }
	slices.SortFunc(declared, byName)
	slices.SortFunc(said, byName)
	if !reflect.DeepEqual(declared, said) {
		t.Errorf("the contract states the causes %v, a slot names %v", declared, said)
	}

	// What an activation is refused for besides is no word about the
	// approval of what is active, and a slot does not answer with it.
	for name, cause := range map[string]error{
		"a publisher who had a part in the revision": checks.ErrPublisherInvolved,
		"a check that compared with something else":  checks.ErrBaselineMismatch,
		"a check that left a rule out":               fmt.Errorf("%w: F04-GOV-004", checks.ErrReleaseCheckMissing),
		"a review that is not what was recorded":     fmt.Errorf("review %s: %w", reviewID, reviewdomain.ErrReviewDamaged),
		"anything else":                              errors.New("the connection is gone"),
	} {
		t.Run(name, func(t *testing.T) {
			slot := activeSlot()
			slot.Unapproved = cause
			s := newPublicationService(t, &publications{slot: slot})
			wantProblem(t, s.do(t, read), http.StatusInternalServerError, CodeInternalError, "")
			if logged := s.log.String(); !strings.Contains(logged, "the slot cannot say why the approval of its release does not hold") ||
				!strings.Contains(logged, cause.Error()) {
				t.Errorf("the log does not say what the slot could not name: %s", logged)
			}
		})
	}
}

// The contract states a change of a slot, a target and a block in the words
// of the model, so what the validator lets through is what the model
// accepts: the form of a portal key and of the key of a retry, the bound of
// a reason, and the actions a slot has.
func TestTheContractStatesPublicationAsTheModelDoes(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas
	for _, name := range []string{"PublicationTargetRegistration", "PublicationTarget"} {
		key := schemas[name].Value.Properties["portal_key"]
		if key == nil || key.Value.Pattern != domain.PortalKeyPattern {
			t.Errorf("the portal key of %s is not stated by the pattern of the model, %q", name, domain.PortalKeyPattern)
		}
	}
	for _, name := range []string{"PublicationChange", "PublicationOperation", "AccessBlockRequest", "AccessBlockClearance", "AccessBlock"} {
		for _, member := range []string{"reason", "clear_reason"} {
			reason := schemas[name].Value.Properties[member]
			if reason == nil {
				continue
			}
			if got := reason.Value; got.MaxLength == nil || *got.MaxLength != domain.ReasonMaxLength || got.MinLength != 1 {
				t.Errorf("the %s of %s is bounded at %d to %v characters, the model at 1 to %d", member, name, got.MinLength, got.MaxLength, domain.ReasonMaxLength)
			}
		}
	}
	for _, name := range []string{"AccessBlockRequest", "AccessBlockClearance"} {
		if got := schemas[name].Value.Required; !reflect.DeepEqual(got, []string{"reason"}) {
			t.Errorf("the contract requires %v of %s, the model a reason", got, name)
		}
	}
	if got, want := schemas["PublicationAction"].Value.Enum, []any{string(domain.Activate), string(domain.Withdraw), string(domain.Restore)}; !reflect.DeepEqual(got, want) {
		t.Errorf("the contract states the actions %v, the model %v", got, want)
	}
	// What a slot can be said to be is what the model says of one.
	said := []any{
		domain.Slot{}.Visibility(),
		domain.Slot{Generation: 1, ActiveReleaseID: "release"}.Visibility(),
		domain.Slot{Generation: 2}.Visibility(),
	}
	if got := schemas["PublicationSlot"].Value.Properties["visibility"].Value.Enum; !reflect.DeepEqual(got, said) {
		t.Errorf("the contract states the visibility of a slot as %v, the model as %v", got, said)
	}
	if got := schemas["PublicationChange"].Value.Required; !reflect.DeepEqual(got, []string{"action", "expected_generation"}) {
		t.Errorf("the contract requires %v of a change, the model an action and the generation it was decided on", got)
	}

	// A change is sent under the key of its retries.
	change := contract.Paths.Find("/v1/products/{product_id}/publication-targets/{publication_target_id}/assets/{api_asset_id}/operations").Post
	key := change.Parameters.GetByInAndName("header", "Idempotency-Key")
	if key == nil || !key.Required || key.Schema.Value.Pattern != domain.IdempotencyKeyPattern {
		t.Errorf("a change of a slot does not require an Idempotency-Key of the pattern of the model, %q", domain.IdempotencyKeyPattern)
	}
}
