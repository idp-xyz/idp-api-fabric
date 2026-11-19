package domain_test

import (
	"errors"
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

func TestCheckCompatibilityEvidenceIsBoundToTheComparatorAndItsOutcome(t *testing.T) {
	policy := domain.Baseline()
	for _, tc := range []struct {
		classification domain.Compatibility
		status         domain.Status
		findings       int
	}{
		{domain.Compatible, domain.StatusPass, 0},
		{domain.Risky, domain.StatusUnknown, 0},
		{domain.Unknown, domain.StatusUnknown, 0},
		{domain.Breaking, domain.StatusFail, 1},
	} {
		t.Run(string(tc.classification), func(t *testing.T) {
			input := draft()
			input.Compatibility = &domain.ComparisonEvidence{
				Baseline: domain.ComparisonBaseline{RevisionID: "previous-revision", InputDigest: digestOf("previous closure")},
				Tool:     domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"},
				Comparison: domain.Comparison{Classification: tc.classification, Changes: []domain.CompatibilityChange{
					{Classification: tc.classification, Code: "EXAMPLE_CHANGE", Pointer: "/paths"},
				}},
			}
			run, err := domain.Complete(input, policy, shipments(), linter, nil, completed)
			if err != nil {
				t.Fatal(err)
			}
			position := slices.IndexFunc(run.Outcomes, func(o domain.Outcome) bool { return o.Rule.ID == "F04-GOV-013" })
			if position < 0 {
				t.Fatal("the compatibility rule is missing")
			}
			outcome := run.Outcomes[position]
			if outcome.Status != tc.status || outcome.Tool != input.Compatibility.Tool || outcome.Findings != tc.findings ||
				len(run.Tools) != 2 || len(run.Findings) != tc.findings {
				t.Fatalf("compatibility evidence: outcome %+v, findings %+v, tools %+v", outcome, run.Findings, run.Tools)
			}
			if _, err := domain.Recorded(run, policy); err != nil {
				t.Fatalf("read back the comparison evidence: %v", err)
			}
			input.Compatibility.Changes[0].Code = "CHANGED"
			if run.Compatibility.Changes[0].Code != "EXAMPLE_CHANGE" {
				t.Fatal("mutating the comparison changed the completed check")
			}
			run.Compatibility = nil
			if _, err := domain.Recorded(run, policy); !errors.Is(err, domain.ErrCheckRunDamaged) {
				t.Fatalf("the compatibility outcome without its evidence was accepted: %v", err)
			}
		})
	}
}

func TestCheckRejectsAnUnboundCompatibilityPass(t *testing.T) {
	policy, input := domain.Baseline(), draft()
	_, err := domain.Complete(input, policy, shipments(), linter, []domain.Observation{{RuleID: "F04-GOV-013"}}, completed)
	if !errors.Is(err, domain.ErrInvalidEvaluation) {
		t.Fatalf("accepted comparison without a baseline: %v", err)
	}
	input.Compatibility = &domain.ComparisonEvidence{
		Baseline:   domain.ComparisonBaseline{RevisionID: input.Revision.RevisionID, InputDigest: shipments().Digest},
		Tool:       domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"},
		Comparison: domain.Comparison{Classification: domain.Compatible},
	}
	_, err = domain.Complete(input, policy, shipments(), linter, nil, completed)
	if !errors.Is(err, domain.ErrInvalidEvaluation) {
		t.Fatalf("accepted the candidate as its own baseline: %v", err)
	}
}
