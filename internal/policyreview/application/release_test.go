package application_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

// answered are the rules a check of a contract answers for: the ones its
// tools evaluate. The other rules of the baseline are held by other gates
// (the registry, publication, rendering, delivery), and a check says of them
// that they were not run. So is the compatibility rule of a check that
// compared the revision with nothing.
var answered = []string{
	"F04-GOV-001", "F04-GOV-002", "F04-GOV-003", "F04-GOV-004", "F04-GOV-005", "F04-GOV-006",
	"F04-GOV-007", "F04-GOV-008", "F04-GOV-009", "F04-GOV-010", "F04-GOV-011", "F04-GOV-012",
}

// before is the revision that is published when another replaces it.
var before = domain.ComparisonBaseline{RevisionID: "before", InputDigest: "sha256:" + strings.Repeat("b", 64)}

var (
	// undescribed is an operation without an operationId: a rule that
	// blocks and that whoever prepares a release may waive.
	undescribed = domain.Observation{RuleID: "F04-GOV-003", Findings: []domain.Finding{{
		RuleID: "F04-GOV-003", Path: "openapi.yaml", Line: 4, Message: "the operation has no operationId; give it one",
	}}}
	// untitled is a document whose info the rules tool could not judge:
	// another rule that blocks and may be waived.
	untitled = domain.Observation{RuleID: "F04-GOV-002", Undecided: true}
	// leaked is a credential the secret scan found: a rule nothing waives.
	leaked = domain.Observation{RuleID: "F04-GOV-004", Findings: []domain.Finding{{
		RuleID: "F04-GOV-004", Path: "openapi.yaml", Line: 1, Message: "the line holds what has the form of a credential",
	}}}
	// unscanned is a secret scan that could not be judged.
	unscanned = domain.Observation{RuleID: "F04-GOV-004", Undecided: true}
	// unproblematic is an error response that is no problem body: a rule
	// that only warns.
	unproblematic = domain.Observation{RuleID: "F04-GOV-007", Findings: []domain.Finding{{
		RuleID: "F04-GOV-007", Path: "openapi.yaml", Line: 9, Message: "the error response is not application/problem+json",
	}}}
)

func waiver(rule string) domain.ReleaseWaiver {
	return domain.ReleaseWaiver{RuleID: rule, Reason: "generated clients are not used yet"}
}

// checked says what a check of the revision was like.
type checked struct {
	// compared is the revision the check compared this one with, if any.
	compared *domain.ComparisonBaseline
	// unanswered is a rule the check answers for that was not run.
	unanswered string
	// observed is what the tools found; every other rule passed.
	observed []domain.Observation
}

