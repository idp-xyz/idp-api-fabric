package domain

import "fmt"

// BaselineSetID names the global rule set of the rule registry (F04 §7.1,
// governance/policies/F04_Rules_Registry.json).
const BaselineSetID = "fabric-global-contract-governance"

// The roles that own the rules of the baseline, as the registry names them.
const (
	architectureOwner = "PlatformArchitectureOwner"
	governanceOwner   = "ContractGovernanceOwner"
	securityOwner     = "SecurityOwner"
)

// How the registry says a rule is verified. A rule of the automated subset
// has an evaluator that can be built into a check. The others need a tool
// adapter of F07 or a person, and a check records them as not run until one
// of its tools knows their evaluator.
const (
	automatedSubset = "AUTOMATED_SUBSET"
	toolAdapter     = "F07_TOOL_ADAPTER"
	manualReview    = "MANUAL_REVIEW"
)

// baseline is the global baseline: the rules the registry states for the MVP
// stage, member for member. A test holds it to the registry, so a rule
// changes there first and here with it.
//
// The rules the registry leaves to V1 are not part of it: nothing of V1 is
// built yet. Every rule of the MVP stage is, also those no tool of a check
// evaluates so far: a check says of each of them that it was not run, which
// is what whoever reads the check has to know before relying on it.
var baseline = []Rule{
	integrity("F04-GOV-001", "Allowed OAS baseline version", "supported_spec_version", automatedSubset),
	quality("F04-GOV-002", "Document must have summary title and version", SeverityBlock, "info_required", automatedSubset),
	quality("F04-GOV-003", "Every path Operation must have stable operationId", SeverityBlock, "operation_id_required", automatedSubset),
	security("F04-GOV-004", "Do not include credential sample or bearer token literal", "secret_scan", automatedSubset),
	security("F04-GOV-005", "Reject non-local HTTP/HTTPS and path-traversal $ref in illustrative policy", "ref_safety", automatedSubset),
	security("F04-GOV-006", "Every business operation must declare security explicitly or effective global security", "operation_security", automatedSubset),
	quality("F04-GOV-007", "Error response must use RFC9457 media type candidate", SeverityWarn, "problem_content", automatedSubset),
	integrity("F04-GOV-008", "OperationId must be unique within document", "operation_id_unique", automatedSubset),
	integrity("F04-GOV-009", "Reject malformed YAML or ambiguous duplicate keys", "parse_safety", automatedSubset),
	security("F04-GOV-010", "Reject internal network URL disclosure", "internal_url_scan", automatedSubset),
	integrity("F04-GOV-011", "Validate against official OAS 3.1 JSON Schema using pinned offline validator", "official_oas_validation", toolAdapter),
	security("F04-GOV-012", "Ensure $ref closure, cycles and budgets are bounded", "ref_closure_budget", toolAdapter),
	{
		ID: "F04-GOV-013", Description: "Run semantic backward compatibility classification and record unknown cases",
		Category: CategoryContractCompatibility, Severity: SeverityBlock, Waivable: true, OwnerRole: governanceOwner,
		Scope: globalBaseline, Stage: stageMVP, Evaluator: "semantic_diff", VerificationMode: toolAdapter,
	},
	integrity("F04-GOV-014", "Binding Source Revision Release digest chain is verified", "release_digest", toolAdapter),
	security("F04-GOV-015", "PublicationTarget×APIAsset scope, CAS and effective deny are checked", "publication_authorization", toolAdapter),
	integrity("F04-GOV-016", "PolicySnapshot and ReviewDecision match fixed scope and expiry", "approval_binding", toolAdapter),
	security("F04-GOV-017", "Examples and descriptions are rendered with HTML sanitization", "sanitized_render", toolAdapter),
	integrity("F04-GOV-018", "CheckRun findings include tool version and input digest", "tool_provenance", toolAdapter),
	security("F04-GOV-019", "Product rule cannot weaken global mandatory requirements", "policy_monotonicity", manualReview),
	integrity("F04-GOV-020", "Unsupported spec and parser unknown block publication", "capability_registry", toolAdapter),
	security("F04-GOV-025", "Reviewers must be independent authorized principals", "four_eyes", toolAdapter),
	security("F04-GOV-026", "No private API appears in public portal search or index", "publication_leak_scan", toolAdapter),
	integrity("F04-GOV-027", "External producer and gateway truth cannot be represented as fresh when unavailable", "external_dependency_state", toolAdapter),
}

const (
	globalBaseline = "GLOBAL_BASELINE"
	stageMVP       = "MVP"
)

// security states a rule nothing waives and whose finding blocks.
func security(id, description, evaluator, mode string) Rule {
	return Rule{
		ID: id, Description: description, Category: CategorySecurityMandatory, Severity: SeverityBlock,
		OwnerRole: securityOwner, Scope: globalBaseline, Stage: stageMVP, Evaluator: evaluator, VerificationMode: mode,
	}
}

// integrity states a rule nothing waives and whose finding blocks.
func integrity(id, description, evaluator, mode string) Rule {
	return Rule{
		ID: id, Description: description, Category: CategoryIntegrityMandatory, Severity: SeverityBlock,
		OwnerRole: architectureOwner, Scope: globalBaseline, Stage: stageMVP, Evaluator: evaluator, VerificationMode: mode,
	}
}

// quality states a rule whoever prepares a release may waive for a reason.
func quality(id, description string, severity Severity, evaluator, mode string) Rule {
	return Rule{
		ID: id, Description: description, Category: CategoryQuality, Severity: severity, Waivable: true,
		OwnerRole: governanceOwner, Scope: globalBaseline, Stage: stageMVP, Evaluator: evaluator, VerificationMode: mode,
	}
}

// Baseline is the policy every check is held to: the global baseline of the
// rule registry. No product adds rules of its own to it yet (FR-026).
func Baseline() Policy {
	policy, err := NewPolicy(BaselineSetID, baseline)
	if err != nil {
		// The baseline is a constant of this package, and a test holds it
		// to what a policy is.
		panic(fmt.Sprintf("the baseline is no policy: %v", err))
	}
	return policy
}
