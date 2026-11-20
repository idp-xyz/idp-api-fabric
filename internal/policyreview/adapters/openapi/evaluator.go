// Package openapi evaluates the rules of the rule registry that can be
// judged from the files of an OpenAPI contract alone: the automated subset
// of F04 §7.1. It reads the files it is handed and nothing else: no
// reference makes it open a file or an address.
//
// Checks cover the official document schema and the built-in rules.
// A check records rules without an evaluator as not run.
package openapi

import (
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// Version is the version of the behaviour of the evaluator. A check of a
// revision is recorded once for a policy and a set of tools, and asking
// again answers what was recorded. So whatever makes the evaluator find
// something else in the same files is another version: a rule it judges
// differently, a credential form it learns, a bound it changes. A test
// holds the behaviour to the version.
//
// Raising it has a price since releases are published (F05 §6.7). A release
// is prepared, activated, restored and served only while the check its
// approval rests on was made by the tools of today. When a build with
// another version starts, every approval given before stops counting, and
// what is published under those approvals is served no more.
const Version = "3"

// Evaluator is the built-in tool of a check.
type Evaluator struct{}

var _ ports.Evaluator = Evaluator{}

func (Evaluator) Tool() domain.Tool {
	return domain.Tool{Name: "fabric-openapi-rules", Version: Version}
}

// evaluators are the evaluators of the rule registry this tool implements,
// by the names the registry gives them. reads says whether the evaluator
// reads the documents: it is undecided on a source whose files are not all
// well-formed, since a rule about what a document says cannot be judged on
// one that says nothing clear. The others read the bytes.
var evaluators = map[string]struct {
	judge func(*bundle) judgment
	reads bool
}{
	"supported_spec_version":  {(*bundle).specVersion, true},
	"info_required":           {(*bundle).infoRequired, true},
	"operation_id_required":   {(*bundle).operationIDRequired, true},
	"operation_id_unique":     {(*bundle).operationIDUnique, true},
	"operation_security":      {(*bundle).operationSecurity, true},
	"problem_content":         {(*bundle).problemContent, true},
	"official_oas_validation": {(*bundle).officialValidation, true},
	"ref_closure_budget":      {(*bundle).referenceClosure, false},
	"ref_safety":              {(*bundle).refSafety, true},
	"parse_safety":            {(*bundle).parseSafety, false},
	"secret_scan":             {func(b *bundle) judgment { return b.scan(credentialOn) }, false},
	"internal_url_scan":       {func(b *bundle) judgment { return b.scan(internalAddressOn) }, false},
}

// Evaluate judges the source by the rules whose evaluator is one of
// evaluators, and says nothing of the others.
func (Evaluator) Evaluate(source domain.Source, rules []domain.Rule) []domain.Observation {
	read := read(source)
	var observations []domain.Observation
	for _, rule := range rules {
		evaluator, implemented := evaluators[rule.Evaluator]
		if !implemented || (rule.Evaluator == "ref_closure_budget" && source.Closure == 0) {
			continue
		}
		observation := domain.Observation{RuleID: rule.ID}
		if evaluator.reads && !read.readable() {
			observation.Undecided = true
		} else {
			judged := evaluator.judge(read)
			observation.Undecided = judged.undecided
			for _, found := range judged.findings {
				observation.Findings = append(observation.Findings, domain.Finding{
					RuleID: rule.ID, Path: found.place.file, Pointer: found.place.pointer, Line: found.place.line, Message: found.message,
				})
			}
		}
		observations = append(observations, observation)
	}
	return observations
}

func (b *bundle) referenceClosure() judgment {
	switch b.source.Closure {
	case domain.ClosureVerified:
		return judgment{}
	case domain.ClosureInvalid:
		return judgment{findings: []found{{place{file: b.source.Entrypoint}, "the recorded reference closure could not be verified within the import bounds"}}}
	default:
		return judgment{undecided: true}
	}
}