// run is the check carol ran, by the tool, as it is stored.
func (c checked) run(t *testing.T, w *world, tool domain.Tool) domain.CheckRun {
	t.Helper()
	said := map[string]domain.Observation{}
	for _, observation := range c.observed {
		said[observation.RuleID] = observation
	}
	var observed []domain.Observation
	for _, rule := range answered {
		if observation, found := said[rule]; found {
			observed = append(observed, observation)
		} else if rule != c.unanswered {
			observed = append(observed, domain.Observation{RuleID: rule})
		}
	}
	draft := domain.Draft{
		ID: "check", PolicySnapshotID: "snapshot", Revision: shipment, RequestedBy: carol, StartedAt: w.now.Add(-time.Hour),
	}
	if c.compared != nil {
		draft.Compatibility = &domain.ComparisonEvidence{
			Baseline: *c.compared, Tool: compareTool, Comparison: domain.Comparison{Classification: domain.Compatible},
		}
	}
	source := domain.Source{
		Entrypoint: "openapi.yaml", Digest: closure, Files: []domain.SourceFile{{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"}},
	}
	run, err := domain.Complete(draft, domain.Baseline(), source, tool, observed, w.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w.runs["check"] = run
	return run
}

// silent are the events of a check, which verifying one never announces.
type silent struct{ t *testing.T }

func (s silent) Completed(context.Context, domain.CheckRun, string) error {
	s.t.Error("verifying the check of a release announced a check")
	return nil
}

// checking is the use cases of a check in a world whose tools are the
// rules tool and the comparator of today, unless the test says otherwise.
// Verifying the check of a release reads: it records and announces nothing.
func (w *world) checking(t *testing.T, evaluator, comparator domain.Tool) *application.Checks {
	t.Helper()
	checks, err := application.NewChecks(application.Dependencies{
		Transactions: w, Revisions: w, Checks: w,
		Evaluators:  map[domain.Specification]ports.Evaluator{domain.OpenAPI: toolOf(evaluator)},
		Comparators: map[domain.Specification]ports.Comparator{domain.OpenAPI: toolOf(comparator)},
		Audit: func(context.Context, ports.AuditEntry) error {
			t.Error("verifying the check of a release left an audit record")
			return nil
		},
		Events: silent{t}, Clock: w, Identifiers: w,
	})
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

// preparing is what is asked when a release of the revision is prepared on
// the check, with the waivers.
func preparing(waivers ...domain.ReleaseWaiver) application.ReleaseCheck {
	return application.ReleaseCheck{Revision: shipment, CheckRunID: "check", InputDigest: closure, Waivers: waivers, Preparing: true}
}

// recordedCheck is what a release that was prepared on the check records of it.
var recordedCheck = application.VerifiedCheck{CheckRunID: "check", PolicySnapshotID: "snapshot", PolicyDigest: domain.Baseline().Digest()}

// A release is prepared on a check of its revision, and on nothing else: no
// review approves it. The check is one made under the policy and by the
// tools of today, and every rule that failed or could not be judged in it
// is waived by name, for a reason, and each exactly once. A rule that only
// warns needs no waiver, and neither does a rule a check of a contract does
// not answer for. The waivers are handed back in the order of their rules.
func TestAReleaseIsPreparedOnACurrentCheckWithTheWaiversItNeeds(t *testing.T) {
	prepared := map[string]struct {
		given   checked
		input   application.ReleaseCheck
		waivers []domain.ReleaseWaiver
	}{
		"a check in which every rule it answers for passed":                    {input: preparing()},
		"a check without a baseline, which did not run the compatibility rule": {input: preparing()},
		"a rule that only warns and failed":                                    {given: checked{observed: []domain.Observation{unproblematic}}, input: preparing()},
		"a rule that only warns and was not run":                               {given: checked{unanswered: "F04-GOV-007"}, input: preparing()},
		"a waivable rule that failed, waived": {
			given: checked{observed: []domain.Observation{undescribed}}, input: preparing(waiver("F04-GOV-003")),
			waivers: []domain.ReleaseWaiver{waiver("F04-GOV-003")}},
		"a waivable rule that could not be judged, waived": {
			given: checked{observed: []domain.Observation{untitled}}, input: preparing(waiver("F04-GOV-002")),
			waivers: []domain.ReleaseWaiver{waiver("F04-GOV-002")}},
		"two waivable rules, waived in another order": {
			given:   checked{observed: []domain.Observation{undescribed, untitled}},
			input:   preparing(waiver("F04-GOV-003"), waiver("F04-GOV-002")),
			waivers: []domain.ReleaseWaiver{waiver("F04-GOV-002"), waiver("F04-GOV-003")}},
		"a check compared with the revision the release replaces": {
			given: checked{compared: &before}, input: func() application.ReleaseCheck {
				input := preparing()
				input.Baseline = &before
				return input
			}()},
		"a check that was compared, where nothing is replaced": {given: checked{compared: &before}, input: preparing()},
	}
	for name, c := range prepared {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			c.given.run(t, w, rulesTool)
			got, err := w.checking(t, rulesTool, compareTool).VerifyReleaseCheck(context.Background(), c.input)
			want := recordedCheck
			want.Waivers = c.waivers
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// A check that a release cannot rest on prepares none. It is refused for
// what it is not: a recorded check of this content, a check of today, one
// in which no rule that nothing waives failed or went unjudged, one that ran
// every rule that blocks and is held nowhere else, and, where the release
// replaces one of another revision, one that compared the two. And it is
// refused for waivers that are not exactly the ones its failures need.
func TestAReleaseIsNotPreparedOnACheckItCannotRestOn(t *testing.T) {
	type arrange func(*testing.T, *world, *application.ReleaseCheck) *application.Checks
	with := func(change func(*application.ReleaseCheck)) arrange {
		return func(_ *testing.T, _ *world, input *application.ReleaseCheck) *application.Checks {
			change(input)
			return nil
		}
	}
	waiving := func(waivers ...domain.ReleaseWaiver) arrange {
		return with(func(input *application.ReleaseCheck) { input.Waivers = waivers })
	}
	over := func(baseline domain.ComparisonBaseline) arrange {
		return with(func(input *application.ReleaseCheck) { input.Baseline = &baseline })
	}
	type refusal struct {
		given   checked
		arrange arrange
		want    error
		// names is what the refusal says besides.
		names string
	}
	refused := map[string]refusal{
		"a check nobody recorded": {arrange: with(func(input *application.ReleaseCheck) { input.CheckRunID = "another" }), want: application.ErrCheckRunNotFound},
		"a check of another revision": {
			arrange: with(func(input *application.ReleaseCheck) { input.Revision.RevisionID = "another" }), want: application.ErrCheckRunNotFound},
		"a check that read other content than the revision has": {
			arrange: with(func(input *application.ReleaseCheck) { input.InputDigest = "sha256:" + strings.Repeat("c", 64) }),
			want:    domain.ErrCheckRunDamaged},

		"a check by a version of the rules tool that moved on": {
			arrange: func(t *testing.T, w *world, _ *application.ReleaseCheck) *application.Checks {
				return w.checking(t, domain.Tool{Name: rulesTool.Name, Version: "4"}, compareTool)
			},
			want: domain.ErrCheckStale},
		"a compared check by a version of the comparator that moved on": {
			given: checked{compared: &before},
			arrange: func(t *testing.T, w *world, _ *application.ReleaseCheck) *application.Checks {
				return w.checking(t, rulesTool, domain.Tool{Name: compareTool.Name, Version: "2"})
			},
			want: domain.ErrCheckStale},
		"a check under another policy": {
			arrange: func(_ *testing.T, w *world, _ *application.ReleaseCheck) *application.Checks {
				run := w.runs["check"]
				run.PolicyDigest = "sha256:" + strings.Repeat("d", 64)
				w.runs["check"] = run
				return nil
			},
			want: domain.ErrCheckStale},

		"a rule nothing waives that failed": {
			given: checked{observed: []domain.Observation{leaked}}, want: domain.ErrReleaseBlocked, names: "F04-GOV-004"},
		"a rule nothing waives that could not be judged": {
			given: checked{observed: []domain.Observation{unscanned}}, want: domain.ErrReleaseBlocked, names: "F04-GOV-004"},
		"a rule nothing waives that failed, beside a waivable one that is waived": {
			given: checked{observed: []domain.Observation{leaked, undescribed}}, arrange: waiving(waiver("F04-GOV-003")),
			want: domain.ErrReleaseBlocked, names: "F04-GOV-004"},

		"a waivable failure without its waiver": {
			given: checked{observed: []domain.Observation{undescribed}}, want: domain.ErrWaiverMissing, names: "F04-GOV-003"},
		"one of two waivable failures waived": {
			given: checked{observed: []domain.Observation{undescribed, untitled}}, arrange: waiving(waiver("F04-GOV-003")),
			want: domain.ErrWaiverMissing, names: "F04-GOV-002"},
		"a waiver of a rule nothing waives": {
			given: checked{observed: []domain.Observation{leaked}}, arrange: waiving(waiver("F04-GOV-004")),
			want: domain.ErrWaiverForbidden, names: "F04-GOV-004"},
		"a waiver of a rule that passed":      {arrange: waiving(waiver("F04-GOV-003")), want: domain.ErrInvalidWaiver, names: "F04-GOV-003"},
		"a waiver of a rule that only warns":  {given: checked{observed: []domain.Observation{unproblematic}}, arrange: waiving(waiver("F04-GOV-007")), want: domain.ErrInvalidWaiver, names: "F04-GOV-007"},
		"a waiver of a rule that was not run": {arrange: waiving(waiver("F04-GOV-013")), want: domain.ErrInvalidWaiver, names: "F04-GOV-013"},
		"a waiver of a rule the policy does not have": {
			given: checked{observed: []domain.Observation{undescribed}}, arrange: waiving(waiver("F04-GOV-003"), waiver("F04-GOV-099")),
			want: domain.ErrInvalidWaiver},
		"a rule waived twice": {
			given: checked{observed: []domain.Observation{undescribed}}, arrange: waiving(waiver("F04-GOV-003"), waiver("F04-GOV-003")),
			want: domain.ErrInvalidWaiver, names: "F04-GOV-003"},
		"a waiver without a reason": {
			given: checked{observed: []domain.Observation{undescribed}}, arrange: waiving(domain.ReleaseWaiver{RuleID: "F04-GOV-003"}),
			want: domain.ErrInvalidWaiver, names: "F04-GOV-003"},
		"a waiver whose reason is two lines": {
			given:   checked{observed: []domain.Observation{undescribed}},
			arrange: waiving(domain.ReleaseWaiver{RuleID: "F04-GOV-003", Reason: "generated clients\nare not used yet"}),
			want:    domain.ErrInvalidWaiver, names: "F04-GOV-003"},
		"a waiver whose reason is too long": {
			given:   checked{observed: []domain.Observation{undescribed}},
			arrange: waiving(domain.ReleaseWaiver{RuleID: "F04-GOV-003", Reason: strings.Repeat("é", domain.MaxReviewReason+1)}),
			want:    domain.ErrInvalidWaiver, names: "F04-GOV-003"},

		// Replacing what is published takes a check that compared this
		// revision with the one that is replaced.
		"a check that compared with nothing": {arrange: over(before), want: application.ErrBaselineMismatch},
		"a check that compared with another revision": {
			given: checked{compared: &before}, arrange: over(domain.ComparisonBaseline{RevisionID: "earlier", InputDigest: before.InputDigest}),
			want: application.ErrBaselineMismatch},
		"a check that compared with other content of it": {
			given: checked{compared: &before}, arrange: over(domain.ComparisonBaseline{RevisionID: before.RevisionID, InputDigest: closure}),
			want: application.ErrBaselineMismatch},
	}
	// A rule that blocks and that a check of a contract answers for has to
	// have been run: nothing else holds a release to it.
	for _, rule := range answered {
		if rule != "F04-GOV-007" {
			refused["a check that did not run "+rule] = refusal{
				given: checked{unanswered: rule}, want: domain.ErrReleaseCheckMissing, names: rule,
			}
		}
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			c.given.run(t, w, rulesTool)
			input := preparing()
			var checks *application.Checks
			if c.arrange != nil {
				checks = c.arrange(t, w, &input)
			}
			if checks == nil {
				checks = w.checking(t, rulesTool, compareTool)
			}
			got, err := checks.VerifyReleaseCheck(context.Background(), input)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, application.VerifiedCheck{}) || !strings.Contains(err.Error(), c.names) {
				t.Fatalf("got %+v, %v; want %v naming %q and no evidence", got, err, c.want, c.names)
			}
		})
	}
}

// A release that was prepared rests on its check for good. Activating it and
// returning a slot to it ask nothing of the policy and the tools of today,
// nor of the rules of the check: they were asked when the release was
// prepared, and a policy that moves on does not take back what was released
// under the one before. What is asked is that the check is the one the
// release recorded: a check of this revision that read this content. The
// waivers are the release's, and are not asked for again.
func TestAPreparedReleaseRestsOnItsCheckWhateverThePolicyAndToolsAreNow(t *testing.T) {
	afterwards := func(input application.ReleaseCheck) application.ReleaseCheck {
		input.Preparing = false
		return input
	}
	accepted := map[string]struct {
		given   checked
		tool    domain.Tool
		arrange func(*world, *application.ReleaseCheck)
	}{
		"a check under the policy and the tools of today":      {},
		"a check by a version of the rules tool that moved on": {tool: domain.Tool{Name: rulesTool.Name, Version: "4"}},
		"a check under another policy": {
			arrange: func(w *world, _ *application.ReleaseCheck) {
				run := w.runs["check"]
				run.PolicyDigest = "sha256:" + strings.Repeat("d", 64)
				w.runs["check"] = run
			}},
		"a waivable failure whose waiver is not given again": {given: checked{observed: []domain.Observation{undescribed}}},
		"waivers that are not asked for": {
			arrange: func(_ *world, input *application.ReleaseCheck) {
				input.Waivers = []domain.ReleaseWaiver{waiver("F04-GOV-099")}
			}},
		"a rule nothing waives that failed":           {given: checked{observed: []domain.Observation{leaked}}},
		"a check that did not run a rule that blocks": {given: checked{unanswered: "F04-GOV-004"}},
		"a check compared with the revision the slot served last": {
			given:   checked{compared: &before},
			arrange: func(_ *world, input *application.ReleaseCheck) { input.Baseline = &before }},
		"a check compared with the revision the slot served last, by tools that moved on": {
			given: checked{compared: &before}, tool: domain.Tool{Name: rulesTool.Name, Version: "4"},
			arrange: func(_ *world, input *application.ReleaseCheck) { input.Baseline = &before }},
	}
	for name, c := range accepted {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			c.given.run(t, w, rulesTool)
			input := afterwards(preparing())
			if c.arrange != nil {
				c.arrange(w, &input)
			}
			tool := rulesTool
			if c.tool != (domain.Tool{}) {
				tool = c.tool
			}
			// What is handed back is what the release recorded of its
			// check: the policy the check was held to, whichever it was.
			want := recordedCheck
			want.PolicyDigest = w.runs["check"].PolicyDigest
			got, err := w.checking(t, tool, compareTool).VerifyReleaseCheck(context.Background(), input)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
		})
	}

	refused := map[string]struct {
		given  checked
		change func(*application.ReleaseCheck)
		want   error
	}{
		// The release names its check, so a check that is not there is a
		// record that is not what the release recorded, and not a check the
		// caller asked for that is missing.
		"a check nobody recorded":     {change: func(input *application.ReleaseCheck) { input.CheckRunID = "another" }, want: domain.ErrCheckRunDamaged},
		"a check of another revision": {change: func(input *application.ReleaseCheck) { input.Revision.RevisionID = "another" }, want: domain.ErrCheckRunDamaged},
		"a check that read other content than the release": {
			change: func(input *application.ReleaseCheck) { input.InputDigest = "sha256:" + strings.Repeat("c", 64) }, want: domain.ErrCheckRunDamaged},
		"a check that compared with nothing, replacing another revision": {
			change: func(input *application.ReleaseCheck) { input.Baseline = &before }, want: application.ErrBaselineMismatch},
		"a check that compared with another revision than the slot served": {
			given: checked{compared: &before},
			change: func(input *application.ReleaseCheck) {
				input.Baseline = &domain.ComparisonBaseline{RevisionID: "earlier", InputDigest: before.InputDigest}
			},
			want: application.ErrBaselineMismatch},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			c.given.run(t, w, rulesTool)
			input := afterwards(preparing())
			c.change(&input)
			got, err := w.checking(t, rulesTool, compareTool).VerifyReleaseCheck(context.Background(), input)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, application.VerifiedCheck{}) {
				t.Fatalf("got %+v, %v; want %v and no evidence", got, err, c.want)
			}
		})
	}
}
