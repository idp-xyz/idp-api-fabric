package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var (
	comparator = domain.Tool{Name: "fabric-openapi-compatibility", Version: "1"}
	current    = []domain.Tool{linter, comparator}
)

// The policy of the release tests: rule 1 is one nothing waives, rule 2
// blocks and can be waived, rule 3 warns, rule 4 is the compatibility rule,
// which no tool of these tests runs and which is enforced outside a check
// when there is no baseline, and rule 5 blocks and is run unless a test
// leaves it out.
func releasePolicy(t *testing.T) domain.Policy {
	t.Helper()
	mandatory := rule("R-RULE-001", domain.SeverityBlock)
	mandatory.Category, mandatory.Waivable = domain.CategorySecurityMandatory, false
	compatibility := rule("R-RULE-004", domain.SeverityBlock)
	compatibility.Category, compatibility.Evaluator = domain.CategoryContractCompatibility, "semantic_diff"
	policy, err := domain.NewPolicy("release-rules", []domain.Rule{
		mandatory, rule("R-RULE-002", domain.SeverityBlock), rule("R-RULE-003", domain.SeverityWarn),
		compatibility, rule("R-RULE-005", domain.SeverityBlock),
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// checked is a check of the revision of these tests under the release
// policy. A rule that is given no observation passed, rule 4 is never run,
// and a rule that is unrun is not run either.
func checked(t *testing.T, observed ...domain.Observation) domain.CheckRun {
	t.Helper()
	return checkedWithout(t, "", observed...)
}

func checkedWithout(t *testing.T, unrun string, observed ...domain.Observation) domain.CheckRun {
	t.Helper()
	said := map[string]domain.Observation{}
	for _, id := range []string{"R-RULE-001", "R-RULE-002", "R-RULE-003", "R-RULE-005"} {
		if id != unrun {
			said[id] = domain.Observation{RuleID: id}
		}
	}
	for _, observation := range observed {
		said[observation.RuleID] = observation
	}
	observations := make([]domain.Observation, 0, len(said))
	for _, observation := range said {
		observations = append(observations, observation)
	}
	run, err := domain.Complete(draft(), releasePolicy(t), shipments(), linter, observations, completed)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func found(ruleID string) domain.Observation {
	return domain.Observation{RuleID: ruleID, Findings: []domain.Finding{{
		RuleID: ruleID, Path: "api/openapi.yaml", Line: 3, Message: "the rule is broken here; mend it",
	}}}
}

func undecided(ruleID string) domain.Observation {
	return domain.Observation{RuleID: ruleID, Undecided: true}
}

func waived(ruleID string) domain.ReleaseWaiver {
	return domain.ReleaseWaiver{RuleID: ruleID, Reason: "accepted for the pilot"}
}

// The rules that stand between a revision and a release are the ones that
// failed or could not be judged, apart by whether anything waives them. A
// rule that warns stands in no way, and neither does one that was not run.
func TestTheBlockersOfACheckAreTheRulesThatDidNotPass(t *testing.T) {
	cases := map[string]struct {
		observed             []domain.Observation
		unwaivable, waivable []string
	}{
		"every evaluated rule passed":                {},
		"a rule that warns":                          {observed: []domain.Observation{found("R-RULE-003")}},
		"a waivable rule that failed":                {observed: []domain.Observation{found("R-RULE-002")}, waivable: []string{"R-RULE-002"}},
		"a waivable rule that could not be judged":   {observed: []domain.Observation{undecided("R-RULE-002")}, waivable: []string{"R-RULE-002"}},
		"a rule nothing waives that failed":          {observed: []domain.Observation{found("R-RULE-001")}, unwaivable: []string{"R-RULE-001"}},
		"a rule nothing waives that was not judged":  {observed: []domain.Observation{undecided("R-RULE-001")}, unwaivable: []string{"R-RULE-001"}},
		"one of each, and a warning":                 {observed: []domain.Observation{found("R-RULE-001"), undecided("R-RULE-002"), found("R-RULE-003")}, unwaivable: []string{"R-RULE-001"}, waivable: []string{"R-RULE-002"}},
		"a warning rule that could not be judged":    {observed: []domain.Observation{undecided("R-RULE-003")}, waivable: []string{"R-RULE-003"}},
		"a warning rule undecided with its findings": {observed: []domain.Observation{{RuleID: "R-RULE-003", Undecided: true, Findings: found("R-RULE-003").Findings}}, waivable: []string{"R-RULE-003"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			run := checked(t, c.observed...)
			if run.Outcomes[3].Status != domain.StatusNotRun {
				t.Fatalf("rule 4 is %s in these tests, want it not run", run.Outcomes[3].Status)
			}
			unwaivable, waivable := domain.Blockers(run)
			if !reflect.DeepEqual(unwaivable, c.unwaivable) || !reflect.DeepEqual(waivable, c.waivable) {
				t.Errorf("unwaivable %v and waivable %v, want %v and %v", unwaivable, waivable, c.unwaivable, c.waivable)
			}
		})
	}
}

// A release is prepared on a check made under the policy and by the tools
// of today, with a waiver of every rule that failed or could not be judged
// and that can be waived, each for a reason. A rule that warns needs none,
// and neither does one that is enforced outside a check and was not run.
// The waivers come back in the order of their rules.
func TestAReleaseIsPreparedOnACurrentCheckWithAWaiverOfEveryRuleThatDidNotPass(t *testing.T) {
	policy := releasePolicy(t)
	for name, c := range map[string]struct {
		run     domain.CheckRun
		waivers []domain.ReleaseWaiver
		want    []domain.ReleaseWaiver
	}{
		"a check in which every evaluated rule passed": {run: checked(t)},
		"a check with a warning":                       {run: checked(t, found("R-RULE-003"))},
		"a waivable rule that failed, waived":          {run: checked(t, found("R-RULE-002")), waivers: []domain.ReleaseWaiver{waived("R-RULE-002")}, want: []domain.ReleaseWaiver{waived("R-RULE-002")}},
		"two rules that did not pass, waived out of order": {
			run:     checked(t, found("R-RULE-002"), undecided("R-RULE-005")),
			waivers: []domain.ReleaseWaiver{waived("R-RULE-005"), {RuleID: "R-RULE-002", Reason: strings.Repeat("评", domain.MaxReviewReason)}},
			want:    []domain.ReleaseWaiver{{RuleID: "R-RULE-002", Reason: strings.Repeat("评", domain.MaxReviewReason)}, waived("R-RULE-005")},
		},
		"a warning rule that could not be judged, waived": {run: checked(t, undecided("R-RULE-003")), waivers: []domain.ReleaseWaiver{waived("R-RULE-003")}, want: []domain.ReleaseWaiver{waived("R-RULE-003")}},
	} {
		t.Run(name, func(t *testing.T) {
			given := append([]domain.ReleaseWaiver(nil), c.waivers...)
			got, err := domain.GateRelease(c.run, policy, current, c.waivers)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("waived %+v, want %+v: %v", got, c.want, err)
			}
			if !reflect.DeepEqual(c.waivers, given) {
				t.Error("the waivers that were handed over were reordered")
			}
		})
	}
}

// What a release refuses to rest on: a check of another policy or other
// tools, a rule nothing waives that did not pass, a waiver that is missing,
// one there is no rule to need it for, one of a rule nothing waives, and a
// rule that blocks and was not run although a check is where it is
// enforced. A refusal for rules names them.
func TestAReleaseRefusesACheckItCannotRestOn(t *testing.T) {
	policy := releasePolicy(t)
	failing := checked(t, found("R-RULE-002"))
	blocked := checked(t, found("R-RULE-001"))
	change := func(of domain.ReleaseWaiver, how func(*domain.ReleaseWaiver)) domain.ReleaseWaiver {
		how(&of)
		return of
	}
	for name, c := range map[string]struct {
		run     domain.CheckRun
		policy  *domain.Policy
		tools   []domain.Tool
		waivers []domain.ReleaseWaiver
		want    error
		names   string
	}{
		"a check under another policy":            {run: checked(t), policy: new(teamPolicy(t)), want: domain.ErrCheckStale},
		"a check by another version of the tool":  {run: checked(t), tools: []domain.Tool{{Name: linter.Name, Version: "2"}, comparator}, want: domain.ErrCheckStale},
		"a check by a tool there is no more":      {run: checked(t), tools: []domain.Tool{comparator}, want: domain.ErrCheckStale},
		"a stale check that is waived as asked":   {run: failing, tools: []domain.Tool{comparator}, waivers: []domain.ReleaseWaiver{waived("R-RULE-002")}, want: domain.ErrCheckStale},
		"a rule nothing waives that failed":       {run: blocked, want: domain.ErrReleaseBlocked, names: "R-RULE-001"},
		"a rule nothing waives that was unjudged": {run: checked(t, undecided("R-RULE-001"), found("R-RULE-002")), waivers: []domain.ReleaseWaiver{waived("R-RULE-002")}, want: domain.ErrReleaseBlocked, names: "R-RULE-001"},
		"a rule nothing waives, waived":           {run: blocked, waivers: []domain.ReleaseWaiver{waived("R-RULE-001")}, want: domain.ErrWaiverForbidden, names: "R-RULE-001"},
		"a rule nothing waives that passed":       {run: failing, waivers: []domain.ReleaseWaiver{waived("R-RULE-001"), waived("R-RULE-002")}, want: domain.ErrWaiverForbidden, names: "R-RULE-001"},
		"a rule that failed and is not waived":    {run: failing, want: domain.ErrWaiverMissing, names: "R-RULE-002"},
		"one of two rules left unwaived":          {run: checked(t, found("R-RULE-002"), undecided("R-RULE-005")), waivers: []domain.ReleaseWaiver{waived("R-RULE-002")}, want: domain.ErrWaiverMissing, names: "R-RULE-005"},
		"a waiver of a rule that passed":          {run: failing, waivers: []domain.ReleaseWaiver{waived("R-RULE-002"), waived("R-RULE-005")}, want: domain.ErrInvalidWaiver},
		"a waiver of a rule that warns":           {run: checked(t, found("R-RULE-003")), waivers: []domain.ReleaseWaiver{waived("R-RULE-003")}, want: domain.ErrInvalidWaiver},
		"a waiver of a rule that was not run":     {run: checked(t), waivers: []domain.ReleaseWaiver{waived("R-RULE-004")}, want: domain.ErrInvalidWaiver},
		"a waiver of a rule the policy has not":   {run: failing, waivers: []domain.ReleaseWaiver{waived("R-RULE-002"), waived("R-RULE-009")}, want: domain.ErrInvalidWaiver},
		"a rule waived twice":                     {run: failing, waivers: []domain.ReleaseWaiver{waived("R-RULE-002"), waived("R-RULE-002")}, want: domain.ErrInvalidWaiver},
		"a waiver without a reason":               {run: failing, waivers: []domain.ReleaseWaiver{change(waived("R-RULE-002"), func(w *domain.ReleaseWaiver) { w.Reason = "" })}, want: domain.ErrInvalidWaiver},
		"a waiver whose reason is two lines":      {run: failing, waivers: []domain.ReleaseWaiver{change(waived("R-RULE-002"), func(w *domain.ReleaseWaiver) { w.Reason = "accepted\nfor now" })}, want: domain.ErrInvalidWaiver},
		"a waiver with space around its reason":   {run: failing, waivers: []domain.ReleaseWaiver{change(waived("R-RULE-002"), func(w *domain.ReleaseWaiver) { w.Reason = " accepted" })}, want: domain.ErrInvalidWaiver},
		"a waiver whose reason is too long": {run: failing, waivers: []domain.ReleaseWaiver{change(waived("R-RULE-002"), func(w *domain.ReleaseWaiver) {
			w.Reason = strings.Repeat("r", domain.MaxReviewReason+1)
		})}, want: domain.ErrInvalidWaiver},
		"a blocking rule that a check enforces and that was not run": {run: checkedWithout(t, "R-RULE-005"), want: domain.ErrReleaseCheckMissing, names: "R-RULE-005"},
	} {
		t.Run(name, func(t *testing.T) {
			held, tools := policy, current
			if c.policy != nil {
				held = *c.policy
			}
			if c.tools != nil {
				tools = c.tools
			}
			got, err := domain.GateRelease(c.run, held, tools, c.waivers)
			if !errors.Is(err, c.want) || got != nil {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, c.want)
			}
			if c.names != "" && !strings.HasSuffix(err.Error(), ": "+c.names) {
				t.Errorf("the refusal is %q, want it to name %s and no other rule", err, c.names)
			}
		})
	}
}

// A check is current when it was made under the policy and by tools a check
// is made with now: every tool it names is one of them, and it names one.
func TestACheckIsCurrentUnderThePolicyAndTheToolsOfToday(t *testing.T) {
	policy := releasePolicy(t)
	run := checked(t)
	if !domain.Current(run, policy, current) || !domain.Current(run, policy, []domain.Tool{linter}) {
		t.Error("a check by the linter of today under the policy of today is not current")
	}
	none := run
	none.Tools = nil
	for name, c := range map[string]struct {
		run    domain.CheckRun
		policy domain.Policy
		tools  []domain.Tool
	}{
		"another policy":          {run, teamPolicy(t), current},
		"another version of it":   {run, policy, []domain.Tool{{Name: linter.Name, Version: "2"}}},
		"no tool of today":        {run, policy, nil},
		"a check that names none": {none, policy, current},
		"a tool there is no more": {run, policy, []domain.Tool{comparator}},
	} {
		if domain.Current(c.run, c.policy, c.tools) {
			t.Errorf("%s: the check is current", name)
		}
	}
}
