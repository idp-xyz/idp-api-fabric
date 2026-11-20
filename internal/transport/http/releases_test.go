package http

import (
	"reflect"
	"testing"

	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// The contract states what a release is in the words of the model, so what
// the validator lets through is what the model accepts: the form of a
// version, where it is asked and where it is answered, the one format an
// artifact has and the one status a release has.
func TestTheContractStatesAReleaseAsTheModelDoes(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas
	for _, name := range []string{"ReleasePreparation", "ContractRelease"} {
		if schemas[name] == nil {
			t.Errorf("the contract declares no %s", name)
			continue
		}
		version := schemas[name].Value.Properties["version"]
		if version == nil || version.Value.Pattern != registrydomain.ReleaseVersionPattern || version.Value.MaxLength != nil {
			t.Errorf("the version of %s is not stated by the pattern of the model, %q, alone", name, registrydomain.ReleaseVersionPattern)
		}
	}
	if got := schemas["ReleasePreparation"].Value.Required; !reflect.DeepEqual(got, []string{"revision_id", "review_id", "version"}) {
		t.Errorf("the contract requires %v of a preparation, the model a revision, a review and a version", got)
	}
	release := schemas["ContractRelease"].Value
	for member, want := range map[string]string{"artifact_format": registrydomain.ArtifactFormat, "status": "READY"} {
		property := release.Properties[member]
		if property == nil || !reflect.DeepEqual(property.Value.Enum, []any{want}) {
			t.Errorf("the contract does not state the %s of a release as %q and nothing else", member, want)
		}
	}
}
