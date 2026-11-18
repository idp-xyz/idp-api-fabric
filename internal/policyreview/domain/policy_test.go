package domain_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

func digestOf(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}

// The baseline is the rule registry of F04 §7.1, as far as it is of the MVP
// stage: every such rule, member for member, and no other. A rule changes in
// the registry first, and this test says so when the two part.
func TestTheBaselineIsTheMVPStageOfTheRuleRegistry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "governance", "policies", "F04_Rules_Registry.json"))
	if err != nil {
		t.Fatalf("read the rule registry: %v", err)
	}
	var registry struct {
		PolicySetID string `json:"policy_set_id"`
		Rules       []struct {
			domain.Rule
			NormativeSource string `json:"normative_source"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatalf("the rule registry is not what this test reads: %v", err)
	}
	var want []domain.Rule
	stages := make(map[string]int)
	for _, stated := range registry.Rules {
		stages[stated.Stage]++
		if stated.Stage == "MVP" {
			want = append(want, stated.Rule)
		}
	}
	slices.SortFunc(want, func(a, b domain.Rule) int { return strings.Compare(a.ID, b.ID) })

	baseline := domain.Baseline()
	if baseline.SetID() != registry.PolicySetID || domain.BaselineSetID != registry.PolicySetID {
		t.Errorf("the baseline is the rule set %q, the registry states %q", baseline.SetID(), registry.PolicySetID)
	}
	got := baseline.Rules()
	if len(got) != len(want) || len(want) != 23 || stages["MVP"]+stages["V1"] != len(registry.Rules) {
		t.Fatalf("the baseline has %d rules, the registry %d of the MVP stage (stages %v); want 23 of both and no stage but MVP and V1",
			len(got), len(want), stages)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("the baseline states\n %+v\nthe registry\n %+v", got[i], want[i])
		}
	}

	// What a check can evaluate by itself is the automated subset, and it
	// is the first ten rules.
	var automated []string
	for _, rule := range got {
		if rule.VerificationMode == "AUTOMATED_SUBSET" {
			automated = append(automated, rule.ID)
		}
		if found, _ := baseline.Rule(rule.ID); found != rule {
			t.Errorf("rule %s is found as %+v", rule.ID, found)
		}
	}
	if len(automated) != 10 || automated[0] != "F04-GOV-001" || automated[9] != "F04-GOV-010" {
		t.Errorf("the automated subset is %v, want F04-GOV-001 to F04-GOV-010", automated)
	}
	if _, found := baseline.Rule("F04-GOV-021"); found {
		t.Error("the baseline holds a rule of the V1 stage")
	}
}

func quality(id string) domain.Rule {
	return domain.Rule{
		ID: id, Description: "Operations are described", Category: domain.CategoryQuality, Severity: domain.SeverityWarn, Waivable: true,
		OwnerRole: "ContractGovernanceOwner", Scope: "GLOBAL_BASELINE", Stage: "MVP", Evaluator: "described", VerificationMode: "AUTOMATED_SUBSET",
	}
}

// A policy is known by the digest of its rules. The same rules are the same
// policy in whatever order they are stated, and a rule that changes in any
// member makes another policy: a check that was held to the one was not held
// to the other (F04 §7.2).
func TestAPolicyIsKnownByTheDigestOfItsRules(t *testing.T) {
	rules := []domain.Rule{quality("T-QUALITY-002"), quality("T-QUALITY-001"), quality("T-QUALITY-010")}
	policy, err := domain.NewPolicy("team-rules", rules)
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.Rules(); got[0].ID != "T-QUALITY-001" || got[1].ID != "T-QUALITY-002" || got[2].ID != "T-QUALITY-010" {
		t.Fatalf("the rules are kept as %v, want them in the order of their identifiers", got)
	}
	if rules[0].ID != "T-QUALITY-002" {
		t.Fatal("the rules handed in were reordered")
	}
	const document = `{"policy_set_id":"team-rules","rules":[` +
		`{"rule_id":"T-QUALITY-001","description":"Operations are described","category":"QUALITY","severity":"WARN","waivable":true,` +
		`"owner_role":"ContractGovernanceOwner","scope":"GLOBAL_BASELINE","stage":"MVP","evaluator":"described","verification_mode":"AUTOMATED_SUBSET"},` +
		`{"rule_id":"T-QUALITY-002","description":"Operations are described","category":"QUALITY","severity":"WARN","waivable":true,` +
		`"owner_role":"ContractGovernanceOwner","scope":"GLOBAL_BASELINE","stage":"MVP","evaluator":"described","verification_mode":"AUTOMATED_SUBSET"},` +
		`{"rule_id":"T-QUALITY-010","description":"Operations are described","category":"QUALITY","severity":"WARN","waivable":true,` +
		`"owner_role":"ContractGovernanceOwner","scope":"GLOBAL_BASELINE","stage":"MVP","evaluator":"described","verification_mode":"AUTOMATED_SUBSET"}]}`
	if policy.Document() != document || policy.Digest() != digestOf(document) {
		t.Fatalf("the policy is written as\n %s\nunder %s, want\n %s\nunder its SHA-256", policy.Document(), policy.Digest(), document)
	}

	reordered, err := domain.NewPolicy("team-rules", []domain.Rule{rules[2], rules[1], rules[0]})
	if err != nil || reordered.Digest() != policy.Digest() {
		t.Errorf("the same rules in another order: %s %v, want the same policy %s", reordered.Digest(), err, policy.Digest())
	}

	changes := map[string]func(*domain.Rule){
		"another description":       func(r *domain.Rule) { r.Description = "Operations are documented" },
		"another category":          func(r *domain.Rule) { r.Category = domain.CategoryContractCompatibility },
		"a finding that blocks":     func(r *domain.Rule) { r.Severity = domain.SeverityBlock },
		"no waiver":                 func(r *domain.Rule) { r.Waivable = false },
		"another owner":             func(r *domain.Rule) { r.OwnerRole = "SecurityOwner" },
		"another scope":             func(r *domain.Rule) { r.Scope = "PRODUCT_EXTENSION" },
		"another stage":             func(r *domain.Rule) { r.Stage = "V1" },
		"another evaluator":         func(r *domain.Rule) { r.Evaluator = "documented" },
		"another verification mode": func(r *domain.Rule) { r.VerificationMode = "MANUAL_REVIEW" },
		"another identifier":        func(r *domain.Rule) { r.ID = "T-QUALITY-003" },
	}
	seen := map[string]string{policy.Digest(): "the policy itself"}
	for name, change := range changes {
		changed := slices.Clone(rules)
		change(&changed[0])
		other, err := domain.NewPolicy("team-rules", changed)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if earlier, taken := seen[other.Digest()]; taken {
			t.Errorf("a rule with %s has the digest of %s", name, earlier)
		}
		seen[other.Digest()] = name
	}
	for name, other := range map[string]struct {
		set   string
		rules []domain.Rule
	}{
		"another rule set": {"other-rules", rules},
		"a rule less":      {"team-rules", rules[:2]},
		"a rule more":      {"team-rules", append(slices.Clone(rules), quality("T-QUALITY-011"))},
	} {
		changed, err := domain.NewPolicy(other.set, other.rules)
		if err != nil || seen[changed.Digest()] != "" {
			t.Errorf("%s: digest of %q, %v; want a policy of its own", name, seen[changed.Digest()], err)
		}
	}
}

// What cannot be a policy is refused: a check held to it would mean nothing
// definite. A mandatory rule in particular is never one that can be waived.
func TestWhatCannotBeAPolicyIsRefused(t *testing.T) {
	with := func(change func(*domain.Rule)) []domain.Rule {
		rule := quality("T-QUALITY-001")
		change(&rule)
		return []domain.Rule{rule}
	}
	many := make([]domain.Rule, domain.MaxRules+1)
	for i := range many {
		many[i] = quality(fmt.Sprintf("T-QUALITY-%03d", i))
	}
	refused := map[string]struct {
		set   string
		rules []domain.Rule
	}{
		"no rule set":                          {"", with(func(*domain.Rule) {})},
		"a rule set in upper case":             {"Team-Rules", with(func(*domain.Rule) {})},
		"a rule set with a space":              {"team rules", with(func(*domain.Rule) {})},
		"no rule":                              {"team-rules", nil},
		"more rules than a policy holds":       {"team-rules", many},
		"a rule stated twice":                  {"team-rules", []domain.Rule{quality("T-QUALITY-001"), quality("T-QUALITY-001")}},
		"a rule without an identifier":         {"team-rules", with(func(r *domain.Rule) { r.ID = "" })},
		"an identifier in lower case":          {"team-rules", with(func(r *domain.Rule) { r.ID = "t-quality-001" })},
		"an identifier of one part":            {"team-rules", with(func(r *domain.Rule) { r.ID = "QUALITY" })},
		"an identifier with a line break":      {"team-rules", with(func(r *domain.Rule) { r.ID = "T-QUALITY-001\n" })},
		"no description":                       {"team-rules", with(func(r *domain.Rule) { r.Description = "" })},
		"a description of two lines":           {"team-rules", with(func(r *domain.Rule) { r.Description = "Operations\nare described" })},
		"a description with space behind":      {"team-rules", with(func(r *domain.Rule) { r.Description = "Operations are described " })},
		"a description that is too long":       {"team-rules", with(func(r *domain.Rule) { r.Description = strings.Repeat("d", 301) })},
		"a description that is not UTF-8":      {"team-rules", with(func(r *domain.Rule) { r.Description = "Operations \xff" })},
		"a category nobody defined":            {"team-rules", with(func(r *domain.Rule) { r.Category = "STYLE" })},
		"no category":                          {"team-rules", with(func(r *domain.Rule) { r.Category = "" })},
		"a severity nobody defined":            {"team-rules", with(func(r *domain.Rule) { r.Severity = "INFO" })},
		"no severity":                          {"team-rules", with(func(r *domain.Rule) { r.Severity = "" })},
		"a security rule that can be waived":   {"team-rules", with(func(r *domain.Rule) { r.Category = domain.CategorySecurityMandatory })},
		"an integrity rule that can be waived": {"team-rules", with(func(r *domain.Rule) { r.Category = domain.CategoryIntegrityMandatory })},
		"no evaluator":                         {"team-rules", with(func(r *domain.Rule) { r.Evaluator = "" })},
		"an evaluator in upper case":           {"team-rules", with(func(r *domain.Rule) { r.Evaluator = "Described" })},
		"no owner":                             {"team-rules", with(func(r *domain.Rule) { r.OwnerRole = "" })},
		"no scope":                             {"team-rules", with(func(r *domain.Rule) { r.Scope = "" })},
		"a scope with a space":                 {"team-rules", with(func(r *domain.Rule) { r.Scope = "GLOBAL BASELINE" })},
		"no stage":                             {"team-rules", with(func(r *domain.Rule) { r.Stage = "" })},
		"no verification mode":                 {"team-rules", with(func(r *domain.Rule) { r.VerificationMode = "" })},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			if policy, err := domain.NewPolicy(c.set, c.rules); !errors.Is(err, domain.ErrInvalidPolicy) {
				t.Fatalf("got %+v %v, want %v", policy, err, domain.ErrInvalidPolicy)
			}
		})
	}

	// The longest of each is a policy, and so is a mandatory rule that
	// cannot be waived.
	longest := quality("T-QUALITY-LONGEST")
	longest.Description = strings.Repeat("订", 300)
	mandatory := quality("T-SECURITY-001")
	mandatory.Category, mandatory.Waivable = domain.CategorySecurityMandatory, false
	if _, err := domain.NewPolicy("team-rules", append(many[:domain.MaxRules-2:domain.MaxRules-2], longest, mandatory)); err != nil {
		t.Errorf("a policy at its bounds: %v", err)
	}
	for category, want := range map[domain.Category]bool{
		domain.CategorySecurityMandatory: true, domain.CategoryIntegrityMandatory: true,
		domain.CategoryContractCompatibility: false, domain.CategoryQuality: false,
	} {
		if category.Mandatory() != want {
			t.Errorf("%s is mandatory: %t, want %t", category, category.Mandatory(), want)
		}
	}
}

// A policy is read back from the document and the digest it was stored with,
// and only as the policy that was stored: a document that is not what the
// digest was computed from, or that this model did not write, is no evidence
// of what a check was held to.
func TestAStoredPolicyIsReadBackOnlyAsItWasRecorded(t *testing.T) {
	policy := domain.Baseline()
	document, digest := policy.Document(), policy.Digest()
	recorded, err := domain.RecordedPolicy(document, digest)
	if err != nil || recorded.SetID() != policy.SetID() || !reflect.DeepEqual(recorded.Rules(), policy.Rules()) || recorded.Digest() != digest {
		t.Fatalf("the baseline read back: %+v %v, want it as it was", recorded, err)
	}

	two, err := domain.NewPolicy("team-rules", []domain.Rule{quality("T-QUALITY-001"), quality("T-QUALITY-002")})
	if err != nil {
		t.Fatal(err)
	}
	first, second, _ := strings.Cut(strings.TrimPrefix(strings.TrimSuffix(two.Document(), "]}"), `{"policy_set_id":"team-rules","rules":[`), "},{")
	first, second = first+"}", "{"+second
	rewritten := func(rules string) string { return `{"policy_set_id":"team-rules","rules":[` + rules + `]}` }
	if rewritten(first+","+second) != two.Document() {
		t.Fatalf("this test takes the document apart wrongly: %s", rewritten(first+","+second))
	}
	// A document that was altered into another policy is found out by the
	// digest it was stored under. With a digest of its own it is that other
	// policy: the check is that document and digest belong together, and it
	// does not hold against someone who rewrites both.
	altered := map[string]string{
		"a rule that was reworded":        strings.Replace(two.Document(), "Operations are described", "Operations are documented", 1),
		"a finding that no longer blocks": strings.Replace(policy.Document(), `"severity":"BLOCK"`, `"severity":"WARN"`, 1),
		"a rule that was removed":         rewritten(first),
	}
	for name, document := range altered {
		t.Run(name, func(t *testing.T) {
			for _, stored := range []string{two.Digest(), policy.Digest()} {
				if got, err := domain.RecordedPolicy(document, stored); !errors.Is(err, domain.ErrPolicyDamaged) {
					t.Fatalf("under the digest it was stored with: got %+v %v, want %v", got, err, domain.ErrPolicyDamaged)
				}
			}
			if other, err := domain.RecordedPolicy(document, digestOf(document)); err != nil || other.Digest() == two.Digest() || other.Digest() == policy.Digest() {
				t.Fatalf("under its own digest: %v, want it read as the other policy it is", err)
			}
		})
	}

	// A document this model did not write is no policy under any digest.
	foreign := map[string]string{
		"a mandatory rule that can be waived": strings.Replace(policy.Document(), `"waivable":false`, `"waivable":true`, 1),
		"a rule that is there twice":          rewritten(first + "," + first),
		"rules in another order":              rewritten(second + "," + first),
		"a member nobody wrote":               strings.Replace(two.Document(), `"rule_id"`, `"note":"x","rule_id"`, 1),
		"members in another order": strings.Replace(two.Document(),
			`"rule_id":"T-QUALITY-001","description":"Operations are described"`, `"description":"Operations are described","rule_id":"T-QUALITY-001"`, 1),
		"white space between the members":  strings.Replace(two.Document(), `,"rules"`, `, "rules"`, 1),
		"something behind the document":    two.Document() + "\n{}",
		"a document that is cut short":     two.Document()[:len(two.Document())-2],
		"a document that is no JSON":       "policy",
		"an empty document":                "",
		"a document without rules":         `{"policy_set_id":"team-rules","rules":[]}`,
		"a document of another kind":       `[]`,
		"a rule set that is named nothing": strings.Replace(two.Document(), `"team-rules"`, `""`, 1),
	}
	for name, document := range foreign {
		t.Run(name, func(t *testing.T) {
			for _, digest := range []string{two.Digest(), policy.Digest(), digestOf(document)} {
				if got, err := domain.RecordedPolicy(document, digest); !errors.Is(err, domain.ErrPolicyDamaged) {
					t.Fatalf("under %s: got %+v %v, want %v", digest, got, err, domain.ErrPolicyDamaged)
				}
			}
		})
	}
	for name, digest := range map[string]string{
		"the digest of another policy": two.Digest(),
		"no digest":                    "",
		"a digest in upper case":       strings.ToUpper(digest),
		"a digest without its prefix":  strings.TrimPrefix(digest, "sha256:"),
	} {
		if got, err := domain.RecordedPolicy(document, digest); !errors.Is(err, domain.ErrPolicyDamaged) {
			t.Errorf("%s: got %+v %v, want %v", name, got, err, domain.ErrPolicyDamaged)
		}
	}
}
