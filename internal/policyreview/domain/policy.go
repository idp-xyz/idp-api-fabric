package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Category says what a rule protects, and with that how far a product or a
// reviewer may bend it (F04 §7.1).
type Category string

const (
	CategorySecurityMandatory     Category = "SECURITY_MANDATORY"
	CategoryIntegrityMandatory    Category = "INTEGRITY_MANDATORY"
	CategoryContractCompatibility Category = "CONTRACT_COMPATIBILITY"
	CategoryQuality               Category = "QUALITY"
)

// Mandatory reports whether nothing waives a rule of the category.
func (c Category) Mandatory() bool {
	return c == CategorySecurityMandatory || c == CategoryIntegrityMandatory
}

// Severity is what a finding of a rule does to the check that made it.
type Severity string

const (
	SeverityBlock Severity = "BLOCK"
	SeverityWarn  Severity = "WARN"
)

// Rule is one rule of a policy, as the rule registry of F04 §7.1 states it.
// Evaluator names what judges the rule: a check evaluates the rule when one
// of its tools knows that name, and records the rule as not run otherwise.
type Rule struct {
	ID               string   `json:"rule_id"`
	Description      string   `json:"description"`
	Category         Category `json:"category"`
	Severity         Severity `json:"severity"`
	Waivable         bool     `json:"waivable"`
	OwnerRole        string   `json:"owner_role"`
	Scope            string   `json:"scope"`
	Stage            string   `json:"stage"`
	Evaluator        string   `json:"evaluator"`
	VerificationMode string   `json:"verification_mode"`
}

const (
	// MaxRules bounds a policy, and with it what one check records.
	MaxRules = 100

	maxRuleDescription = 300
	maxRuleLabel       = 60
)

var (
	// ErrInvalidPolicy reports a rule set that cannot be a policy.
	ErrInvalidPolicy = errors.New("the policy is invalid")
	// ErrPolicyDamaged reports a stored policy that is not the one that was
	// recorded: its document is not what its digest was computed from, or
	// it is not a document this model wrote.
	ErrPolicyDamaged = errors.New("the stored policy is not the one that was recorded")

	ruleID        = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)+$`)
	ruleLabel     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*$`)
	ruleEvaluator = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	policySetID   = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

func (r Rule) validate() error {
	switch {
	case len(r.ID) > maxRuleLabel || !ruleID.MatchString(r.ID):
		return invalidPolicy("a rule has no identifier of the form F04-GOV-001")
	case !plainText(r.Description, maxRuleDescription):
		return invalidPolicy("rule " + r.ID + " has no description of one line")
	case r.Category != CategorySecurityMandatory && r.Category != CategoryIntegrityMandatory &&
		r.Category != CategoryContractCompatibility && r.Category != CategoryQuality:
		return invalidPolicy("rule " + r.ID + " has a category the model does not know")
	case r.Severity != SeverityBlock && r.Severity != SeverityWarn:
		return invalidPolicy("rule " + r.ID + " has a severity the model does not know")
	case r.Category.Mandatory() && r.Waivable:
		return invalidPolicy("rule " + r.ID + " is mandatory and waivable at once")
	case len(r.Evaluator) > maxRuleLabel || !ruleEvaluator.MatchString(r.Evaluator):
		return invalidPolicy("rule " + r.ID + " names no evaluator")
	}
	for _, label := range []string{r.OwnerRole, r.Scope, r.Stage, r.VerificationMode} {
		if len(label) > maxRuleLabel || !ruleLabel.MatchString(label) {
			return invalidPolicy("rule " + r.ID + " carries a label that is not a plain name")
		}
	}
	return nil
}

// plainText reports whether text is one non-empty line of at most limit
// characters, without control characters and without space around it.
func plainText(text string, limit int) bool {
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > limit || strings.TrimSpace(text) != text {
		return false
	}
	return !strings.ContainsFunc(text, unicode.IsControl)
}

func invalidPolicy(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPolicy, reason)
}

// Policy is the rule set a check is held to. It is known by the digest of
// its rules: two policies with the same digest say the same, and a policy
// whose rules change in any way is another policy (F04 §7.2).
type Policy struct {
	setID string
	rules []Rule
}

// NewPolicy makes a policy of the rules. The rules are kept in the order of
// their identifiers, whatever order they are given in.
func NewPolicy(setID string, rules []Rule) (Policy, error) {
	if len(setID) > maxRuleLabel || !policySetID.MatchString(setID) {
		return Policy{}, invalidPolicy("the rule set has no name")
	}
	if len(rules) < 1 || len(rules) > MaxRules {
		return Policy{}, invalidPolicy("a policy has between one rule and " + fmt.Sprint(MaxRules))
	}
	ordered := slices.Clone(rules)
	slices.SortFunc(ordered, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	for i, rule := range ordered {
		if err := rule.validate(); err != nil {
			return Policy{}, err
		}
		if i > 0 && ordered[i-1].ID == rule.ID {
			return Policy{}, invalidPolicy("rule " + rule.ID + " is stated twice")
		}
	}
	return Policy{setID: setID, rules: ordered}, nil
}

// SetID names the rule set the policy was taken from.
func (p Policy) SetID() string { return p.setID }

// Rules returns the rules in the order of their identifiers.
func (p Policy) Rules() []Rule { return slices.Clone(p.rules) }

// Rule finds a rule of the policy by its identifier.
func (p Policy) Rule(id string) (Rule, bool) {
	index, found := slices.BinarySearchFunc(p.rules, id, func(rule Rule, id string) int { return strings.Compare(rule.ID, id) })
	if !found {
		return Rule{}, false
	}
	return p.rules[index], true
}

type policyDocument struct {
	SetID string `json:"policy_set_id"`
	Rules []Rule `json:"rules"`
}

// Document is the policy written out one way: the name of its rule set and
// its rules in the order of their identifiers, every member of a rule in a
// fixed order. It is what the digest is computed from and what is stored as
// the snapshot of the policy.
func (p Policy) Document() string {
	encoded, err := json.Marshal(policyDocument{SetID: p.setID, Rules: p.rules})
	if err != nil {
		// A policy holds strings and booleans, and they encode.
		panic(fmt.Sprintf("encode a policy: %v", err))
	}
	return string(encoded)
}

// Digest is the SHA-256 digest of the document of the policy.
func (p Policy) Digest() string { return digestOf(p.Document()) }

// RecordedPolicy reads a policy back from the document and the digest it was
// stored with. It fails with ErrPolicyDamaged unless the digest is the digest
// of the document and the document is one Document wrote: a snapshot that was
// altered is not the policy a check was held to.
func RecordedPolicy(document, digest string) (Policy, error) {
	if digestOf(document) != digest {
		return Policy{}, ErrPolicyDamaged
	}
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.DisallowUnknownFields()
	var stored policyDocument
	if err := decoder.Decode(&stored); err != nil {
		return Policy{}, ErrPolicyDamaged
	}
	policy, err := NewPolicy(stored.SetID, stored.Rules)
	if err != nil || policy.Document() != document {
		return Policy{}, ErrPolicyDamaged
	}
	return policy, nil
}

func digestOf(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}

var digestForm = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
