package http

import (
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

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
