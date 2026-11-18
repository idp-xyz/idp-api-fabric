// Package openapi evaluates the rules of the rule registry that can be
// judged from the files of an OpenAPI contract alone: the automated subset
// of F04 §7.1. It reads the files it is handed and nothing else: no
// reference makes it open a file or an address.
//
// What it does not evaluate it does not report on. Validation against the
// official OpenAPI schema, the comparison with a published baseline and the
// rules about review, release and publication are not here, and a check
// records them as not run.
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
const Version = "1"

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
	"supported_spec_version": {(*bundle).specVersion, true},
	"info_required":          {(*bundle).infoRequired, true},
	"operation_id_required":  {(*bundle).operationIDRequired, true},
	"operation_id_unique":    {(*bundle).operationIDUnique, true},
	"operation_security":     {(*bundle).operationSecurity, true},
	"problem_content":        {(*bundle).problemContent, true},
	"ref_safety":             {(*bundle).refSafety, true},
	"parse_safety":           {(*bundle).parseSafety, false},
	"secret_scan":            {func(b *bundle) judgment { return b.scan(credentialOn) }, false},
	"internal_url_scan":      {func(b *bundle) judgment { return b.scan(internalAddressOn) }, false},
}

// Evaluate judges the source by the rules whose evaluator is one of
// evaluators, and says nothing of the others.
func (Evaluator) Evaluate(source domain.Source, rules []domain.Rule) []domain.Observation {
	read := read(source)
	var observations []domain.Observation
	for _, rule := range rules {
		evaluator, implemented := evaluators[rule.Evaluator]
		if !implemented {
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
