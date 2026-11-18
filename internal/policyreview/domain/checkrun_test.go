package domain_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var (
	started   = time.Date(2026, 11, 18, 16, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
	completed = started.Add(42 * time.Millisecond)
	linter    = domain.Tool{Name: "fabric-openapi-rules", Version: "1"}
)

func rule(id string, severity domain.Severity) domain.Rule {
	stated := quality(id)
	stated.Severity = severity
	return stated
}

// The policy of these tests: rules 1, 3 and 5 block, rules 2 and 4 warn.
func teamPolicy(t *testing.T) domain.Policy {
	t.Helper()
	policy, err := domain.NewPolicy("team-rules", []domain.Rule{
		rule("T-RULE-001", domain.SeverityBlock), rule("T-RULE-002", domain.SeverityWarn), rule("T-RULE-003", domain.SeverityBlock),
		rule("T-RULE-004", domain.SeverityWarn), rule("T-RULE-005", domain.SeverityBlock),
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func shipments() domain.Source {
	return domain.Source{
		Entrypoint: "api/openapi.yaml", Digest: digestOf("the closure of the revision"),
		Files: []domain.SourceFile{
			{Path: "api/openapi.yaml", Content: "openapi: 3.1.2\n"},
			{Path: "schemas/shipment.yaml", Content: "type: object\n"},
		},
	}
}

func draft() domain.Draft {
	return domain.Draft{
		ID: "check-1", PolicySnapshotID: "snapshot-1", StartedAt: started,
		Revision:    domain.RevisionRef{ProductID: "product-1", WorkspaceID: "workspace-1", AssetID: "asset-1", RevisionID: "revision-1"},
		RequestedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "carol"},
	}
}

func finding(rule string, line int) domain.Finding {
	return domain.Finding{
		RuleID: rule, Path: "api/openapi.yaml", Pointer: "/paths/~1v1~1shipments/get", Line: line,
		Message: "the operation has no operationId; give it one",
	}
}

// nothing reports that the rules were evaluated and nothing was found.
func nothing(rules ...string) []domain.Observation {
	observations := make([]domain.Observation, len(rules))
	for i, id := range rules {
		observations[i] = domain.Observation{RuleID: id}
	}
	return observations
}

// A check says of every rule of its policy what came of it: evaluated and
// passed, evaluated with findings that warn or block, evaluated and
// undecided, or not run at all. It names the input, the policy and the tool
// it was concluded from, and whoever reads it can tell each of the five
// apart.
func TestACheckSaysOfEveryRuleOfItsPolicyWhatCameOfIt(t *testing.T) {
	policy := teamPolicy(t)
	observations := []domain.Observation{
		{RuleID: "T-RULE-003", Findings: []domain.Finding{finding("T-RULE-003", 31), finding("T-RULE-003", 12)}},
		{RuleID: "T-RULE-001"},
		{RuleID: "T-RULE-004", Undecided: true},
		// What a tool says of the level of a finding is not asked: a
		// finding has the severity of its rule.
		{RuleID: "T-RULE-002", Findings: []domain.Finding{{
			RuleID: "T-RULE-002", Level: domain.SeverityBlock, Path: "schemas/shipment.yaml", Line: 1, Message: "the schema has no description",
		}}},
	}
	handed := slices.Clone(observations[0].Findings)
	run, err := domain.Complete(draft(), policy, shipments(), linter, observations, completed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(observations[0].Findings, handed) {
		t.Error("the findings handed in were reordered")
	}

	rules := policy.Rules()
	want := domain.CheckRun{
		ID: "check-1", PolicySnapshotID: "snapshot-1", Revision: draft().Revision, RequestedBy: draft().RequestedBy,
		InputDigest: shipments().Digest, PolicySetID: "team-rules", PolicyDigest: policy.Digest(), Tools: []domain.Tool{linter},
		Status: domain.StatusFail,
		Outcomes: []domain.Outcome{
			{Rule: rules[0], Status: domain.StatusPass, Tool: linter},
			{Rule: rules[1], Status: domain.StatusWarn, Findings: 1, Tool: linter},
			{Rule: rules[2], Status: domain.StatusFail, Findings: 2, Tool: linter},
			{Rule: rules[3], Status: domain.StatusUnknown, Tool: linter},
			{Rule: rules[4], Status: domain.StatusNotRun},
		},
		Findings: []domain.Finding{
			{RuleID: "T-RULE-002", Level: domain.SeverityWarn, Path: "schemas/shipment.yaml", Line: 1, Message: "the schema has no description"},
			{RuleID: "T-RULE-003", Level: domain.SeverityBlock, Path: "api/openapi.yaml", Pointer: "/paths/~1v1~1shipments/get", Line: 12,
				Message: "the operation has no operationId; give it one"},
			{RuleID: "T-RULE-003", Level: domain.SeverityBlock, Path: "api/openapi.yaml", Pointer: "/paths/~1v1~1shipments/get", Line: 31,
				Message: "the operation has no operationId; give it one"},
		},
		// Times are kept as storage keeps them: in UTC, to the microsecond.
		StartedAt:   time.Date(2026, 11, 18, 8, 0, 0, 123456000, time.UTC),
		CompletedAt: time.Date(2026, 11, 18, 8, 0, 0, 165456000, time.UTC),
	}
	if !reflect.DeepEqual(run, want) {
		t.Fatalf("the check is\n %+v\nwant\n %+v", run, want)
	}
	if got, want := run.Summary(), (domain.Summary{Pass: 1, Warn: 1, Fail: 1, Unknown: 1, NotRun: 1, Findings: 3}); got != want {
		t.Errorf("the check counts %+v, want %+v", got, want)
	}

	// A clock that was set back while the check ran does not make it end
	// before it began.
	earlier, err := domain.Complete(draft(), policy, shipments(), linter, nothing("T-RULE-001"), started.Add(-time.Hour))
	if err != nil || !earlier.CompletedAt.Equal(earlier.StartedAt) {
		t.Errorf("a check that was completed before it began: from %v to %v, %v", earlier.StartedAt, earlier.CompletedAt, err)
	}
}

// A check is as good as the worst of its rules. One that blocks fails it.
// Short of that, a rule that was not run or could not be judged leaves it
// unknown, however many others passed: a check does not pass on the rules it
// happened to evaluate (F04 §8).
func TestACheckIsAsGoodAsTheWorstOfItsRules(t *testing.T) {
	all := []string{"T-RULE-001", "T-RULE-002", "T-RULE-003", "T-RULE-004", "T-RULE-005"}
	with := func(changed ...domain.Observation) []domain.Observation {
		observations := nothing(all...)
		for _, change := range changed {
			for i := range observations {
				if observations[i].RuleID == change.RuleID {
					observations[i] = change
				}
			}
		}
		return observations
	}
	warns := domain.Observation{RuleID: "T-RULE-002", Findings: []domain.Finding{finding("T-RULE-002", 3)}}
	blocks := domain.Observation{RuleID: "T-RULE-003", Findings: []domain.Finding{finding("T-RULE-003", 3)}}
	cases := map[string]struct {
		observations []domain.Observation
		status       domain.Status
		// rule is what is concluded of the rule the case is about.
		about string
		rule  domain.Status
	}{
		"every rule evaluated and nothing found":    {with(), domain.StatusPass, "T-RULE-001", domain.StatusPass},
		"a finding that warns":                      {with(warns), domain.StatusWarn, "T-RULE-002", domain.StatusWarn},
		"a rule that was not run":                   {nothing(all[:4]...), domain.StatusUnknown, "T-RULE-005", domain.StatusNotRun},
		"a rule that warns and was not run":         {nothing("T-RULE-001", "T-RULE-003", "T-RULE-004", "T-RULE-005"), domain.StatusUnknown, "T-RULE-002", domain.StatusNotRun},
		"no rule that was run":                      {nil, domain.StatusUnknown, "T-RULE-001", domain.StatusNotRun},
		"a warning beside a rule that was not run":  {with(warns)[:4], domain.StatusUnknown, "T-RULE-002", domain.StatusWarn},
		"a rule that could not be judged":           {with(domain.Observation{RuleID: "T-RULE-001", Undecided: true}), domain.StatusUnknown, "T-RULE-001", domain.StatusUnknown},
		"a warning of a rule that is undecided":     {with(domain.Observation{RuleID: "T-RULE-002", Undecided: true, Findings: warns.Findings}), domain.StatusUnknown, "T-RULE-002", domain.StatusUnknown},
		"a finding that blocks":                     {with(blocks), domain.StatusFail, "T-RULE-003", domain.StatusFail},
		"a finding that blocks beside a warning":    {with(blocks, warns), domain.StatusFail, "T-RULE-002", domain.StatusWarn},
		"a finding that blocks and rules not run":   {[]domain.Observation{blocks}, domain.StatusFail, "T-RULE-001", domain.StatusNotRun},
		"a blocking finding of an undecided rule":   {with(domain.Observation{RuleID: "T-RULE-003", Undecided: true, Findings: blocks.Findings}), domain.StatusFail, "T-RULE-003", domain.StatusFail},
		"a finding that blocks beside an undecided": {with(blocks, domain.Observation{RuleID: "T-RULE-001", Undecided: true}), domain.StatusFail, "T-RULE-001", domain.StatusUnknown},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			run, err := domain.Complete(draft(), teamPolicy(t), shipments(), linter, c.observations, completed)
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(run.Outcomes, func(outcome domain.Outcome) bool { return outcome.Rule.ID == c.about })
			if run.Status != c.status || run.Outcomes[index].Status != c.rule || len(run.Outcomes) != 5 {
				t.Fatalf("the check is %s and rule %s is %s, want %s and %s: %+v", run.Status, c.about, run.Outcomes[index].Status, c.status, c.rule, run.Outcomes)
			}
			if run.Summary().Status() != run.Status {
				t.Errorf("the check is %s and its counts %+v add up to %s", run.Status, run.Summary(), run.Summary().Status())
			}
			for _, outcome := range run.Outcomes {
				if (outcome.Status == domain.StatusNotRun) != (outcome.Tool == domain.Tool{}) {
					t.Errorf("rule %s is %s and names the tool %+v", outcome.Rule.ID, outcome.Status, outcome.Tool)
				}
			}
		})
	}

	// The counts decide alone, whatever else is counted.
	for counts, want := range map[domain.Summary]domain.Status{
		{}:                           domain.StatusPass,
		{Pass: 23}:                   domain.StatusPass,
		{Pass: 22, Warn: 1}:          domain.StatusWarn,
		{Pass: 10, NotRun: 13}:       domain.StatusUnknown,
		{Pass: 22, Unknown: 1}:       domain.StatusUnknown,
		{Warn: 5, NotRun: 1}:         domain.StatusUnknown,
		{Pass: 22, Fail: 1}:          domain.StatusFail,
		{Fail: 1, Unknown: 9}:        domain.StatusFail,
		{Fail: 1, NotRun: 9}:         domain.StatusFail,
		{Fail: 1, Warn: 9, Pass: 13}: domain.StatusFail,
	} {
		if got := counts.Status(); got != want {
			t.Errorf("%+v adds up to %s, want %s", counts, got, want)
		}
	}
}

// The findings of a rule are kept in the order of their places, and at most
// fifty of them: the outcome of the rule counts them all, so whoever reads
// the check knows how many there are besides the ones it shows.
func TestFindingsAreKeptInOrderAndBounded(t *testing.T) {
	var reported []domain.Finding
	for line := 70; line >= 1; line-- {
		reported = append(reported, finding("T-RULE-003", line))
	}
	places := []domain.Finding{
		{RuleID: "T-RULE-002", Path: "schemas/shipment.yaml", Line: 2, Message: "b"},
		{RuleID: "T-RULE-002", Path: "api/openapi.yaml", Pointer: "/info/title", Line: 9, Message: "b"},
		{RuleID: "T-RULE-002", Path: "api/openapi.yaml", Pointer: "/info", Line: 9, Message: "b"},
		{RuleID: "T-RULE-002", Path: "api/openapi.yaml", Pointer: "/info", Line: 9, Message: "a"},
		{RuleID: "T-RULE-002", Path: "api/openapi.yaml", Line: 2, Message: "b"},
		{RuleID: "T-RULE-002", Path: "api/openapi.yaml", Pointer: "/zzz", Message: "b"},
	}
	run, err := domain.Complete(draft(), teamPolicy(t), shipments(), linter, []domain.Observation{
		{RuleID: "T-RULE-003", Findings: reported}, {RuleID: "T-RULE-002", Findings: places},
	}, completed)
	if err != nil {
		t.Fatal(err)
	}
	if got := run.Outcomes[2]; got.Findings != 70 || got.Status != domain.StatusFail || run.Summary().Findings != 76 {
		t.Fatalf("the rule counts %d findings and the check %d, want 70 and 76", got.Findings, run.Summary().Findings)
	}
	if len(run.Findings) != len(places)+domain.MaxFindingsPerRule || domain.MaxFindingsPerRule != 50 {
		t.Fatalf("the check keeps %d findings, want the %d of the one rule and %d of the other", len(run.Findings), len(places), domain.MaxFindingsPerRule)
	}
	// By file, then line, then pointer, then message; a place whose line
	// is not known comes first in its file.
	for i, want := range []domain.Finding{places[5], places[4], places[3], places[2], places[1], places[0]} {
		want.Level = domain.SeverityWarn
		if run.Findings[i] != want {
			t.Errorf("finding %d is %+v, want %+v", i, run.Findings[i], want)
		}
	}
	for i, kept := range run.Findings[len(places):] {
		if kept.Line != i+1 || kept.RuleID != "T-RULE-003" || kept.Level != domain.SeverityBlock {
			t.Fatalf("finding %d of the rule is %+v, want the one of line %d", i, kept, i+1)
		}
	}
}

// What a tool hands over is held to the policy and to the source before it
// is recorded as evidence. A tool that reports on a rule nobody stated, in a
// file nobody submitted or in words that are no single line has failed, and
// the check fails with it instead of recording what it said.
func TestWhatAToolHandsOverIsHeldToThePolicyAndTheSource(t *testing.T) {
	type evaluation struct {
		draft        domain.Draft
		source       domain.Source
		tool         domain.Tool
		observations []domain.Observation
	}
	found := func(change func(*domain.Finding)) func(*evaluation) {
		return func(e *evaluation) {
			reported := finding("T-RULE-003", 12)
			change(&reported)
			e.observations = []domain.Observation{{RuleID: "T-RULE-003", Findings: []domain.Finding{reported}}}
		}
	}
	refused := map[string]func(*evaluation){
		"a check without an identifier":        func(e *evaluation) { e.draft.ID = "" },
		"a check without a policy snapshot":    func(e *evaluation) { e.draft.PolicySnapshotID = "" },
		"a check of no product":                func(e *evaluation) { e.draft.Revision.ProductID = "" },
		"a check of no workspace":              func(e *evaluation) { e.draft.Revision.WorkspaceID = "" },
		"a check of no asset":                  func(e *evaluation) { e.draft.Revision.AssetID = "" },
		"a check of no revision":               func(e *evaluation) { e.draft.Revision.RevisionID = "" },
		"a check nobody asked for":             func(e *evaluation) { e.draft.RequestedBy.Subject = "" },
		"a requester of no issuer":             func(e *evaluation) { e.draft.RequestedBy.Issuer = "" },
		"a source without a digest":            func(e *evaluation) { e.source.Digest = "" },
		"a source with a digest of other form": func(e *evaluation) { e.source.Digest = "md5:d41d8cd98f00b204e9800998ecf8427e" },
		"a source without files":               func(e *evaluation) { e.source.Files = nil },
		"a tool without a name":                func(e *evaluation) { e.tool.Name = "" },
		"a tool named in upper case":           func(e *evaluation) { e.tool.Name = "Linter" },
		"a tool without a version":             func(e *evaluation) { e.tool.Version = "" },
		"a tool whose version has a space":     func(e *evaluation) { e.tool.Version = "1 beta" },
		"a rule the policy does not have": func(e *evaluation) {
			e.observations = append(e.observations, domain.Observation{RuleID: "T-RULE-006"})
		},
		"a rule evaluated twice": func(e *evaluation) {
			e.observations = append(e.observations, domain.Observation{RuleID: "T-RULE-001", Undecided: true})
		},
		"a finding of another rule":             found(func(f *domain.Finding) { f.RuleID = "T-RULE-001" }),
		"a finding of no rule":                  found(func(f *domain.Finding) { f.RuleID = "" }),
		"a finding in a file nobody submitted":  found(func(f *domain.Finding) { f.Path = "drafts/unused.yaml" }),
		"a finding in no file":                  found(func(f *domain.Finding) { f.Path = "" }),
		"a pointer that is none":                found(func(f *domain.Finding) { f.Pointer = "paths" }),
		"a pointer with a control character":    found(func(f *domain.Finding) { f.Pointer = "/paths/\x00" }),
		"a pointer with a line break":           found(func(f *domain.Finding) { f.Pointer = "/paths/a\nb" }),
		"a pointer that is not UTF-8":           found(func(f *domain.Finding) { f.Pointer = "/paths/\xff" }),
		"a pointer longer than a finding keeps": found(func(f *domain.Finding) { f.Pointer = "/" + strings.Repeat("p", domain.MaxFindingPointer) }),
		"a line before the first":               found(func(f *domain.Finding) { f.Line = -1 }),
		"a line no file has":                    found(func(f *domain.Finding) { f.Line = 1 << 30 }),
		"no message":                            found(func(f *domain.Finding) { f.Message = "" }),
		"a message of two lines":                found(func(f *domain.Finding) { f.Message = "no operationId\ngive it one" }),
		"a message with a control character":    found(func(f *domain.Finding) { f.Message = "no operationId\x1b[31m" }),
		"a message with space around it":        found(func(f *domain.Finding) { f.Message = " no operationId" }),
		"a message that is not UTF-8":           found(func(f *domain.Finding) { f.Message = "no operationId \xff" }),
		"a message longer than a finding keeps": found(func(f *domain.Finding) { f.Message = strings.Repeat("m", domain.MaxFindingMessage+1) }),
	}
	for name, change := range refused {
		t.Run(name, func(t *testing.T) {
			handed := evaluation{draft: draft(), source: shipments(), tool: linter, observations: nothing("T-RULE-001")}
			change(&handed)
			run, err := domain.Complete(handed.draft, teamPolicy(t), handed.source, handed.tool, handed.observations, completed)
			if !errors.Is(err, domain.ErrInvalidEvaluation) || !reflect.DeepEqual(run, domain.CheckRun{}) {
				t.Fatalf("got %+v %v, want %v and no check", run, err, domain.ErrInvalidEvaluation)
			}
		})
	}
	if run, err := domain.Complete(draft(), domain.Policy{}, shipments(), linter, nil, completed); !errors.Is(err, domain.ErrInvalidEvaluation) {
		t.Errorf("a check without a policy: %+v %v, want %v", run, err, domain.ErrInvalidEvaluation)
	}

	// The bounds themselves are kept.
	longest := finding("T-RULE-003", 1<<24)
	longest.Pointer = "/" + strings.Repeat("订", domain.MaxFindingPointer-1)
	longest.Message = strings.Repeat("订", domain.MaxFindingMessage)
	run, err := domain.Complete(draft(), teamPolicy(t), shipments(), domain.Tool{Name: "a", Version: "2026.11+build-7"},
		[]domain.Observation{{RuleID: "T-RULE-003", Findings: []domain.Finding{longest}}}, completed)
	longest.Level = domain.SeverityBlock
	if err != nil || len(run.Findings) != 1 || run.Findings[0] != longest {
		t.Fatalf("a finding at its bounds: %+v %v, want it kept as it is", run.Findings, err)
	}
	for pointer, want := range map[string]bool{
		"": true, "/": true, "/paths/~1v1~1shipments/get": true, "/信息/标题": true,
		"paths": false, "#/paths": false, "/a\tb": false, "/a\u0085b": false,
	} {
		if domain.ValidPointer(pointer) != want {
			t.Errorf("the pointer %q can be kept: %t, want %t", pointer, !want, want)
		}
	}
}

// stored is a check as storage hands it back: its outcomes name their rules
// by identifier, and the rules themselves are in the policy snapshot.
func stored(run domain.CheckRun) domain.CheckRun {
	stored := run
	stored.PolicySetID = ""
	stored.Tools = slices.Clone(run.Tools)
	stored.Findings = slices.Clone(run.Findings)
	stored.Outcomes = make([]domain.Outcome, len(run.Outcomes))
	for i, outcome := range run.Outcomes {
		outcome.Rule = domain.Rule{ID: outcome.Rule.ID}
		stored.Outcomes[i] = outcome
	}
	return stored
}

// A check is read back with the rules of the policy it was held to, and only
// as a check that could have been concluded under that policy: the rows of a
// check that no longer agree with each other are not shown as evidence.
func TestAStoredCheckIsReadBackOnlyAsItWasConcluded(t *testing.T) {
	policy := teamPolicy(t)
	var many []domain.Finding
	for line := 1; line <= 60; line++ {
		many = append(many, finding("T-RULE-003", line))
	}
	run, err := domain.Complete(draft(), policy, shipments(), linter, []domain.Observation{
		{RuleID: "T-RULE-001"},
		{RuleID: "T-RULE-002", Findings: []domain.Finding{finding("T-RULE-002", 4)}},
		{RuleID: "T-RULE-003", Findings: many},
		{RuleID: "T-RULE-004", Undecided: true, Findings: []domain.Finding{finding("T-RULE-004", 5)}},
	}, completed)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := domain.Recorded(stored(run), policy)
	if err != nil || !reflect.DeepEqual(recorded, run) {
		t.Fatalf("the check read back:\n %+v %v\nwant it as it was concluded\n %+v", recorded, err, run)
	}
	passed, err := domain.Complete(draft(), policy, shipments(), linter, nothing("T-RULE-001", "T-RULE-002", "T-RULE-003", "T-RULE-004", "T-RULE-005"), completed)
	if err != nil {
		t.Fatal(err)
	}
	if recorded, err := domain.Recorded(stored(passed), policy); err != nil || !reflect.DeepEqual(recorded, passed) {
		t.Fatalf("a check that passed, read back: %+v %v", recorded, err)
	}

	other, err := domain.NewPolicy("team-rules", append(policy.Rules()[:4:4], rule("T-RULE-005", domain.SeverityWarn)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.Recorded(stored(run), other); !errors.Is(err, domain.ErrCheckRunDamaged) {
		t.Errorf("under a policy with another digest: %v, want %v", err, domain.ErrCheckRunDamaged)
	}

	outcome := func(index int, change func(*domain.Outcome)) func(*domain.CheckRun) {
		return func(r *domain.CheckRun) { change(&r.Outcomes[index]) }
	}
	kept := func(index int, change func(*domain.Finding)) func(*domain.CheckRun) {
		return func(r *domain.CheckRun) { change(&r.Findings[index]) }
	}
	damaged := map[string]func(*domain.CheckRun){
		"no identifier":                       func(r *domain.CheckRun) { r.ID = "" },
		"no policy snapshot":                  func(r *domain.CheckRun) { r.PolicySnapshotID = "" },
		"no revision":                         func(r *domain.CheckRun) { r.Revision.RevisionID = "" },
		"no requester":                        func(r *domain.CheckRun) { r.RequestedBy.Subject = "" },
		"an input digest of other form":       func(r *domain.CheckRun) { r.InputDigest = "sha256:" + strings.Repeat("G", 64) },
		"the digest of another policy":        func(r *domain.CheckRun) { r.PolicyDigest = digestOf("another policy") },
		"no tool":                             func(r *domain.CheckRun) { r.Tools = nil },
		"a tool without a version":            func(r *domain.CheckRun) { r.Tools[0].Version = "" },
		"an end before the start":             func(r *domain.CheckRun) { r.CompletedAt = r.StartedAt.Add(-time.Microsecond) },
		"a status that was rewritten":         func(r *domain.CheckRun) { r.Status = domain.StatusPass },
		"a status nobody defined":             func(r *domain.CheckRun) { r.Status = "SKIPPED" },
		"an outcome that is missing":          func(r *domain.CheckRun) { r.Outcomes = r.Outcomes[:4] },
		"an outcome too many":                 func(r *domain.CheckRun) { r.Outcomes = append(r.Outcomes, r.Outcomes[4]) },
		"outcomes in another order":           func(r *domain.CheckRun) { r.Outcomes[0], r.Outcomes[4] = r.Outcomes[4], r.Outcomes[0] },
		"an outcome of another rule":          outcome(4, func(o *domain.Outcome) { o.Rule.ID = "T-RULE-006" }),
		"a rule that failed, now passed":      outcome(2, func(o *domain.Outcome) { o.Status = domain.StatusPass }),
		"a rule that failed, now a warning":   outcome(2, func(o *domain.Outcome) { o.Status = domain.StatusWarn }),
		"a rule that warned, now a failure":   outcome(1, func(o *domain.Outcome) { o.Status = domain.StatusFail }),
		"a rule that passed, with a finding":  outcome(0, func(o *domain.Outcome) { o.Findings = 1 }),
		"a failure without a finding":         outcome(2, func(o *domain.Outcome) { o.Findings = 0 }),
		"fewer findings than are kept":        outcome(2, func(o *domain.Outcome) { o.Findings = 49 }),
		"a count below zero":                  outcome(0, func(o *domain.Outcome) { o.Findings = -1 }),
		"a rule that was not run, now run":    outcome(4, func(o *domain.Outcome) { o.Status = domain.StatusPass }),
		"a rule not run that names a tool":    outcome(4, func(o *domain.Outcome) { o.Tool = linter }),
		"a rule not run with findings":        outcome(4, func(o *domain.Outcome) { o.Findings = 1 }),
		"a rule that was run by no tool":      outcome(0, func(o *domain.Outcome) { o.Tool = domain.Tool{} }),
		"a rule run by a tool not listed":     outcome(0, func(o *domain.Outcome) { o.Tool.Version = "2" }),
		"a failure, now not run":              outcome(2, func(o *domain.Outcome) { o.Status, o.Tool = domain.StatusNotRun, domain.Tool{} }),
		"a status of a rule nobody defined":   outcome(0, func(o *domain.Outcome) { o.Status = "SKIPPED" }),
		"a finding that was removed":          func(r *domain.CheckRun) { r.Findings = r.Findings[1:] },
		"every finding removed":               func(r *domain.CheckRun) { r.Findings = nil },
		"a finding that was added":            func(r *domain.CheckRun) { r.Findings = append(r.Findings, r.Findings[0]) },
		"a finding of a rule that passed":     kept(0, func(f *domain.Finding) { f.RuleID = "T-RULE-001" }),
		"a finding of a rule nobody stated":   kept(0, func(f *domain.Finding) { f.RuleID = "T-RULE-006" }),
		"a finding at another level":          kept(0, func(f *domain.Finding) { f.Level = domain.SeverityBlock }),
		"a finding in no file":                kept(0, func(f *domain.Finding) { f.Path = "" }),
		"a finding at a pointer that is none": kept(0, func(f *domain.Finding) { f.Pointer = "paths" }),
		"a finding on a line below zero":      kept(0, func(f *domain.Finding) { f.Line = -1 }),
		"a finding without a message":         kept(0, func(f *domain.Finding) { f.Message = "" }),
		"a finding of two lines":              kept(0, func(f *domain.Finding) { f.Message = "a\nb" }),
	}
	for name, damage := range damaged {
		t.Run(name, func(t *testing.T) {
			altered := stored(run)
			damage(&altered)
			if got, err := domain.Recorded(altered, policy); !errors.Is(err, domain.ErrCheckRunDamaged) || !reflect.DeepEqual(got, domain.CheckRun{}) {
				t.Fatalf("got %+v %v, want %v and no check", got, err, domain.ErrCheckRunDamaged)
			}
		})
	}
}

// The tools of a check are written out one way, so that the same tools are
// known by the same digest, and read back only as they were written.
func TestTheToolsOfACheckAreKnownByTheDigestOfTheirManifest(t *testing.T) {
	differ := domain.Tool{Name: "fabric-openapi-diff", Version: "0.3.1"}
	const manifest = `[{"name":"fabric-openapi-diff","version":"0.3.1"},{"name":"fabric-openapi-rules","version":"1"}]`
	for name, tools := range map[string][]domain.Tool{"in one order": {linter, differ}, "in the other": {differ, linter}} {
		if got := domain.ToolManifest(tools); got != manifest || domain.ToolDigest(tools) != digestOf(manifest) {
			t.Errorf("%s: the manifest is %s under %s, want %s under its SHA-256", name, got, domain.ToolDigest(tools), manifest)
		}
	}
	if next := (domain.Tool{Name: linter.Name, Version: "2"}); domain.ToolDigest([]domain.Tool{next}) == domain.ToolDigest([]domain.Tool{linter}) {
		t.Error("another version of a tool has the digest of the one before")
	}
	tools, err := domain.RecordedTools(manifest)
	if err != nil || !reflect.DeepEqual(tools, []domain.Tool{differ, linter}) {
		t.Fatalf("the tools read back: %+v %v", tools, err)
	}
	for name, altered := range map[string]string{
		"no tool":                    `[]`,
		"no manifest":                ``,
		"tools in another order":     `[{"name":"fabric-openapi-rules","version":"1"},{"name":"fabric-openapi-diff","version":"0.3.1"}]`,
		"a tool that is there twice": `[{"name":"fabric-openapi-rules","version":"1"},{"name":"fabric-openapi-rules","version":"2"}]`,
		"a member nobody wrote":      `[{"name":"fabric-openapi-rules","version":"1","verified":true}]`,
		"a tool without a version":   `[{"name":"fabric-openapi-rules","version":""}]`,
		"a tool named in upper case": `[{"name":"Fabric","version":"1"}]`,
		"white space":                `[ {"name":"fabric-openapi-rules","version":"1"}]`,
		"something behind it":        manifest + `[]`,
		"a manifest of another kind": `{"name":"fabric-openapi-rules","version":"1"}`,
	} {
		if got, err := domain.RecordedTools(altered); !errors.Is(err, domain.ErrCheckRunDamaged) {
			t.Errorf("%s: got %+v %v, want %v", name, got, err, domain.ErrCheckRunDamaged)
		}
	}
	if got := fmt.Sprintf("%s %s %s %s %s", domain.StatusPass, domain.StatusWarn, domain.StatusFail, domain.StatusUnknown, domain.StatusNotRun); got != "PASS WARN FAIL UNKNOWN NOT_RUN" {
		t.Errorf("the statuses are %s, not the five of F04 §8", got)
	}
}
